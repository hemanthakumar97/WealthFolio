package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/domain"
	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

type InstrumentsHandler struct {
	pool    *pgxpool.Pool
	fetcher priceFetcher
}

type priceFetcher interface {
	FetchAll(ctx context.Context) (services.FetchResult, error)
	PriceHistory(ctx context.Context, instrumentID int64, limit int) ([]services.PriceRow, error)
}

func NewInstrumentsHandler(pool *pgxpool.Pool, fetcher priceFetcher) *InstrumentsHandler {
	return &InstrumentsHandler{pool: pool, fetcher: fetcher}
}

type instrumentResponse struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	ISIN        *string   `json:"isin"`
	AMFICode    *string   `json:"amfi_code"`
	YahooSymbol *string   `json:"yahoo_symbol"`
	AssetType   string    `json:"asset_type"`
	Currency    string    `json:"currency"`
	Exchange    *string   `json:"exchange"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type instrumentDetail struct {
	instrumentResponse
	TransactionCount int     `json:"transaction_count"`
	TotalUnits       float64 `json:"total_units"`
	InvestedAmount   float64 `json:"invested_amount"`
	Categories       []string `json:"categories"`
}

func (h *InstrumentsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	assetType := strings.ToUpper(strings.TrimSpace(q.Get("asset_type")))
	search := strings.TrimSpace(q.Get("search"))

	sql := `SELECT id, name, isin, amfi_code, yahoo_symbol, asset_type, currency, exchange, created_at, updated_at
	          FROM instruments`
	args := []any{}
	conds := []string{}
	if assetType != "" {
		args = append(args, assetType)
		conds = append(conds, "asset_type = $"+itoa(len(args)))
	}
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		conds = append(conds, "(LOWER(name) LIKE $"+itoa(len(args))+" OR LOWER(COALESCE(isin,'')) LIKE $"+itoa(len(args))+")")
	}
	if len(conds) > 0 {
		sql += " WHERE " + strings.Join(conds, " AND ")
	}
	sql += " ORDER BY name ASC LIMIT 500"

	rows, err := h.pool.Query(r.Context(), sql, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []instrumentResponse{}
	for rows.Next() {
		var i instrumentResponse
		if err := rows.Scan(&i.ID, &i.Name, &i.ISIN, &i.AMFICode, &i.YahooSymbol,
			&i.AssetType, &i.Currency, &i.Exchange, &i.CreatedAt, &i.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, i)
	}
	writeJSON(w, http.StatusOK, out)
}

type instrumentInput struct {
	Name      string `json:"name"`
	ISIN      string `json:"isin"`
	AMFICode  string `json:"amfi_code"`
	AssetType string `json:"asset_type"`
	Currency  string `json:"currency"`
	Exchange  string `json:"exchange"`
}

func (h *InstrumentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req instrumentInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	at := strings.ToUpper(strings.TrimSpace(req.AssetType))
	if !isValidAssetType(at) {
		at = domain.AssetTypeOther
	}
	cur := strings.ToUpper(strings.TrimSpace(req.Currency))
	if cur == "" {
		cur = domain.CurrencyINR
	}

	var i instrumentResponse
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO instruments (name, isin, amfi_code, asset_type, currency, exchange)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, name, isin, amfi_code, yahoo_symbol, asset_type, currency, exchange, created_at, updated_at`,
		strings.TrimSpace(req.Name),
		nullableString(req.ISIN),
		nullableString(req.AMFICode),
		at, cur,
		nullableString(req.Exchange),
	).Scan(&i.ID, &i.Name, &i.ISIN, &i.AMFICode, &i.YahooSymbol,
		&i.AssetType, &i.Currency, &i.Exchange, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, i)
}

