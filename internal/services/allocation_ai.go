package services

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

// ─── Allocation suggestion types ──────────────────────────────────────────────

// AllocSuggestInput is the request for an AI allocation suggestion.
type AllocSuggestInput struct {
	RiskProfile string
	Horizon     string
}

// AllocCategorySuggestion is the AI's recommended target for one alloc_category.
type AllocCategorySuggestion struct {
	AllocCategory          string  `json:"alloc_category"`
	CurrentPercent         float64 `json:"current_percent"`
	SuggestedTargetPercent float64 `json:"suggested_target_percent"`
	Trend                  string  `json:"trend"` // UPTREND | DOWNTREND | NEUTRAL
	Reason                 string  `json:"reason"`
}

// AllocInstrumentSuggestion is the AI's recommended target for one instrument.
type AllocInstrumentSuggestion struct {
	InstrumentID           int64   `json:"instrument_id"`
	InstrumentName         string  `json:"instrument_name"`
	AllocCategory          string  `json:"alloc_category"`
	CurrentPercent         float64 `json:"current_percent"`
	SuggestedTargetPercent float64 `json:"suggested_target_percent"`
	Trend                  string  `json:"trend"`
	MomentumScore          int     `json:"momentum_score"`
	Reason                 string  `json:"reason"`
}

// AllocSuggestResult is the full reviewed-before-apply suggestion set.
type AllocSuggestResult struct {
	RiskProfile           string                      `json:"risk_profile"`
	GeneratedAt           string                      `json:"generated_at"`
	MarketContext         string                      `json:"market_context"`
	Rationale             string                      `json:"rationale"`
	CategorySuggestions   []AllocCategorySuggestion   `json:"category_suggestions"`
	InstrumentSuggestions []AllocInstrumentSuggestion `json:"instrument_suggestions"`
}

// allocTrendInstrument is one held instrument annotated with trend facts, sent
// to the AI as grounding data.
type allocTrendInstrument struct {
	InstrumentID    int64   `json:"instrument_id"`
	Name            string  `json:"name"`
	AssetType       string  `json:"asset_type"`
	AllocCategory   string  `json:"alloc_category"`
	CurrentValue    float64 `json:"current_value"`
	CurrentPercent  float64 `json:"current_percent"`
	Return1M        float64 `json:"return_1m_pct"`
	Return3M        float64 `json:"return_3m_pct"`
	Return6M        float64 `json:"return_6m_pct"`
	Return1Y        float64 `json:"return_1y_pct"`
	Zero1Score      int     `json:"zero1_score,omitempty"`
	RelativeRank    int     `json:"relative_rank,omitempty"`
	CategoryBearish bool    `json:"category_bearish,omitempty"`
	Trend           string  `json:"trend"`
}

// ─── Risk guardrail bands ─────────────────────────────────────────────────────

type allocBand struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// riskBands returns the soft guidance bands per alloc_category for a risk
// profile, used to steer (not hard-clamp) the AI.
func riskBands(profile string) map[string]allocBand {
	switch strings.ToLower(profile) {
	case "conservative":
		return map[string]allocBand{
			domain.AllocEquity: {40, 55}, domain.AllocDebt: {25, 40}, domain.AllocMetals: {10, 20},
			domain.AllocUSEquity: {5, 15}, domain.AllocOthers: {0, 10},
		}
	case "aggressive":
		return map[string]allocBand{
			domain.AllocEquity: {65, 85}, domain.AllocDebt: {0, 15}, domain.AllocMetals: {5, 12},
			domain.AllocUSEquity: {10, 25}, domain.AllocOthers: {0, 10},
		}
	default: // moderate
		return map[string]allocBand{
			domain.AllocEquity: {55, 70}, domain.AllocDebt: {10, 25}, domain.AllocMetals: {8, 15},
			domain.AllocUSEquity: {8, 18}, domain.AllocOthers: {0, 10},
		}
	}
}

// deriveTrend tags an instrument UPTREND/DOWNTREND/NEUTRAL from trailing returns,
// so the AI is grounded in deterministic momentum rather than guessing.
func deriveTrend(ret3m, ret1y float64) string {
	switch {
	case ret3m >= 3 && ret1y >= 0:
		return "UPTREND"
	case ret3m <= -3 && ret1y <= 0:
		return "DOWNTREND"
	case ret3m >= 1:
		return "UPTREND"
	case ret3m <= -1:
		return "DOWNTREND"
	default:
		return "NEUTRAL"
	}
}

// ─── Main entry ───────────────────────────────────────────────────────────────

