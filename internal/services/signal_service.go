package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Provider constants ───────────────────────────────────────────────────────

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderAntigravity = "antigravity"
)

// DefaultModel returns the default model ID for a given provider.
func DefaultModel(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return "gpt-4o"
	case ProviderAntigravity:
		return "gemini-3.1-pro-preview"
	default:
		return "claude-sonnet-4-6"
	}
}

const signalMaxTokens = 8192

// AIConfig holds the provider, API key, and model loaded from DB.
type AIConfig struct {
	Provider string
	APIKey   string
	Model    string // if empty, DefaultModel(Provider) is used
}

func (c AIConfig) model() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel(c.Provider)
}

// RiskProfile is the investor's risk appetite.
type RiskProfile string

const (
	RiskConservative RiskProfile = "conservative"
	RiskModerate     RiskProfile = "moderate"
	RiskAggressive   RiskProfile = "aggressive"
)

// InvestorProfile is injected into the system prompt placeholders.
type InvestorProfile struct {
	Name        string
	Goal        string
	Horizon     string
	RiskProfile RiskProfile
}

// SignalService builds portfolio payloads and streams AI analysis.
type SignalService struct {
	pool *pgxpool.Pool
}

func NewSignalService(pool *pgxpool.Pool) *SignalService {
	return &SignalService{pool: pool}
}

// ─── Portfolio payload builder ────────────────────────────────────────────────

type signalLot struct {
	Date   string  `json:"date"`
	Units  float64 `json:"units"`
	Price  float64 `json:"price"`
	Amount float64 `json:"amount_invested"`
}

type signalHolding struct {
	InstrumentID      int64       `json:"instrument_id"`
	InstrumentType    string      `json:"instrument_type"`
	Name              string      `json:"name"`
	Exchange          string      `json:"exchange,omitempty"`
	ISIN              string      `json:"isin,omitempty"`
	AMFICode          string      `json:"amfi_code,omitempty"`
	PurchaseLots      []signalLot `json:"purchase_lots"`
	CurrentPrice      float64     `json:"current_price"`
	CurrentValue      float64     `json:"current_value"`
	UnrealisedGainAbs float64     `json:"unrealised_gain_abs"`
	UnrealisedGainPct float64     `json:"unrealised_gain_pct"`
	ExitLoadSchedule  any         `json:"exit_load_schedule"`
	SIPActive         bool        `json:"sip_active"`
	YahooSymbol       string      `json:"yahoo_symbol,omitempty"`
	GrowwSlug         string      `json:"-"` // internal only, not sent to AI
	// Scorecard metrics — one of these is populated depending on asset_type.
	MFMetrics    *MFMetrics    `json:"mf_metrics,omitempty"`
	ETFMetrics   *ETFMetrics   `json:"etf_metrics,omitempty"`
	StockMetrics *StockMetrics `json:"stock_metrics,omitempty"`
}

// ─── Structured holdings signals ──────────────────────────────────────────────

// HoldingSignal is one AI decision for a single holding.
type HoldingSignal struct {
	InstrumentID    int64    `json:"instrument_id"`
	InstrumentName  string   `json:"instrument_name"`
	Action          string   `json:"action"`           // BUY_MORE | HOLD | PARTIAL_SELL | BOOK_PROFIT
	Confidence      int      `json:"confidence"`        // 55–95
	Reason          string   `json:"reason"`            // 2–3 sentence rationale
	KeyPoints       []string `json:"key_points"`        // 4–5 data-backed bullet points
	QualitativeNote string   `json:"qualitative_note"`  // what numbers can't capture
	RecentEvents    string   `json:"recent_events,omitempty"`  // news/events from AI training knowledge
	EventsImpact    string   `json:"events_impact,omitempty"`  // "positive" | "negative" | "neutral"
	TaxNote         string   `json:"tax_note,omitempty"`
	Zero1Score      int      `json:"zero1_score,omitempty"`
}

// HoldingsSignalsResult is what the holdings endpoint returns.
type HoldingsSignalsResult struct {
	Signals     []HoldingSignal `json:"signals"`
	RiskProfile string          `json:"risk_profile"`
	GeneratedAt *string         `json:"generated_at,omitempty"` // ISO timestamp
}

// SaveSignals upserts a full set of signals for a given risk profile, replacing
// any previous results for instruments no longer in the current set.
func (s *SignalService) SaveSignals(ctx context.Context, riskProfile string, signals []HoldingSignal) error {
	if len(signals) == 0 {
		return nil
	}

	// Collect the instrument IDs we are about to write.
	ids := make([]int64, len(signals))
	for i, sig := range signals {
		ids[i] = sig.InstrumentID
	}

	// Delete stale signals for this profile (instruments no longer in the set).
	_, err := s.pool.Exec(ctx,
		`DELETE FROM signal_results WHERE risk_profile = $1 AND instrument_id <> ALL($2)`,
		riskProfile, ids,
	)
	if err != nil {
		return fmt.Errorf("delete stale signals: %w", err)
	}

	// Upsert each signal.
	for _, sig := range signals {
		kpJSON, _ := json.Marshal(sig.KeyPoints)
		_, err := s.pool.Exec(ctx, `
			INSERT INTO signal_results
			       (instrument_id, risk_profile, action, confidence, reason, key_points, qualitative_note, tax_note, zero1_score, generated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
			ON CONFLICT (instrument_id, risk_profile)
			DO UPDATE SET action            = EXCLUDED.action,
			              confidence        = EXCLUDED.confidence,
			              reason            = EXCLUDED.reason,
			              key_points        = EXCLUDED.key_points,
			              qualitative_note  = EXCLUDED.qualitative_note,
			              tax_note          = EXCLUDED.tax_note,
			              zero1_score       = EXCLUDED.zero1_score,
			              generated_at      = EXCLUDED.generated_at
		`, sig.InstrumentID, riskProfile, sig.Action, sig.Confidence, sig.Reason,
			string(kpJSON), sig.QualitativeNote, sig.TaxNote, sig.Zero1Score)
		if err != nil {
			return fmt.Errorf("upsert signal for %d: %w", sig.InstrumentID, err)
		}
	}
	return nil
}

