package services

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

const mfAPIBase = "https://api.mfapi.in/mf"
const riskFreeRate = 7.0 // approximate Indian 91-day T-bill rate

// MFPillarScores breaks the Zero1 score into 5 thematic pillars for UI display.
type MFPillarScores struct {
	ReturnQuality    int `json:"return_quality"`     // Alpha + CatRank + Roll3Y  (max 35)
	RiskAdjusted     int `json:"risk_adjusted"`      // Sharpe + Sortino + MaxDD  (max 30)
	Consistency      int `json:"consistency"`        // Consistency + Worst3Y + StdDev (max 20)
	FundOps          int `json:"fund_ops"`           // TER + Beta + AUM          (max 15)
	Validation       int `json:"validation"`         // GrowwRating               (max 5)
	ReturnQualityMax int `json:"return_quality_max"` // available max for pillar A
	RiskAdjustedMax  int `json:"risk_adjusted_max"`
	ConsistencyMax   int `json:"consistency_max"`
	FundOpsMax       int `json:"fund_ops_max"`
	ValidationMax    int `json:"validation_max"`
}

// MFMetrics holds the Zero1 by Zerodha 6-metric scorecard results for a mutual fund.
type MFMetrics struct {
	AMFICode      string  `json:"amfi_code"`
	SchemeName    string  `json:"scheme_name"`
	Category      string  `json:"category"`
	Rolling1YAvg  float64 `json:"rolling_1y_avg_pct"`
	Rolling3YAvg  float64 `json:"rolling_3y_avg_pct"`
	Rolling3YMin  float64 `json:"rolling_3y_min_pct"`
	Consistency1Y float64 `json:"consistency_1y_pct"` // % of rolling 1yr periods > 0
	StdDev1Y      float64 `json:"std_dev_1y_pct"`
	Sharpe1Y      float64 `json:"sharpe_1y"`
	MaxDrawdown3Y float64 `json:"max_drawdown_3y_pct"`
	Beta          float64 `json:"beta"`
	AUMCr         float64 `json:"aum_cr"`
	TER           float64 `json:"ter_pct"`
	StdDev5YMedian float64 `json:"std_dev_5y_median"` // fund's own median annual std dev over 5Y (for relative comparison)
	Zero1Score     int     `json:"zero1_score"`        // 0–100, normalised to available metrics
	AvailableMax   int     `json:"available_max"`      // max pts from non-gap metrics
	DataGaps       []string `json:"data_gaps,omitempty"`

	TERSource  string `json:"ter_source"`  // "live:groww" | "hardcoded" | ""
	BetaSource string `json:"beta_source"` // "live:groww" | "hardcoded" | ""

	// Groww-sourced live metrics (fetched from fund page __NEXT_DATA__).
	GrowwRating  int     `json:"groww_rating"`   // Groww star rating 1–5
	ExitLoad     string  `json:"exit_load"`       // e.g. "Exit load of 1% if redeemed within 1 year"
	Alpha        float64 `json:"alpha"`           // 3Y alpha vs benchmark (annualised %)
	SortinoRatio float64 `json:"sortino_ratio"`   // Sortino ratio (Groww, 3Y window)
	GrowwSharpe  float64 `json:"groww_sharpe"`    // Groww Sharpe ratio (3Y window)
	GrowwStdDev  float64 `json:"groww_std_dev"`   // Groww annualised std dev
	CatReturn1Y  float64 `json:"cat_return_1y"`   // category avg 1Y annualised return (%)
	CatReturn3Y  float64 `json:"cat_return_3y"`   // category avg 3Y annualised return (%)
	CatReturn5Y  float64 `json:"cat_return_5y"`   // category avg 5Y annualised return (%)
	CatRank1Y    int     `json:"cat_rank_1y"`     // rank within category (1Y)
	CatRank3Y    int     `json:"cat_rank_3y"`     // rank within category (3Y)
	CatRank5Y    int     `json:"cat_rank_5y"`     // rank within category (5Y)
	GrowwDataOK  bool    `json:"groww_data_ok"`   // true when Groww live fetch succeeded
	GrowwSlug    string  `json:"groww_slug"`      // actual Groww URL slug that resolved successfully

	// Pillar scores — populated alongside Zero1Score for UI breakdown.
	Pillars MFPillarScores `json:"pillars"`

	// Category-relative context — set by PostProcessMFBatch, not FetchMFMetrics.
	RelativeRank    int  `json:"relative_rank"`    // 0–100 percentile vs category peers in this portfolio
	CategoryBearish bool `json:"category_bearish"` // whole category median below risk-free rate

	// Chart series (sampled for payload efficiency)
	NAVHistory      []ChartPoint `json:"nav_history,omitempty"`
	Rolling1YSeries []ChartPoint `json:"roll_1y_series,omitempty"`
	DrawdownSeries  []ChartPoint `json:"drawdown_series,omitempty"`
}