// SuggestAllocations builds a trend payload from cached metrics + trailing
// returns + market mood, asks the AI for tilted target percentages, and returns
// a validated suggestion set. It does NOT persist anything.
func (s *SignalService) SuggestAllocations(ctx context.Context, cfg AIConfig, in AllocSuggestInput) (*AllocSuggestResult, error) {
	profile := strings.ToLower(strings.TrimSpace(in.RiskProfile))
	if profile != "conservative" && profile != "aggressive" {
		profile = "moderate"
	}
	horizon := strings.TrimSpace(in.Horizon)
	if horizon == "" {
		horizon = "5+ years"
	}

	instruments, total, err := s.buildAllocTrendData(ctx)
	if err != nil {
		return nil, err
	}
	if len(instruments) == 0 || total <= 0 {
		return nil, fmt.Errorf("no held instruments with prices to analyse")
	}

	// Enrich with cached scorecard metrics (no external calls).
	if payload, err := s.BuildPortfolioPayloadFromCache(ctx); err == nil {
		byID := map[int64]signalHolding{}
		for _, h := range payload.Holdings {
			byID[h.InstrumentID] = h
		}
		for i := range instruments {
			if h, ok := byID[instruments[i].InstrumentID]; ok {
				switch {
				case h.MFMetrics != nil:
					instruments[i].Zero1Score = h.MFMetrics.Zero1Score
					instruments[i].RelativeRank = h.MFMetrics.RelativeRank
					instruments[i].CategoryBearish = h.MFMetrics.CategoryBearish
				case h.ETFMetrics != nil:
					instruments[i].Zero1Score = h.ETFMetrics.Zero1Score
					instruments[i].RelativeRank = h.ETFMetrics.RelativeRank
				case h.StockMetrics != nil:
					instruments[i].Zero1Score = h.StockMetrics.Zero1Score
				}
			}
		}
	}

	// Category aggregates (current %, value-weighted trend).
	type catAgg struct {
		value       float64
		retWeighted float64
	}
	catAggs := map[string]*catAgg{}
	for _, it := range instruments {
		a := catAggs[it.AllocCategory]
		if a == nil {
			a = &catAgg{}
			catAggs[it.AllocCategory] = a
		}
		a.value += it.CurrentValue
		a.retWeighted += it.Return3M * it.CurrentValue
	}

	// Market mood (best-effort; non-fatal).
	var moods []MarketMoodResponse
	if mm, err := NewMarketMoodService(s.pool).Moods(ctx); err == nil {
		moods = mm
	}

	// Build the AI prompts.
	prompts := s.loadPromptsFromDB(ctx)
	system := buildAllocSystemPrompt(profile, horizon, prompts)

	userPayload := map[string]any{
		"analysis_date": time.Now().Format("2006-01-02"),
		"risk_profile":  profile,
		"horizon":       horizon,
		"total_value":   math.Round(total),
		"market_mood":   moods,
		"instruments":   instruments,
	}
	userJSON, _ := json.Marshal(userPayload)

	raw, err := callProviderJSON(ctx, cfg, system, string(userJSON))
	if err != nil {
		return nil, err
	}
	raw = stripJSONFences(raw)

	var parsed struct {
		MarketContext       string                      `json:"market_context"`
		Rationale           string                      `json:"rationale"`
		CategorySuggestions []AllocCategorySuggestion   `json:"category_suggestions"`
		InstrumentSugg      []AllocInstrumentSuggestion `json:"instrument_suggestions"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("parse AI response: %w\nraw: %.400s", err, raw)
	}

	result := &AllocSuggestResult{
		RiskProfile:   profile,
		GeneratedAt:   time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		MarketContext: strings.TrimSpace(parsed.MarketContext),
		Rationale:     strings.TrimSpace(parsed.Rationale),
	}

	// ── Merge + validate instrument suggestions ──
	// Index the AI output by instrument; fill any missing held instrument with
	// its current weight so every holding is represented.
	aiByInstr := map[int64]AllocInstrumentSuggestion{}
	for _, s := range parsed.InstrumentSugg {
		aiByInstr[s.InstrumentID] = s
	}
	instrByCat := map[string][]AllocInstrumentSuggestion{}
	for _, it := range instruments {
		sug := AllocInstrumentSuggestion{
			InstrumentID:           it.InstrumentID,
			InstrumentName:         it.Name,
			AllocCategory:          it.AllocCategory,
			CurrentPercent:         round2(it.CurrentPercent),
			SuggestedTargetPercent: round2(it.CurrentPercent),
			Trend:                  it.Trend,
			MomentumScore:          it.Zero1Score,
		}
		if ai, ok := aiByInstr[it.InstrumentID]; ok {
			sug.SuggestedTargetPercent = clamp(ai.SuggestedTargetPercent, 0, 100)
			if ai.Reason != "" {
				sug.Reason = strings.TrimSpace(ai.Reason)
			}
			if ai.Trend != "" {
				sug.Trend = ai.Trend
			}
			if ai.MomentumScore > 0 {
				sug.MomentumScore = ai.MomentumScore
			}
		}
		instrByCat[it.AllocCategory] = append(instrByCat[it.AllocCategory], sug)
	}

	// ── Merge + validate category suggestions ──
	aiByCat := map[string]AllocCategorySuggestion{}
	for _, c := range parsed.CategorySuggestions {
		aiByCat[strings.ToUpper(strings.TrimSpace(c.AllocCategory))] = c
	}
	cats := make([]AllocCategorySuggestion, 0, len(catAggs))
	for cat, agg := range catAggs {
		curPct := agg.value / total * 100
		trend := "NEUTRAL"
		if agg.value > 0 {
			trend = deriveTrend(agg.retWeighted/agg.value, 0)
		}
		c := AllocCategorySuggestion{
			AllocCategory:          cat,
			CurrentPercent:         round2(curPct),
			SuggestedTargetPercent: round2(curPct),
			Trend:                  trend,
		}
		if ai, ok := aiByCat[cat]; ok {
			c.SuggestedTargetPercent = clamp(ai.SuggestedTargetPercent, 0, 100)
			if ai.Reason != "" {
				c.Reason = strings.TrimSpace(ai.Reason)
			}
			if ai.Trend != "" {
				c.Trend = ai.Trend
			}
		}
		cats = append(cats, c)
	}

	// Renormalize category targets to sum exactly 100 across HELD categories.
	normalizePercents(cats, func(i int) *float64 { return &cats[i].SuggestedTargetPercent })

	// For each category, renormalize its instrument targets to equal the
	// category target, so instrument + category plans are internally consistent.
	catTarget := map[string]float64{}
	for _, c := range cats {
		catTarget[c.AllocCategory] = c.SuggestedTargetPercent
	}
	instrSugg := []AllocInstrumentSuggestion{}
	for cat, list := range instrByCat {
		scaleToTarget(list, catTarget[cat])
		instrSugg = append(instrSugg, list...)
	}

	result.CategorySuggestions = cats
	result.InstrumentSuggestions = instrSugg
	return result, nil
}

// buildAllocTrendData returns one annotated row per currently-held instrument
// with trailing returns computed from the prices table (no external calls).
// Trailing returns use correlated subqueries against the latest price date.
func (s *SignalService) buildAllocTrendData(ctx context.Context) ([]allocTrendInstrument, float64, error) {
	ret := func(days int) string {
		return fmt.Sprintf(`COALESCE((lp.nav_price / NULLIF((SELECT p.nav_price FROM prices p WHERE p.instrument_id=i.id AND p.price_date <= lp.price_date - INTERVAL '%d days' ORDER BY p.price_date DESC LIMIT 1),0) - 1)*100, 0)::float`, days)
	}
	q := `
		WITH holdings AS (
			SELECT instrument_id,
			       SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			                WHEN transaction_type IN ('SELL','SWITCH_OUT')       THEN -quantity
			                ELSE 0 END) AS units
			  FROM transactions GROUP BY instrument_id
			HAVING SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			                WHEN transaction_type IN ('SELL','SWITCH_OUT')       THEN -quantity
			                ELSE 0 END) > 0.1
		),
		latest AS (
			SELECT DISTINCT ON (instrument_id) instrument_id, nav_price, price_date
			  FROM prices ORDER BY instrument_id, price_date DESC
		)
		SELECT i.id, i.name, i.asset_type,
		       COALESCE(ia.alloc_category, CASE i.asset_type
		           WHEN 'METAL' THEN 'METALS' WHEN 'US_FUND' THEN 'US_EQUITY' WHEN 'BOND' THEN 'DEBT'
		           WHEN 'MF' THEN 'EQUITY' WHEN 'ETF' THEN 'EQUITY' WHEN 'STOCK' THEN 'EQUITY'
		           ELSE 'OTHERS' END) AS cat,
		       COALESCE(lp.nav_price,0)::float AS price,
		       COALESCE(h.units * lp.nav_price,0)::float AS value,
		       ` + ret(30) + ` AS ret1m, ` + ret(90) + ` AS ret3m,
		       ` + ret(180) + ` AS ret6m, ` + ret(365) + ` AS ret1y
		  FROM holdings h
		  JOIN instruments i ON i.id = h.instrument_id
		  JOIN latest lp ON lp.instrument_id = h.instrument_id AND lp.nav_price > 0
		  LEFT JOIN instrument_allocations ia ON ia.instrument_id = h.instrument_id
		 ORDER BY value DESC, i.name ASC`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []allocTrendInstrument{}
	var total float64
	for rows.Next() {
		var it allocTrendInstrument
		var price float64
		if err := rows.Scan(&it.InstrumentID, &it.Name, &it.AssetType, &it.AllocCategory,
			&price, &it.CurrentValue, &it.Return1M, &it.Return3M, &it.Return6M, &it.Return1Y); err != nil {
			return nil, 0, err
		}
		it.Return1M = round2(it.Return1M)
		it.Return3M = round2(it.Return3M)
		it.Return6M = round2(it.Return6M)
		it.Return1Y = round2(it.Return1Y)
		it.Trend = deriveTrend(it.Return3M, it.Return1Y)
		total += it.CurrentValue
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range out {
		if total > 0 {
			out[i].CurrentPercent = out[i].CurrentValue / total * 100
		}
	}
	return out, total, nil
}

// ─── Prompt ───────────────────────────────────────────────────────────────────

func buildAllocSystemPrompt(profile, horizon string, prompts map[string]string) string {
	tmpl := prompts["allocation_suggest"]
	if strings.TrimSpace(tmpl) == "" {
		tmpl = defaultAllocPrompt
	}
	bands := riskBands(profile)
	var b strings.Builder
	order := []string{domain.AllocEquity, domain.AllocDebt, domain.AllocMetals, domain.AllocUSEquity, domain.AllocOthers}
	for _, c := range order {
		bd := bands[c]
		fmt.Fprintf(&b, "- %s: %.0f–%.0f%%\n", c, bd.Min, bd.Max)
	}
	return strings.NewReplacer(
		"{{risk_profile}}", profile,
		"{{horizon}}", horizon,
		"{{bands}}", strings.TrimRight(b.String(), "\n"),
	).Replace(tmpl)
}

const defaultAllocPrompt = `You are a portfolio allocation strategist for an Indian retail investor. You are given the investor's CURRENT holdings, each annotated with trailing returns (1M/3M/6M/1Y), a quality score (zero1_score, 0–100), a relative rank vs category peers, a category_bearish flag, and a deterministic momentum tag (UPTREND/DOWNTREND/NEUTRAL). You also get the current market mood (index P/E read).

