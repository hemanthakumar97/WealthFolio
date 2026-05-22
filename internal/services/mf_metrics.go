package services

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

const mfAPIBase = "https://api.mfapi.in/mf"
const riskFreeRate = 7.0 // approximate Indian 91-day T-bill rate

// MFMetrics holds the Zero1 by Zerodha 6-metric scorecard results for a mutual fund.
type MFMetrics struct {
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
	StdDev5YMedian float64 `json:"std_dev_5y_median"`     // fund's own median annual std dev over 5Y (for relative comparison)
	Zero1Score     int     `json:"zero1_score"`           // 0–100, normalised to available metrics
	AvailableMax   int     `json:"available_max"`         // max pts from non-gap metrics
	DataGaps       []string `json:"data_gaps,omitempty"` // metrics with no data

	// Chart series (sampled for payload efficiency)
	NAVHistory      []ChartPoint `json:"nav_history,omitempty"`
	Rolling1YSeries []ChartPoint `json:"roll_1y_series,omitempty"`
	DrawdownSeries  []ChartPoint `json:"drawdown_series,omitempty"`
}

// knownFundData stores static Beta/AUM/TER for common Indian MFs.
// AUM in Crores and TER in % as of Q1 FY27. Update quarterly.
var knownFundData = map[string]struct {
	beta float64
	aum  float64
	ter  float64
}{
	"118778": {beta: 0.89, aum: 62000, ter: 0.61}, // Nippon India Small Cap
	"122639": {beta: 0.72, aum: 98000, ter: 0.59}, // Parag Parikh Flexi Cap
	"127042": {beta: 0.88, aum: 22000, ter: 0.56}, // Motilal Oswal Midcap
	"120828": {beta: 0.93, aum: 27000, ter: 0.62}, // quant Small Cap
	"120847": {beta: 0.92, aum: 10000, ter: 0.50}, // quant ELSS
	"120465": {beta: 0.85, aum: 8000, ter: 0.55},  // Axis Large Cap
	"120505": {beta: 0.87, aum: 25000, ter: 0.48}, // Axis Midcap
	"120594": {beta: 0.92, aum: 3000, ter: 0.70},  // ICICI Pru Technology
	"147844": {beta: 0.88, aum: 5000, ter: 0.58},  // ABSL PSU Equity
}

type mfAPIResponse struct {
	Meta struct {
		SchemeCategory string `json:"scheme_category"`
	} `json:"meta"`
	Data []struct {
		Date string `json:"date"`
		NAV  string `json:"nav"`
	} `json:"data"`
	Status string `json:"status"`
}