// LoadSignals reads stored signals for the given risk profile.
// Returns an empty result (no error) if none are stored yet.
func (s *SignalService) LoadSignals(ctx context.Context, riskProfile string) (*HoldingsSignalsResult, error) {
	// Only return signals for instruments that are still actively held.
	// The net-units subquery mirrors the FIFO check in BuildPortfolioPayload,
	// preventing stale rows (fully-sold funds) from surfacing.
	rows, err := s.pool.Query(ctx, `
		SELECT sr.instrument_id, i.name, sr.action, sr.confidence, sr.reason,
		       COALESCE(sr.key_points,'[]'), COALESCE(sr.qualitative_note,''),
		       sr.tax_note, sr.zero1_score,
		       to_char(sr.generated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS generated_at
		  FROM signal_results sr
		  JOIN instruments i ON sr.instrument_id = i.id
		 WHERE sr.risk_profile = $1
		   AND sr.instrument_id IN (
		         SELECT instrument_id
		           FROM transactions
		          WHERE transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		          GROUP BY instrument_id
		         HAVING SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS')
		                         THEN quantity::float ELSE 0 END)
		              - SUM(CASE WHEN transaction_type IN ('SELL','SWITCH_OUT')
		                         THEN quantity::float ELSE 0 END) >= 0.5
		           AND SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN')
		                         THEN amount::float ELSE 0 END) > 0
		       )
		 ORDER BY sr.generated_at DESC, i.name
	`, riskProfile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	signals := []HoldingSignal{} // never nil — serialises as [] not null
	var generatedAt *string
	for rows.Next() {
		var sig HoldingSignal
		var ga, kpJSON string
		if err := rows.Scan(&sig.InstrumentID, &sig.InstrumentName, &sig.Action,
			&sig.Confidence, &sig.Reason, &kpJSON, &sig.QualitativeNote,
			&sig.TaxNote, &sig.Zero1Score, &ga); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(kpJSON), &sig.KeyPoints)
		if sig.KeyPoints == nil {
			sig.KeyPoints = []string{}
		}
		if generatedAt == nil {
			generatedAt = &ga
		}
		signals = append(signals, sig)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &HoldingsSignalsResult{
		Signals:     signals,
		RiskProfile: riskProfile,
		GeneratedAt: generatedAt,
	}, nil
}

type signalPortfolioPayload struct {
	AnalysisDate     string          `json:"analysis_date"`
	FinancialYear    string          `json:"financial_year"`
	LTCGBookedThisFY float64         `json:"ltcg_booked_this_fy"`
	Holdings         []signalHolding `json:"holdings"`
}

type fifoActiveLot struct {
	date  string
	units float64
	price float64
	amt   float64
}

func assetTypeToInstrumentType(assetType string) string {
	switch assetType {
	case "STOCK":
		return "equity_stock"
	case "MF":
		return "equity_mf"
	case "ETF":
		return "equity_mf"
	case "BOND":
		return "debt_mf"
	case "GOLD", "METAL":
		return "gold_etf"
	case "US_FUND":
		return "equity_mf"
	default:
		return "equity_mf"
	}
}

// buildPortfolioBase builds the payload with FIFO lots, prices, and LTCG data
// but without any metric enrichment. Call enrichHoldingsLive or enrichHoldingsFromCache afterward.
func (s *SignalService) buildPortfolioBase(ctx context.Context) (*signalPortfolioPayload, error) {
	// Only include instruments with ≥ 0.5 net units remaining — mirrors the
	// portfolio holdings API filter and excludes fully-exited positions.
	rows, err := s.pool.Query(ctx, `
		SELECT t.instrument_id, i.name, i.isin, i.asset_type, i.exchange, i.amfi_code,
		       COALESCE(i.yahoo_symbol, ''), COALESCE(i.groww_slug, ''), COALESCE(i.currency, 'INR'),
		       t.transaction_date::text, t.transaction_type,
		       t.quantity::float, t.price::float, t.amount::float
		  FROM transactions t
		  JOIN instruments i ON t.instrument_id = i.id
		 WHERE t.transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		   AND t.instrument_id IN (
		         SELECT instrument_id
		           FROM transactions
		          WHERE transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		          GROUP BY instrument_id
		         HAVING SUM(
		           CASE WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS')
		                THEN quantity::float ELSE -quantity::float END
		         ) >= 0.5
		       )
		 ORDER BY t.instrument_id, t.transaction_date, t.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query transactions: %w", err)
	}
	defer rows.Close()

	type instrMeta struct{ name, isin, assetType, exchange, amfiCode, yahooSymbol, growwSlug, currency string }
	type instrState struct {
		meta instrMeta
		lots []fifoActiveLot
	}
	states := map[int64]*instrState{}

	for rows.Next() {
		var instrID int64
		var name, assetType, yahooSymbol, growwSlug, currency, txDate, txType string
		var isin, exchange, amfiCode *string
		var qty, price, amount float64
		if err := rows.Scan(&instrID, &name, &isin, &assetType, &exchange, &amfiCode, &yahooSymbol, &growwSlug, &currency, &txDate, &txType, &qty, &price, &amount); err != nil {
			return nil, err
		}
		st, ok := states[instrID]
		if !ok {
			st = &instrState{meta: instrMeta{
				name:        name,
				isin:        derefStr(isin),
				assetType:   assetType,
				exchange:    derefStr(exchange),
				amfiCode:    derefStr(amfiCode),
				yahooSymbol: yahooSymbol,
				growwSlug:   growwSlug,
				currency:    currency,
			}}
			states[instrID] = st
		}
		switch txType {
		case "BUY", "SWITCH_IN":
			if qty > 0 {
				st.lots = append(st.lots, fifoActiveLot{date: txDate, units: qty, price: price, amt: amount})
			}
		case "BONUS":
			if qty > 0 {
				st.lots = append(st.lots, fifoActiveLot{date: txDate, units: qty})
			}
		case "SELL", "SWITCH_OUT":
			remaining := qty
			for len(st.lots) > 0 && remaining > 0.000001 {
				if st.lots[0].units <= remaining+0.000001 {
					remaining -= st.lots[0].units
					st.lots = st.lots[1:]
				} else {
					st.lots[0].units -= remaining
					remaining = 0
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Latest prices
	activeIDs := make([]int64, 0, len(states))
	for id, st := range states {
		if len(st.lots) > 0 {
			activeIDs = append(activeIDs, id)
		}
	}
	priceMap := map[int64]float64{}
	if len(activeIDs) > 0 {
		prows, err := s.pool.Query(ctx, `
			SELECT p.instrument_id, p.nav_price::float
			  FROM prices p
			  JOIN (
			    SELECT instrument_id, MAX(price_date) AS max_date
			      FROM prices WHERE instrument_id = ANY($1)
			     GROUP BY instrument_id
			  ) latest ON p.instrument_id = latest.instrument_id AND p.price_date = latest.max_date
			 WHERE p.instrument_id = ANY($1)
		`, activeIDs)
		if err != nil {
			return nil, err
		}
		defer prows.Close()
		for prows.Next() {
			var id int64
			var nav float64
			if err := prows.Scan(&id, &nav); err != nil {
				return nil, err
			}
			priceMap[id] = nav
		}
	}

	// LTCG booked this FY (best-effort approximation)
	now := time.Now().In(time.FixedZone("IST", 5*60*60+30*60))
	fyStart := time.Date(now.Year(), time.April, 1, 0, 0, 0, 0, now.Location())
	if now.Before(fyStart) {
		fyStart = fyStart.AddDate(-1, 0, 0)
	}
	var ltcgBooked float64
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(t.amount::float - (t.quantity::float * avg_cost.cost)), 0)
		  FROM transactions t
		  JOIN (
		    SELECT instrument_id, SUM(amount::float)/NULLIF(SUM(quantity::float),0) AS cost
		      FROM transactions WHERE transaction_type IN ('BUY','SWITCH_IN') GROUP BY instrument_id
		  ) avg_cost ON t.instrument_id = avg_cost.instrument_id
		 WHERE t.transaction_type IN ('SELL','SWITCH_OUT')
		   AND t.transaction_date >= $1 AND t.amount > 0
	`, fyStart.Format("2006-01-02")).Scan(&ltcgBooked)
	if ltcgBooked < 0 {
		ltcgBooked = 0
	}

	// Fetch USD→INR rate for currency normalisation (nav_price for USD instruments is stored in INR).
	usdInrRate := 84.0 // fallback
	_ = s.pool.QueryRow(ctx, `
		SELECT nav_price::float FROM prices
		 WHERE instrument_id = (SELECT id FROM instruments WHERE yahoo_symbol = 'INR=X' LIMIT 1)
		 ORDER BY price_date DESC LIMIT 1
	`).Scan(&usdInrRate)
	if usdInrRate < 50 || usdInrRate > 200 {
		usdInrRate = 84.0 // sanity fallback
	}

	holdings := make([]signalHolding, 0, len(activeIDs))
	for _, id := range activeIDs {
		st := states[id]
		if len(st.lots) == 0 {
			continue
		}
		currentPrice := priceMap[id]
		isUSD := strings.EqualFold(st.meta.currency, "USD")

		var totalUnits, totalInvested float64
		lots := make([]signalLot, 0, len(st.lots))
		for _, l := range st.lots {
			totalUnits += l.units
			totalInvested += l.amt
			if l.amt > 0 || l.units > 0 {
				lots = append(lots, signalLot{Date: l.date, Units: r2(l.units), Price: r2(l.price), Amount: r2(l.amt)})
			}
		}
		// Skip instruments that have been fully exited — floating-point FIFO
		// residuals can leave tiny non-zero lot quantities.
		if totalUnits < 0.001 {
			continue
		}
		// Skip holdings with no recorded cost basis (e.g. bonus-only lots).
		if totalInvested <= 0 {
			continue
		}

		// nav_price for USD instruments is stored in INR; transaction amounts are in USD.
		// Normalise invested to INR so both sides are in the same currency.
		totalInvestedINR := totalInvested
		if isUSD {
			totalInvestedINR = totalInvested * usdInrRate
		}

		currentValue := totalUnits * currentPrice // always INR (price stored in INR)
		gainAbs := currentValue - totalInvestedINR
		var gainPct float64
		if totalInvestedINR > 0 {
			gainPct = gainAbs / totalInvestedINR * 100
		}
		holdings = append(holdings, signalHolding{
			InstrumentID:      id,
			InstrumentType:    assetTypeToInstrumentType(st.meta.assetType),
			Name:              st.meta.name,
			ISIN:              st.meta.isin,
			Exchange:          st.meta.exchange,
			AMFICode:          st.meta.amfiCode,
			YahooSymbol:       st.meta.yahooSymbol,
			GrowwSlug:         st.meta.growwSlug,
			PurchaseLots:      lots,
			CurrentPrice:      r2(currentPrice),
			CurrentValue:      r2(currentValue),
			UnrealisedGainAbs: r2(gainAbs),
			UnrealisedGainPct: r2(gainPct),
			ExitLoadSchedule:  nil,
			SIPActive:         false,
		})
	}

	fy := fmt.Sprintf("%d-%02d", fyStart.Year(), fyStart.Year()%100+1)
	return &signalPortfolioPayload{
		AnalysisDate:     now.Format("2006-01-02"),
		FinancialYear:    fy,
		LTCGBookedThisFY: r2(ltcgBooked),
		Holdings:         holdings,
	}, nil
}

// BuildPortfolioPayload builds the full payload with live metric fetches from external APIs.
func (s *SignalService) BuildPortfolioPayload(ctx context.Context) (*signalPortfolioPayload, error) {
	payload, err := s.buildPortfolioBase(ctx)
	if err != nil {
		return nil, err
	}
	enrichHoldingsLive(ctx, s.pool, payload.Holdings)
	return payload, nil
}

// BuildPortfolioPayloadFromCache builds the portfolio payload using metrics already
// cached in instrument_scores — no external API calls. Call this after RefreshAllScores
// to avoid re-fetching what was just fetched.
func (s *SignalService) BuildPortfolioPayloadFromCache(ctx context.Context) (*signalPortfolioPayload, error) {
	payload, err := s.buildPortfolioBase(ctx)
	if err != nil {
		return nil, err
	}
	enrichHoldingsFromCache(ctx, s.pool, payload.Holdings)
	return payload, nil
}

// enrichHoldingsLive fetches live metrics from external APIs and attaches them to holdings.
func enrichHoldingsLive(ctx context.Context, pool *pgxpool.Pool, holdings []signalHolding) {
	amfiCodes := make([]string, 0)
	type stockETFJob struct {
		idx         int
		id          int64
		yahooSymbol string
		assetType   string
	}
	var stockETFJobs []stockETFJob

	for i := range holdings {
		switch holdings[i].InstrumentType {
		case "equity_mf":
			if holdings[i].AMFICode != "" {
				amfiCodes = append(amfiCodes, holdings[i].AMFICode)
			}
		case "equity_stock":
			if holdings[i].YahooSymbol != "" {
				stockETFJobs = append(stockETFJobs, stockETFJob{i, holdings[i].InstrumentID, holdings[i].YahooSymbol, "STOCK"})
			}
		}
	}
	for i := range holdings {
		if holdings[i].YahooSymbol != "" && holdings[i].AMFICode == "" &&
			(holdings[i].InstrumentType == "equity_mf" || holdings[i].InstrumentType == "gold_etf") {
			stockETFJobs = append(stockETFJobs, stockETFJob{i, holdings[i].InstrumentID, holdings[i].YahooSymbol, "ETF"})
		}
	}
	if len(amfiCodes) > 0 {
		metricsMap := FetchMFMetricsBatch(amfiCodes, 20*time.Second)
		for i := range holdings {
			if m, ok := metricsMap[holdings[i].AMFICode]; ok {
				holdings[i].MFMetrics = m
			}
		}
	}
	for _, job := range stockETFJobs {
		switch job.assetType {
		case "STOCK":
			if m, err := FetchStockMetrics(ctx, job.id, job.yahooSymbol, pool, 20*time.Second); err == nil {
				holdings[job.idx].StockMetrics = m
			}
		case "ETF":
			if m, err := FetchETFMetrics(ctx, job.id, job.yahooSymbol, pool, 20*time.Second); err == nil {
				holdings[job.idx].ETFMetrics = m
			}
		}
	}
}

// enrichHoldingsFromCache reads instrument_scores and attaches cached metrics to each
// holding — no external API calls.
func enrichHoldingsFromCache(ctx context.Context, pool *pgxpool.Pool, holdings []signalHolding) {
	for i := range holdings {
		var kind string
		var metricsJSON []byte
		err := pool.QueryRow(ctx,
			`SELECT kind, metrics_json FROM instrument_scores WHERE instrument_id = $1`,
			holdings[i].InstrumentID).Scan(&kind, &metricsJSON)
		if err != nil || len(metricsJSON) == 0 {
			continue
		}
		switch kind {
		case "mf":
			var m MFMetrics
			if json.Unmarshal(metricsJSON, &m) == nil {
				holdings[i].MFMetrics = &m
			}
		case "etf":
			var m ETFMetrics
			if json.Unmarshal(metricsJSON, &m) == nil {
				holdings[i].ETFMetrics = &m
			}
		case "stock":
			var m StockMetrics
			if json.Unmarshal(metricsJSON, &m) == nil {
				holdings[i].StockMetrics = &m
			}
		}
	}
}

func r2(v float64) float64 { return math.Round(v*100) / 100 }

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ─── Public streaming methods ─────────────────────────────────────────────────

func (s *SignalService) StreamHoldingsAnalysis(ctx context.Context, w http.ResponseWriter, payload *signalPortfolioPayload, profile InvestorProfile, cfg AIConfig) error {
	payloadBytes, _ := json.MarshalIndent(payload, "", "  ")
	prompts := s.loadPromptsFromDB(ctx)
	system := buildSystemPrompt(profile, prompts)
	user := fmt.Sprintf(
		"Please analyse my investment portfolio.\n\nProfile:\n- Goal: %s\n- Horizon: %s\n\nPortfolio data:\n```json\n%s\n```",
		profile.Goal, profile.Horizon, payloadBytes,
	)
	return streamFromProvider(ctx, w, cfg, system, user)
}

func (s *SignalService) StreamStockAnalysis(ctx context.Context, w http.ResponseWriter, query string, profile InvestorProfile, cfg AIConfig) error {
	prompts := s.loadPromptsFromDB(ctx)
	system := buildStockAnalysisPrompt(profile.RiskProfile, prompts)
	user := fmt.Sprintf("Please provide a complete investment analysis for: %s\n\nInvestor risk profile: %s", query, profile.RiskProfile)
	return streamFromProvider(ctx, w, cfg, system, user)
}

// FetchHoldingsSignals sends full portfolio data + scorecard to AI and lets it decide
// the action. The score-based suggestion is provided as a reference signal, not a mandate.
// AI integrates quantitative data + qualitative knowledge (fund reputation, category
// macro, manager track record, recent news from its training knowledge) to decide.
func (s *SignalService) FetchHoldingsSignals(ctx context.Context, payload *signalPortfolioPayload, profile InvestorProfile, cfg AIConfig) (*HoldingsSignalsResult, error) {
	// Post-process MF category relative ranks for peer context.
	mfByCode := make(map[string]*MFMetrics)
	for i := range payload.Holdings {
		if payload.Holdings[i].MFMetrics != nil && payload.Holdings[i].AMFICode != "" {
			mfByCode[payload.Holdings[i].AMFICode] = payload.Holdings[i].MFMetrics
		}
	}
	if len(mfByCode) > 0 {
		PostProcessMFBatch(mfByCode)
	}

	// Build score + STCG context per holding — sent as reference signals to AI.
	type hintEntry struct {
		score   int
		action  string // score-derived suggestion
		hasSTCG bool
		ctx     AssetContext
	}
	hints := make(map[int64]hintEntry, len(payload.Holdings))
	for _, h := range payload.Holdings {
		var score int
		var assetCtx AssetContext
		switch {
		case h.MFMetrics != nil:
			score = h.MFMetrics.Zero1Score
			assetCtx = AssetContext{RelativeRank: h.MFMetrics.RelativeRank, CategoryBearish: h.MFMetrics.CategoryBearish}
		case h.ETFMetrics != nil:
			score = h.ETFMetrics.Zero1Score
			assetCtx = AssetContext{RelativeRank: h.ETFMetrics.RelativeRank, CategoryBearish: h.ETFMetrics.CategoryBearish}
		case h.StockMetrics != nil:
			score = h.StockMetrics.Zero1Score
			assetCtx = AssetContext{RelativeRank: h.StockMetrics.RelativeRank, CategoryBearish: h.StockMetrics.SectorBearish}
		}
		cutoff := time.Now().AddDate(-1, 0, 0)
		hasSTCG := false
		for _, lot := range h.PurchaseLots {
			if t, err := time.Parse("2006-01-02", lot.Date); err == nil && t.After(cutoff) {
				hasSTCG = true
				break
			}
		}
		hints[h.InstrumentID] = hintEntry{
			score:   score,
			action:  scoreToAction(score, profile.RiskProfile, assetCtx),
			hasSTCG: hasSTCG,
			ctx:     assetCtx,
		}
	}

	// Build score reference summary for the user message.
	var scoreSummary strings.Builder
	for id, h := range hints {
		scoreSummary.WriteString(fmt.Sprintf("  instrument_id=%d  score=%d  suggested=%s  relative_rank=%d",
			id, h.score, h.action, h.ctx.RelativeRank))
		if h.hasSTCG {
			scoreSummary.WriteString("  [has STCG lots — note in tax_note]")
		}
		if h.ctx.CategoryBearish {
			scoreSummary.WriteString("  [category/sector broadly bearish]")
		}
		scoreSummary.WriteString("\n")
	}

	payloadBytes, _ := json.MarshalIndent(payload, "", "  ")
	system := buildSignalsJSONPrompt(profile)
	user := fmt.Sprintf(
		"INVESTOR PROFILE:\n- Goal: %s\n- Horizon: %s\n- Risk: %s\n\n"+
			"QUANTITATIVE SCORE REFERENCE (your starting point — override with qualitative reasoning if warranted):\n%s\n\n"+
			"FULL PORTFOLIO DATA:\n```json\n%s\n```",
		profile.Goal, profile.Horizon, string(profile.RiskProfile),
		scoreSummary.String(), payloadBytes,
	)

	raw, err := callProviderJSON(ctx, cfg, system, user)
	if err != nil {
		return nil, err
	}

	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		raw = raw[strings.Index(raw, "\n")+1:]
		if idx := strings.LastIndex(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}

	var wrapper struct {
		Signals []HoldingSignal `json:"signals"`
	}
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("parse AI response: %w\nraw: %.300s", err, raw)
	}
	if wrapper.Signals == nil {
		wrapper.Signals = []HoldingSignal{}
	}

	// Validate: if AI returns an invalid action, fall back to score-derived suggestion.
	validActions := map[string]bool{"BUY_MORE": true, "HOLD": true, "PARTIAL_SELL": true, "BOOK_PROFIT": true}
	for i := range wrapper.Signals {
		if !validActions[wrapper.Signals[i].Action] {
			if h, ok := hints[wrapper.Signals[i].InstrumentID]; ok {
				wrapper.Signals[i].Action = h.action
			} else {
				wrapper.Signals[i].Action = "HOLD"
			}
		}
		// Always populate score from our computation.
		if h, ok := hints[wrapper.Signals[i].InstrumentID]; ok {
			wrapper.Signals[i].Zero1Score = h.score
		}
	}

	return &HoldingsSignalsResult{
		Signals:     wrapper.Signals,
		RiskProfile: string(profile.RiskProfile),
	}, nil
}

