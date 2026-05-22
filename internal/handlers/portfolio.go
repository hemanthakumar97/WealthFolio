package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

type PortfolioHandler struct {
	pool *pgxpool.Pool
	calc *services.PortfolioCalculator
}

func NewPortfolioHandler(pool *pgxpool.Pool, calc *services.PortfolioCalculator) *PortfolioHandler {
	return &PortfolioHandler{pool: pool, calc: calc}
}

func (h *PortfolioHandler) Summary(w http.ResponseWriter, r *http.Request) {
	summary, err := h.calc.Summary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Augment with XIRR (best-effort, non-fatal).
	type summaryWithXIRR struct {
		services.PortfolioSummary
		XIRR *float64 `json:"xirr"`
	}
	resp := summaryWithXIRR{PortfolioSummary: summary}
	if rate, err := services.XIRRFromTransactions(r.Context(), h.pool, summary.TotalCurrentValue); err == nil && rate != 0 {
		resp.XIRR = &rate
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PortfolioHandler) Holdings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := services.FilterParams{
		Platform:  q.Get("platform"),
		AssetType: q.Get("asset_type"),
		SortBy:    q.Get("sort_by"),
	}
	holdings, err := h.calc.Holdings(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if holdings == nil {
		holdings = []services.HoldingInfo{}
	}
	writeJSON(w, http.StatusOK, holdings)
}

func (h *PortfolioHandler) ClosedPositions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := services.FilterParams{
		Platform:  q.Get("platform"),
		AssetType: q.Get("asset_type"),
	}
	positions, err := h.calc.ClosedPositions(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if positions == nil {
		positions = []services.ClosedPositionInfo{}
	}
	writeJSON(w, http.StatusOK, positions)
}
