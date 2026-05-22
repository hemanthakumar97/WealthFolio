package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StockMetrics holds the fundamental scorecard for a stock holding.
type StockMetrics struct {
	Symbol       string `json:"symbol"`
	CompanyName  string `json:"company_name"`
	Sector       string `json:"sector"`
	Industry     string `json:"industry"`

	// Valuation
	TrailingPE  float64 `json:"trailing_pe"`
	ForwardPE   float64 `json:"forward_pe"`
	PriceToBook float64 `json:"price_to_book"`
	PEGRatio    float64 `json:"peg_ratio"`

	// Profitability
	ROE             float64 `json:"roe"`
	ProfitMargin    float64 `json:"profit_margin"`
	OperatingMargin float64 `json:"operating_margin"`

	// Growth
	RevenueGrowth  float64 `json:"revenue_growth"`
	EarningsGrowth float64 `json:"earnings_growth"`

	// Financial health
	DebtToEquity float64 `json:"debt_to_equity"`
	CurrentRatio float64 `json:"current_ratio"`

	// Size & income
	MarketCapCr   float64 `json:"market_cap_cr"`
	DividendYield float64 `json:"dividend_yield"`
	Beta          float64 `json:"beta"`
	Week52High    float64 `json:"week_52_high"`
	Week52Low     float64 `json:"week_52_low"`

	// Price-based (from our prices table)
	Return1YPct   float64 `json:"return_1y_pct"`
	MaxDrawdown3Y float64 `json:"max_drawdown_3y_pct"`

	// Chart series
	PriceHistory   []ChartPoint `json:"price_history,omitempty"`
	DrawdownSeries []ChartPoint `json:"drawdown_series,omitempty"`

	Zero1Score   int      `json:"zero1_score"`           // 0–100
	AvailableMax int      `json:"available_max"`         // max pts from non-gap metrics
	DataGaps     []string `json:"data_gaps,omitempty"`   // metrics with no data
}

// sectorPE maps Yahoo Finance sector names to rough median trailing PE.
// Used as benchmark for valuation scoring.
var sectorPE = map[string]float64{
	"Technology":             28,
	"Financial Services":     14,
	"Healthcare":             30,
	"Consumer Defensive":     45,
	"Consumer Cyclical":      25,
	"Communication Services": 22,
	"Industrials":            22,
	"Energy":                 12,
	"Basic Materials":        15,
	"Real Estate":            30,
	"Utilities":              20,
}

// yahooQuoteSummaryResp is the subset of Yahoo v10 quoteSummary we care about.
type yahooQuoteSummaryResp struct {
	Finance struct {
		Result []struct {
			SummaryDetail struct {
				TrailingPE       yahooFloat `json:"trailingPE"`
				ForwardPE        yahooFloat `json:"forwardPE"`
				Beta             yahooFloat `json:"beta"`
				MarketCap        yahooFloat `json:"marketCap"`
				FiftyTwoWeekHigh yahooFloat `json:"fiftyTwoWeekHigh"`
				FiftyTwoWeekLow  yahooFloat `json:"fiftyTwoWeekLow"`
				DividendYield    yahooFloat `json:"dividendYield"`
			} `json:"summaryDetail"`
			DefaultKeyStatistics struct {
				PriceToBook    yahooFloat `json:"priceToBook"`
				PEGRatio       yahooFloat `json:"pegRatio"`
				ProfitMargins  yahooFloat `json:"profitMargins"`
				TrailingEPS    yahooFloat `json:"trailingEps"`
			} `json:"defaultKeyStatistics"`
			FinancialData struct {
				ReturnOnEquity  yahooFloat `json:"returnOnEquity"`
				DebtToEquity    yahooFloat `json:"debtToEquity"`
				CurrentRatio    yahooFloat `json:"currentRatio"`
				RevenueGrowth   yahooFloat `json:"revenueGrowth"`
				EarningsGrowth  yahooFloat `json:"earningsGrowth"`
				OperatingMargins yahooFloat `json:"operatingMargins"`
				GrossMargins    yahooFloat `json:"grossMargins"`
			} `json:"financialData"`
			Price struct {
				LongName       string     `json:"longName"`
				ShortName      string     `json:"shortName"`
				Currency       string     `json:"currency"`
				RegularMarketPrice yahooFloat `json:"regularMarketPrice"`
			} `json:"price"`
			AssetProfile struct {
				Sector   string `json:"sector"`
				Industry string `json:"industry"`
			} `json:"assetProfile"`
		} `json:"result"`
		Error *struct{ Code string } `json:"error"`
	} `json:"finance"`
}