// PipelineEvent is the unified progress event for RefreshAndAnalyse.
// A single fund goes through: fund_start → fund_scored → fund_done (or fund_error).
type PipelineEvent struct {
	Type             string `json:"type"`                        // fund_start | fund_scored | fund_done | fund_error | all_done
	ID               int64  `json:"instrument_id"`
	Name             string `json:"name"`
	AssetType        string `json:"asset_type,omitempty"`
	Score            int    `json:"score,omitempty"`
	Action           string `json:"action,omitempty"`
	Error            string `json:"error,omitempty"`
	Success          int    `json:"success,omitempty"`           // all_done only
	Total            int    `json:"total,omitempty"`             // all_done only
	SignalsGenerated bool   `json:"signals_generated,omitempty"` // all_done only
}

// RefreshAndAnalyse is the unified pipeline: for each holding it fetches live metrics,
// saves the score, then immediately runs the AI analysis — all in one pass with a
// semaphore of 3 so at most 3 funds are in-flight (fetch + AI) at any moment.
// This replaces the old two-phase RefreshAllScores → FetchSignalsPerFund approach.
func (s *SignalService) RefreshAndAnalyse(
	ctx context.Context,
	profile InvestorProfile,
	cfg AIConfig,
	timeout time.Duration,
	progressCh chan<- PipelineEvent,
) (*HoldingsSignalsResult, error) {
	emit := func(e PipelineEvent) {
		if progressCh != nil {
			select {
			case progressCh <- e:
			default:
			}
		}
	}

	// Pre-load all holdings (FIFO lots + prices) from DB — cheap, no external calls.
	payload, err := s.buildPortfolioBase(ctx)
	if err != nil {
		return nil, fmt.Errorf("build base: %w", err)
	}
	if len(payload.Holdings) == 0 {
		return &HoldingsSignalsResult{Signals: []HoldingSignal{}, RiskProfile: string(profile.RiskProfile)}, nil
	}

	system := buildFundSignalPrompt(profile)

	type result struct {
		signal HoldingSignal
		ok     bool
	}
	results := make([]result, len(payload.Holdings))
	var mu sync.Mutex
	successCount := 0

	sem := make(chan struct{}, 3) // ← single semaphore for fetch + AI combined
	var wg sync.WaitGroup

	for i, h := range payload.Holdings {
		wg.Add(1)
		go func(idx int, holding signalHolding) {
			defer wg.Done()
			sem <- struct{}{}        // acquire — holds slot for entire fetch+AI
			defer func() { <-sem }() // release when both are done

			emit(PipelineEvent{Type: "fund_start", ID: holding.InstrumentID, Name: holding.Name, AssetType: holding.InstrumentType})

			// ── Step 1: fetch live metrics ────────────────────────────────────
			var score int
			var fetchErr error

			switch {
			case holding.AMFICode != "": // MF with AMFI code
				m, err := FetchMFMetrics(holding.AMFICode, timeout, holding.GrowwSlug)
				if err != nil {
					fetchErr = err
				} else {
					_ = SaveScore(ctx, s.pool, holding.InstrumentID, "mf", m.Zero1Score, m)
					holding.MFMetrics = m
					score = m.Zero1Score
				}

			case holding.YahooSymbol != "" && holding.InstrumentType == "equity_stock":
				m, err := FetchStockMetrics(ctx, holding.InstrumentID, holding.YahooSymbol, s.pool, timeout)
				if err != nil {
					fetchErr = err
				} else {
					_ = SaveScore(ctx, s.pool, holding.InstrumentID, "stock", m.Zero1Score, m)
					holding.StockMetrics = m
					score = m.Zero1Score
				}

			case holding.YahooSymbol != "": // ETF / metal / US fund
				m, err := FetchETFMetrics(ctx, holding.InstrumentID, holding.YahooSymbol, s.pool, timeout)
				if err != nil {
					fetchErr = err
				} else {
					_ = SaveScore(ctx, s.pool, holding.InstrumentID, "etf", m.Zero1Score, m)
					holding.ETFMetrics = m
					score = m.Zero1Score
				}

			default:
				fetchErr = fmt.Errorf("no AMFI code or Yahoo symbol for %s", holding.Name)
			}

			if fetchErr != nil {
				emit(PipelineEvent{Type: "fund_error", ID: holding.InstrumentID, Name: holding.Name, Error: fetchErr.Error()})
				return
			}

			emit(PipelineEvent{Type: "fund_scored", ID: holding.InstrumentID, Name: holding.Name, Score: score})

			// ── Step 2: AI analysis ───────────────────────────────────────────
			assetCtx := assetContextFor(&holding)
			suggestedAction := scoreToAction(score, profile.RiskProfile, assetCtx)
			hasSTCG := holdingHasSTCG(&holding)

			sig, aiErr := fetchSingleFundSignal(ctx, holding, score, suggestedAction, hasSTCG, assetCtx, profile, cfg, system)
			if aiErr != nil {
				// AI failed — fall back to score-derived signal rather than dropping the fund.
				sig = &HoldingSignal{
					InstrumentID:   holding.InstrumentID,
					InstrumentName: holding.Name,
					Action:         suggestedAction,
					Confidence:     60,
					Reason:         "AI analysis failed; action derived from quantitative score.",
					Zero1Score:     score,
				}
			}
			sig.Zero1Score = score

			mu.Lock()
			results[idx] = result{signal: *sig, ok: true}
			successCount++
			mu.Unlock()

			emit(PipelineEvent{Type: "fund_done", ID: holding.InstrumentID, Name: holding.Name, Score: score, Action: sig.Action})
		}(i, h)
	}
	wg.Wait()

	signals := make([]HoldingSignal, 0, len(payload.Holdings))
	for _, r := range results {
		if r.ok {
			signals = append(signals, r.signal)
		}
	}

	emit(PipelineEvent{Type: "all_done", Success: successCount, Total: len(payload.Holdings), SignalsGenerated: len(signals) > 0})

	return &HoldingsSignalsResult{
		Signals:     signals,
		RiskProfile: string(profile.RiskProfile),
	}, nil
}