// FetchMFMetrics downloads NAV history from mfapi.in and computes the Zero1 scorecard.
func FetchMFMetrics(amfiCode string, timeout time.Duration) (*MFMetrics, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(fmt.Sprintf("%s/%s", mfAPIBase, amfiCode))
	if err != nil {
		return nil, fmt.Errorf("mfapi fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mfapi status %d", resp.StatusCode)
	}

	var raw mfAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("mfapi decode: %w", err)
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

	if kd, ok := knownFundData[amfiCode]; ok {
		m.Beta = kd.beta
		m.AUMCr = kd.aum
		m.TER = kd.ter
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

// ─── MF Scoring (100 pts) ────────────────────────────────────────────────────
//
// Rolling(30) + Sharpe(15) + StdDev(15) + Beta(10) + AUM(10) + TER(10) = 90 raw
// Normalised to 100 out of AVAILABLE metrics only (spec: "score out of remaining available points").
// Missing Beta/AUM/TER → 0 pts + logged in DataGaps + excluded from denominator.

// computeZero1Score scores a fund out of the AVAILABLE metric points only.
// Missing Beta/AUM/TER are logged as DataGaps and excluded from the denominator
// so the score still reflects 0–100 fairly.
// Std Dev is compared against the fund's own historical volatility percentile
// (not a fixed category number) to avoid penalising funds when the whole category moves.
func computeZero1Score(m *MFMetrics) int {
	s := 0
	availMax := 60 // Rolling(30) + Sharpe(15) + StdDev(15) always computable
	var gaps []string

	// ── 1. Rolling Returns — 30 pts ──────────────────────────────────────────
	// Avg 3Y CAGR vs own history context: 15 pts
	// Use absolute thresholds as a proxy for market-relative return quality.
	switch {
	case m.Rolling3YAvg >= 24:
		s += 15
	case m.Rolling3YAvg >= 18:
		s += 11
	case m.Rolling3YAvg >= 12:
		s += 7
	case m.Rolling3YAvg >= 6:
		s += 4
	default:
		s += 1
	}
	// Consistency — % positive 1Y periods: 10 pts
	switch {
	case m.Consistency1Y >= 92:
		s += 10
	case m.Consistency1Y >= 78:
		s += 7
	case m.Consistency1Y >= 65:
		s += 4
	default:
		s += 1
	}
	// Worst 3Y rolling (downside protection): 5 pts
	switch {
	case m.Rolling3YMin > 5:
		s += 5
	case m.Rolling3YMin > 0:
		s += 4
	case m.Rolling3YMin > -5:
		s += 2
	case m.Rolling3YMin > -10:
		s += 1
	default:
		s += 0
	}

	// ── 2. Sharpe Ratio — 15 pts ──────────────────────────────────────────────
	// Uses Sharpe vs 0 as the neutral line (category-agnostic — a positive Sharpe
	// always means the fund beats risk-free regardless of market conditions).
	switch {
	case m.Sharpe1Y >= 1.0:
		s += 15
	case m.Sharpe1Y >= 0.5:
		s += 12
	case m.Sharpe1Y >= 0.1:
		s += 8
	case m.Sharpe1Y >= -0.2:
		s += 5
	case m.Sharpe1Y >= -0.5:
		s += 2
	default:
		s += 0
	}

	// ── 3. Std Deviation — 15 pts ─────────────────────────────────────────────
	// Compare fund's 1Y std dev against its OWN 5Y median std dev
	// so the score reflects relative volatility control, not absolute thresholds.
	// This handles market-wide high-volatility periods fairly.
	if m.StdDev1Y > 0 {
		ownMedian := m.StdDev5YMedian
		if ownMedian <= 0 {
			ownMedian = categoryBenchStdDev(m.Category) // fallback
		}
		ratio := m.StdDev1Y / ownMedian
		switch {
		case ratio <= 0.80:
			s += 15 // significantly below own norm — excellent control
		case ratio <= 0.95:
			s += 12
		case ratio <= 1.10:
			s += 9
		case ratio <= 1.25:
			s += 5
		default:
			s += 2
		}
	} else {
		s += 5 // no data — neutral
	}

	// ── 4. Beta — 10 pts ──────────────────────────────────────────────────────
	if m.Beta > 0 {
		availMax += 10
		switch {
		case m.Beta <= 0.75:
			s += 10
		case m.Beta <= 0.85:
			s += 8
		case m.Beta <= 0.95:
			s += 6
		case m.Beta <= 1.05:
			s += 4
		default:
			s += 2
		}
	} else {
		gaps = append(gaps, "Beta")
	}

	// ── 5. AUM — 10 pts ───────────────────────────────────────────────────────
	if m.AUMCr > 0 {
		availMax += 10
		switch m.Category {
		case "Small Cap":
			switch {
			case m.AUMCr < 5000:
				s += 5
			case m.AUMCr < 20000:
				s += 10
			case m.AUMCr < 40000:
				s += 7
			default:
				s += 3 // capacity concern
			}
		case "Mid Cap":
			switch {
			case m.AUMCr < 2000:
				s += 5
			case m.AUMCr < 30000:
				s += 10
			case m.AUMCr < 60000:
				s += 7
			default:
				s += 4
			}
		default:
			switch {
			case m.AUMCr < 500:
				s += 4
			case m.AUMCr < 80000:
				s += 10
			default:
				s += 8
			}
		}
	} else {
		gaps = append(gaps, "AUM")
	}

	// ── 6. TER — 10 pts ───────────────────────────────────────────────────────
	if m.TER > 0 {
		availMax += 10
		switch {
		case m.TER < 0.5:
			s += 10
		case m.TER < 0.7:
			s += 8
		case m.TER < 1.0:
			s += 5
		case m.TER < 1.5:
			s += 3
		default:
			s += 1
		}
	} else {
		gaps = append(gaps, "TER")
	}

	m.AvailableMax = availMax
	m.DataGaps = gaps

	// Normalise to 100 out of AVAILABLE metric points only.
	if availMax == 0 {
		return 0
	}
	return int(math.Round(float64(s) * 100.0 / float64(availMax)))
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
