package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

type SignalHandler struct {
	pool   *pgxpool.Pool
	signal *services.SignalService
}

func NewSignalHandler(pool *pgxpool.Pool) *SignalHandler {
	return &SignalHandler{
		pool:   pool,
		signal: services.NewSignalService(pool),
	}
}

// withLongTimeout replaces the context with a 5-minute timeout so AI calls
// are not cut off by the global 60s middleware.
func (h *SignalHandler) withLongTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetHoldings loads previously stored signals for a risk profile from the DB.
func (h *SignalHandler) GetHoldings(w http.ResponseWriter, r *http.Request) {
	riskProfile := strings.ToLower(r.URL.Query().Get("risk_profile"))
	if riskProfile == "" {
		riskProfile = "moderate"
	}

	result, err := h.signal.LoadSignals(r.Context(), riskProfile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type holdingsAnalysisRequest struct {
	RiskProfile string `json:"risk_profile"`
	Goal        string `json:"goal"`
	Horizon     string `json:"horizon"`
}

type analyseStockRequest struct {
	Query       string `json:"query"`
	RiskProfile string `json:"risk_profile"`
}

// AnalyseHoldings generates AI signals, saves them to DB, returns the result.
func (h *SignalHandler) AnalyseHoldings(w http.ResponseWriter, r *http.Request) {
	var req holdingsAnalysisRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "AI provider not configured — add your API key in Settings → AI")
		return
	}

	profile := services.InvestorProfile{
		Goal:        sanitise(req.Goal, "Long-term wealth creation"),
		Horizon:     sanitise(req.Horizon, "5+ years"),
		RiskProfile: toRiskProfile(req.RiskProfile),
	}

	payload, err := h.signal.BuildPortfolioPayload(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build portfolio payload: "+err.Error())
		return
	}
	if len(payload.Holdings) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no active holdings found — add transactions first")
		return
	}

	result, err := h.signal.FetchHoldingsSignals(r.Context(), payload, profile, *cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AI analysis failed: "+err.Error())
		return
	}

	// Persist to DB (best-effort — don't fail the response if save fails).
	if err := h.signal.SaveSignals(r.Context(), string(profile.RiskProfile), result.Signals); err != nil {
		// Log but continue — the user still gets their result.
		_ = err
	}

	// Reload from DB so GeneratedAt is populated from the actual DB timestamp.
	if saved, err := h.signal.LoadSignals(r.Context(), string(profile.RiskProfile)); err == nil {
		result = saved
	}

	writeJSON(w, http.StatusOK, result)
}

// AnalyseStock streams a single-stock/fund analysis.
func (h *SignalHandler) AnalyseStock(w http.ResponseWriter, r *http.Request) {
	var req analyseStockRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "AI provider not configured — add your API key in Settings → AI")
		return
	}

	profile := services.InvestorProfile{RiskProfile: toRiskProfile(req.RiskProfile)}
	if err := h.signal.StreamStockAnalysis(r.Context(), w, req.Query, profile, *cfg); err != nil {
		if w.Header().Get("Content-Type") == "" {
			writeError(w, http.StatusInternalServerError, "AI analysis failed: "+err.Error())
		}
	}
}

// GetInstrumentMetrics returns the scorecard for any instrument.
// Checks the DB cache first (24h TTL); fetches live only on cache miss.
// Returns { kind: "mf"|"etf"|"stock", metrics: {...} }.
func (h *SignalHandler) GetInstrumentMetrics(w http.ResponseWriter, r *http.Request) {
	instrID, err := strconv.ParseInt(chi.URLParam(r, "instrument_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instrument_id")
		return
	}

	// ── Cache hit ──────────────────────────────────────────────────────────────
	if cached, _ := services.LoadScore(r.Context(), h.pool, instrID); cached != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Score-Source", "cache")
		w.WriteHeader(http.StatusOK)
		// Wrap raw JSON bytes into { kind, metrics: <raw> }
		w.Write([]byte(`{"kind":"` + cached.Kind + `","metrics":`))
		w.Write(cached.MetricsJSON)
		w.Write([]byte(`}`))
		return
	}

	// ── Cache miss: fetch live ─────────────────────────────────────────────────
	var assetType, amfiCode, yahooSymbol string
	err = h.pool.QueryRow(r.Context(), `
		SELECT COALESCE(asset_type,''), COALESCE(amfi_code,''), COALESCE(yahoo_symbol,'')
		  FROM instruments WHERE id = $1`, instrID).
		Scan(&assetType, &amfiCode, &yahooSymbol)
	if err != nil {
		writeError(w, http.StatusNotFound, "instrument not found")
		return
	}

	type response struct {
		Kind    string `json:"kind"`
		Metrics any    `json:"metrics"`
	}

	switch {
	case assetType == "MF" && amfiCode != "":
		m, err := services.FetchMFMetrics(amfiCode, 55*time.Second)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fetch MF metrics: "+err.Error())
			return
		}
		_ = services.SaveScore(r.Context(), h.pool, instrID, "mf", m.Zero1Score, m)
		writeJSON(w, http.StatusOK, response{Kind: "mf", Metrics: m})

	case assetType == "ETF" || assetType == "US_FUND" || assetType == "METAL" || assetType == "GOLD" || (assetType == "MF" && amfiCode == ""):
		m, err := services.FetchETFMetrics(r.Context(), instrID, yahooSymbol, h.pool, 55*time.Second)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fetch ETF metrics: "+err.Error())
			return
		}
		_ = services.SaveScore(r.Context(), h.pool, instrID, "etf", m.Zero1Score, m)
		writeJSON(w, http.StatusOK, response{Kind: "etf", Metrics: m})

	case assetType == "STOCK":
		if yahooSymbol == "" {
			writeError(w, http.StatusUnprocessableEntity, "stock has no Yahoo symbol — backfill first")
			return
		}
		m, err := services.FetchStockMetrics(r.Context(), instrID, yahooSymbol, h.pool, 55*time.Second)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fetch stock metrics: "+err.Error())
			return
		}
		_ = services.SaveScore(r.Context(), h.pool, instrID, "stock", m.Zero1Score, m)
		writeJSON(w, http.StatusOK, response{Kind: "stock", Metrics: m})

	default:
		writeError(w, http.StatusUnprocessableEntity, "scorecard not available for asset type: "+assetType)
	}
}

// RefreshScores batch-recomputes and caches scores for all active holdings.
func (h *SignalHandler) RefreshScores(w http.ResponseWriter, r *http.Request) {
	result := services.RefreshAllScores(r.Context(), h.pool, 45*time.Second)
	writeJSON(w, http.StatusOK, result)
}

// GetMFMetrics is kept for backward compatibility; delegates to GetInstrumentMetrics.
func (h *SignalHandler) GetMFMetrics(w http.ResponseWriter, r *http.Request) {
	h.GetInstrumentMetrics(w, r)
}

func (h *SignalHandler) loadConfig(ctx context.Context) (*services.AIConfig, error) {
	provider, key, model, err := loadAIConfig(ctx, h.pool)
	if err != nil {
		return nil, err
	}
	if provider == "" || key == "" {
		return nil, nil
	}
	return &services.AIConfig{Provider: provider, APIKey: key, Model: model}, nil
}

func toRiskProfile(s string) services.RiskProfile {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "conservative":
		return services.RiskConservative
	case "aggressive":
		return services.RiskAggressive
	default:
		return services.RiskModerate
	}
}

func sanitise(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}