// isETFLike returns true for asset types handled by FetchETFMetrics.
func isETFLike(instrType string) bool {
	switch instrType {
	case "gold_etf", "silver_etf", "index_etf", "sector_etf", "us_fund", "metal", "other_etf":
		return true
	}
	return false
}

// assetContextFor builds the AssetContext from a holding's already-populated metrics.
func assetContextFor(h *signalHolding) AssetContext {
	switch {
	case h.MFMetrics != nil:
		return AssetContext{RelativeRank: h.MFMetrics.RelativeRank, CategoryBearish: h.MFMetrics.CategoryBearish}
	case h.ETFMetrics != nil:
		return AssetContext{RelativeRank: h.ETFMetrics.RelativeRank, CategoryBearish: h.ETFMetrics.CategoryBearish}
	case h.StockMetrics != nil:
		return AssetContext{RelativeRank: h.StockMetrics.RelativeRank, CategoryBearish: h.StockMetrics.SectorBearish}
	}
	return AssetContext{}
}

// holdingHasSTCG returns true if any purchase lot is within the last 12 months.
func holdingHasSTCG(h *signalHolding) bool {
	cutoff := time.Now().AddDate(-1, 0, 0)
	for _, lot := range h.PurchaseLots {
		if t, err := time.Parse("2006-01-02", lot.Date); err == nil && t.After(cutoff) {
			return true
		}
	}
	return false
}

// PerFundAIProgressEvent is emitted during FetchSignalsPerFund for each fund's AI call.
type PerFundAIProgressEvent struct {
	Type           string `json:"type"`            // "ai_fund_start" | "ai_fund_done" | "ai_fund_error"
	ID             int64  `json:"instrument_id"`
	Name           string `json:"name"`
	Score          int    `json:"score,omitempty"`
	Action         string `json:"action,omitempty"`
	Error          string `json:"error,omitempty"`
}

