package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/services"
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

	// Clear stale cached scores and previous signals so analysis always uses fresh data.
	h.pool.Exec(r.Context(), `DELETE FROM instrument_scores`)
	h.pool.Exec(r.Context(), `DELETE FROM signal_results WHERE risk_profile = $1`, string(profile.RiskProfile))

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

	// ── Cache hit (skip when ?force=true) ────────────────────────────────────
	force := r.URL.Query().Get("force") == "true"
	if !force {
		if cached, _ := services.LoadScore(r.Context(), h.pool, instrID); cached != nil {
			// Rebuild sources from the cached metrics so the UI always shows them.
			sources := buildSourcesFromCache(r.Context(), h.pool, instrID, cached)
			sourcesJSON, _ := json.Marshal(sources)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Score-Source", "cache")
			w.WriteHeader(http.StatusOK)
			ts := cached.ComputedAt.UTC().Format(time.RFC3339)
			w.Write([]byte(`{"kind":"` + cached.Kind + `","computed_at":"` + ts + `","sources":`))
			w.Write(sourcesJSON)
			w.Write([]byte(`,"metrics":`))
			w.Write(cached.MetricsJSON)
			w.Write([]byte(`}`))
			return
		}
	}

	// ── Cache miss / force: fetch live ────────────────────────────────────────
	var assetType, amfiCode, yahooSymbol, manualGrowwSlug, manualTickertapeSlug string
	err = h.pool.QueryRow(r.Context(), `
		SELECT COALESCE(asset_type,''), COALESCE(amfi_code,''), COALESCE(yahoo_symbol,''), COALESCE(groww_slug,''), COALESCE(tickertape_slug,'')
		  FROM instruments WHERE id = $1`, instrID).
		Scan(&assetType, &amfiCode, &yahooSymbol, &manualGrowwSlug, &manualTickertapeSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "instrument not found")
		return
	}

	type response struct {
		Kind       string            `json:"kind"`
		ComputedAt string            `json:"computed_at"`
		Sources    map[string]string `json:"sources"`
		Metrics    any               `json:"metrics"`
	}
	now := time.Now().UTC().Format(time.RFC3339)

	switch {
	case assetType == "MF" && amfiCode != "":
		m, err := services.FetchMFMetrics(amfiCode, 55*time.Second, manualGrowwSlug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fetch MF metrics: "+err.Error())
			return
		}
		_ = services.SaveScore(r.Context(), h.pool, instrID, "mf", m.Zero1Score, m)

		sources := map[string]string{
			"NAV history (returns, Sharpe, drawdown)": "https://api.mfapi.in/mf/" + amfiCode,
		}
		if m.GrowwDataOK && m.GrowwSlug != "" {
			sources["Live fund data (AUM, TER, Beta, Alpha, Sortino, ranks)"] = "https://groww.in/mutual-funds/" + m.GrowwSlug
		} else {
			sources["Groww fund page (Beta/AUM/TER fallback to hardcoded)"] = "https://groww.in/mutual-funds?search=" + url.QueryEscape(m.SchemeName)
		}

		writeJSON(w, http.StatusOK, response{Kind: "mf", ComputedAt: now, Metrics: m, Sources: sources})

	case assetType == "ETF" || assetType == "US_FUND" || assetType == "METAL" || assetType == "GOLD" || (assetType == "MF" && amfiCode == ""):
		m, err := services.FetchETFMetrics(r.Context(), instrID, yahooSymbol, h.pool, 55*time.Second)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fetch ETF metrics: "+err.Error())
			return
		}
		_ = services.SaveScore(r.Context(), h.pool, instrID, "etf", m.Zero1Score, m)
		sources := map[string]string{}
		if assetType == "US_FUND" {
			sources["Yahoo Finance"] = "https://finance.yahoo.com/quote/" + yahooSymbol + "/"
		} else {
			ticker := strings.TrimSuffix(strings.TrimSuffix(yahooSymbol, ".NS"), ".BO")
			effectiveTickertapeSlug := manualTickertapeSlug
			if effectiveTickertapeSlug == "" {
				effectiveTickertapeSlug = m.TickertapeSlug
			}
			if effectiveTickertapeSlug != "" {
				sources["Tickertape"] = "https://www.tickertape.in" + effectiveTickertapeSlug
			} else if ticker != "" {
				sources["NSE India"] = "https://www.nseindia.com/get-quote/equity/" + ticker
			}
		}
		writeJSON(w, http.StatusOK, response{Kind: "etf", ComputedAt: now, Metrics: m, Sources: sources})

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
		ticker := strings.TrimSuffix(strings.TrimSuffix(yahooSymbol, ".NS"), ".BO")
		stockSources := map[string]string{
			"Screener.in": "https://www.screener.in/company/" + ticker + "/",
		}
		if manualTickertapeSlug != "" {
			stockSources["Tickertape"] = "https://www.tickertape.in" + manualTickertapeSlug
		} else {
			stockSources["NSE India"] = "https://www.nseindia.com/get-quote/equity/" + ticker
			stockSources["Search on Tickertape"] = "https://www.tickertape.in/stocks?q=" + url.QueryEscape(ticker)
		}
		writeJSON(w, http.StatusOK, response{Kind: "stock", ComputedAt: now, Metrics: m, Sources: stockSources})

	default:
		writeError(w, http.StatusUnprocessableEntity, "scorecard not available for asset type: "+assetType)
	}
}

