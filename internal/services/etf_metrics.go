package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ETFMetrics mirrors MFMetrics so the frontend can use the same panel layout.
// NAV/price history comes from our prices table; AUM/TER/Beta from Yahoo quoteSummary.
type ETFMetrics struct {
	Category       string   `json:"category"`
	Rolling1YAvg   float64  `json:"rolling_1y_avg_pct"`
	Rolling3YAvg   float64  `json:"rolling_3y_avg_pct"`
	Rolling3YMin   float64  `json:"rolling_3y_min_pct"`
	Consistency1Y  float64  `json:"consistency_1y_pct"`
	StdDev1Y       float64  `json:"std_dev_1y_pct"`
	StdDev5YMedian float64  `json:"std_dev_5y_median"`
	Sharpe1Y       float64  `json:"sharpe_1y"`
	MaxDrawdown3Y  float64  `json:"max_drawdown_3y_pct"`
	Beta           float64  `json:"beta"`
	AUMCr          float64  `json:"aum_cr"`
	TER            float64  `json:"ter_pct"`
	Zero1Score     int      `json:"zero1_score"`
	AvailableMax   int      `json:"available_max"`
	DataGaps       []string `json:"data_gaps,omitempty"`

	NAVHistory      []ChartPoint `json:"nav_history,omitempty"`
	Rolling1YSeries []ChartPoint `json:"roll_1y_series,omitempty"`
	DrawdownSeries  []ChartPoint `json:"drawdown_series,omitempty"`
}

// yahooETFSummaryResp holds the ETF-relevant fields from Yahoo quoteSummary.
type yahooETFSummaryResp struct {
	Finance struct {
		Result []struct {
			SummaryDetail struct {
				Beta        yahooFloat `json:"beta"`
				TotalAssets yahooFloat `json:"totalAssets"`
			} `json:"summaryDetail"`
			DefaultKeyStatistics struct {
				AnnualReportExpenseRatio yahooFloat `json:"annualReportExpenseRatio"`
				Beta3Year               yahooFloat `json:"beta3Year"`
			} `json:"defaultKeyStatistics"`
			FundProfile struct {
				FeesExpensesInvestment struct {
					AnnualReportExpenseRatio yahooFloat `json:"annualReportExpenseRatio"`
				} `json:"feesExpensesInvestment"`
			} `json:"fundProfile"`
			AssetProfile struct {
				Category string `json:"category"`
			} `json:"assetProfile"`
			Price struct {
				LongName  string `json:"longName"`
				ShortName string `json:"shortName"`
			} `json:"price"`
		} `json:"result"`
		Error *struct{ Code string } `json:"error"`
	} `json:"finance"`
}

// knownETFData stores Beta/AUM/TER for popular Indian ETFs and US ETFs.
// AUM in Crores (₹), TER in %, Beta vs Nifty 50 (or relevant benchmark).
// Update semi-annually. Source: NSE/AMC fact sheets + AMFI.
var knownETFData = map[string]struct {
	beta  float64
	aumCr float64
	ter   float64
}{
	// Nippon India BeES series
	"NIFTYBEES.NS":   {beta: 1.00, aumCr: 27000, ter: 0.04},
	"JUNIORBEES.NS":  {beta: 0.92, aumCr: 8000,  ter: 0.19},
	"GOLDBEES.NS":    {beta: 0.05, aumCr: 8500,  ter: 0.82}, // gold: near-zero equity beta
	"SILVERBEES.NS":  {beta: 0.08, aumCr: 1500,  ter: 0.40},
	"BANKBEES.NS":    {beta: 1.10, aumCr: 7500,  ter: 0.19},
	"ITBEES.NS":      {beta: 0.85, aumCr: 2000,  ter: 0.19},
	// SBI / UTI
	"SETFNN50.NS":    {beta: 0.93, aumCr: 4000,  ter: 0.12},
	"UTINIFTETF.NS":  {beta: 1.00, aumCr: 12000, ter: 0.07},
	// HDFC
	"HDFCNIFTY.NS":   {beta: 1.00, aumCr: 5000,  ter: 0.10},
	// CPSE / PSU
	"CPSEETF.NS":     {beta: 0.85, aumCr: 2500,  ter: 0.01},
	// Mirae
	"MAFANG.NS":      {beta: 0.60, aumCr: 2000,  ter: 0.69}, // international
	// US ETFs (AUM in ₹ equivalent; TER in %)
	"TQQQ":           {beta: 3.00, aumCr: 150000, ter: 0.86}, // 3× leveraged, high beta
	"QQQ":            {beta: 1.20, aumCr: 600000, ter: 0.20},
	"SPY":            {beta: 1.00, aumCr: 2000000, ter: 0.09},
	"VTI":            {beta: 1.00, aumCr: 1500000, ter: 0.03},
}

