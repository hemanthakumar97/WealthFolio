package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
	"github.com/hemanthhku/wealthfolio-v2/internal/sse"
)

// AutoSearchResult is a unified match from MFAPI or Yahoo Finance.
type AutoSearchResult struct {
	Source   string `json:"source"`     // "mfapi" or "yahoo"
	Code     string `json:"code"`       // amfi_code or yahoo symbol (e.g. "NIFTYBEES.NS")
	Name     string `json:"name"`
	Exchange string `json:"exchange"`   // raw exchange code (e.g. "NGM")
	ExchDisp string `json:"exch_disp"`  // display name for exchange (e.g. "NASDAQ")
}

// AutoSearch decides the right data source for an instrument and returns ranked matches.
// Logic: MF / ISIN-starts-INF → MFAPI; ETF → try MFAPI first, fall back to Yahoo; STOCK → Yahoo.
func (h *BackfillHandler) AutoSearch(w http.ResponseWriter, r *http.Request) {
	instrIDStr := strings.TrimSpace(r.URL.Query().Get("instrument_id"))
	instrID, err := strconv.ParseInt(instrIDStr, 10, 64)
	if err != nil || instrID == 0 {
		writeError(w, http.StatusBadRequest, "instrument_id required")
		return
	}

	var name, assetType, currency string
	var isin, amfiCode, yahooSymbol, exchange *string
	err = h.pool.QueryRow(r.Context(),
		`SELECT name, asset_type, currency, isin, amfi_code, yahoo_symbol, exchange FROM instruments WHERE id = $1`,
		instrID,
	).Scan(&name, &assetType, &currency, &isin, &amfiCode, &yahooSymbol, &exchange)
	if err != nil {
		writeError(w, http.StatusNotFound, "instrument not found")
		return
	}

	// Exchange-traded instruments (ETFs, REITs, stocks with an exchange field) use Yahoo Finance.
	// Pure mutual funds (no exchange — direct/regular plans from Groww etc.) use MFAPI by name.
	isExchangeTraded := exchange != nil && *exchange != ""

	mfapiClient := services.NewMFAPIClient()
	yahooClient := services.NewYahooClient()

	out := make([]AutoSearchResult, 0)

	isUS := assetType == "US_FUND" || currency == "USD"

	if !isExchangeTraded && !isUS {
		// Pure MF: search MFAPI by fund name.
		matches, err := mfapiClient.Search(r.Context(), name, 8)
		if err == nil {
			for _, m := range matches {
				out = append(out, AutoSearchResult{
					Source: "mfapi",
					Code:   strconv.Itoa(m.SchemeCode),
					Name:   m.SchemeName,
				})
			}
		}
	}

	// Exchange-traded (ETF/REIT/stock) or US fund → Yahoo Finance.
	// Also fall back to Yahoo if MFAPI found nothing.
	if isExchangeTraded || isUS || len(out) == 0 {
		matches, err := yahooClient.Search(r.Context(), name, 8, isUS)
		if err == nil {
			for _, m := range matches {
				out = append(out, AutoSearchResult{
					Source:   "yahoo",
					Code:     m.Symbol,
					Name:     m.Name,
					Exchange: m.Exchange,
					ExchDisp: m.ExchDisp,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// StartAuto kicks off a backfill from either MFAPI or Yahoo depending on the source field.
type startAutoRequest struct {
	InstrumentID int64  `json:"instrument_id"`
	Source       string `json:"source"` // "mfapi" or "yahoo"
	Code         string `json:"code"`
}

func (h *BackfillHandler) StartAuto(w http.ResponseWriter, r *http.Request) {
	var req startAutoRequest
	if err := decodeJSON(r, &req); err != nil || req.InstrumentID == 0 || req.Code == "" {
		writeError(w, http.StatusBadRequest, "instrument_id, source and code are required")
		return
	}
	switch req.Source {
	case "mfapi":
		if err := h.svc.RunFromMFAPI(r.Context(), req.InstrumentID, strings.TrimSpace(req.Code)); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	case "yahoo":
		if err := h.svc.RunFromYahoo(r.Context(), req.InstrumentID, strings.TrimSpace(req.Code)); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "source must be 'mfapi' or 'yahoo'")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// SearchMFAPI proxies a search to api.mfapi.in and returns up to 8 matches.
func (h *BackfillHandler) SearchMFAPI(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	client := services.NewMFAPIClient()
	results, err := client.Search(r.Context(), q, 8)
	if err != nil {
		writeError(w, http.StatusBadGateway, "mfapi search failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// UpdateAMFICode saves amfi_code to an instrument without starting a backfill.
type updateAMFICodeRequest struct {
	AMFICode string `json:"amfi_code"`
}

func (h *BackfillHandler) UpdateAMFICode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateAMFICodeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	code := strings.TrimSpace(req.AMFICode)
	if _, err := h.pool.Exec(r.Context(),
		`UPDATE instruments SET amfi_code = $1, updated_at = NOW() WHERE id = $2`,
		code, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"amfi_code": code})
}

// StartFromMFAPI kicks off a history backfill using MFAPI for the given instrument + amfi_code.
type startMFAPIRequest struct {
	InstrumentID int64  `json:"instrument_id"`
	AMFICode     string `json:"amfi_code"`
}

func (h *BackfillHandler) StartFromMFAPI(w http.ResponseWriter, r *http.Request) {
	var req startMFAPIRequest
	if err := decodeJSON(r, &req); err != nil || req.InstrumentID == 0 || req.AMFICode == "" {
		writeError(w, http.StatusBadRequest, "instrument_id and amfi_code are required")
		return
	}
	if err := h.svc.RunFromMFAPI(r.Context(), req.InstrumentID, strings.TrimSpace(req.AMFICode)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// sseEmitter adapts *sse.Broker to BackfillEmitter.
type sseEmitter struct {
	broker *sse.Broker
}

func (e *sseEmitter) Emit(msg string) {
	e.broker.Publish("backfill", map[string]string{"log": msg})
}

// BackfillHandler handles the /api/backfill routes.
type BackfillHandler struct {
	pool    *pgxpool.Pool
	svc     *services.BackfillService
}

func NewBackfillHandler(pool *pgxpool.Pool, broker *sse.Broker) *BackfillHandler {
	sheets := services.NewSheetsService()
	fx := services.NewFXService(pool)
	emitter := &sseEmitter{broker: broker}
	svc := services.NewBackfillService(pool, sheets, fx, emitter)
	return &BackfillHandler{pool: pool, svc: svc}
}

// Start kicks off a backfill job from a sheet URL or uploaded file.
func (h *BackfillHandler) Start(w http.ResponseWriter, r *http.Request) {
	instrIDStr := r.FormValue("instrument_id")
	if instrIDStr == "" {
		writeError(w, http.StatusBadRequest, "instrument_id required")
		return
	}
	instrID, err := strconv.ParseInt(instrIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instrument_id")
		return
	}

	sheetURL := strings.TrimSpace(r.FormValue("sheet_url"))
	file, _, fileErr := r.FormFile("file")

	if sheetURL != "" {
		if err := h.svc.RunFromSheet(r.Context(), instrID, sheetURL); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	} else if fileErr == nil {
		defer file.Close()
		if err := h.svc.RunFromReader(r.Context(), instrID, file); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "provide sheet_url or file")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// Status returns current job state.
func (h *BackfillHandler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Status())
}

