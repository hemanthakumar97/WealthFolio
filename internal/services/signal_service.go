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
	Action          string   `json:"action"`          // BUY_MORE | HOLD | SWITCH | BOOK_PROFIT
	Confidence      int      `json:"confidence"`       // 55–95
	Reason          string   `json:"reason"`           // 1–2 sentence summary
	KeyPoints       []string `json:"key_points"`       // 3–5 data-backed bullet points
	QualitativeNote string   `json:"qualitative_note"` // AI qualitative context (company/sector/fund)
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

func (s *SignalService) BuildPortfolioPayload(ctx context.Context) (*signalPortfolioPayload, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.instrument_id, i.name, i.isin, i.asset_type, i.exchange, i.amfi_code,
		       COALESCE(i.yahoo_symbol, ''),
		       t.transaction_date::text, t.transaction_type,
		       t.quantity::float, t.price::float, t.amount::float
		  FROM transactions t
		  JOIN instruments i ON t.instrument_id = i.id
		 WHERE t.transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		 ORDER BY t.instrument_id, t.transaction_date, t.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query transactions: %w", err)
	}
	defer rows.Close()

	type instrMeta struct{ name, isin, assetType, exchange, amfiCode, yahooSymbol string }
	type instrState struct {
		meta instrMeta
		lots []fifoActiveLot
	}
	states := map[int64]*instrState{}

	for rows.Next() {
		var instrID int64
		var name, assetType, yahooSymbol, txDate, txType string
		var isin, exchange, amfiCode *string
		var qty, price, amount float64
		if err := rows.Scan(&instrID, &name, &isin, &assetType, &exchange, &amfiCode, &yahooSymbol, &txDate, &txType, &qty, &price, &amount); err != nil {
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

	holdings := make([]signalHolding, 0, len(activeIDs))
	for _, id := range activeIDs {
		st := states[id]
		if len(st.lots) == 0 {
			continue
		}
		currentPrice := priceMap[id]
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
		// These show "invested: -" in the UI and have no meaningful signal.
		if totalInvested <= 0 {
			continue
		}
		currentValue := totalUnits * currentPrice
		gainAbs := currentValue - totalInvested
		var gainPct float64
		if totalInvested > 0 {
			gainPct = gainAbs / totalInvested * 100
		}
		holdings = append(holdings, signalHolding{
			InstrumentID:      id,
			InstrumentType:    assetTypeToInstrumentType(st.meta.assetType),
			Name:              st.meta.name,
			ISIN:              st.meta.isin,
			Exchange:          st.meta.exchange,
			AMFICode:          st.meta.amfiCode,
			YahooSymbol:       st.meta.yahooSymbol,
			PurchaseLots:      lots,
			CurrentPrice:      r2(currentPrice),
			CurrentValue:      r2(currentValue),
			UnrealisedGainAbs: r2(gainAbs),
			UnrealisedGainPct: r2(gainPct),
			ExitLoadSchedule:  nil,
			SIPActive:         false,
		})
	}

	// Enrich holdings with scorecard metrics by asset type.
	// Fetch concurrently for MFs; sequentially (crumb) for stocks/ETFs.
	// Failures are silently skipped — metrics are best-effort.
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
		// ETFs: treat same as equity_mf but flagged by InstrumentType; re-check by raw asset type
	}
	// Also enrich ETFs (InstrumentType "equity_mf" but no amfi_code).
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
			if m, err := FetchStockMetrics(ctx, job.id, job.yahooSymbol, s.pool, 20*time.Second); err == nil {
				holdings[job.idx].StockMetrics = m
			}
		case "ETF":
			if m, err := FetchETFMetrics(ctx, job.id, job.yahooSymbol, s.pool, 20*time.Second); err == nil {
				holdings[job.idx].ETFMetrics = m
			}
		}
	}

	fy := fmt.Sprintf("%d-%02d", fyStart.Year(), fyStart.Year()%100+1)
	return &signalPortfolioPayload{
		AnalysisDate:     now.Format("2006-01-02"),
		FinancialYear:    fy,
		LTCGBookedThisFY: r2(ltcgBooked),
		Holdings:         holdings,
	}, nil
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