// yahooFloat handles Yahoo's {raw, fmt} number objects.
type yahooFloat struct {
	Raw float64 `json:"raw"`
}

// FetchStockMetrics fetches Yahoo fundamentals + price history from DB and computes a score.
func FetchStockMetrics(ctx context.Context, instrumentID int64, yahooSymbol string, pool *pgxpool.Pool, timeout time.Duration) (*StockMetrics, error) {
	client := &http.Client{Timeout: timeout}

	// Fetch crumb from Yahoo (required since 2023).
	crumb, cookies, err := fetchYahooCrumb(client)
	if err != nil {
		return nil, fmt.Errorf("yahoo crumb: %w", err)
	}

	// Fetch quoteSummary with fundamentals.
	modules := "summaryDetail,defaultKeyStatistics,financialData,price,assetProfile"
	qsURL := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=%s&crumb=%s",
		url.PathEscape(yahooSymbol),
		url.QueryEscape(modules),
		url.QueryEscape(crumb),
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, qsURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yahoo quoteSummary fetch: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo quoteSummary: HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var qs yahooQuoteSummaryResp
	if err := json.Unmarshal(body, &qs); err != nil {
		return nil, fmt.Errorf("yahoo quoteSummary decode: %w", err)
	}
	if qs.Finance.Error != nil {
		return nil, fmt.Errorf("yahoo quoteSummary error: %s", qs.Finance.Error.Code)
	}
	if len(qs.Finance.Result) == 0 {
		return nil, fmt.Errorf("yahoo quoteSummary: no result for %s", yahooSymbol)
	}
	r := qs.Finance.Result[0]

	sm := &StockMetrics{
		Symbol:      yahooSymbol,
		CompanyName: r.Price.LongName,
		Sector:      r.AssetProfile.Sector,
		Industry:    r.AssetProfile.Industry,

		TrailingPE:      r.SummaryDetail.TrailingPE.Raw,
		ForwardPE:       r.SummaryDetail.ForwardPE.Raw,
		Beta:            r.SummaryDetail.Beta.Raw,
		Week52High:      r.SummaryDetail.FiftyTwoWeekHigh.Raw,
		Week52Low:       r.SummaryDetail.FiftyTwoWeekLow.Raw,
		DividendYield:   r.SummaryDetail.DividendYield.Raw * 100, // convert 0.02 → 2%

		PriceToBook:     r.DefaultKeyStatistics.PriceToBook.Raw,
		PEGRatio:        r.DefaultKeyStatistics.PEGRatio.Raw,
		ProfitMargin:    r.DefaultKeyStatistics.ProfitMargins.Raw * 100,

		ROE:             r.FinancialData.ReturnOnEquity.Raw * 100,
		OperatingMargin: r.FinancialData.OperatingMargins.Raw * 100,
		DebtToEquity:    r.FinancialData.DebtToEquity.Raw,
		CurrentRatio:    r.FinancialData.CurrentRatio.Raw,
		RevenueGrowth:   r.FinancialData.RevenueGrowth.Raw * 100,
		EarningsGrowth:  r.FinancialData.EarningsGrowth.Raw * 100,
	}

	// Market cap: Yahoo returns USD/INR in native currency; convert to Crores.
	marketCap := r.SummaryDetail.MarketCap.Raw
	if strings.HasSuffix(yahooSymbol, ".NS") || strings.HasSuffix(yahooSymbol, ".BO") {
		sm.MarketCapCr = roundF(marketCap/1e7, 0) // ₹ → Crores
	} else {
		sm.MarketCapCr = roundF(marketCap/1e7, 0) // approximate
	}

	if sm.CompanyName == "" {
		sm.CompanyName = r.Price.ShortName
	}

	// Fetch price history from our prices table.
	navs, err := loadPriceHistory(ctx, pool, instrumentID)
	if err == nil && len(navs) > 10 {
		today := time.Now()
		oneYearAgo := today.AddDate(-1, 0, 0)
		threeYearsAgo := today.AddDate(-3, 0, 0)
		fiveYearsAgo := today.AddDate(-5, 0, 0)

		current := navs[len(navs)-1].NAV
		nav1y := valueOnOrBefore(navs, oneYearAgo)
		if nav1y > 0 {
			sm.Return1YPct = roundF((current/nav1y-1)*100, 2)
		}
		sm.MaxDrawdown3Y = roundF(maxDrawdown(navs, threeYearsAgo), 2)
		sm.PriceHistory = sampleSeries(navs, fiveYearsAgo, 260)
		sm.DrawdownSeries = drawdownSeries(navs, threeYearsAgo, 156)
	}

	sm.Zero1Score = computeStockScore(sm)
	return sm, nil
}