// FetchSignalsPerFund runs one AI call per holding in parallel (max 3 concurrent).
// progressCh receives live events; pass nil to skip. Returns combined result.
func (s *SignalService) FetchSignalsPerFund(
	ctx context.Context,
	payload *signalPortfolioPayload,
	profile InvestorProfile,
	cfg AIConfig,
	progressCh chan<- PerFundAIProgressEvent,
) (*HoldingsSignalsResult, error) {
	emitAI := func(e PerFundAIProgressEvent) {
		if progressCh != nil {
			select {
			case progressCh <- e:
			default:
			}
		}
	}

	// Pre-compute score + STCG hints (same as FetchHoldingsSignals).
	mfByCode := make(map[string]*MFMetrics)
	for i := range payload.Holdings {
		if payload.Holdings[i].MFMetrics != nil && payload.Holdings[i].AMFICode != "" {
			mfByCode[payload.Holdings[i].AMFICode] = payload.Holdings[i].MFMetrics
		}
	}
	if len(mfByCode) > 0 {
		PostProcessMFBatch(mfByCode)
	}

	type hintEntry struct {
		score  int
		action string
		hasSTCG bool
		ctx    AssetContext
	}
	hints := make(map[int64]hintEntry, len(payload.Holdings))
	for _, h := range payload.Holdings {
		var score int
		var assetCtx AssetContext
		switch {
		case h.MFMetrics != nil:
			score = h.MFMetrics.Zero1Score
			assetCtx = AssetContext{RelativeRank: h.MFMetrics.RelativeRank, CategoryBearish: h.MFMetrics.CategoryBearish}
		case h.ETFMetrics != nil:
			score = h.ETFMetrics.Zero1Score
			assetCtx = AssetContext{RelativeRank: h.ETFMetrics.RelativeRank, CategoryBearish: h.ETFMetrics.CategoryBearish}
		case h.StockMetrics != nil:
			score = h.StockMetrics.Zero1Score
			assetCtx = AssetContext{RelativeRank: h.StockMetrics.RelativeRank, CategoryBearish: h.StockMetrics.SectorBearish}
		}
		cutoff := time.Now().AddDate(-1, 0, 0)
		hasSTCG := false
		for _, lot := range h.PurchaseLots {
			if t, err := time.Parse("2006-01-02", lot.Date); err == nil && t.After(cutoff) {
				hasSTCG = true
				break
			}
		}
		hints[h.InstrumentID] = hintEntry{
			score:   score,
			action:  scoreToAction(score, profile.RiskProfile, assetCtx),
			hasSTCG: hasSTCG,
			ctx:     assetCtx,
		}
	}

	system := buildFundSignalPrompt(profile)

	type result struct {
		signal HoldingSignal
		err    error
	}

	results := make([]result, len(payload.Holdings))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup

	for i, h := range payload.Holdings {
		wg.Add(1)
		go func(idx int, holding signalHolding) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			hint := hints[holding.InstrumentID]
			emitAI(PerFundAIProgressEvent{Type: "ai_fund_start", ID: holding.InstrumentID, Name: holding.Name})

			sig, err := fetchSingleFundSignal(ctx, holding, hint.score, hint.action, hint.hasSTCG, hint.ctx, profile, cfg, system)
			if err != nil {
				results[idx] = result{err: err}
				emitAI(PerFundAIProgressEvent{Type: "ai_fund_error", ID: holding.InstrumentID, Name: holding.Name, Error: err.Error()})
				return
			}
			// Always inject our computed score.
			sig.Zero1Score = hint.score
			results[idx] = result{signal: *sig}
			emitAI(PerFundAIProgressEvent{Type: "ai_fund_done", ID: holding.InstrumentID, Name: holding.Name, Score: hint.score, Action: sig.Action})
		}(i, h)
	}
	wg.Wait()

	signals := make([]HoldingSignal, 0, len(payload.Holdings))
	for _, r := range results {
		if r.err == nil {
			signals = append(signals, r.signal)
		}
	}

	return &HoldingsSignalsResult{
		Signals:     signals,
		RiskProfile: string(profile.RiskProfile),
	}, nil
}

// fetchSingleFundSignal calls the AI for one holding and returns its signal.
func fetchSingleFundSignal(
	ctx context.Context,
	h signalHolding,
	score int,
	suggestedAction string,
	hasSTCG bool,
	assetCtx AssetContext,
	profile InvestorProfile,
	cfg AIConfig,
	system string,
) (*HoldingSignal, error) {
	stcgNote := ""
	if hasSTCG {
		stcgNote = "  [has STCG lots — note 20% tax in tax_note]"
	}
	bearNote := ""
	if assetCtx.CategoryBearish {
		bearNote = "  [category/sector broadly bearish — factor into decision]"
	}

	holdingJSON, _ := json.MarshalIndent(h, "", "  ")
	user := fmt.Sprintf(
		"INVESTOR PROFILE:\n- Goal: %s\n- Horizon: %s\n- Risk: %s\n\n"+
			"QUANTITATIVE SIGNAL:\n  score=%d  suggested_action=%s  relative_rank=%d%s%s\n\n"+
			"HOLDING DATA:\n```json\n%s\n```",
		profile.Goal, profile.Horizon, string(profile.RiskProfile),
		score, suggestedAction, assetCtx.RelativeRank, stcgNote, bearNote,
		holdingJSON,
	)

	raw, err := callProviderJSON(ctx, cfg, system, user)
	if err != nil {
		return nil, err
	}

	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		raw = raw[strings.Index(raw, "\n")+1:]
		if idx := strings.LastIndex(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}

	var sig HoldingSignal
	if err := json.Unmarshal([]byte(raw), &sig); err != nil {
		return nil, fmt.Errorf("parse: %w\nraw: %.200s", err, raw)
	}

	validActions := map[string]bool{"BUY_MORE": true, "HOLD": true, "PARTIAL_SELL": true, "BOOK_PROFIT": true}
	if !validActions[sig.Action] {
		sig.Action = suggestedAction
	}
	if sig.InstrumentID == 0 {
		sig.InstrumentID = h.InstrumentID
	}
	if sig.InstrumentName == "" {
		sig.InstrumentName = h.Name
	}
	return &sig, nil
}

// buildFundSignalPrompt builds the per-fund system prompt (hardcoded — not from DB).
func buildFundSignalPrompt(profile InvestorProfile) string {
	return fmt.Sprintf(`You are a senior investment analyst specialising in Indian equity markets (BSE/NSE, SEBI), Indian Mutual Funds, and US markets.

You will receive data for ONE holding — quantitative scorecard metrics, purchase history, current value, and unrealised gain. Analyse everything thoroughly and give your best independent judgment.

INVESTOR RISK PROFILE: %s
INVESTOR GOAL: %s
INVESTOR HORIZON: %s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
WHAT TO ANALYSE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Quantitative data provided:
- zero1_score (0–100), available_max, data_gaps
- Returns: rolling 1Y/3Y avg, consistency, Sharpe, Sortino, alpha
- Risk: max drawdown, std dev, beta
- Fund ops: AUM, TER/expense ratio, tracking error (ETFs)
- Valuation: PE, PB, dividend yield, sector comparisons (stocks/ETFs)
- Position: current_value, unrealised_gain_abs, unrealised_gain_pct
- Tax context: purchase_lots dates (determine LTCG/STCG eligibility)

Apply your own knowledge:
- Fund house reputation, manager track record, style consistency
- Category/sector macro outlook and cycle positioning
- Product-specific risks (leverage decay, liquidity, capacity constraints)
- Tax implications: LTCG >12 months = 12.5%% flat, ₹1.25L/FY exempt; STCG ≤12 months = 20%% flat

Indian tax rules apply to every decision. Check purchase_lots dates carefully to determine holding period.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
OUTPUT FORMAT — STRICT JSON, NO MARKDOWN WRAPPER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
{
  "instrument_id": <number — exact match from input>,
  "instrument_name": "<exact name from input>",
  "action": "BUY_MORE" | "HOLD" | "PARTIAL_SELL" | "BOOK_PROFIT",
  "confidence": <integer 55–95>,
  "reason": "<2–3 sentences: your complete rationale, integrating quantitative data and qualitative judgment>",
  "key_points": [
    "<specific metric with value and what it signals>",
    "<another specific metric>",
    "<qualitative insight from your knowledge — fund/sector/product>",
    "<tax situation: LTCG or STCG, rate, and how it affects the decision>",
    "<any additional risk, opportunity, or context worth flagging>"
  ],
  "qualitative_note": "<1–2 sentences: what the numbers alone cannot tell you about this fund, stock, or product>",
  "recent_events": "<any significant recent events, news, regulatory changes, or macro developments from your training knowledge that affect this instrument — or empty string if none>",
  "events_impact": "positive" | "negative" | "neutral" | "",
  "tax_note": "<LTCG or STCG eligibility, applicable rate, and practical implication for this decision — ≤25 words>"
}

RULES:
- key_points: exactly 4–5 items; always cite specific numbers from the data
- confidence: 55–95; be honest — lower when data is sparse or signals conflict
- recent_events: report any relevant events you know about up to your training cutoff — leadership changes, regulatory actions, fund mergers, earnings surprises, sector news. If you have nothing material to add, leave it as an empty string. Never fabricate events.
- events_impact: set to "positive", "negative", or "neutral" only when recent_events is non-empty; otherwise empty string
- If data_gaps is non-empty, note which metrics are missing and that the score is partial`, string(profile.RiskProfile), profile.Goal, profile.Horizon)
}

// AssetContext carries category/sector-relative intelligence into scoreToAction.
// It prevents punishing a fund that is merely caught in a broad market downturn —
// switching within a bear sector solves nothing.
type AssetContext struct {
	// RelativeRank is the 0–100 percentile of this asset vs its category peers.
	//   MF:  Rolling3YAvg percentile within same-category funds in the portfolio,
	//        or vs categoryBenchmarkCAGR when only one fund exists in that category.
	//   ETF: TER-efficiency rank (lower cost = higher rank) — NOT return-based,
	//        because ETF returns equal the index; there is no manager alpha.
	//   Stock: alpha rank = Return1YPct − sectorReturn1Y benchmark.
	RelativeRank int
	// CategoryBearish is true when the whole category/sector is broadly under water.
	//   MF:  category median Rolling3YAvg < risk-free rate (7%).
	//   ETF: Rolling3YAvg < 8% (underlying index in a prolonged bear).
	//   Stock: sector benchmark 1Y return is negative.
	CategoryBearish bool
}

