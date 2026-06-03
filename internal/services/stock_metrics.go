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

type StockFinancialYear struct {
	Year        string  `json:"year"`
	RevenueCr   float64 `json:"revenue_cr"`
	NetIncomeCr float64 `json:"net_income_cr"`
	EBITDACr    float64 `json:"ebitda_cr,omitempty"`
	OpCFCr      float64 `json:"op_cf_cr,omitempty"`
	CapExCr     float64 `json:"capex_cr,omitempty"`
	FreeCFCr    float64 `json:"free_cf_cr,omitempty"`
	TotalDebtCr float64 `json:"total_debt_cr,omitempty"`
	CashCr      float64 `json:"cash_cr,omitempty"`
	EquityCr    float64 `json:"equity_cr,omitempty"`
}

// StockMetrics holds the fundamental scorecard for a stock holding.
type StockMetrics struct {
	Symbol      string `json:"symbol"`
	CompanyName string `json:"company_name"`
	Sector      string `json:"sector"`
	Industry    string `json:"industry"`

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

	// Extended fields — populated by analysis_service (not used in Zero1Score).
	ROCE            float64 `json:"roce,omitempty"`              // EBITDA / Capital Employed × 100
	PromoterHolding float64 `json:"promoter_holding,omitempty"`  // % from Yahoo insiders
	FreeCashFlowCr  float64 `json:"free_cash_flow_cr,omitempty"` // FCF in ₹ Crores

	// Chart series
	PriceHistory   []ChartPoint `json:"price_history,omitempty"`
	DrawdownSeries []ChartPoint `json:"drawdown_series,omitempty"`

	Zero1Score   int      `json:"zero1_score"`         // 0–100
	AvailableMax int      `json:"available_max"`       // max pts from non-gap metrics
	DataGaps     []string `json:"data_gaps,omitempty"` // metrics with no data

	// Sector-relative context — set by setStockContext after score is computed.
	RelativeRank  int  `json:"relative_rank"`  // 0–100, based on alpha vs sector benchmark
	SectorBearish bool `json:"sector_bearish"` // sector 1Y return is negative

	// Sector comparison (from static maps)
	SectorPE       float64 `json:"sector_pe"`
	SectorPB       float64 `json:"sector_pb"`
	SectorDivYield float64 `json:"sector_div_yield"`

	// Annual financials (last 3 years, most recent first)
	Financials []StockFinancialYear `json:"financials,omitempty"`

	// Shareholding
	PromoterPct float64 `json:"promoter_pct"`
	FIIPct      float64 `json:"fii_pct"`
	DIIPct      float64 `json:"dii_pct"`
	PublicPct   float64 `json:"public_pct"`
}