// FetchStockMetricsBatch fetches stock metrics concurrently.
func FetchStockMetricsBatch(ctx context.Context, instruments []struct {
	ID          int64
	YahooSymbol string
}, pool *pgxpool.Pool, timeout time.Duration) map[int64]*StockMetrics {
	result := make(map[int64]*StockMetrics, len(instruments))
	// Stock fundamentals: fetch sequentially to avoid hammering Yahoo with crumb issues.
	for _, instr := range instruments {
		m, err := FetchStockMetrics(ctx, instr.ID, instr.YahooSymbol, pool, timeout)
		if err == nil && m != nil {
			result[instr.ID] = m
		}
	}
	return result
}

// loadPriceHistory fetches sorted daily prices from our prices table for a given instrument.
func loadPriceHistory(ctx context.Context, pool *pgxpool.Pool, instrumentID int64) ([]navPoint, error) {
	rows, err := pool.Query(ctx, `
		SELECT price_date, nav_price::float
		  FROM prices
		 WHERE instrument_id = $1
		 ORDER BY price_date ASC
	`, instrumentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var navs []navPoint
	for rows.Next() {
		var d time.Time
		var v float64
		if err := rows.Scan(&d, &v); err != nil {
			continue
		}
		if v > 0 {
			navs = append(navs, navPoint{Date: d, NAV: v})
		}
	}
	return navs, rows.Err()
}

// computeStockScore scores a stock out of available metric points only.
// Missing Yahoo fields (PE=0, PEG=0, etc.) are treated as Data Gaps and excluded
// from the denominator so the score is always meaningful, not artificially depressed.
func computeStockScore(m *StockMetrics) int {
	s := 0
	availMax := 0
	var gaps []string
	medianPE := sectorMedianPE(m.Sector)

	addPts := func(pts, max int, gap string) {
		if pts < 0 { // sentinel for "no data"
			gaps = append(gaps, gap)
		} else {
			s += pts
			availMax += max
		}
	}
	_ = addPts

	// ── 1. Valuation — 20 pts ────────────────────────────────────────────────
	// P/E vs sector median: 8 pts (gap if no PE data)
	if m.TrailingPE > 0 {
		availMax += 8
		ratio := m.TrailingPE / medianPE
		switch {
		case ratio < 0.7:
			s += 8
		case ratio < 0.9:
			s += 6
		case ratio < 1.1:
			s += 4
		case ratio < 1.5:
			s += 2
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "P/E")
	}
	// P/B: 6 pts
	if m.PriceToBook > 0 {
		availMax += 6
		switch {
		case m.PriceToBook < 1.0:
			s += 6
		case m.PriceToBook < 2.0:
			s += 5
		case m.PriceToBook < 4.0:
			s += 3
		case m.PriceToBook < 7.0:
			s += 1
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "P/B")
	}
	// PEG: 6 pts
	if m.PEGRatio > 0 {
		availMax += 6
		switch {
		case m.PEGRatio < 0.5:
			s += 6
		case m.PEGRatio < 1.0:
			s += 5
		case m.PEGRatio < 1.5:
			s += 3
		case m.PEGRatio < 2.5:
			s += 1
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "PEG")
	}

	// ── 2. Profitability — 20 pts ─────────────────────────────────────────────
	// ROE: 8 pts
	if m.ROE != 0 {
		availMax += 8
		switch {
		case m.ROE >= 25:
			s += 8
		case m.ROE >= 18:
			s += 6
		case m.ROE >= 12:
			s += 4
		case m.ROE > 0:
			s += 2
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "ROE")
	}
	// Net margin: 6 pts
	if m.ProfitMargin != 0 {
		availMax += 6
		switch {
		case m.ProfitMargin >= 20:
			s += 6
		case m.ProfitMargin >= 12:
			s += 5
		case m.ProfitMargin >= 6:
			s += 3
		case m.ProfitMargin > 0:
			s += 1
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "Net Margin")
	}
	// Op margin: 6 pts
	if m.OperatingMargin != 0 {
		availMax += 6
		switch {
		case m.OperatingMargin >= 25:
			s += 6
		case m.OperatingMargin >= 15:
			s += 5
		case m.OperatingMargin >= 8:
			s += 3
		case m.OperatingMargin > 0:
			s += 1
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "Op Margin")
	}

	// ── 3. Growth — 15 pts ───────────────────────────────────────────────────
	// Revenue growth YoY: 8 pts
	if m.RevenueGrowth != 0 {
		availMax += 8
		switch {
		case m.RevenueGrowth >= 25:
			s += 8
		case m.RevenueGrowth >= 15:
			s += 6
		case m.RevenueGrowth >= 8:
			s += 4
		case m.RevenueGrowth > 0:
			s += 2
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "Revenue Growth")
	}
	// EPS growth: 7 pts
	if m.EarningsGrowth != 0 {
		availMax += 7
		switch {
		case m.EarningsGrowth >= 25:
			s += 7
		case m.EarningsGrowth >= 15:
			s += 5
		case m.EarningsGrowth >= 8:
			s += 3
		case m.EarningsGrowth > 0:
			s += 1
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "EPS Growth")
	}

	// ── 4. Financial Health — 15 pts ─────────────────────────────────────────
	// D/E: 10 pts (zero-debt = full marks)
	availMax += 10
	if m.DebtToEquity <= 0 {
		s += 10 // zero-debt or data not applicable (e.g. financials)
	} else {
		switch {
		case m.DebtToEquity < 20:
			s += 10
		case m.DebtToEquity < 50:
			s += 8
		case m.DebtToEquity < 100:
			s += 5
		case m.DebtToEquity < 200:
			s += 2
		default:
			s += 0
		}
	}
	// Current ratio: 5 pts
	if m.CurrentRatio > 0 {
		availMax += 5
		switch {
		case m.CurrentRatio >= 2.5:
			s += 5
		case m.CurrentRatio >= 1.5:
			s += 4
		case m.CurrentRatio >= 1.0:
			s += 2
		default:
			s += 0
		}
	} else {
		gaps = append(gaps, "Current Ratio")
	}

	// ── 5. Returns / Momentum — 10 pts ───────────────────────────────────────
	// Always available (from prices table).
	availMax += 10
	switch {
	case m.Return1YPct >= 30:
		s += 5
	case m.Return1YPct >= 15:
		s += 4
	case m.Return1YPct >= 0:
		s += 2
	default:
		s += 0
	}
	switch {
	case m.MaxDrawdown3Y < 15:
		s += 5
	case m.MaxDrawdown3Y < 25:
		s += 4
	case m.MaxDrawdown3Y < 40:
		s += 2
	case m.MaxDrawdown3Y < 60:
		s += 1
	default:
		s += 0
	}

	// ── 6. Size / Quality — 10 pts ───────────────────────────────────────────
	availMax += 5
	switch {
	case m.MarketCapCr >= 20000:
		s += 5
	case m.MarketCapCr >= 5000:
		s += 4
	case m.MarketCapCr >= 500:
		s += 3
	default:
		s += 1
	}
	if m.Beta > 0 {
		availMax += 5
		switch {
		case m.Beta <= 0.8:
			s += 5
		case m.Beta <= 1.0:
			s += 4
		case m.Beta <= 1.2:
			s += 3
		case m.Beta <= 1.5:
			s += 2
		default:
			s += 1
		}
	} else {
		gaps = append(gaps, "Beta")
	}

	m.AvailableMax = availMax
	m.DataGaps = gaps

	if availMax == 0 {
		return 0
	}
	return int(math.Round(float64(s) * 100.0 / float64(availMax)))
}

func sectorMedianPE(sector string) float64 {
	if pe, ok := sectorPE[sector]; ok {
		return pe
	}
	return 22 // broad market default
}

// fetchYahooCrumb obtains the crumb token required for Yahoo Finance v10 API calls.
func fetchYahooCrumb(client *http.Client) (string, []*http.Cookie, error) {
	// Step 1: Visit finance.yahoo.com to receive the session cookie.
	req1, _ := http.NewRequest(http.MethodGet, "https://finance.yahoo.com/", nil)
	req1.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	resp1, err := client.Do(req1)
	if err != nil {
		return "", nil, fmt.Errorf("yahoo cookie: %w", err)
	}
	resp1.Body.Close()
	cookies := resp1.Cookies()

	// Step 2: Exchange cookie for crumb.
	req2, _ := http.NewRequest(http.MethodGet, "https://query2.finance.yahoo.com/v1/test/getcrumb", nil)
	req2.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	resp2, err := client.Do(req2)
	if err != nil {
		return "", nil, fmt.Errorf("yahoo crumb fetch: %w", err)
	}
	defer resp2.Body.Close()
	b, _ := io.ReadAll(resp2.Body)
	crumb := strings.TrimSpace(string(b))
	if crumb == "" || strings.Contains(crumb, "Unauthorized") {
		return "", nil, fmt.Errorf("yahoo crumb: empty or unauthorized")
	}
	return crumb, cookies, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
