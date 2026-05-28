package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/services"
)

// AnalysisHandler provides REST endpoints for stock fundamental + technical analysis.
type AnalysisHandler struct {
	pool *pgxpool.Pool
	svc  *services.AnalysisService
}

func NewAnalysisHandler(pool *pgxpool.Pool) *AnalysisHandler {
	return &AnalysisHandler{pool: pool, svc: services.NewAnalysisService(pool)}
}

// Run triggers a fresh analysis for the given Yahoo symbol.
// POST /api/analysis/run  body: {"symbol": "RELIANCE.NS"}
func (h *AnalysisHandler) Run(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol string `json:"symbol"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}
	fa, err := h.svc.Analyze(r.Context(), req.Symbol)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fa)
}

// Get returns the most recent stored analysis for a symbol.
// GET /api/analysis/{symbol}
func (h *AnalysisHandler) Get(w http.ResponseWriter, r *http.Request) {
	symbol := chi.URLParam(r, "symbol")
	fa, err := h.svc.GetLatest(r.Context(), symbol)
	if err != nil {
		writeError(w, http.StatusNotFound, "no analysis found for "+symbol)
		return
	}
	writeJSON(w, http.StatusOK, fa)
}

// Watchlist returns latest analyses for all watchlist symbols.
// GET /api/analysis/watchlist
func (h *AnalysisHandler) Watchlist(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.GetWatchlistAnalyses(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