// RefreshScores streams SSE progress while running the unified pipeline:
// for each fund — fetch metrics → run AI analysis — all 3 concurrent max.
// Body: { "risk_profile": "aggressive", "goal": "...", "horizon": "..." }
func (h *SignalHandler) RefreshScores(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RiskProfile string `json:"risk_profile"`
		Goal        string `json:"goal"`
		Horizon     string `json:"horizon"`
	}
	_ = decodeJSON(r, &req)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)

	emit := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Scores-only mode: no risk profile provided, just refresh scores via legacy path.
	if req.RiskProfile == "" {
		progressCh := make(chan services.RefreshProgressEvent, 32)
		doneCh := make(chan services.RefreshResult, 1)
		go func() {
			result := services.RefreshAllScores(r.Context(), h.pool, 45*time.Second, progressCh)
			close(progressCh)
			doneCh <- result
		}()
		for evt := range progressCh {
			emit(evt)
		}
		result := <-doneCh
		emit(map[string]any{"type": "done", "success": result.Success, "total": result.Total, "signals_generated": false})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	cfg, err := h.loadConfig(r.Context())
	if err != nil || cfg == nil {
		emit(map[string]any{"type": "error", "message": "AI not configured — set provider in Settings"})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	profile := services.InvestorProfile{
		Goal:        sanitise(req.Goal, "Long-term wealth creation"),
		Horizon:     sanitise(req.Horizon, "5+ years"),
		RiskProfile: toRiskProfile(req.RiskProfile),
	}

	// Unified pipeline: fetch → AI per fund, 3 concurrent max.
	pipelineCh := make(chan services.PipelineEvent, 64)
	doneCh := make(chan *services.HoldingsSignalsResult, 1)
	errCh := make(chan error, 1)

	go func() {
		result, err := h.signal.RefreshAndAnalyse(r.Context(), profile, *cfg, 50*time.Second, pipelineCh)
		close(pipelineCh)
		if err != nil {
			errCh <- err
		} else {
			doneCh <- result
		}
	}()

	for evt := range pipelineCh {
		emit(evt)
	}

	select {
	case signals := <-doneCh:
		_ = h.signal.SaveSignals(r.Context(), string(profile.RiskProfile), signals.Signals)
		emit(map[string]any{"type": "done", "signals_generated": true, "total": len(signals.Signals)})
	case err := <-errCh:
		emit(map[string]any{"type": "done", "signals_generated": false, "signal_error": err.Error()})
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// GetMFMetrics is kept for backward compatibility; delegates to GetInstrumentMetrics.
func (h *SignalHandler) GetMFMetrics(w http.ResponseWriter, r *http.Request) {
	h.GetInstrumentMetrics(w, r)
}

// GetStockFinancials fetches annual income statement, balance sheet, cash flow and shareholding
// from Yahoo Finance. Called lazily by the frontend when the user opens financial tabs.
func (h *SignalHandler) GetStockFinancials(w http.ResponseWriter, r *http.Request) {
	instrID, err := strconv.ParseInt(chi.URLParam(r, "instrument_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instrument_id")
		return
	}
	var yahooSymbol string
	if err := h.pool.QueryRow(r.Context(), `SELECT COALESCE(yahoo_symbol,'') FROM instruments WHERE id = $1`, instrID).Scan(&yahooSymbol); err != nil || yahooSymbol == "" {
		writeError(w, http.StatusNotFound, "instrument not found or no Yahoo symbol")
		return
	}
	f, err := services.FetchStockFinancials(yahooSymbol, 30*time.Second)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fetch financials: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// SaveGrowwSlug stores a manually-specified Groww URL slug for an instrument.
// Body: { "slug": "quant-small-cap-fund-direct-plan-growth" }
// Also accepts a full URL: { "slug": "https://groww.in/mutual-funds/quant-..." }
func (h *SignalHandler) SaveGrowwSlug(w http.ResponseWriter, r *http.Request) {
	instrID, err := strconv.ParseInt(chi.URLParam(r, "instrument_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instrument_id")
		return
	}
	var body struct {
		Slug string `json:"slug"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// Accept full URL or just the slug.
	slug := strings.TrimSpace(body.Slug)
	slug = strings.TrimPrefix(slug, "https://groww.in/mutual-funds/")
	slug = strings.TrimPrefix(slug, "http://groww.in/mutual-funds/")
	slug = strings.Trim(slug, "/")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	_, err = h.pool.Exec(r.Context(),
		`UPDATE instruments SET groww_slug = $1 WHERE id = $2`, slug, instrID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save groww slug: "+err.Error())
		return
	}
	// Invalidate cached score so next fetch uses the new slug.
	_, _ = h.pool.Exec(r.Context(),
		`DELETE FROM instrument_scores WHERE instrument_id = $1`, instrID)
	writeJSON(w, http.StatusOK, map[string]string{"slug": slug})
}

// SaveTickertapeSlug stores a manually-specified Tickertape URL slug for an ETF or stock.
// Body: { "slug": "/etfs/nippon-india-silver-etf-NETFS" }
// Also accepts a full URL: { "slug": "https://www.tickertape.in/etfs/..." }
func (h *SignalHandler) SaveTickertapeSlug(w http.ResponseWriter, r *http.Request) {
	instrID, err := strconv.ParseInt(chi.URLParam(r, "instrument_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instrument_id")
		return
	}
	var body struct {
		Slug string `json:"slug"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	slug = strings.TrimPrefix(slug, "https://www.tickertape.in")
	slug = strings.TrimPrefix(slug, "http://www.tickertape.in")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	_, err = h.pool.Exec(r.Context(),
		`UPDATE instruments SET tickertape_slug = $1 WHERE id = $2`, slug, instrID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save tickertape slug: "+err.Error())
		return
	}
	_, _ = h.pool.Exec(r.Context(),
		`DELETE FROM instrument_scores WHERE instrument_id = $1`, instrID)
	writeJSON(w, http.StatusOK, map[string]string{"slug": slug})
}

// buildSourcesFromCache reconstructs the sources map from cached metrics JSON so the
// full-analysis panel always shows data source links, even on cache hits.
func buildSourcesFromCache(ctx context.Context, pool *pgxpool.Pool, instrID int64, cached *services.CachedScore) map[string]string {
	sources := map[string]string{}

	// Read asset_type and manual tickertape_slug from instruments table.
	var assetType, manualTickertapeSlug string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(asset_type,''), COALESCE(tickertape_slug,'') FROM instruments WHERE id = $1`, instrID).
		Scan(&assetType, &manualTickertapeSlug)

	switch cached.Kind {
	case "mf":
		var m struct {
			AMFICode    string `json:"amfi_code"`
			GrowwDataOK bool   `json:"groww_data_ok"`
			GrowwSlug   string `json:"groww_slug"`
			SchemeName  string `json:"scheme_name"`
		}
		if json.Unmarshal(cached.MetricsJSON, &m) == nil {
			if m.AMFICode != "" {
				sources["NAV history (returns, Sharpe, drawdown)"] = "https://api.mfapi.in/mf/" + m.AMFICode
			}
			if m.GrowwDataOK && m.GrowwSlug != "" {
				sources["Live fund data (AUM, TER, Beta, Alpha, Sortino, ranks)"] = "https://groww.in/mutual-funds/" + m.GrowwSlug
			} else if m.SchemeName != "" {
				sources["Groww fund page (Beta/AUM/TER fallback to hardcoded)"] = "https://groww.in/mutual-funds?search=" + url.QueryEscape(m.SchemeName)
			}
		}

	case "etf":
		var m struct {
			Symbol         string `json:"symbol"`
			TickertapeSlug string `json:"tickertape_slug"`
		}
		if json.Unmarshal(cached.MetricsJSON, &m) == nil && m.Symbol != "" {
			if assetType == "US_FUND" || (!strings.HasSuffix(m.Symbol, ".NS") && !strings.HasSuffix(m.Symbol, ".BO")) {
				sources["Yahoo Finance"] = "https://finance.yahoo.com/quote/" + m.Symbol + "/"
			} else {
				effectiveSlug := manualTickertapeSlug
				if effectiveSlug == "" {
					effectiveSlug = m.TickertapeSlug
				}
				if effectiveSlug != "" {
					sources["Tickertape"] = "https://www.tickertape.in" + effectiveSlug
				} else {
					ticker := strings.TrimSuffix(strings.TrimSuffix(m.Symbol, ".NS"), ".BO")
					sources["NSE India"] = "https://www.nseindia.com/get-quote/equity/" + ticker
				}
			}
		}

	case "stock":
		var m struct {
			Symbol string `json:"symbol"`
		}
		if json.Unmarshal(cached.MetricsJSON, &m) == nil && m.Symbol != "" {
			ticker := strings.TrimSuffix(strings.TrimSuffix(m.Symbol, ".NS"), ".BO")
			sources["Screener.in"] = "https://www.screener.in/company/" + ticker + "/"
			if manualTickertapeSlug != "" {
				sources["Tickertape"] = "https://www.tickertape.in" + manualTickertapeSlug
			} else if ticker != "" && (strings.HasSuffix(m.Symbol, ".NS") || strings.HasSuffix(m.Symbol, ".BO")) {
				sources["NSE India"] = "https://www.nseindia.com/get-quote/equity/" + ticker
				sources["Search on Tickertape"] = "https://www.tickertape.in/stocks?q=" + url.QueryEscape(ticker)
			}
		}
	}

	return sources
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