Recommend TARGET allocation percentages — at category level (EQUITY, DEBT, METALS, US_EQUITY, OTHERS) and per instrument — that gently tilt toward what is trending up and away from what is downtrending, while staying diversified.

Investor risk profile: {{risk_profile}}. Horizon: {{horizon}}.
Soft guardrail bands for this profile (aim within these where the holdings allow):
{{bands}}

Rules:
- Only allocate across the categories and instruments PROVIDED (the investor's current holdings). Do NOT invent new instruments.
- Category target percentages must sum to 100.
- Within each category, instrument target percentages must sum to that category's target.
- Tilt toward higher trailing returns, higher zero1_score and relative_rank, and UPTREND tags. Reduce DOWNTREND funds and those with category_bearish = true — but do NOT zero out a sound long-term holding entirely unless it is clearly broken.
- Be gradual: most shifts should be within ±10 percentage points of the current weight. This is a tilt, not a teardown.
- Respect market mood: if an index is "Red" (expensive), be cautious about increasing that exposure.

Output STRICT JSON ONLY — no markdown, no commentary — matching exactly:
{
  "market_context": "<1–2 sentences on the current market read>",
  "rationale": "<2–4 sentences summarising your overall tilt strategy>",
  "category_suggestions": [
    {"alloc_category": "EQUITY", "suggested_target_percent": <number>, "trend": "UPTREND|DOWNTREND|NEUTRAL", "reason": "<short>"}
  ],
  "instrument_suggestions": [
    {"instrument_id": <id>, "suggested_target_percent": <number>, "trend": "UPTREND|DOWNTREND|NEUTRAL", "momentum_score": <0-100>, "reason": "<short>"}
  ]
}`

// ─── helpers ──────────────────────────────────────────────────────────────────

func stripJSONFences(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		if nl := strings.Index(raw, "\n"); nl >= 0 {
			raw = raw[nl+1:]
		}
		if idx := strings.LastIndex(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}
	return raw
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalizePercents scales the values selected by `get` so they sum to 100.
func normalizePercents[T any](items []T, get func(i int) *float64) {
	var sum float64
	for i := range items {
		sum += *get(i)
	}
	if sum <= 0 {
		return
	}
	for i := range items {
		p := get(i)
		*p = round2(*p / sum * 100)
	}
}

// scaleToTarget scales instrument target percentages so they sum to `target`.
func scaleToTarget(list []AllocInstrumentSuggestion, target float64) {
	var sum float64
	for i := range list {
		sum += list[i].SuggestedTargetPercent
	}
	if sum <= 0 {
		// Distribute evenly if the AI gave nothing usable.
		if len(list) > 0 {
			even := round2(target / float64(len(list)))
			for i := range list {
				list[i].SuggestedTargetPercent = even
			}
		}
		return
	}
	for i := range list {
		list[i].SuggestedTargetPercent = round2(list[i].SuggestedTargetPercent / sum * target)
	}
}