// knownFundData stores static Beta/AUM/TER for common Indian MFs.
// ⚠ MANUALLY MAINTAINED — update quarterly from AMFI TER disclosure + AMC fact sheets.
// AUM in Crores (₹), TER in % (Direct plan), Beta vs Nifty 50. Last updated: May 2026.
// Source: AMFI TER data (amfiindia.com) + Groww/Zerodha fund pages.
// Funds not in this map get no Beta/AUM/TER score — shown as DataGaps in the scorecard.
var knownFundData = map[string]struct {
	beta float64
	aum  float64
	ter  float64
}{
	"118778": {beta: 0.89, aum: 62000, ter: 0.61}, // Nippon India Small Cap
	"122639": {beta: 0.72, aum: 98000, ter: 0.59}, // Parag Parikh Flexi Cap
	"127042": {beta: 0.88, aum: 22000, ter: 0.56}, // Motilal Oswal Midcap
	"120828": {beta: 0.93, aum: 27000, ter: 0.73}, // quant Small Cap — TER updated May 2026
	"120847": {beta: 0.92, aum: 10000, ter: 0.50}, // quant ELSS
	"120465": {beta: 0.85, aum: 8000, ter: 0.55},  // Axis Large Cap
	"120505": {beta: 0.87, aum: 25000, ter: 0.48}, // Axis Midcap
	"120594": {beta: 0.92, aum: 3000, ter: 0.70},  // ICICI Pru Technology
	"147844": {beta: 0.88, aum: 5000, ter: 0.58},  // ABSL PSU Equity
}

type mfAPIResponse struct {
	Meta struct {
		SchemeCategory string `json:"scheme_category"`
		SchemeName     string `json:"scheme_name"`
	} `json:"meta"`
	Data []struct {
		Date string `json:"date"`
		NAV  string `json:"nav"`
	} `json:"data"`
	Status string `json:"status"`
}