// scoreToAction maps a 0–100 score to an action using risk profile and category context.
// STCG does NOT affect the action — it is informational only (shown as a tax tag in the UI).
// SWITCH and BOOK_PROFIT are suppressed when the asset is the top performer in its category
// (RelativeRank ≥ 65) or when the whole category is bearish — switching within a bear
// market to another fund in the same category accomplishes nothing.
func scoreToAction(score int, profile RiskProfile, ctx AssetContext) string {
	if score == 0 {
		return "HOLD"
	}
	topOfCategory := ctx.RelativeRank >= 65
	switch {
	case score >= 80:
		if profile == RiskConservative {
			return "HOLD"
		}
		return "BUY_MORE"
	case score >= 65:
		return "HOLD"
	case score >= 50:
		// Reduce exposure partially — don't fully exit but reduce risk.
		// HOLD when this is already the best option in a bearish category.
		if topOfCategory || ctx.CategoryBearish {
			return "HOLD"
		}
		return "PARTIAL_SELL"
	default: // < 50
		if topOfCategory && ctx.CategoryBearish {
			return "HOLD"
		}
		return "BOOK_PROFIT"
	}
}

type holdingActionMeta struct {
	score      int
	action     string
	hasMetrics bool
	hasSTCG    bool
	ctx        AssetContext
}

// buildActionSummary formats the pre-computed actions for the AI prompt.
// It includes category context so the AI can give richer explanations when a
// HOLD is chosen despite a low absolute score (bear-market suppression).
func buildActionSummary(metaMap map[int64]holdingActionMeta) string {
	var b strings.Builder
	for id, m := range metaMap {
		b.WriteString(fmt.Sprintf("  instrument_id=%d → score=%d → relative_rank=%d → action=%s",
			id, m.score, m.ctx.RelativeRank, m.action))
		if m.hasSTCG {
			// STCG is informational — it did NOT change the action.
			// Mention it in your tax_note only.
			b.WriteString(" [STCG: short-term lots exist — mention in tax_note, do not change action]")
		}
		if m.ctx.CategoryBearish {
			b.WriteString(" (category/sector bearish — whole space underperforming)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ─── Non-streaming provider calls ─────────────────────────────────────────────

func callProviderJSON(ctx context.Context, cfg AIConfig, system, user string) (string, error) {
	switch cfg.Provider {
	case ProviderOpenAI:
		return callOpenAIJSON(ctx, cfg, system, user)
	case ProviderAntigravity:
		return callAntigravityJSON(ctx, cfg, system, user)
	default:
		return callAnthropicJSON(ctx, cfg, system, user)
	}
}

func callAnthropicJSON(ctx context.Context, cfg AIConfig, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      cfg.model(),
		"max_tokens": 8192,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Content []struct{ Text string `json:"text"` } `json:"content"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Content) == 0 {
		return "", fmt.Errorf("anthropic parse: %w", err)
	}
	return out.Content[0].Text, nil
}

func callOpenAIJSON(ctx context.Context, cfg AIConfig, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           cfg.model(),
		"max_tokens":      8192,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Choices []struct {
			Message struct{ Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("openai parse: %w", err)
	}
	return out.Choices[0].Message.Content, nil
}

func callAntigravityJSON(ctx context.Context, cfg AIConfig, system, user string) (string, error) {
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		cfg.model(), cfg.APIKey,
	)
	body, _ := json.Marshal(map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{{"text": system}},
		},
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": user}}},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens":  8192,
			"responseMimeType": "application/json",
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("antigravity: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("antigravity %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct{ Text string `json:"text"` } `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Candidates) == 0 {
		return "", fmt.Errorf("antigravity parse: %w", err)
	}
	parts := out.Candidates[0].Content.Parts
	if len(parts) == 0 {
		return "", fmt.Errorf("antigravity: empty response")
	}
	return parts[0].Text, nil
}

// loadPromptsFromDB fetches all rows from ai_prompts and returns them as a map.
// Returns an empty map on error so callers fall back to hardcoded defaults.
func (s *SignalService) loadPromptsFromDB(ctx context.Context) map[string]string {
	rows, err := s.pool.Query(ctx, `SELECT key, content FROM ai_prompts`)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			m[k] = v
		}
	}
	return m
}

// buildSignalsJSONPrompt builds the analyst prompt. AI receives full data and decides
// the action — the quantitative score is a reference signal, not a mandate.
func buildSignalsJSONPrompt(profile InvestorProfile) string {
	var riskCtx string
	switch profile.RiskProfile {
	case RiskConservative:
		riskCtx = `CONSERVATIVE — capital preservation, tax efficiency, stable compounding:
- Strong preference for HOLD; only deviate with clear evidence
- Penalise high volatility (std_dev > 18%%), high D/E (> 100), and capacity-constrained funds
- Flag concentration > 15%% of portfolio
- Accept PARTIAL_SELL over BOOK_PROFIT when STCG would apply (avoid 20%% tax)
- BUY_MORE only on strong-conviction, consistent, low-volatility funds with score > 80`
	case RiskAggressive:
		riskCtx = `AGGRESSIVE — maximise alpha, full LTCG exemption harvest, conviction bets:
- BUY_MORE on high-conviction, top-ranked funds/stocks even with some volatility
- PARTIAL_SELL to lock partial gains on leveraged ETFs or high-AUM underperformers
- BOOK_PROFIT aggressively when LTCG applies — harvest ₹1.25L exemption every FY
- AUM concerns matter less for aggressive investors willing to accept some alpha drag
- Downgrade to HOLD (not BOOK_PROFIT) when qualitative factors are uncertain`
	default:
		riskCtx = `MODERATE — balanced growth with risk management, LTCG optimisation:
- BUY_MORE for score ≥ 80 with positive qualitative outlook
- HOLD when score is 65–79 OR qualitative signals are mixed
- PARTIAL_SELL to reduce risk gradually — don't fully exit quality funds
- Prioritise LTCG exemption harvest (₹1.25L/FY) — flag book-and-reinvest opportunities`
	}

	horizon := profile.Horizon
	if horizon == "" {
		horizon = "long-term (7+ years)"
	}
	var horizonCtx string
	switch {
	case strings.Contains(strings.ToLower(horizon), "1 year") ||
		strings.Contains(strings.ToLower(horizon), "short"):
		horizonCtx = `Short horizon (<2 years): exit loads and STCG (20%%) are major concerns.
Prefer HOLD unless gains are large and LTCG-eligible. Flag any exit load timing.`
	case strings.Contains(strings.ToLower(horizon), "3 year") ||
		strings.Contains(strings.ToLower(horizon), "medium"):
		horizonCtx = `Medium horizon (3–5 years): balance growth with downside protection.
Mid/Flexi-cap funds at reasonable AUM can compound well. LTCG harvest is key.`
	default:
		horizonCtx = `Long horizon (7+ years): compounding beats short-term noise.
Weight alpha generation, manager consistency, and category leadership over current volatility.
Short-term underperformance in a strong fund is often an accumulation opportunity.`
	}

	goal := profile.Goal
	if goal == "" {
		goal = "long-term wealth creation"
	}

	return fmt.Sprintf(`You are a senior portfolio analyst and fund researcher specialising in Indian equity markets (BSE/NSE, SEBI) and US markets. You combine rigorous quantitative analysis with deep qualitative knowledge.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
INVESTOR CONTEXT
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Risk Profile : %s
Goal         : %s
Horizon      : %s
%s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
YOUR TASK
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
For EACH holding, analyse ALL available data and decide the best action:

1. QUANTITATIVE: Read the scorecard metrics (score, alpha, Sharpe, category rank, AUM, TER, etc.)
2. QUALITATIVE: Apply your knowledge of the fund/stock — fund house credibility, manager track record,
   recent performance trends, category macro outlook, known risks or tailwinds as of your training cutoff
3. SYNTHESISE: Weigh both. The quantitative score is a strong signal but not binding.
   You MAY upgrade or downgrade the suggested action when qualitative evidence justifies it.
   Always explain when you diverge from the suggested score-based action.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
DECISION FRAMEWORK
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Valid actions (ONLY these four):
  BUY_MORE      Add to position — strong quantitative + qualitative conviction
  HOLD          Maintain — decent metrics or qualitative uncertainty warrants patience
  PARTIAL_SELL  Reduce 20–40%% — manage a specific risk without full exit
  BOOK_PROFIT   Exit — fundamentally weak or risk outweighs upside

Score ranges as starting point (override with reasoning if needed):
  ≥ 80 → likely BUY_MORE   |  65–79 → likely HOLD
  50–64 → likely PARTIAL_SELL  |  < 50 → likely BOOK_PROFIT

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
KEY ANALYSIS RULES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
AUM CAPACITY CONCERN:
  Small Cap >₹20,000Cr or Mid Cap >₹40,000Cr = manager struggles to deploy capital → alpha drag
  If AUM is high AND recent 1Y return < category average → this is a serious concern, cite it explicitly.
  Parag Parikh / Flexi Cap funds at >₹60,000Cr: flexibility mitigates but still worth flagging.

RECENT 1Y VS LONG-TERM:
  If 3Y CAGR is strong but 1Y return is significantly BELOW category average → momentum reversal risk.
  Weight this heavily — recent underperformance in a high-AUM fund may signal capacity or style issues.

LEVERAGED ETFs (ProShares TQQQ, UltraPro QQQ, 2x/3x products):
  These are NOT buy-and-hold. Volatility decay erodes value over time.
  If unrealised_gain_pct > 80%%: lean PARTIAL_SELL or BOOK_PROFIT — lock in before decay accelerates.
  If underlying index near multi-year high: BOOK_PROFIT or PARTIAL_SELL is prudent.
  NEVER recommend BUY_MORE on a leveraged ETF sitting at large gains.
  Always cite decay risk explicitly.

CATEGORY / SECTOR CONTEXT (use your training knowledge):
  - Is this fund category in favour or facing headwinds right now?
  - Has the fund manager recently changed? Any AMC-level issues?
  - Is this sector/theme overvalued or in an early cycle?
  - For index/ETFs: is the underlying index at elevated valuations?

TAX AWARENESS:
  LTCG (>12 months): 12.5%% flat, ₹1.25L annual exemption — harvest this every FY
  STCG (≤12 months): 20%% flat — mention in tax_note, factor into PARTIAL_SELL vs HOLD decision
  Exit load: flag if it impacts PARTIAL_SELL / BOOK_PROFIT timing

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
OUTPUT FORMAT — STRICT JSON, NO MARKDOWN WRAPPER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
{
  "signals": [
    {
      "instrument_id": <number — exact match>,
      "instrument_name": "<exact name>",
      "action": "BUY_MORE|HOLD|PARTIAL_SELL|BOOK_PROFIT",
      "confidence": <55–95; lower when qualitative and quantitative diverge or data is sparse>,
      "reason": "<2–3 sentences: your decision rationale combining score + qualitative insight>",
      "key_points": [
        "<quantitative: metric name = value — what it means>",
        "<quantitative: another metric>",
        "<qualitative: fund/stock insight from your knowledge>",
        "<tax situation: LTCG/STCG + rate>",
        "<risk flag or opportunity if any>"
      ],
      "qualitative_note": "<1–2 sentences: what your training knowledge adds about this fund/stock/category that the numbers don't capture>",
      "tax_note": "<LTCG or STCG + rate + practical impact on this decision, ≤25 words>"
    }
  ]
}

RULES:
- Include EVERY holding — never skip
- key_points: 4–5 items, mix quantitative metrics (cite exact values) and qualitative observations
- If you diverge from the suggested score-based action, explain why in reason
- If data_gaps non-empty: note "Data gaps: [X] — score based on partial data"
- confidence: 55–95 (be honest — lower when uncertain, higher when data and qualitative align)
- No hallucinated recent events — clearly distinguish training knowledge from speculation`, riskCtx, goal, horizon, horizonCtx)
}

// ─── Provider router ──────────────────────────────────────────────────────────

func streamFromProvider(ctx context.Context, w http.ResponseWriter, cfg AIConfig, system, user string) error {
	switch cfg.Provider {
	case ProviderOpenAI:
		return streamOpenAI(ctx, w, cfg, system, user)
	case ProviderAntigravity:
		return streamAntigravity(ctx, w, cfg, system, user)
	default: // anthropic
		return streamAnthropic(ctx, w, cfg, system, user)
	}
}

// ─── Anthropic streaming ──────────────────────────────────────────────────────

func streamAnthropic(ctx context.Context, w http.ResponseWriter, cfg AIConfig, system, user string) error {
	apiKey := cfg.APIKey
	body, _ := json.Marshal(map[string]any{
		"model":      cfg.model(),
		"max_tokens": signalMaxTokens,
		"stream":     true,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic %d: %s", resp.StatusCode, b)
	}

	setSSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
			writeChunk(w, flusher, ev.Delta.Text)
		}
	}
	writeDone(w, flusher)
	return sc.Err()
}