// FetchHoldingsSignals calls the AI synchronously and returns one structured
// signal per holding.
//
// ACTION is computed DETERMINISTICALLY from the score + risk profile + tax status.
// AI is used only for reason, key_points, qualitative_note, and confidence.
// This guarantees Signal always reflects the Score.
func (s *SignalService) FetchHoldingsSignals(ctx context.Context, payload *signalPortfolioPayload, profile InvestorProfile, cfg AIConfig) (*HoldingsSignalsResult, error) {
	// Build instrument_id → zero1_score map and compute deterministic actions.
	metaMap := make(map[int64]holdingActionMeta, len(payload.Holdings))

	for _, h := range payload.Holdings {
		var score int
		switch {
		case h.MFMetrics != nil:
			score = h.MFMetrics.Zero1Score
		case h.ETFMetrics != nil:
			score = h.ETFMetrics.Zero1Score
		case h.StockMetrics != nil:
			score = h.StockMetrics.Zero1Score
		}

		// Determine if any lot is STCG (< 12 months old).
		cutoff := time.Now().AddDate(-1, 0, 0)
		hasSTCG := false
		for _, lot := range h.PurchaseLots {
			if t, err := time.Parse("2006-01-02", lot.Date); err == nil && t.After(cutoff) {
				hasSTCG = true
				break
			}
		}

		action := scoreToAction(score, profile.RiskProfile, hasSTCG)
		metaMap[h.InstrumentID] = holdingActionMeta{
			score:      score,
			action:     action,
			hasMetrics: score > 0,
			hasSTCG:    hasSTCG,
		}
	}

	// Ask AI only for explanatory content (reason, key_points, qualitative_note, confidence).
	// We inject the pre-computed action so AI knows what decision to justify.
	payloadBytes, _ := json.MarshalIndent(payload, "", "  ")
	prompts := s.loadPromptsFromDB(ctx)
	system := buildSignalsJSONPrompt(profile.RiskProfile, prompts)
	user := fmt.Sprintf(
		"Profile:\n- Goal: %s\n- Horizon: %s\n\nPRE-COMPUTED ACTIONS (you must justify these, not change them):\n%s\n\nPortfolio:\n```json\n%s\n```",
		profile.Goal, profile.Horizon,
		buildActionSummary(metaMap), payloadBytes,
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

	// Override action and score with deterministic values — AI cannot change these.
	for i := range wrapper.Signals {
		if meta, ok := metaMap[wrapper.Signals[i].InstrumentID]; ok {
			wrapper.Signals[i].Action = meta.action
			wrapper.Signals[i].Zero1Score = meta.score
		}
	}

	return &HoldingsSignalsResult{
		Signals:     wrapper.Signals,
		RiskProfile: string(profile.RiskProfile),
	}, nil
}

// scoreToAction maps a 0–100 score to an action, applying risk profile and tax adjustments.
func scoreToAction(score int, profile RiskProfile, hasSTCG bool) string {
	if score == 0 {
		return "HOLD" // no data — default safe
	}
	switch {
	case score >= 80:
		if profile == RiskConservative {
			return "HOLD" // conservative: don't buy more even on strong funds
		}
		return "BUY_MORE"
	case score >= 65:
		return "HOLD"
	case score >= 50:
		if hasSTCG && profile != RiskAggressive {
			return "HOLD" // avoid SWITCH if it triggers 20% STCG
		}
		return "SWITCH"
	default: // < 50
		if hasSTCG && profile == RiskConservative {
			return "HOLD" // wait for LTCG before exiting
		}
		return "BOOK_PROFIT"
	}
}

type holdingActionMeta struct {
	score      int
	action     string
	hasMetrics bool
	hasSTCG    bool
}

// buildActionSummary formats the pre-computed actions for the AI prompt.
func buildActionSummary(metaMap map[int64]holdingActionMeta) string {
	var b strings.Builder
	for id, m := range metaMap {
		b.WriteString(fmt.Sprintf("  instrument_id=%d → score=%d → action=%s", id, m.score, m.action))
		if m.hasSTCG {
			b.WriteString(" (STCG applies)")
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

// buildSignalsJSONPrompt builds a fully data-driven prompt that uses the pre-computed
// scorecard metrics as primary inputs, augmented by AI qualitative knowledge.
func buildSignalsJSONPrompt(riskProfile RiskProfile, prompts map[string]string) string {
	key := "signals_" + string(riskProfile)
	if p := prompts[key]; p != "" {
		return p
	}

	var riskCtx string
	switch riskProfile {
	case RiskConservative:
		riskCtx = `conservative investor (capital preservation first):
- Prefer HOLD over action unless score < 50 AND LTCG is applicable
- Penalise high volatility (std_dev > 18%%) and high D/E (> 100) more heavily
- Avoid SWITCH if STCG would apply — tax drag erodes conservative gains
- Flag any position > 15%% of portfolio value as over-concentrated`
	case RiskAggressive:
		riskCtx = `aggressive investor (maximise alpha and exemption harvest):
- BUY_MORE freely on score >= 75 if capital is available
- SWITCH aggressively when score is 50–64 — move to sector leaders
- BOOK_PROFIT on any multi-bagger holding where LTCG applies (harvest ₹1.25L exemption)
- Accept higher volatility; downweight TER/AUM in your reasoning`
	default:
		riskCtx = `moderate investor (balance growth with risk management):
- BUY_MORE only when score >= 80 and no STCG penalty applies
- HOLD for scores 65–79; trim only if concentration > 15%%
- SWITCH scores 50–64 to better peer in same category when LTCG applicable
- Highlight LTCG exemption harvest opportunities`
	}

	return fmt.Sprintf(`You are an expert AI Financial Analyst with deep knowledge of Indian and US equity markets.

IMPORTANT: The ACTION for each holding has ALREADY been determined by the quantitative scoring engine.
Your job is ONLY to write the explanation (reason, key_points, qualitative_note, confidence, tax_note).
Do NOT change the action — it will be overridden by the server anyway.

INVESTOR PROFILE: %s

═══════════════════════════════════════════════
YOUR TASK — EXPLAIN EACH DECISION
═══════════════════════════════════════════════
For every holding in the portfolio, write a compelling, data-backed explanation of WHY
the pre-computed action makes sense. Use the metric values from the payload.

SCORECARD METRIC REFERENCE:
MF / ETF (from mf_metrics / etf_metrics):
  - rolling_3y_avg_pct: avg 3Y CAGR → core return signal
  - consistency_1y_pct: %% positive 1Y periods → downside discipline
  - sharpe_1y: risk-adjusted return (>0.5 = good, <0 = underperforms risk-free)
  - std_dev_1y_pct vs std_dev_5y_median: is this year more/less volatile than usual?
  - beta: market sensitivity
  - aum_cr: AUM ₹Cr (small cap > ₹20,000Cr = capacity concern)
  - ter_pct: expense ratio (higher TER erodes returns)
  - data_gaps: metrics missing — mention these honestly

STOCK (from stock_metrics):
  - trailing_pe: cite vs sector median
  - roe: Return on equity (>20%% = excellent)
  - profit_margin / operating_margin
  - revenue_growth / earnings_growth (YoY %%)
  - debt_to_equity (<50 = conservative, >100 = high risk)
  - return_1y_pct: 1Y price momentum
  - max_drawdown_3y_pct: worst drawdown

TAX CONTEXT (from purchase_lots dates):
  - LTCG (> 12 months): 12.5%% flat, ₹1.25L annual exemption
  - STCG (≤ 12 months): 20%% flat
  - Always state which applies

QUALITATIVE (your training knowledge):
  - MF: fund house reputation, manager track record, category outlook
  - ETF: what it tracks, macro context
  - STOCK: business moat, sector trends, key risks
  - Keep under 40 words. No hallucinated recent events.

═══════════════════════════════════════════════
OUTPUT FORMAT (STRICT JSON — no markdown)
═══════════════════════════════════════════════
{
  "signals": [
    {
      "instrument_id": <number — must match input exactly>,
      "instrument_name": "<exact name from input>",
      "action": "<copy the pre-computed action from the PRE-COMPUTED ACTIONS list above>",
      "confidence": <55–95; reduce by 10 if data_gaps, by 5 if Sharpe negative>,
      "reason": "<1–2 sentences: score verdict + top 2 metric drivers>",
      "key_points": [
        "<metric: exact value — interpretation>",
        "<metric: exact value — interpretation>",
        "<metric: exact value — interpretation>",
        "<tax situation>",
        "<optional: switch target or risk flag>"
      ],
      "qualitative_note": "<1 sentence AI knowledge about this fund/stock>",
      "tax_note": "<LTCG or STCG + rate + impact, ≤20 words>"
    }
  ]
}

RULES:
- Include EVERY holding — never skip any
- key_points: 3–5 items, each citing an actual metric number
- If data_gaps non-empty: add bullet "Data gaps: [X, Y] — score normalised to available metrics"
- confidence: 55–95`, riskCtx)
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