// FetchETFMetrics fetches ETF price history from the prices table (same as MF computation)
// and pulls AUM/TER/Beta from the knownETFData map first, falling back to Yahoo quoteSummary.
func FetchETFMetrics(ctx context.Context, instrumentID int64, yahooSymbol string, pool *pgxpool.Pool, timeout time.Duration) (*ETFMetrics, error) {
	// Load price history from our DB (ETFs are backfilled via Yahoo like stocks).
	navs, err := loadPriceHistory(ctx, pool, instrumentID)
	if err != nil {
		return nil, fmt.Errorf("load ETF price history: %w", err)
	}
	if len(navs) < 20 {
		return nil, fmt.Errorf("not enough price history for this ETF (%d days). Go to Backfill and sync prices for this instrument first", len(navs))
	}

	m := &ETFMetrics{}
	m.Category = inferETFCategory(yahooSymbol)

	today := time.Now()
	oneYearAgo := today.AddDate(-1, 0, 0)
	threeYearsAgo := today.AddDate(-3, 0, 0)
	fiveYearsAgo := today.AddDate(-5, 0, 0)

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
	m.StdDev5YMedian = roundF(medianAnnualStdDev(navs, fiveYearsAgo), 2)

	// 1. Known ETF map — instant, reliable, no external call needed.
	if kd, ok := knownETFData[yahooSymbol]; ok {
		m.Beta = kd.beta
		m.AUMCr = kd.aumCr
		m.TER = kd.ter
	} else if yahooSymbol != "" {
		// 2. Fall back to Yahoo quoteSummary (best-effort).
		if yd, fetchErr := fetchETFYahooData(yahooSymbol, timeout); fetchErr == nil {
			m.Beta = yd.beta
			m.AUMCr = yd.aumCr
			m.TER = yd.ter
		}
	}

	m.Zero1Score = computeETFScore(m)

	m.NAVHistory = sampleSeries(navs, fiveYearsAgo, 260)
	m.Rolling1YSeries = rollingCAGRSeries(navs, 365, 520)
	m.DrawdownSeries = drawdownSeries(navs, threeYearsAgo, 156)

	return m, nil
}

type etfYahooData struct {
	beta  float64
	aumCr float64
	ter   float64
}