// sectorReturn1Y is the approximate 1Y price return for each sector (NSE/BSE).
// Used to compute stock alpha: if the whole sector fell 15%, a stock down 8% is
// actually outperforming. Update semi-annually. Source: NSE sectoral indices.
var sectorReturn1Y = map[string]float64{
	"Technology":             8.0,
	"Financial Services":     12.0,
	"Healthcare":             15.0,
	"Consumer Defensive":     10.0,
	"Consumer Cyclical":      5.0,
	"Communication Services": 10.0,
	"Industrials":            14.0,
	"Energy":                 10.0,
	"Basic Materials":        8.0,
	"Real Estate":            20.0,
	"Utilities":              12.0,
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

var sectorPB = map[string]float64{
	"Technology":             7.0,
	"Financial Services":     2.5,
	"Healthcare":             5.0,
	"Consumer Defensive":     8.0,
	"Consumer Cyclical":      4.0,
	"Communication Services": 3.5,
	"Industrials":            4.0,
	"Energy":                 2.0,
	"Basic Materials":        2.5,
	"Real Estate":            3.0,
	"Utilities":              2.5,
}

var sectorDivYield = map[string]float64{
	"Technology":             0.5,
	"Financial Services":     2.0,
	"Healthcare":             0.8,
	"Consumer Defensive":     2.5,
	"Consumer Cyclical":      1.5,
	"Communication Services": 3.0,
	"Industrials":            1.5,
	"Energy":                 4.0,
	"Basic Materials":        3.0,
	"Real Estate":            4.0,
	"Utilities":              4.5,
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
				PriceToBook   yahooFloat `json:"priceToBook"`
				PEGRatio      yahooFloat `json:"pegRatio"`
				ProfitMargins yahooFloat `json:"profitMargins"`
				TrailingEPS   yahooFloat `json:"trailingEps"`
			} `json:"defaultKeyStatistics"`
			FinancialData struct {
				ReturnOnEquity   yahooFloat `json:"returnOnEquity"`
				DebtToEquity     yahooFloat `json:"debtToEquity"`
				CurrentRatio     yahooFloat `json:"currentRatio"`
				RevenueGrowth    yahooFloat `json:"revenueGrowth"`
				EarningsGrowth   yahooFloat `json:"earningsGrowth"`
				OperatingMargins yahooFloat `json:"operatingMargins"`
				GrossMargins     yahooFloat `json:"grossMargins"`
			} `json:"financialData"`
			Price struct {
				LongName           string     `json:"longName"`
				ShortName          string     `json:"shortName"`
				Currency           string     `json:"currency"`
				RegularMarketPrice yahooFloat `json:"regularMarketPrice"`
			} `json:"price"`
			AssetProfile struct {
				Sector   string `json:"sector"`
				Industry string `json:"industry"`
			} `json:"assetProfile"`
			// Note: income/balance/cashflow/holders are fetched lazily in FetchStockFinancials.
			_unusedIncome struct {
				Statements []struct {
					EndDate   yahooFloat `json:"endDate"`
					Revenue   yahooFloat `json:"totalRevenue"`
					EBITDA    yahooFloat `json:"ebitda"`
					NetIncome yahooFloat `json:"netIncome"`
				} `json:"incomeStatementHistory"`
			} `json:"incomeStatementHistory"`
			_unusedBalance struct {
				Statements []struct {
					EndDate   yahooFloat `json:"endDate"`
					TotalDebt yahooFloat `json:"longTermDebt"`
					Cash      yahooFloat `json:"cash"`
					Equity    yahooFloat `json:"totalStockholderEquity"`
				} `json:"balanceSheetHistory"`
			} `json:"balanceSheetHistory"`
			CashflowStatementHistory struct {
				Statements []struct {
					EndDate yahooFloat `json:"endDate"`
					OpCF    yahooFloat `json:"totalCashFromOperatingActivities"`
					CapEx   yahooFloat `json:"capitalExpenditures"`
				} `json:"cashflowStatementHistory"`
			} `json:"cashflowStatementHistory"`
			MajorHoldersBreakdown struct {
				InsidersPercentHeld     yahooFloat `json:"insidersPercentHeld"`
				InstitutionsPercentHeld yahooFloat `json:"institutionsPercentHeld"`
			} `json:"majorHoldersBreakdown"`
		} `json:"result"`
		Error *struct{ Code string } `json:"error"`
	} `json:"quoteSummary"`
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

	// Fetch quoteSummary with fundamentals (no financial history — fetched lazily via FetchStockFinancials).
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

		TrailingPE:    r.SummaryDetail.TrailingPE.Raw,
		ForwardPE:     r.SummaryDetail.ForwardPE.Raw,
		Beta:          r.SummaryDetail.Beta.Raw,
		Week52High:    r.SummaryDetail.FiftyTwoWeekHigh.Raw,
		Week52Low:     r.SummaryDetail.FiftyTwoWeekLow.Raw,
		DividendYield: r.SummaryDetail.DividendYield.Raw * 100, // convert 0.02 → 2%

		PriceToBook:  r.DefaultKeyStatistics.PriceToBook.Raw,
		PEGRatio:     r.DefaultKeyStatistics.PEGRatio.Raw,
		ProfitMargin: r.DefaultKeyStatistics.ProfitMargins.Raw * 100,

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
	// Start with static-map fallbacks for sector comparison.
	sm.SectorPE = sectorPE[sm.Sector]
	sm.SectorPB = sectorPB[sm.Sector]
	sm.SectorDivYield = sectorDivYield[sm.Sector]

	// Tickertape live data — overrides Yahoo PE/PB/divYield and sector comparisons.
	ticker := strings.TrimSuffix(strings.TrimSuffix(yahooSymbol, ".NS"), ".BO")
	if ticker != "" {
		if td, err := fetchTickertapeStockData(ticker, timeout/3); err == nil {
			if td.pe > 0       { sm.TrailingPE = td.pe }
			if td.pb > 0       { sm.PriceToBook = td.pb }
			if td.divYield > 0 { sm.DividendYield = td.divYield }
			if td.indPE > 0    { sm.SectorPE = td.indPE }
			if td.indPB > 0    { sm.SectorPB = td.indPB }
			if td.indDY > 0    { sm.SectorDivYield = td.indDY }
		}
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
	setStockContext(sm)
	return sm, nil
}