// FetchMFMetrics downloads NAV history from mfapi.in and computes the Zero1 scorecard.
// manualGrowwSlug overrides auto slug detection when provided (user-specified via UI).
func FetchMFMetrics(amfiCode string, timeout time.Duration, manualGrowwSlug ...string) (*MFMetrics, error) {
	client := &http.Client{Timeout: timeout}

	// Retry up to 3 times with backoff for transient mfapi errors (502, 503, 429).
	var raw mfAPIResponse
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
		resp, err := client.Get(fmt.Sprintf("%s/%s", mfAPIBase, amfiCode))
		if err != nil {
			lastErr = fmt.Errorf("mfapi fetch: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close()
			lastErr = fmt.Errorf("mfapi status %d (attempt %d)", resp.StatusCode, attempt+1)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("mfapi status %d", resp.StatusCode)
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			lastErr = fmt.Errorf("mfapi decode: %w", decodeErr)
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}

	navs := make([]navPoint, 0, len(raw.Data))
	for _, d := range raw.Data {
		t, err := time.Parse("02-01-2006", d.Date)
		if err != nil {
			continue
		}
		var nav float64
		fmt.Sscanf(d.NAV, "%f", &nav)
		if nav <= 0 {
			continue
		}
		navs = append(navs, navPoint{Date: t, NAV: nav})
	}
	sortNavPoints(navs)

	if len(navs) < 60 {
		return nil, fmt.Errorf("insufficient NAV data: %d points", len(navs))
	}

	m := &MFMetrics{}
	m.AMFICode = amfiCode
	m.SchemeName = raw.Meta.SchemeName
	m.Category = inferMFCategory(raw.Meta.SchemeCategory)

	today := time.Now()
	oneYearAgo := today.AddDate(-1, 0, 0)
	threeYearsAgo := today.AddDate(-3, 0, 0)

	currentNAV := navs[len(navs)-1].NAV
	nav1y := valueOnOrBefore(navs, oneYearAgo)

	roll1y := rollingCAGRs(navs, 365)
	roll3y := rollingCAGRs(navs, 365*3)

	if len(roll1y) > 0 {
		m.Rolling1YAvg = roundF(average(roll1y), 2)
		m.Consistency1Y = roundF(percentPositive(roll1y), 1)
	}
	if len(roll3y) > 0 {
		m.Rolling3YAvg = roundF(average(roll3y), 2)
		m.Rolling3YMin = roundF(minFloatVal(roll3y), 2)
	}

	m.StdDev1Y = roundF(annualisedStdDev(navs, oneYearAgo), 2)

	if nav1y > 0 && m.StdDev1Y > 0 {
		ret1y := (currentNAV/nav1y - 1) * 100
		m.Sharpe1Y = roundF((ret1y-riskFreeRate)/m.StdDev1Y, 3)
	}

	m.MaxDrawdown3Y = roundF(maxDrawdown(navs, threeYearsAgo), 2)

	// Compute fund's own 5Y median annual std dev for relative volatility comparison.
	fiveYearsAgo := today.AddDate(-5, 0, 0)
	m.StdDev5YMedian = roundF(medianAnnualStdDev(navs, fiveYearsAgo), 2)

	// Fetch live fund data from Groww (AUM, TER, Beta, Alpha, Sharpe, Sortino, ratings, etc.)
	// Use the manual slug from the DB when provided; otherwise auto-detect from the scheme name.
	// Fall back to knownFundData for Beta/AUM/TER if Groww fetch fails.
	var growwOverrideSlug string
	if len(manualGrowwSlug) > 0 {
		growwOverrideSlug = strings.TrimSpace(manualGrowwSlug[0])
	}
	if gd, err := fetchGrowwFundData(raw.Meta.SchemeName, timeout/2, growwOverrideSlug); err == nil {
		m.GrowwDataOK = true
		m.GrowwSlug = gd.Slug
		if gd.AUM > 0 {
			m.AUMCr = roundF(gd.AUM, 0)
		}
		if gd.TER > 0 {
			m.TER = roundF(gd.TER, 2)
			m.TERSource = "live:groww"
		}
		if gd.Beta > 0 {
			m.Beta = roundF(gd.Beta, 4)
			m.BetaSource = "live:groww"
		}
		if gd.Alpha != 0 {
			m.Alpha = roundF(gd.Alpha, 4)
		}
		if gd.Sharpe != 0 {
			m.GrowwSharpe = roundF(gd.Sharpe, 4)
		}
		if gd.Sortino != 0 {
			m.SortinoRatio = roundF(gd.Sortino, 4)
		}
		if gd.StdDev > 0 {
			m.GrowwStdDev = roundF(gd.StdDev, 4)
		}
		if gd.Rating > 0 {
			m.GrowwRating = gd.Rating
		}
		if gd.ExitLoad != "" {
			m.ExitLoad = gd.ExitLoad
		}
		m.CatReturn1Y = roundF(gd.CatReturn1Y, 2)
		m.CatReturn3Y = roundF(gd.CatReturn3Y, 2)
		m.CatReturn5Y = roundF(gd.CatReturn5Y, 2)
		m.CatRank1Y = gd.CatRank1Y
		m.CatRank3Y = gd.CatRank3Y
		m.CatRank5Y = gd.CatRank5Y
	} else {
		// Groww failed — fall back to knownFundData for Beta/AUM/TER.
		if kd, ok := knownFundData[amfiCode]; ok {
			m.Beta = kd.beta
			m.AUMCr = kd.aum
			m.BetaSource = "hardcoded"
			m.TER = kd.ter
			m.TERSource = "hardcoded"
		}
	}

	m.Zero1Score = computeZero1Score(m)

	m.NAVHistory = sampleSeries(navs, fiveYearsAgo, 260)
	m.Rolling1YSeries = rollingCAGRSeries(navs, 365, 520)
	m.DrawdownSeries = drawdownSeries(navs, threeYearsAgo, 156)

	return m, nil
}

// FetchMFMetricsBatch fetches metrics concurrently; failed fetches are silently skipped.
func FetchMFMetricsBatch(amfiCodes []string, timeout time.Duration) map[string]*MFMetrics {
	result := make(map[string]*MFMetrics, len(amfiCodes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, code := range amfiCodes {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			m, err := FetchMFMetrics(c, timeout)
			if err == nil && m != nil {
				mu.Lock()
				result[c] = m
				mu.Unlock()
			}
		}(code)
	}
	wg.Wait()
	return result
}

func inferMFCategory(schemeCategory string) string {
	s := strings.ToLower(schemeCategory)
	switch {
	case strings.Contains(s, "small cap"):
		return "Small Cap"
	case strings.Contains(s, "mid cap"):
		return "Mid Cap"
	case strings.Contains(s, "large cap"):
		return "Large Cap"
	case strings.Contains(s, "flexi cap"):
		return "Flexi Cap"
	case strings.Contains(s, "multi cap"):
		return "Multi Cap"
	case strings.Contains(s, "elss"):
		return "ELSS"
	case strings.Contains(s, "index"):
		return "Index"
	default:
		return "Diversified"
	}
}

// ─── MF Scoring — 5-Pillar Framework ────────────────────────────────────────
//
// Pillar A — Returns Quality   (max 35 pts): Alpha×15 + CatRank3Y×12 + Roll3Y×8
// Pillar B — Risk-Adjusted     (max 30 pts): Sharpe×15 + Sortino×10 + MaxDD×5
// Pillar C — Consistency       (max 20 pts): Consistency×10 + Worst3Y×5 + StdDevRatio×5
// Pillar D — Fund Operations   (max 15 pts): TER×6 + Beta×5 + AUM×4
// Pillar E — Validation        (max  5 pts): GrowwRating×5
//
// Max possible: 105 pts → normalised to 100.
// Optional metrics (Alpha, CatRank, Sortino, Rating, Beta, AUM, TER) excluded
// from the denominator when unavailable so the score remains 0–100.
//
// Weight rationale:
//   Alpha leads (14.3%) — THE signal of manager skill; negative alpha = worse than index.
//   Sharpe ties Alpha — risk-adjusted quality matters as much as raw outperformance.
//   CatRank (11.4%) — separates manager skill from riding a bull category.
//   Sortino (9.5%) — more relevant than Sharpe for equity (upside vol is welcome).
//   Consistency (9.5%) — reliability beats a few great years surrounded by losses.

func computeZero1Score(m *MFMetrics) int {
	var p MFPillarScores
	var gaps []string

	// ── Pillar A: Returns Quality ─────────────────────────────────────────────

	// A1. Alpha — 15 pts (manager outperformance vs benchmark, risk-adjusted)
	if m.Alpha != 0 {
		p.ReturnQualityMax += 15
		switch {
		case m.Alpha >= 5:
			p.ReturnQuality += 15
		case m.Alpha >= 3:
			p.ReturnQuality += 12
		case m.Alpha >= 1:
			p.ReturnQuality += 9
		case m.Alpha >= 0:
			p.ReturnQuality += 6
		case m.Alpha >= -2:
			p.ReturnQuality += 3
		default:
			p.ReturnQuality += 0
		}
	} else {
		gaps = append(gaps, "Alpha")
	}

	// A2. Category Rank 3Y — 12 pts (outperformance vs peers, not just category tailwind)
	if m.CatRank3Y > 0 {
		p.ReturnQualityMax += 12
		switch {
		case m.CatRank3Y <= 3:
			p.ReturnQuality += 12
		case m.CatRank3Y <= 10:
			p.ReturnQuality += 10
		case m.CatRank3Y <= 20:
			p.ReturnQuality += 7
		case m.CatRank3Y <= 30:
			p.ReturnQuality += 4
		default:
			p.ReturnQuality += 2
		}
	} else {
		gaps = append(gaps, "CategoryRank")
	}

	// A3. Rolling 3Y Avg CAGR — 8 pts (absolute returns; lower weight since alpha contextualises it)
	p.ReturnQualityMax += 8
	switch {
	case m.Rolling3YAvg >= 24:
		p.ReturnQuality += 8
	case m.Rolling3YAvg >= 18:
		p.ReturnQuality += 6
	case m.Rolling3YAvg >= 12:
		p.ReturnQuality += 4
	case m.Rolling3YAvg >= 8:
		p.ReturnQuality += 2
	default:
		p.ReturnQuality += 1
	}

	// ── Pillar B: Risk-Adjusted Performance ──────────────────────────────────

	// B1. Sharpe Ratio — 15 pts (prefer Groww 3Y; fall back to computed 1Y)
	sharpe := m.GrowwSharpe
	if sharpe == 0 {
		sharpe = m.Sharpe1Y
	}
	p.RiskAdjustedMax += 15
	switch {
	case sharpe >= 1.0:
		p.RiskAdjusted += 15
	case sharpe >= 0.5:
		p.RiskAdjusted += 12
	case sharpe >= 0.1:
		p.RiskAdjusted += 8
	case sharpe >= -0.2:
		p.RiskAdjusted += 4
	case sharpe >= -0.5:
		p.RiskAdjusted += 2
	default:
		p.RiskAdjusted += 0
	}

	// B2. Sortino Ratio — 10 pts (downside risk only; better suited to equity funds)
	if m.SortinoRatio != 0 {
		p.RiskAdjustedMax += 10
		switch {
		case m.SortinoRatio >= 1.5:
			p.RiskAdjusted += 10
		case m.SortinoRatio >= 1.0:
			p.RiskAdjusted += 8
		case m.SortinoRatio >= 0.5:
			p.RiskAdjusted += 6
		case m.SortinoRatio >= 0:
			p.RiskAdjusted += 3
		default:
			p.RiskAdjusted += 0
		}
	} else {
		gaps = append(gaps, "Sortino")
	}

	// B3. Max Drawdown 3Y — 5 pts (worst peak-to-trough; stored as positive %)
	p.RiskAdjustedMax += 5
	switch {
	case m.MaxDrawdown3Y <= 15:
		p.RiskAdjusted += 5
	case m.MaxDrawdown3Y <= 25:
		p.RiskAdjusted += 4
	case m.MaxDrawdown3Y <= 35:
		p.RiskAdjusted += 3
	case m.MaxDrawdown3Y <= 45:
		p.RiskAdjusted += 2
	default:
		p.RiskAdjusted += 1
	}

	// ── Pillar C: Consistency & Stability ────────────────────────────────────

	// C1. Rolling 1Y Consistency — 10 pts (% of 1Y windows with positive return)
	p.ConsistencyMax += 10
	switch {
	case m.Consistency1Y >= 92:
		p.Consistency += 10
	case m.Consistency1Y >= 78:
		p.Consistency += 7
	case m.Consistency1Y >= 65:
		p.Consistency += 4
	case m.Consistency1Y >= 50:
		p.Consistency += 2
	default:
		p.Consistency += 1
	}

	// C2. Worst 3Y Rolling Return — 5 pts (tail-risk protection)
	p.ConsistencyMax += 5
	switch {
	case m.Rolling3YMin > 5:
		p.Consistency += 5
	case m.Rolling3YMin > 0:
		p.Consistency += 4
	case m.Rolling3YMin > -5:
		p.Consistency += 2
	case m.Rolling3YMin > -10:
		p.Consistency += 1
	default:
		p.Consistency += 0
	}

	// C3. Std Dev ratio vs 5Y median — 5 pts (relative volatility control)
	sdVal := m.GrowwStdDev
	if sdVal <= 0 {
		sdVal = m.StdDev1Y
	}
	p.ConsistencyMax += 5
	if sdVal > 0 {
		ownMedian := m.StdDev5YMedian
		if ownMedian <= 0 {
			ownMedian = categoryBenchStdDev(m.Category)
		}
		ratio := sdVal / ownMedian
		switch {
		case ratio <= 0.80:
			p.Consistency += 5
		case ratio <= 0.95:
			p.Consistency += 4
		case ratio <= 1.10:
			p.Consistency += 3
		case ratio <= 1.25:
			p.Consistency += 2
		default:
			p.Consistency += 1
		}
	} else {
		p.Consistency += 3 // neutral when no data
	}

	// ── Pillar D: Fund Operations ─────────────────────────────────────────────

	// D1. TER — 6 pts (guaranteed cost drag; direct plans should be <1%)
	if m.TER > 0 {
		p.FundOpsMax += 6
		switch {
		case m.TER < 0.5:
			p.FundOps += 6
		case m.TER < 0.7:
			p.FundOps += 5
		case m.TER < 1.0:
			p.FundOps += 3
		case m.TER < 1.5:
			p.FundOps += 2
		default:
			p.FundOps += 1
		}
	} else {
		gaps = append(gaps, "TER")
	}

	// D2. Beta — 5 pts (market sensitivity; lower = more cushion in downturns)
	if m.Beta > 0 {
		p.FundOpsMax += 5
		switch {
		case m.Beta <= 0.75:
			p.FundOps += 5
		case m.Beta <= 0.85:
			p.FundOps += 4
		case m.Beta <= 0.95:
			p.FundOps += 3
		case m.Beta <= 1.05:
			p.FundOps += 2
		default:
			p.FundOps += 1
		}
	} else {
		gaps = append(gaps, "Beta")
	}

	// D3. AUM — 4 pts (capacity concern matters mainly for small/mid cap)
	if m.AUMCr > 0 {
		p.FundOpsMax += 4
		switch m.Category {
		case "Small Cap":
			switch {
			case m.AUMCr < 5000:
				p.FundOps += 2
			case m.AUMCr < 20000:
				p.FundOps += 4
			case m.AUMCr < 40000:
				p.FundOps += 3
			default:
				p.FundOps += 1
			}
		case "Mid Cap":
			switch {
			case m.AUMCr < 2000:
				p.FundOps += 2
			case m.AUMCr < 30000:
				p.FundOps += 4
			case m.AUMCr < 60000:
				p.FundOps += 3
			default:
				p.FundOps += 2
			}
		default:
			switch {
			case m.AUMCr < 500:
				p.FundOps += 2
			case m.AUMCr < 80000:
				p.FundOps += 4
			default:
				p.FundOps += 3
			}
		}
	} else {
		gaps = append(gaps, "AUM")
	}

	// ── Pillar E: Validation ──────────────────────────────────────────────────

	// E1. Groww Rating — 5 pts (editorial signal aggregating factors we may miss)
	if m.GrowwRating > 0 {
		p.ValidationMax += 5
		p.Validation += m.GrowwRating // 1–5 maps directly to 1–5 pts
	} else {
		gaps = append(gaps, "GrowwRating")
	}

	m.Pillars = p
	m.DataGaps = gaps

	availMax := p.ReturnQualityMax + p.RiskAdjustedMax + p.ConsistencyMax + p.FundOpsMax + p.ValidationMax
	m.AvailableMax = availMax
	if availMax == 0 {
		return 0
	}
	total := p.ReturnQuality + p.RiskAdjusted + p.Consistency + p.FundOps + p.Validation
	return int(math.Round(float64(total) * 100.0 / float64(availMax)))
}

// ─── Groww fund data fetch ────────────────────────────────────────────────────

// growwFundData holds live metrics fetched from a Groww MF fund page.
type growwFundData struct {
	Slug        string // the URL slug that successfully resolved
	AUM         float64
	TER         float64
	Beta        float64
	Alpha       float64
	Sharpe      float64
	Sortino     float64
	StdDev      float64
	Rating      int
	ExitLoad    string
	CatReturn1Y float64
	CatReturn3Y float64
	CatReturn5Y float64
	CatRank1Y   int
	CatRank3Y   int
	CatRank5Y   int
}

// fetchGrowwFundData scrapes the Groww MF page for live fund metrics.
// Groww embeds full fund data in a __NEXT_DATA__ JSON block on every fund page.
// The __NEXT_DATA__ tag may include a nonce attribute, so we match id="__NEXT_DATA__"
// flexibly rather than requiring the exact tag format.
//
// mfapi scheme names often use "Fund Name - Growth Option - Direct Plan" format which
// does not match Groww's URL slug. We extract the base name and try multiple suffixes.
func fetchGrowwFundData(schemeName string, timeout time.Duration, overrideSlug ...string) (*growwFundData, error) {
	var slugs []string
	if len(overrideSlug) > 0 && overrideSlug[0] != "" {
		// Manual slug from DB goes first; still fall back to auto-detected candidates.
		slugs = append([]string{overrideSlug[0]}, growwSlugCandidates(schemeName)...)
	} else {
		slugs = growwSlugCandidates(schemeName)
	}
	if len(slugs) == 0 {
		return nil, fmt.Errorf("groww: no slug candidates for %q", schemeName)
	}

	client := &http.Client{Timeout: timeout}

	var lastErr error
	for _, slug := range slugs {
		req, err := http.NewRequest(http.MethodGet, "https://groww.in/mutual-funds/"+slug, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("groww fetch %q: %w", slug, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("groww: HTTP %d for slug %q", resp.StatusCode, slug)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		result, err := parseGrowwNextData(string(body))
		if err != nil {
			lastErr = err
			continue
		}
		result.Slug = slug
		return result, nil
	}
	return nil, lastErr
}

// growwSlugCandidates returns URL slug candidates for a Groww MF page.
// mfapi names use "Fund Name - Growth Option - Direct Plan" format; Groww uses
// "fund-name-direct-plan-growth" or "fund-name-direct-growth".
// We try the preferred forms first, then fall back to the full-name slug.
func growwSlugCandidates(name string) []string {
	lower := strings.ToLower(name)

	// Extract base fund name by stripping known mfapi suffixes.
	// mfapi uses both " - " (spaced) and "-" (plain) as separators depending on the fund.
	base := lower
	for _, sfx := range []string{
		" - growth option - direct plan",
		" - growth - direct plan",
		" - direct plan - growth",
		" - growth option",
		" - direct plan growth",
		"-direct plan-growth option", // plain hyphen format e.g. "Fund-Direct Plan-Growth Option"
		"-direct plan-growth",
		"-direct growth",
		" - direct growth",
	} {
		if idx := strings.Index(lower, sfx); idx > 0 {
			base = lower[:idx]
			break
		}
	}

	toSlug := func(s string) string {
		var b strings.Builder
		prev := false
		for _, r := range s {
			switch {
			case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
				b.WriteRune(r)
				prev = false
			case r == ' ' || r == '-' || r == '_' || r == '/' || r == '&' || r == '.':
				if !prev && b.Len() > 0 {
					b.WriteRune('-')
					prev = true
				}
			}
		}
		return strings.TrimRight(b.String(), "-")
	}

	baseSlug := toSlug(base)
	isRegular := strings.Contains(lower, "regular plan") || strings.Contains(lower, " regular ")

	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	if isRegular {
		add(baseSlug + "-regular-plan-growth")
		add(baseSlug + "-regular-growth")
	} else {
		add(baseSlug + "-direct-plan-growth")
		add(baseSlug + "-direct-growth")
	}
	add(SchemeNameToGrowwSlug(name)) // full-name slug as final fallback

	return out
}

// parseGrowwNextData extracts fund metrics from a Groww MF page's __NEXT_DATA__ JSON.
func parseGrowwNextData(s string) (*growwFundData, error) {

	// Find __NEXT_DATA__ JSON — Groww adds nonce/crossorigin attrs, so we
	// locate id="__NEXT_DATA__" then seek to the next >.
	const idMarker = `id="__NEXT_DATA__"`
	idIdx := strings.Index(s, idMarker)
	if idIdx == -1 {
		return nil, fmt.Errorf("groww: __NEXT_DATA__ not found")
	}
	jsonStart := strings.Index(s[idIdx:], ">")
	if jsonStart == -1 {
		return nil, fmt.Errorf("groww: __NEXT_DATA__ tag end not found")
	}
	jsonStart += idIdx + 1
	jsonEnd := strings.Index(s[jsonStart:], "</script>")
	if jsonEnd == -1 {
		return nil, fmt.Errorf("groww: __NEXT_DATA__ closing tag not found")
	}

	var nd struct {
		Props struct {
			PageProps struct {
				MFServerSideData struct {
					AUM          float64         `json:"aum"`
					ExpenseRatio json.RawMessage `json:"expense_ratio"`
					GrowwRating  int             `json:"groww_rating"`
					ExitLoad     string          `json:"exit_load"`
					ReturnStats  []struct {
						Sharpe   *float64 `json:"sharpe_ratio"`
						Beta     *float64 `json:"beta"`
						StdDev   *float64 `json:"standard_deviation"`
						Alpha    *float64 `json:"alpha"`
						Sortino  *float64 `json:"sortino_ratio"`
						CatRet1Y *float64 `json:"cat_return1y"`
						CatRet3Y *float64 `json:"cat_return3y"`
						CatRet5Y *float64 `json:"cat_return5y"`
						Rank1Y   *int     `json:"rank1yr"`
						Rank3Y   *int     `json:"rank3yr"`
						Rank5Y   *int     `json:"rank5yr"`
					} `json:"return_stats"`
				} `json:"mfServerSideData"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(s[jsonStart:jsonStart+jsonEnd]), &nd); err != nil {
		return nil, fmt.Errorf("groww: parse __NEXT_DATA__: %w", err)
	}

	mfsd := nd.Props.PageProps.MFServerSideData
	out := &growwFundData{
		AUM:      mfsd.AUM,
		Rating:   mfsd.GrowwRating,
		ExitLoad: mfsd.ExitLoad,
	}

	// expense_ratio can be a JSON string "0.73" or a number 0.73 depending on page version.
	if len(mfsd.ExpenseRatio) > 0 {
		raw := strings.Trim(strings.TrimSpace(string(mfsd.ExpenseRatio)), `"`)
		fmt.Sscanf(raw, "%f", &out.TER)
	}

	// ReturnStats[0] has Sharpe, Beta, Alpha, Sortino, StdDev, category returns/ranks.
	if len(mfsd.ReturnStats) > 0 {
		rs := mfsd.ReturnStats[0]
		if rs.Sharpe != nil {
			out.Sharpe = *rs.Sharpe
		}
		if rs.Beta != nil {
			out.Beta = *rs.Beta
		}
		if rs.StdDev != nil {
			out.StdDev = *rs.StdDev
		}
		if rs.Alpha != nil {
			out.Alpha = *rs.Alpha
		}
		if rs.Sortino != nil {
			out.Sortino = *rs.Sortino
		}
		if rs.CatRet1Y != nil {
			out.CatReturn1Y = *rs.CatRet1Y
		}
		if rs.CatRet3Y != nil {
			out.CatReturn3Y = *rs.CatRet3Y
		}
		if rs.CatRet5Y != nil {
			out.CatReturn5Y = *rs.CatRet5Y
		}
		if rs.Rank1Y != nil {
			out.CatRank1Y = *rs.Rank1Y
		}
		if rs.Rank3Y != nil {
			out.CatRank3Y = *rs.Rank3Y
		}
		if rs.Rank5Y != nil {
			out.CatRank5Y = *rs.Rank5Y
		}
	}

	return out, nil
}

// SchemeNameToGrowwSlug converts an AMFI scheme name to the Groww URL slug (exported for handler use).
// e.g. "Quant Small Cap Fund Direct Plan Growth" → "quant-small-cap-fund-direct-plan-growth"
func SchemeNameToGrowwSlug(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == '&':
			if !prevHyphen && b.Len() > 0 {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// categoryBenchmarkCAGR is the expected 3Y average CAGR for each MF category
// in a normal market environment. Used as a synthetic peer when a portfolio holds
// only one fund in a category. Update semi-annually.
var categoryBenchmarkCAGR = map[string]float64{
	"Small Cap":   16.0,
	"Mid Cap":     14.0,
	"Large Cap":   12.0,
	"Flexi Cap":   13.0,
	"Multi Cap":   13.0,
	"ELSS":        13.0,
	"Index":       12.0,
	"Diversified": 12.0,
}

// PostProcessMFBatch computes RelativeRank and CategoryBearish for each fund
// after all individual metrics have been fetched.
//
// Relative rank is computed within the portfolio's own category peers. When only
// one fund exists in a category the fund is compared against categoryBenchmarkCAGR.
// CategoryBearish is set when the category median 3Y CAGR falls below the risk-free
// rate, meaning the whole space is underperforming cash — a structural headwind.
func PostProcessMFBatch(metricsMap map[string]*MFMetrics) {
	// Group by category.
	type catGroup struct{ returns []float64 }
	groups := map[string]*catGroup{}
	for _, m := range metricsMap {
		g := groups[m.Category]
		if g == nil {
			g = &catGroup{}
			groups[m.Category] = g
		}
		g.returns = append(g.returns, m.Rolling3YAvg)
	}

	for _, m := range metricsMap {
		g := groups[m.Category]
		catMedian := medianFloat(g.returns)
		m.CategoryBearish = catMedian < riskFreeRate

		if len(g.returns) <= 1 {
			// Single fund in category — compare to benchmark CAGR.
			bench := categoryBenchmarkCAGR[m.Category]
			if bench == 0 {
				bench = 12.0
			}
			switch {
			case m.Rolling3YAvg >= bench*1.2:
				m.RelativeRank = 85
			case m.Rolling3YAvg >= bench:
				m.RelativeRank = 65
			case m.Rolling3YAvg >= bench*0.8:
				m.RelativeRank = 45
			default:
				m.RelativeRank = 20
			}
		} else {
			m.RelativeRank = mfPercentileRank(m.Rolling3YAvg, g.returns)
		}
	}
}

// mfPercentileRank returns 0–100 indicating what fraction of peers (excluding self)
// this fund outperforms. Returns 50 when the peer set is too small to be meaningful.
func mfPercentileRank(value float64, all []float64) int {
	if len(all) <= 1 {
		return 50
	}
	below := 0
	for _, v := range all {
		if v < value {
			below++
		}
	}
	return int(math.Round(float64(below) * 100.0 / float64(len(all)-1)))
}

// medianFloat returns the median of a float64 slice (unsorted copy).
func medianFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	// simple insertion sort — slice is always tiny
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	mid := len(cp) / 2
	if len(cp)%2 == 0 {
		return (cp[mid-1] + cp[mid]) / 2
	}
	return cp[mid]
}

func categoryBenchStdDev(category string) float64 {
	switch category {
	case "Small Cap":
		return 18.0
	case "Mid Cap":
		return 17.0
	case "Large Cap":
		return 14.0
	case "Flexi Cap", "Multi Cap":
		return 15.0
	case "ELSS":
		return 17.0
	default:
		return 16.0
	}
}