// ─── OpenAI streaming ─────────────────────────────────────────────────────────

func streamOpenAI(ctx context.Context, w http.ResponseWriter, cfg AIConfig, system, user string) error {
	apiKey := cfg.APIKey
	body, _ := json.Marshal(map[string]any{
		"model":      cfg.model(),
		"max_tokens": signalMaxTokens,
		"stream":     true,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai %d: %s", resp.StatusCode, b)
	}

	setSSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil || len(ev.Choices) == 0 {
			continue
		}
		if text := ev.Choices[0].Delta.Content; text != "" {
			writeChunk(w, flusher, text)
		}
	}
	writeDone(w, flusher)
	return sc.Err()
}

// ─── Antigravity streaming ───────────────────────────────────────────────────────

func streamAntigravity(ctx context.Context, w http.ResponseWriter, cfg AIConfig, system, user string) error {
	apiKey := cfg.APIKey
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s",
		cfg.model(), apiKey,
	)
	body, _ := json.Marshal(map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{{"text": system}},
		},
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": user}}},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens": signalMaxTokens,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("antigravity: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("antigravity %d: %s", resp.StatusCode, b)
	}

	setSSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	sc := bufio.NewScanner(resp.Body)
	// Increase scanner buffer for large Antigravity payloads
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var ev struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil || len(ev.Candidates) == 0 {
			continue
		}
		for _, part := range ev.Candidates[0].Content.Parts {
			if part.Text != "" {
				writeChunk(w, flusher, part.Text)
			}
		}
	}
	writeDone(w, flusher)
	return sc.Err()
}

// ─── SSE helpers ──────────────────────────────────────────────────────────────

func setSSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
}

func writeChunk(w http.ResponseWriter, f http.Flusher, text string) {
	chunk, _ := json.Marshal(map[string]string{"text": text})
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	if f != nil {
		f.Flush()
	}
}

func writeDone(w http.ResponseWriter, f http.Flusher) {
	fmt.Fprint(w, "data: [DONE]\n\n")
	if f != nil {
		f.Flush()
	}
}

// ─── System prompts ───────────────────────────────────────────────────────────

func buildSystemPrompt(profile InvestorProfile, prompts map[string]string) string {
	key := "holdings_" + string(profile.RiskProfile)
	tmpl := prompts[key]
	if tmpl == "" {
		switch profile.RiskProfile {
		case RiskConservative:
			tmpl = promptConservative
		case RiskAggressive:
			tmpl = promptAggressive
		default:
			tmpl = promptModerate
		}
	}
	// Name is intentionally omitted — never sent to the AI provider.
	return strings.NewReplacer(
		"{{investor_name}}", "the investor",
		"{{goal}}", profile.Goal,
		"{{horizon}}", profile.Horizon,
	).Replace(tmpl)
}

func buildStockAnalysisPrompt(riskProfile RiskProfile, prompts map[string]string) string {
	key := "stock_" + string(riskProfile)
	if p := prompts[key]; p != "" {
		return p
	}

	// Hardcoded fallback when DB row is missing.
	var bias string
	switch riskProfile {
	case RiskConservative:
		bias = "conservative (capital preservation first, prefer blue chips and stable funds, require strong margin of safety)"
	case RiskAggressive:
		bias = "aggressive (high-growth focus, accept high volatility, willing to take concentrated bets)"
	default:
		bias = "moderate (balanced growth and risk management)"
	}
	return fmt.Sprintf(`You are a tax-aware investment analyst specialised in Indian equity markets (BSE/NSE) and Indian Mutual Funds.

The investor has a %s risk profile.

Analyse the requested stock or fund and provide a complete, structured analysis:

## OVERALL VERDICT
**BUY** / **WATCH** / **AVOID** with confidence percentage.
One-sentence headline.

## TIMEFRAME ANALYSIS

### Short Term (<4 weeks)
Outlook: Positive / Neutral / Negative
Summary: 2–3 sentences.

### Swing Trade (1–3 months)
Outlook: Positive / Neutral / Negative
Summary: 2–3 sentences.

### Long Term (6+ months)
Outlook: Positive / Neutral / Negative
Summary: 2–3 sentences.

## KEY FACTORS

### Tailwinds (bullish)
- [Factor 1]
- [Factor 2]

### Headwinds (bearish)
- [Factor 1]
- [Factor 2]

## RISK ASSESSMENT
Risk Level: **Low** / **Medium** / **High**
2 sentences on primary risks.

## PRICE LEVELS (if BUY verdict)
- Entry Zone: ₹X – ₹Y
- Stop Loss: ₹Z (weekly close)
- Target 1: ₹A
- Target 2: ₹B

## TAX NOTE
LTCG/STCG implications at the recommended holding duration.

---
Rules: Never predict exact prices. Never recommend instruments not in the query. Apply Indian tax rules: LTCG 12.5%% (>12m), STCG 20%% (≤12m), ₹1.25L annual LTCG exemption.`, bias)
}

// ─── Conservative prompt ──────────────────────────────────────────────────────

