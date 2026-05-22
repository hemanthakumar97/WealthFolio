package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

type MarketHandler struct {
	pool    *pgxpool.Pool
	mood    *services.MarketMoodService
	mmi     *services.TickertapeService
	metals  *services.PreciousMetalsService
}

func NewMarketHandler(
	pool *pgxpool.Pool,
	mood *services.MarketMoodService,
	mmi *services.TickertapeService,
	metals *services.PreciousMetalsService,
) *MarketHandler {
	return &MarketHandler{pool: pool, mood: mood, mmi: mmi, metals: metals}
}

// --- Index config ---

func (h *MarketHandler) Config(w http.ResponseWriter, r *http.Request) {
	items, err := h.mood.ListConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []services.IndexConfigItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *MarketHandler) ToggleConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "index_name")
	// URL decode spaces that may be encoded as %20 or +
	name = strings.ReplaceAll(name, "+", " ")
	item, err := h.mood.ToggleIndex(r.Context(), name)
	if err != nil {
		if err == pgx.ErrNoRows || strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "index not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// --- Moods ---

func (h *MarketHandler) Moods(w http.ResponseWriter, r *http.Request) {
	moods, err := h.mood.Moods(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if moods == nil {
		moods = []services.MarketMoodResponse{}
	}
	writeJSON(w, http.StatusOK, moods)
}

func (h *MarketHandler) IndexHistory(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "index_name")
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "1Y"
	}
	details, err := h.mood.IndexDetails(r.Context(), name, period)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, details)
}

// --- MMI ---

func (h *MarketHandler) MMI(w http.ResponseWriter, r *http.Request) {
	cached, err := h.mmi.GetMMI(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "updated_at": nil})
		return
	}
	writeJSON(w, http.StatusOK, cached)
}

// --- Metals ---

func (h *MarketHandler) Metals(w http.ResponseWriter, r *http.Request) {
	cached, err := h.metals.GetMetals(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "updated_at": nil})
		return
	}
	writeJSON(w, http.StatusOK, cached)
}

// --- Refresh ---

func (h *MarketHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	mmiResult, _ := h.mmi.Refresh(r.Context())
	metalsResult, _ := h.metals.Refresh(r.Context())

	resp := map[string]any{
		"mmi_updated":    mmiResult != nil,
		"metals_updated": metalsResult != nil,
		"refreshed_at":   time.Now(),
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Sync ---

func (h *MarketHandler) Sync(w http.ResponseWriter, r *http.Request) {
	if err := h.mood.SyncMarketData(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "sync triggered"})
}

// --- Ingest P/E (manual data entry) ---

type ingestPERequest struct {
	IndexName string   `json:"index_name"`
	Date      string   `json:"date"` // YYYY-MM-DD
	PERatio   *float64 `json:"pe_ratio"`
	PBRatio   *float64 `json:"pb_ratio"`
	DivYield  *float64 `json:"div_yield"`
}

func (h *MarketHandler) IngestPE(w http.ResponseWriter, r *http.Request) {
	var req ingestPERequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.IndexName) == "" {
		writeError(w, http.StatusBadRequest, "index_name required")
		return
	}
	d, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if err := h.mood.IngestPE(r.Context(), req.IndexName, d, req.PERatio, req.PBRatio, req.DivYield); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"message": "ingested"})
}

// --- Watchlist ---

type watchlistItem struct {
	ID        int64     `json:"id"`
	Symbol    string    `json:"symbol"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *MarketHandler) ListWatchlist(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, symbol, created_at FROM watchlist ORDER BY created_at DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []watchlistItem{}
	for rows.Next() {
		var item watchlistItem
		if err := rows.Scan(&item.ID, &item.Symbol, &item.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

type addWatchlistRequest struct {
	Symbol string `json:"symbol"`
}

func (h *MarketHandler) AddWatchlist(w http.ResponseWriter, r *http.Request) {
	var req addWatchlistRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	sym := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if sym == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}
	var item watchlistItem
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO watchlist (symbol) VALUES ($1)
		 ON CONFLICT (symbol) DO UPDATE SET symbol = EXCLUDED.symbol
		 RETURNING id, symbol, created_at`, sym,
	).Scan(&item.ID, &item.Symbol, &item.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *MarketHandler) RemoveWatchlist(w http.ResponseWriter, r *http.Request) {
	sym := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "symbol")))
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM watchlist WHERE symbol = $1`, sym)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