// fetchTickertapeStockData fetches live PE, PB, div yield and sector comparisons from Tickertape.
func fetchTickertapeStockData(ticker string, timeout time.Duration) (tickertapeData, error) {
	client := &http.Client{Timeout: timeout}
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

	searchURL := "https://api.tickertape.in/search?text=" + url.QueryEscape(ticker)
	req1, _ := http.NewRequest(http.MethodGet, searchURL, nil)
	req1.Header.Set("User-Agent", ua)
	resp1, err := client.Do(req1)
	if err != nil {
		return tickertapeData{}, err
	}
	defer resp1.Body.Close()

	var searchResp struct {
		Data struct {
			Stocks []struct {
				SID  string `json:"sid"`
				Type string `json:"type"`
			} `json:"stocks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp1.Body).Decode(&searchResp); err != nil {
		return tickertapeData{}, err
	}
	sid := ""
	for _, s := range searchResp.Data.Stocks {
		if s.Type == "stock" {
			sid = s.SID
			break
		}
	}
	if sid == "" {
		return tickertapeData{}, fmt.Errorf("tickertape: no stock result for %s", ticker)
	}

	infoURL := "https://api.tickertape.in/stocks/info/" + url.PathEscape(sid)
	req2, _ := http.NewRequest(http.MethodGet, infoURL, nil)
	req2.Header.Set("User-Agent", ua)
	resp2, err := client.Do(req2)
	if err != nil {
		return tickertapeData{}, err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return tickertapeData{}, fmt.Errorf("tickertape stock info: HTTP %d", resp2.StatusCode)
	}

	var infoResp struct {
		Data struct {
			Ratios struct {
				TTMPE    *float64 `json:"ttmPe"`
				PE       *float64 `json:"pe"`
				PB       *float64 `json:"pb"`
				DivYield float64  `json:"divYield"`
				IndPE    *float64 `json:"indpe"`
				IndPB    *float64 `json:"indpb"`
				IndDY    float64  `json:"inddy"`
			} `json:"ratios"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&infoResp); err != nil {
		return tickertapeData{}, err
	}
	r := infoResp.Data.Ratios
	pe := 0.0
	if r.TTMPE != nil { pe = *r.TTMPE } else if r.PE != nil { pe = *r.PE }
	pb := 0.0
	if r.PB != nil { pb = *r.PB }
	indPE := 0.0
	if r.IndPE != nil { indPE = *r.IndPE }
	indPB := 0.0
	if r.IndPB != nil { indPB = *r.IndPB }
	return tickertapeData{pe: pe, pb: pb, divYield: r.DivYield, indPE: indPE, indPB: indPB, indDY: r.IndDY}, nil
}

// StockFinancials holds annual income statement, balance sheet, cash flow and shareholding data.
// Fetched lazily via FetchStockFinancials (separate from the main metrics call).
type StockFinancials struct {
	Years       []StockFinancialYear `json:"years"`
	PromoterPct float64              `json:"promoter_pct"`
	FIIPct      float64              `json:"fii_pct"`
	DIIPct      float64              `json:"dii_pct"`
	PublicPct   float64              `json:"public_pct"`
}

// FetchStockFinancials fetches annual income statement, balance sheet, cash flow and shareholding
// from Yahoo Finance quoteSummary. Called lazily when the user opens financial tabs.
func FetchStockFinancials(yahooSymbol string, timeout time.Duration) (*StockFinancials, error) {
	client := &http.Client{Timeout: timeout}
	crumb, cookies, err := fetchYahooCrumb(client)
	if err != nil {
		return nil, fmt.Errorf("yahoo crumb: %w", err)
	}

	modules := "incomeStatementHistory,balanceSheetHistory,cashflowStatementHistory,majorHoldersBreakdown"
	qsURL := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=%s&crumb=%s",
		url.PathEscape(yahooSymbol), url.QueryEscape(modules), url.QueryEscape(crumb),
	)
	req, _ := http.NewRequest(http.MethodGet, qsURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo: HTTP %d", resp.StatusCode)
	}

	var qs struct {
		QuoteSummary struct {
			Result []struct {
				IncomeStatementHistory struct {
					Statements []struct {
						EndDate   yahooFloat `json:"endDate"`
						Revenue   yahooFloat `json:"totalRevenue"`
						EBITDA    yahooFloat `json:"ebitda"`
						NetIncome yahooFloat `json:"netIncome"`
					} `json:"incomeStatementHistory"`
				} `json:"incomeStatementHistory"`
				BalanceSheetHistory struct {
					Statements []struct {
						EndDate   yahooFloat `json:"endDate"`
						TotalDebt yahooFloat `json:"longTermDebt"`
						Cash      yahooFloat `json:"cash"`
						Equity    yahooFloat `json:"totalStockholderEquity"`
					} `json:"balanceSheetHistory"`
				} `json:"balanceSheetHistory"`
				CashflowStatementHistory struct {
					Statements []struct {
						EndDate yahooFloat `json:"endDate"`
						OpCF    yahooFloat `json:"totalCashFromOperatingActivities"`
						CapEx   yahooFloat `json:"capitalExpenditures"`
					} `json:"cashflowStatementHistory"`
				} `json:"cashflowStatementHistory"`
				MajorHoldersBreakdown struct {
					InsidersPercentHeld     yahooFloat `json:"insidersPercentHeld"`
					InstitutionsPercentHeld yahooFloat `json:"institutionsPercentHeld"`
				} `json:"majorHoldersBreakdown"`
			} `json:"result"`
			Error *struct{ Code string } `json:"error"`
		} `json:"quoteSummary"`
	}
	if err := json.Unmarshal(body, &qs); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if qs.QuoteSummary.Error != nil {
		return nil, fmt.Errorf("yahoo error: %s", qs.QuoteSummary.Error.Code)
	}
	if len(qs.QuoteSummary.Result) == 0 {
		return nil, fmt.Errorf("no result")
	}
	r := qs.QuoteSummary.Result[0]

	f := &StockFinancials{}
	incStmts := r.IncomeStatementHistory.Statements
	balSheets := r.BalanceSheetHistory.Statements
	cashFlows := r.CashflowStatementHistory.Statements
	maxYears := 4
	if len(incStmts) < maxYears { maxYears = len(incStmts) }
	for i := 0; i < maxYears; i++ {
		stmt := incStmts[i]
		fy := StockFinancialYear{
			Year:        time.Unix(int64(stmt.EndDate.Raw), 0).Format("Jan 2006"),
			RevenueCr:   roundF(stmt.Revenue.Raw/1e7, 0),
			NetIncomeCr: roundF(stmt.NetIncome.Raw/1e7, 0),
			EBITDACr:    roundF(stmt.EBITDA.Raw/1e7, 0),
		}
		if i < len(balSheets) {
			bs := balSheets[i]
			fy.TotalDebtCr = roundF(bs.TotalDebt.Raw/1e7, 0)
			fy.CashCr = roundF(bs.Cash.Raw/1e7, 0)
			fy.EquityCr = roundF(bs.Equity.Raw/1e7, 0)
		}
		if i < len(cashFlows) {
			cf := cashFlows[i]
			fy.OpCFCr = roundF(cf.OpCF.Raw/1e7, 0)
			fy.CapExCr = roundF(math.Abs(cf.CapEx.Raw)/1e7, 0)
			fy.FreeCFCr = roundF((cf.OpCF.Raw+cf.CapEx.Raw)/1e7, 0)
		}
		f.Years = append(f.Years, fy)
	}

	mhb := r.MajorHoldersBreakdown
	f.PromoterPct = roundF(mhb.InsidersPercentHeld.Raw*100, 2)
	f.FIIPct = roundF(mhb.InstitutionsPercentHeld.Raw*100, 2)
	remaining := 100 - f.PromoterPct - f.FIIPct
	if remaining < 0 { remaining = 0 }
	f.PublicPct = roundF(remaining, 2)
	return f, nil
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

// setStockContext populates RelativeRank and SectorBearish using the stock's
// 1Y return relative to its sector benchmark.
//
// Alpha = Return1YPct − sectorReturn1Y[sector]
// A positive alpha means the stock is outperforming its sector even when the
// absolute return looks mediocre. A negative alpha on a broadly-down sector
// does not warrant SWITCH — there may be no better stock in that space.
func setStockContext(m *StockMetrics) {
	sectorBench, ok := sectorReturn1Y[m.Sector]
	if !ok {
		sectorBench = 12.0 // broad market default (approximate Nifty long-run CAGR)
	}

	m.SectorBearish = sectorBench < 0

	if m.Return1YPct == 0 && !ok {
		m.RelativeRank = 50
		return
	}

	alpha := m.Return1YPct - sectorBench
	switch {
	case alpha >= 15:
		m.RelativeRank = 90
	case alpha >= 8:
		m.RelativeRank = 78
	case alpha >= 2:
		m.RelativeRank = 65
	case alpha >= -5:
		m.RelativeRank = 50
	case alpha >= -12:
		m.RelativeRank = 35
	default:
		m.RelativeRank = 18
	}
}

func sectorMedianPE(sector string) float64 {
	if pe, ok := sectorPE[sector]; ok {
		return pe
	}
	return 22 // broad market default
}

// fetchYahooCrumb obtains the crumb token required for Yahoo Finance v10 API calls.
func fetchYahooCrumb(client *http.Client) (string, []*http.Cookie, error) {
	// Step 1: Visit fc.yahoo.com to receive the session cookie.
	// (finance.yahoo.com no longer sets the consent cookies needed for the crumb endpoint.)
	req1, _ := http.NewRequest(http.MethodGet, "https://fc.yahoo.com", nil)
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