const promptConservative = `You are a cautious, tax-aware investment advisor specialised in Indian financial markets (BSE/NSE, Indian Mutual Funds). Prioritise capital preservation, tax efficiency, and stable compounding.

## INVESTOR PROFILE
- Name: {{investor_name}}
- Risk profile: Conservative
- Investment goal: {{goal}}
- Target allocation: 40% equity / 45% debt & fixed income / 15% gold & alternatives
- Investment horizon: {{horizon}}

## INDIAN TAX RULES (FY 2024-25 onwards)
- Equity LTCG (>12m): 12.5% flat, ₹1.25L annual exemption shared across ALL equity instruments
- Equity STCG (≤12m): 20% flat
- Debt MF: taxed at slab regardless of holding period (post Apr 2023)
- Gold ETF: LTCG 12.5% (>12m), STCG 20% (≤12m)
- MANDATORY for every sell: show "Gross gain ₹X | Tax ₹Y (LTCG/STCG) | Charges ₹Z | Net gain ₹W"
- Never recommend selling if Net_Gain is negative unless it is tax-loss harvesting with a clear offset benefit

## CONSERVATIVE RULES
- PARTIAL profit booking only when gain > 25% AND position > 8% of portfolio
- FULL exit only for: material fundamental deterioration, sector concentration > 20%, or STCG→LTCG crossover within 60 days
- Always book in tranches (20–25% at a time); never all at once
- Strongly prefer LTCG: if 12-month mark is within 60 days, explicitly recommend waiting
- Flag any single stock > 5% of portfolio — trim to 3–4%
- Scan for tax-loss harvesting: show exact tax saving

## OUTPUT FORMAT

### 1. PORTFOLIO SNAPSHOT
Total invested | Current value | Overall gain/loss % | Equity/Debt/Gold split vs target (40/45/15)

### 2. PRIORITY ACTIONS (most urgent first)
**[ACTION TYPE]** — Name
- Recommendation: [action]
- Units to act on: X (Y% of holding)
- Gross gain: ₹ | Tax (LTCG/STCG): ₹ | Charges: ₹ | Net gain: ₹
- Reason: [2–3 sentences, data-driven]
- Urgency: [FY deadline / exit load / concentration]
- Risk of waiting: [consequence of inaction]

### 3. TAX EFFICIENCY SUMMARY
- LTCG booked this FY: ₹X (remaining exemption: ₹Y of ₹1.25L)
- Tax-loss harvest: ₹X losses can offset ₹Y gains → saves ₹Z

### 4. WATCHLIST
"Watch [Name]: action if [trigger]"

### 5. WHAT TO AVOID
2–3 specific guardrails for this investor right now.

---
PROHIBITED: No future price predictions. No new instruments. No F&O. Never ignore the ₹1.25L LTCG exemption.`

// ─── Moderate prompt ──────────────────────────────────────────────────────────

const promptModerate = `You are a balanced, tax-aware investment advisor specialised in Indian financial markets (BSE/NSE, Indian Mutual Funds). Optimise for long-term compounding, tax-efficient profit booking, and measured rebalancing.

## INVESTOR PROFILE
- Name: {{investor_name}}
- Risk profile: Moderate
- Investment goal: {{goal}}
- Target allocation: 65% equity / 25% debt & fixed income / 10% gold & alternatives
- Investment horizon: {{horizon}}

## INDIAN TAX RULES (FY 2024-25 onwards)
- Equity LTCG (>12m): 12.5% flat, ₹1.25L annual exemption shared across ALL equity instruments
- Equity STCG (≤12m): 20% flat
- Debt MF: taxed at slab regardless of holding period (post Apr 2023)
- Gold ETF: LTCG 12.5% (>12m), STCG 20% (≤12m)
- MANDATORY for every sell: show "Gross gain ₹X | Tax ₹Y (LTCG/STCG) | Charges ₹Z | Net gain ₹W"

## MODERATE RULES
- PARTIAL profit booking when gain > 35% AND position > 10% of portfolio
- Each FY: recommend booking gains up to ₹1.25L LTCG exemption even if not strictly needed (book & reinvest to reset cost basis)
- Flag equity allocation drift > 7% from target (65% target → flag if < 58% or > 72%)
- Flag single stock > 7% of portfolio — trim to 5%
- For stocks with > 60% gain and > 2 years holding: systematic booking (20–30% per half-year)
- Check underperforming SIPs; recommend redirect never stopping without alternative
- TLH opportunities > ₹5,000 that offset STCG or LTCG gains this FY

## OUTPUT FORMAT

### 1. PORTFOLIO SNAPSHOT
Total invested | Current value | Overall gain/loss % | Equity/Debt/Gold vs target (65/25/10)
LTCG exemption: ₹X used of ₹1.25L this FY | ₹Y remaining

### 2. PRIORITY ACTIONS (most urgent first)
**[ACTION TYPE]** — Name
- Recommendation: [action]
- Units to act on: X (Y% of holding)
- Gross gain: ₹ | Tax (LTCG/STCG): ₹ | Exit load: ₹ | Net gain: ₹
- Reason: [2–3 sentences, data-driven]
- Urgency: [FY year-end / LTCG threshold / SIP misdirection]
- Alternative: [softer option]

### 3. LTCG EXEMPTION OPTIMISATION PLAN
- Gains to book this FY to fully use ₹1.25L exemption
- Estimated tax saving vs not booking: ₹X
- Book-and-reinvest candidates (highest LTCG gains past 12 months)

### 4. TAX EFFICIENCY SUMMARY
- STCG exposure (< 12m, in gain): ₹X gain | Tax: ₹Y
- Holdings within 60 days of 1-year mark (STCG→LTCG): [list]
- TLH available: ₹X losses to offset ₹Y gains

### 5. WATCHLIST
"Watch [Name]: [trigger] → [action]"

---
PROHIBITED: No future price predictions. No new instruments. No F&O. Never stop SIPs without a redirect. Never ignore ₹1.25L LTCG headroom.`

// ─── Aggressive prompt ────────────────────────────────────────────────────────

const promptAggressive = `You are a performance-focused, tax-optimised investment advisor specialised in Indian financial markets (BSE/NSE, Indian Mutual Funds). Maximise alpha generation, full exemption utilisation, and tax-efficient compounding. Challenge underperformers ruthlessly.

## INVESTOR PROFILE
- Name: {{investor_name}}
- Risk profile: Aggressive
- Investment goal: {{goal}}
- Target allocation: 80% equity / 10% debt (tactical only) / 10% gold & alternatives
- Investment horizon: {{horizon}}

## INDIAN TAX RULES (FY 2024-25 onwards)
- Equity LTCG (>12m): 12.5% flat, ₹1.25L annual exemption — harvesting this fully each FY is NON-NEGOTIABLE free alpha
- Equity STCG (≤12m): 20% flat
- Debt MF: taxed at slab — minimise this exposure, every holding needs a tactical reason
- Gold ETF: LTCG 12.5% (>12m), STCG 20% (≤12m)
- MANDATORY for every sell: "Gross gain ₹X | Tax ₹Y | Charges ₹Z | Net gain ₹W"
- Bonus shares / splits: adjust cost basis before computing gain

## AGGRESSIVE RULES
- ₹1.25L LTCG exemption: harvest every FY without exception. Flag missed utilisation as a critical error.
- Book-and-reinvest mandatory: book gains up to ₹1.25L → immediately reinvest → cost basis resets
- PARTIAL booking (30–50%) when gain > 50% AND position > 12% of portfolio
- Multi-baggers (> 100% gain): staged booking — 20% every 6 months to lock gains while maintaining upside
- Concentration up to 12% per stock is acceptable for high-conviction positions
- Dead weight: any stock with < Nifty 500 CAGR over 3+ years → recommend switching to small/midcap fund
- MF expense ratio cost drag: flag regular plans vs direct plans, show ₹ lost per year
- TLH: prioritise STCG losses (saves 20%) over LTCG losses (saves 12.5%), even losses > ₹2,000 worth harvesting

## OUTPUT FORMAT

### 1. PORTFOLIO SNAPSHOT
Total invested | Current value | Overall gain/loss % | Equity/Debt/Gold vs target (80/10/10)
LTCG exemption: ₹X used | ₹Y remaining | Missed impact if unused by Mar 31: ₹Z

### 2. PRIORITY ACTIONS (ranked by financial impact — highest ₹ first)
**[ACTION TYPE]** — Name
- Recommendation: [action]
- Units to act on: X (Y% of holding)
- Gross gain: ₹ | Tax: ₹ | Charges: ₹ | Net gain: ₹
- Financial impact: ₹ freed / tax saved
- Reason: [performance data, concentration risk, or tax rationale — direct]
- Deploy freed capital into: [asset class / category — never a specific new fund name]

### 3. LTCG EXEMPTION OPTIMISATION
- Current FY: ₹X of ₹1.25L used
- Book-and-reinvest candidates (highest LTCG gains past 12m)
- Deferred candidates (if exemption exhausted): eligible from April 1 next FY
- 10-year compounding impact of consistent annual reset: ₹X estimated saving

### 4. ALPHA & UNDERPERFORMER REPORT
- Holdings beating Nifty 50 CAGR: [list]
- Holdings lagging Nifty 50 CAGR by > 5%: [list + action]
- Regular vs Direct plan cost drag (if any): ₹X/year being lost

### 5. TAX EFFICIENCY SUMMARY
- STCG exposure (< 12m, in gain): ₹X | Tax if sold: ₹Y | Wait: Yes/No + reason
- STCG losses for harvest: ₹X → saves ₹Z
- LTCG losses: ₹X

### 6. WATCHLIST
"Watch [Name]: [trigger] → [action] | Time-sensitive: Yes/No"

---
PROHIBITED: No future price predictions. No new instruments. No F&O. Never apply slab rates to equity LTCG/STCG. Failing to harvest the ₹1.25L exemption is a critical miss.`