func fetchETFYahooData(yahooSymbol string, timeout time.Duration) (etfYahooData, error) {
	client := &http.Client{Timeout: timeout}
	crumb, cookies, err := fetchYahooCrumb(client)
	if err != nil {
		return etfYahooData{}, err
	}

	modules := "summaryDetail,defaultKeyStatistics,fundProfile,price,assetProfile"
	qsURL := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=%s&crumb=%s",
		url.PathEscape(yahooSymbol),
		url.QueryEscape(modules),
		url.QueryEscape(crumb),
	)
	req, _ := http.NewRequest(http.MethodGet, qsURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	resp, err := client.Do(req)
	if err != nil {
		return etfYahooData{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var qs yahooETFSummaryResp
	if err := json.Unmarshal(body, &qs); err != nil || len(qs.Finance.Result) == 0 {
		return etfYahooData{}, fmt.Errorf("parse ETF yahoo data")
	}
	r := qs.Finance.Result[0]

	beta := r.SummaryDetail.Beta.Raw
	if beta == 0 {
		beta = r.DefaultKeyStatistics.Beta3Year.Raw
	}

	ter := r.FundProfile.FeesExpensesInvestment.AnnualReportExpenseRatio.Raw
	if ter == 0 {
		ter = r.DefaultKeyStatistics.AnnualReportExpenseRatio.Raw
	}
	ter = ter * 100 // convert 0.005 → 0.5%

	// totalAssets is in USD/INR; convert to Crores (₹ → Cr = /1e7).
	aumCr := math.Round(r.SummaryDetail.TotalAssets.Raw / 1e7)

	return etfYahooData{beta: beta, aumCr: aumCr, ter: ter}, nil
}

// computeETFScore scores an ETF out of the AVAILABLE metric points only.
func computeETFScore(m *ETFMetrics) int {
	s := 0
	availMax := 60 // Rolling(30) + Sharpe(15) + StdDev(15) always available
	var gaps []string

	// 1. Rolling Returns — 30 pts
	switch {
	case m.Rolling3YAvg >= 18:
		s += 15
	case m.Rolling3YAvg >= 13:
		s += 11
	case m.Rolling3YAvg >= 8:
		s += 7
	case m.Rolling3YAvg >= 4:
		s += 4
	default:
		s += 1
	}
	switch {
	case m.Consistency1Y >= 90:
		s += 10
	case m.Consistency1Y >= 75:
		s += 7
	case m.Consistency1Y >= 60:
		s += 4
	default:
		s += 1
	}
	switch {
	case m.Rolling3YMin > 3:
		s += 5
	case m.Rolling3YMin > 0:
		s += 4
	case m.Rolling3YMin > -5:
		s += 2
	default:
		s += 0
	}

	// 2. Sharpe — 15 pts
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

	// 3. Std Dev vs own 5Y median — 15 pts (relative, not fixed benchmark)
	if m.StdDev1Y > 0 {
		median := m.StdDev5YMedian
		if median <= 0 {
			median = etfBenchStdDev(m.Category)
		}
		ratio := m.StdDev1Y / median
		switch {
		case ratio <= 0.80:
			s += 15
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
		s += 5
	}

	// 4. Beta — 10 pts (gap if missing)
	if m.Beta > 0 {
		availMax += 10
		switch {
		case m.Beta <= 0.80:
			s += 10
		case m.Beta <= 0.90:
			s += 8
		case m.Beta <= 1.00:
			s += 6
		case m.Beta <= 1.10:
			s += 4
		default:
			s += 2
		}
	} else {
		gaps = append(gaps, "Beta")
	}

	// 5. AUM — 10 pts (gap if missing)
	if m.AUMCr > 0 {
		availMax += 10
		switch {
		case m.AUMCr < 100:
			s += 2
		case m.AUMCr < 500:
			s += 5
		case m.AUMCr < 5000:
			s += 8
		default:
			s += 10
		}
	} else {
		gaps = append(gaps, "AUM")
	}

	// 6. TER — 10 pts (gap if missing)
	if m.TER > 0 {
		availMax += 10
		switch {
		case m.TER < 0.1:
			s += 10
		case m.TER < 0.2:
			s += 9
		case m.TER < 0.4:
			s += 7
		case m.TER < 0.7:
			s += 4
		default:
			s += 1
		}
	} else {
		gaps = append(gaps, "TER")
	}

	m.AvailableMax = availMax
	m.DataGaps = gaps

	if availMax == 0 {
		return 0
	}
	return int(math.Round(float64(s) * 100.0 / float64(availMax)))
}

func etfBenchStdDev(category string) float64 {
	switch category {
	case "Sectoral ETF":
		return 20.0
	case "Mid/Small Cap ETF":
		return 18.0
	case "International ETF":
		return 16.0
	default:
		return 14.0 // Nifty 50 / broad market
	}
}

func inferETFCategory(symbol string) string {
	// Heuristic based on common Indian ETF naming conventions.
	upper := symbol
	switch {
	case contains(upper, "BANK", "FIN"):
		return "Sectoral ETF"
	case contains(upper, "IT", "TECH", "PHARMA", "INFRA", "AUTO", "MNC", "PSU", "CPSE", "FMCG", "ENERGY", "CONSUMPTION", "MEDIA"):
		return "Sectoral ETF"
	case contains(upper, "NEXT50", "NIFTYNEXT", "JR", "JUNIOR", "MID", "SMALL", "MICROCAP", "ALPHA", "LOWVOL", "MOMENTUM"):
		return "Mid/Small Cap ETF"
	case contains(upper, "NASDAQ", "SP500", "WORLD", "HANG", "INTL", "US"):
		return "International ETF"
	default:
		return "Broad Market ETF"
	}
}

func contains(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				match := true
				for j := 0; j < len(sub); j++ {
					if s[i+j] != sub[j] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false
}