func (h *InstrumentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var d instrumentDetail
	d.Categories = []string{}
	var totalUnits, invested *float64
	err = h.pool.QueryRow(r.Context(),
		`SELECT i.id, i.name, i.isin, i.amfi_code, i.yahoo_symbol, i.asset_type, i.currency, i.exchange,
		        i.created_at, i.updated_at,
		        (SELECT COUNT(*)::int FROM transactions t WHERE t.instrument_id = i.id),
		        (SELECT SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS')
		                          THEN quantity
		                         WHEN transaction_type IN ('SELL','SWITCH_OUT')
		                          THEN -quantity
		                         ELSE 0 END)::float
		           FROM transactions WHERE instrument_id = i.id),
		        (SELECT SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN') THEN amount
		                         WHEN transaction_type IN ('SELL','SWITCH_OUT') THEN -amount
		                         ELSE 0 END)::float
		           FROM transactions WHERE instrument_id = i.id)
		   FROM instruments i WHERE i.id = $1`,
		id,
	).Scan(&d.ID, &d.Name, &d.ISIN, &d.AMFICode, &d.YahooSymbol,
		&d.AssetType, &d.Currency, &d.Exchange,
		&d.CreatedAt, &d.UpdatedAt,
		&d.TransactionCount, &totalUnits, &invested)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if totalUnits != nil {
		d.TotalUnits = *totalUnits
	}
	if invested != nil {
		d.InvestedAmount = *invested
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *InstrumentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req instrumentInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	sets := []string{}
	args := []any{}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	if strings.TrimSpace(req.Name) != "" {
		add("name", strings.TrimSpace(req.Name))
	}
	if strings.TrimSpace(req.ISIN) != "" {
		add("isin", strings.ToUpper(strings.TrimSpace(req.ISIN)))
	}
	if strings.TrimSpace(req.AMFICode) != "" {
		add("amfi_code", strings.TrimSpace(req.AMFICode))
	}
	if at := strings.ToUpper(strings.TrimSpace(req.AssetType)); at != "" && isValidAssetType(at) {
		add("asset_type", at)
	}
	if cur := strings.ToUpper(strings.TrimSpace(req.Currency)); cur != "" {
		add("currency", cur)
	}
	if strings.TrimSpace(req.Exchange) != "" {
		add("exchange", strings.TrimSpace(req.Exchange))
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)
	sql := "UPDATE instruments SET " + strings.Join(sets, ", ") +
		" WHERE id = $" + itoa(len(args)) +
		" RETURNING id, name, isin, amfi_code, yahoo_symbol, asset_type, currency, exchange, created_at, updated_at"

	var i instrumentResponse
	err = h.pool.QueryRow(r.Context(), sql, args...).Scan(
		&i.ID, &i.Name, &i.ISIN, &i.AMFICode, &i.YahooSymbol,
		&i.AssetType, &i.Currency, &i.Exchange, &i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, i)
}

func (h *InstrumentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM instruments WHERE id = $1`, id)
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

type transactionRow struct {
	ID              int64     `json:"id"`
	InstrumentID    int64     `json:"instrument_id"`
	TransactionDate time.Time `json:"transaction_date"`
	TransactionType string    `json:"transaction_type"`
	Quantity        float64   `json:"quantity"`
	Price           float64   `json:"price"`
	Amount          float64   `json:"amount"`
	Platform        string    `json:"platform"`
	OrderID         *string   `json:"order_id"`
}

func (h *InstrumentsHandler) Transactions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, instrument_id, transaction_date, transaction_type,
		        quantity::float, price::float, amount::float, platform, order_id
		   FROM transactions WHERE instrument_id = $1
		  ORDER BY transaction_date DESC, id DESC`,
		id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []transactionRow{}
	for rows.Next() {
		var t transactionRow
		if err := rows.Scan(&t.ID, &t.InstrumentID, &t.TransactionDate, &t.TransactionType,
			&t.Quantity, &t.Price, &t.Amount, &t.Platform, &t.OrderID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *InstrumentsHandler) Prices(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if h.fetcher == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	prices, err := h.fetcher.PriceHistory(r.Context(), id, 365)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prices == nil {
		prices = []services.PriceRow{}
	}
	writeJSON(w, http.StatusOK, prices)
}

func (h *InstrumentsHandler) FetchPrices(w http.ResponseWriter, r *http.Request) {
	if h.fetcher == nil {
		writeJSON(w, http.StatusOK, map[string]int{"fetched": 0, "failed": 0})
		return
	}
	result, err := h.fetcher.FetchAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"fetched": result.Fetched, "failed": result.Failed})
}

// --- helpers ---

func isValidAssetType(t string) bool {
	switch t {
	case domain.AssetTypeMF, domain.AssetTypeETF, domain.AssetTypeStock,
		domain.AssetTypeBond, domain.AssetTypeMetal, domain.AssetTypeOther,
		domain.AssetTypeUSFund:
		return true
	}
	return false
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
