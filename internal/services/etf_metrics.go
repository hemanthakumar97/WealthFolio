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

// ETFMetrics mirrors MFMetrics so the frontend can use the same panel layout.
// NAV/price history comes from our prices table; AUM/TER/Beta from Yahoo quoteSummary.
type ETFMetrics struct {
	Symbol           string   `json:"symbol"` // Yahoo Finance ticker (e.g. TQQQ, NIFTYBEES.NS)
	Category         string   `json:"category"`
	Rolling1YAvg     float64  `json:"rolling_1y_avg_pct"`
	Rolling3YAvg     float64  `json:"rolling_3y_avg_pct"`
	Rolling3YMin     float64  `json:"rolling_3y_min_pct"`
	Consistency1Y    float64  `json:"consistency_1y_pct"`
	StdDev1Y         float64  `json:"std_dev_1y_pct"`
	StdDev5YMedian   float64  `json:"std_dev_5y_median"`
	Sharpe1Y         float64  `json:"sharpe_1y"`
	MaxDrawdown3Y    float64  `json:"max_drawdown_3y_pct"`
	Beta             float64  `json:"beta"`
	AUMCr            float64  `json:"aum_cr"`
	TER              float64  `json:"ter_pct"`
	TrackingError    float64  `json:"tracking_error_pct"`
	CatTERPct        float64  `json:"cat_ter_pct"`
	CatTrackingError float64  `json:"cat_tracking_error_pct"`
	// Valuation (from Tickertape — populated for commodity/sectoral ETFs)
	TTMPE    float64 `json:"ttm_pe"`
	PBRatio  float64 `json:"pb_ratio"`
	DivYield float64 `json:"div_yield"`
	IndPE    float64 `json:"ind_pe"`
	IndPB    float64 `json:"ind_pb"`
	IndDY    float64 `json:"ind_dy"`
	TickertapeSlug   string   `json:"tickertape_slug"`
	Zero1Score       int      `json:"zero1_score"`
	AvailableMax     int      `json:"available_max"`
	DataGaps         []string `json:"data_gaps,omitempty"`

	// Pillar scores — same structure as MFPillarScores for UI reuse.
	// ETF uses: A=Returns, B=Risk-Adjusted, C=Consistency, D=Fund Ops (TER+Beta+AUM+TrackingError), E=0
	Pillars MFPillarScores `json:"pillars"`

	// RelativeRank is TER-efficiency based (NOT return-based) — a low-TER ETF ranks
	// high because ETF returns simply equal the index; manager skill is irrelevant.
	RelativeRank    int  `json:"relative_rank"`
	CategoryBearish bool `json:"category_bearish"`

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
				Beta3Year                yahooFloat `json:"beta3Year"`
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
	} `json:"quoteSummary"`
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
	"NIFTYBEES.NS":  {beta: 1.00, aumCr: 27000, ter: 0.04},
	"JUNIORBEES.NS": {beta: 0.92, aumCr: 8000, ter: 0.19},
	"GOLDBEES.NS":   {beta: 0.05, aumCr: 8500, ter: 0.82}, // gold: near-zero equity beta
	"SILVERBEES.NS": {beta: 0.08, aumCr: 1500, ter: 0.40},
	"BANKBEES.NS":   {beta: 1.10, aumCr: 7500, ter: 0.19},
	"ITBEES.NS":     {beta: 0.85, aumCr: 2000, ter: 0.19},
	// SBI / UTI
	"SETFNN50.NS":   {beta: 0.93, aumCr: 4000, ter: 0.12},
	"UTINIFTETF.NS": {beta: 1.00, aumCr: 12000, ter: 0.07},
	// HDFC
	"HDFCNIFTY.NS": {beta: 1.00, aumCr: 5000, ter: 0.10},
	// CPSE / PSU
	"CPSEETF.NS": {beta: 0.85, aumCr: 2500, ter: 0.01},
	// Mirae
	"MAFANG.NS": {beta: 0.60, aumCr: 2000, ter: 0.69}, // international
	// US ETFs (AUM in ₹ equivalent; TER in %)
	"TQQQ": {beta: 3.00, aumCr: 150000, ter: 0.86}, // 3× leveraged, high beta
	"QQQ":  {beta: 1.20, aumCr: 600000, ter: 0.20},
	"SPY":  {beta: 1.00, aumCr: 2000000, ter: 0.09},
	"VTI":  {beta: 1.00, aumCr: 1500000, ter: 0.03},
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
	m.Symbol = yahooSymbol
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

	// 1. Tickertape — live TER, AUM, tracking error (best source for Indian ETFs).
	// trackErr and indTrackErr are already in % from the API (e.g. 52.90, not 0.529).
	if yahooSymbol != "" {
		if td, err := fetchTickertapeData(yahooSymbol, timeout/2); err == nil {
			if td.ter > 0      { m.TER = roundF(td.ter, 3) }
			if td.aumCr > 0   { m.AUMCr = roundF(td.aumCr, 0) }
			if td.trackErr > 0 { m.TrackingError = roundF(td.trackErr, 4) }
			if td.catTER > 0  { m.CatTERPct = roundF(td.catTER, 4) }
			if td.catTE > 0   { m.CatTrackingError = roundF(td.catTE, 4) }
			if td.pe > 0      { m.TTMPE = roundF(td.pe, 2) }
			if td.pb > 0      { m.PBRatio = roundF(td.pb, 2) }
			if td.divYield > 0 { m.DivYield = roundF(td.divYield, 2) }
			if td.indPE > 0   { m.IndPE = roundF(td.indPE, 2) }
			if td.indPB > 0   { m.IndPB = roundF(td.indPB, 2) }
			if td.indDY > 0   { m.IndDY = roundF(td.indDY, 2) }
			if td.slug != ""  { m.TickertapeSlug = td.slug }
		}
	}

	// 2. knownETFData — fills Beta; also fills TER/AUM if Tickertape missed them.
	if kd, ok := knownETFData[yahooSymbol]; ok {
		m.Beta = kd.beta
		if m.AUMCr == 0 {
			m.AUMCr = kd.aumCr
		}
		if m.TER == 0 {
			m.TER = kd.ter
		}
	} else if yahooSymbol != "" && m.TER == 0 {
		// 3. Yahoo quoteSummary last resort for TER/AUM/Beta.
		if yd, fetchErr := fetchETFYahooData(yahooSymbol, timeout/3); fetchErr == nil {
			if m.Beta == 0 {
				m.Beta = yd.beta
			}
			if m.AUMCr == 0 {
				m.AUMCr = yd.aumCr
			}
			m.TER = yd.ter
		}
	}

	m.Zero1Score = computeETFScore(m)
	setETFContext(m)

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

// ─── Tickertape ETF data ──────────────────────────────────────────────────────

type tickertapeData struct {
	ter      float64 // expense ratio in % (e.g. 0.04)
	aumCr    float64 // AUM in ₹ crores
	trackErr float64 // tracking error already in % (e.g. 52.90 for SILVERBEES)
	catTER   float64 // category average expense ratio in %
	catTE    float64 // category average tracking error in %
	pe       float64 // TTM PE ratio
	pb       float64 // Price-to-Book ratio
	divYield float64 // dividend yield in %
	indPE    float64 // sector/index PE
	indPB    float64 // sector/index PB
	indDY    float64 // sector/index dividend yield
	slug     string  // Tickertape URL slug e.g. "/etfs/nippon-india-nifty-50-bees-etf-NBES"
}

// fetchTickertapeData fetches live TER, AUM, and tracking error from Tickertape.
// Step 1: resolve NSE ticker → internal sid via search API.
// Step 2: fetch ETF ratios via etfs/info/{sid}.
func fetchTickertapeData(yahooSymbol string, timeout time.Duration) (tickertapeData, error) {
	// Strip exchange suffix (.NS / .BO) to get the NSE ticker.
	ticker := yahooSymbol
	for _, suf := range []string{".NS", ".BO"} {
		ticker = strings.TrimSuffix(ticker, suf)
	}

	client := &http.Client{Timeout: timeout}
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

	// ── Step 1: ticker → sid ──────────────────────────────────────────────────
	searchURL := "https://api.tickertape.in/search?text=" + url.QueryEscape(ticker)
	req1, _ := http.NewRequest(http.MethodGet, searchURL, nil)
	req1.Header.Set("User-Agent", ua)
	resp1, err := client.Do(req1)
	if err != nil {
		return tickertapeData{}, fmt.Errorf("tickertape search: %w", err)
	}
	defer resp1.Body.Close()

	var searchResp struct {
		Data struct {
			Stocks []struct {
				SID    string `json:"sid"`
				Ticker string `json:"ticker"`
				Slug   string `json:"slug"`
			} `json:"stocks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp1.Body).Decode(&searchResp); err != nil {
		return tickertapeData{}, fmt.Errorf("tickertape search decode: %w", err)
	}
	if len(searchResp.Data.Stocks) == 0 {
		return tickertapeData{}, fmt.Errorf("tickertape: no results for %s", ticker)
	}
	sid := searchResp.Data.Stocks[0].SID
	ttSlug := searchResp.Data.Stocks[0].Slug // e.g. "/etfs/nippon-india-nifty-50-bees-etf-NBES"

	// ── Step 2: sid → ratios ──────────────────────────────────────────────────
	infoURL := "https://api.tickertape.in/etfs/info/" + url.PathEscape(sid)
	req2, _ := http.NewRequest(http.MethodGet, infoURL, nil)
	req2.Header.Set("User-Agent", ua)
	resp2, err := client.Do(req2)
	if err != nil {
		return tickertapeData{}, fmt.Errorf("tickertape info: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return tickertapeData{}, fmt.Errorf("tickertape info: HTTP %d for sid %s", resp2.StatusCode, sid)
	}

	var infoResp struct {
		Data struct {
			Ratios struct {
				ExpenseRatio    float64  `json:"expenseRatio"`
				IndExpenseRatio float64  `json:"indExpenseRatio"` // category avg TER
				TrackErr        float64  `json:"trackErr"`        // already in %
				IndTrackErr     float64  `json:"indTrackErr"`     // category avg TE, already in %
				AssetUnderMgmt  float64  `json:"asstUnderMan"`
				TTMPE           *float64 `json:"ttmPe"`
				PE              *float64 `json:"pe"`
				PB              *float64 `json:"pb"`
				DivYield        float64  `json:"divYield"`
				IndPE           *float64 `json:"indpe"`
				IndPB           *float64 `json:"indpb"`
				IndDY           float64  `json:"inddy"`
			} `json:"ratios"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&infoResp); err != nil {
		return tickertapeData{}, fmt.Errorf("tickertape info decode: %w", err)
	}

	r := infoResp.Data.Ratios
	pe := 0.0
	if r.TTMPE != nil {
		pe = *r.TTMPE
	} else if r.PE != nil {
		pe = *r.PE
	}
	pb := 0.0
	if r.PB != nil {
		pb = *r.PB
	}
	indPE := 0.0
	if r.IndPE != nil {
		indPE = *r.IndPE
	}
	indPB := 0.0
	if r.IndPB != nil {
		indPB = *r.IndPB
	}
	return tickertapeData{
		ter: r.ExpenseRatio, aumCr: r.AssetUnderMgmt,
		trackErr: r.TrackErr, catTER: r.IndExpenseRatio, catTE: r.IndTrackErr,
		pe: pe, pb: pb, divYield: r.DivYield,
		indPE: indPE, indPB: indPB, indDY: r.IndDY,
		slug: ttSlug,
	}, nil
}

// computeETFScore scores an ETF using the same 5-pillar framework as MFs.
// ETF-specific differences: no Alpha/CatRank/GrowwRating (index trackers have no manager alpha);
// TER carries much higher weight (it's the primary differentiator between ETFs tracking same index).
//
// Pillar A: Returns Quality  (max 30)  — Rolling3Y + Consistency + Worst3Y
// Pillar B: Risk-Adjusted    (max 30)  — Sharpe + StdDevRatio + MaxDrawdown
// Pillar C: Consistency      (max 10)  — Tracking Error (low = good index replication)
// Pillar D: Fund Ops         (max 25)  — TER(12) + Beta(8) + AUM(5)
// Pillar E: Validation       (max  0)  — ETFs have no editorial rating
func computeETFScore(m *ETFMetrics) int {
	var p MFPillarScores
	var gaps []string

	// ── Pillar A: Returns Quality ─────────────────────────────────────────────
	p.ReturnQualityMax = 30

	// Rolling 3Y Avg — 15 pts
	switch {
	case m.Rolling3YAvg >= 18:
		p.ReturnQuality += 15
	case m.Rolling3YAvg >= 13:
		p.ReturnQuality += 11
	case m.Rolling3YAvg >= 8:
		p.ReturnQuality += 7
	case m.Rolling3YAvg >= 4:
		p.ReturnQuality += 4
	default:
		p.ReturnQuality += 1
	}
	// Consistency — 10 pts
	switch {
	case m.Consistency1Y >= 90:
		p.ReturnQuality += 10
	case m.Consistency1Y >= 75:
		p.ReturnQuality += 7
	case m.Consistency1Y >= 60:
		p.ReturnQuality += 4
	default:
		p.ReturnQuality += 1
	}
	// Worst 3Y rolling — 5 pts
	switch {
	case m.Rolling3YMin > 3:
		p.ReturnQuality += 5
	case m.Rolling3YMin > 0:
		p.ReturnQuality += 4
	case m.Rolling3YMin > -5:
		p.ReturnQuality += 2
	default:
		p.ReturnQuality += 0
	}

	// ── Pillar B: Risk-Adjusted ───────────────────────────────────────────────
	p.RiskAdjustedMax = 30

	// Sharpe — 15 pts
	switch {
	case m.Sharpe1Y >= 1.0:
		p.RiskAdjusted += 15
	case m.Sharpe1Y >= 0.5:
		p.RiskAdjusted += 12
	case m.Sharpe1Y >= 0.1:
		p.RiskAdjusted += 8
	case m.Sharpe1Y >= -0.2:
		p.RiskAdjusted += 5
	case m.Sharpe1Y >= -0.5:
		p.RiskAdjusted += 2
	default:
		p.RiskAdjusted += 0
	}
	// Std Dev vs own 5Y median — 10 pts
	p.RiskAdjustedMax -= 5 // 15→10 since no Sortino
	if m.StdDev1Y > 0 {
		median := m.StdDev5YMedian
		if median <= 0 {
			median = etfBenchStdDev(m.Category)
		}
		ratio := m.StdDev1Y / median
		switch {
		case ratio <= 0.80:
			p.RiskAdjusted += 10
		case ratio <= 0.95:
			p.RiskAdjusted += 8
		case ratio <= 1.10:
			p.RiskAdjusted += 6
		case ratio <= 1.25:
			p.RiskAdjusted += 3
		default:
			p.RiskAdjusted += 1
		}
	} else {
		p.RiskAdjusted += 5
	}
	// Max Drawdown — 5 pts
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

	// ── Pillar C: Tracking Quality ────────────────────────────────────────────
	// Tracking error measures index replication quality — lower is better.
	if m.TrackingError > 0 {
		p.ConsistencyMax += 10
		switch {
		case m.TrackingError < 0.1:
			p.Consistency += 10
		case m.TrackingError < 0.3:
			p.Consistency += 8
		case m.TrackingError < 0.5:
			p.Consistency += 6
		case m.TrackingError < 1.0:
			p.Consistency += 4
		default:
			p.Consistency += 2
		}
	} else {
		gaps = append(gaps, "TrackingError")
	}

	// ── Pillar D: Fund Operations ─────────────────────────────────────────────
	// TER is the single most important metric for ETFs — it's the guaranteed return drag.
	if m.TER > 0 {
		p.FundOpsMax += 12
		switch {
		case m.TER < 0.1:
			p.FundOps += 12
		case m.TER < 0.2:
			p.FundOps += 10
		case m.TER < 0.4:
			p.FundOps += 7
		case m.TER < 0.7:
			p.FundOps += 4
		default:
			p.FundOps += 1
		}
	} else {
		gaps = append(gaps, "TER")
	}
	if m.Beta > 0 {
		p.FundOpsMax += 8
		switch {
		case m.Beta <= 0.80:
			p.FundOps += 8
		case m.Beta <= 0.90:
			p.FundOps += 6
		case m.Beta <= 1.00:
			p.FundOps += 5
		case m.Beta <= 1.10:
			p.FundOps += 3
		default:
			p.FundOps += 1
		}
	} else {
		gaps = append(gaps, "Beta")
	}
	if m.AUMCr > 0 {
		p.FundOpsMax += 5
		switch {
		case m.AUMCr < 100:
			p.FundOps += 1
		case m.AUMCr < 500:
			p.FundOps += 3
		case m.AUMCr < 5000:
			p.FundOps += 4
		default:
			p.FundOps += 5
		}
	} else {
		gaps = append(gaps, "AUM")
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

// setETFContext populates RelativeRank and CategoryBearish for an ETF.
//
// Unlike MFs, ETF returns equal the index return — there is no manager alpha to
// compare. SWITCH between ETFs only makes sense when a lower-TER alternative
// tracks the same index. So RelativeRank is purely TER-efficiency based.
// CategoryBearish flags indices in a prolonged bear so scoreToAction can avoid
// triggering BOOK_PROFIT when the holding is simply underwater with the market.
func setETFContext(m *ETFMetrics) {
	// TER-based efficiency rank.
	if m.TER > 0 {
		switch {
		case m.TER < 0.10:
			m.RelativeRank = 90
		case m.TER < 0.20:
			m.RelativeRank = 80
		case m.TER < 0.40:
			m.RelativeRank = 65
		case m.TER < 0.70:
			m.RelativeRank = 45
		default:
			m.RelativeRank = 25
		}
	} else {
		m.RelativeRank = 50 // no TER data — neutral
	}

	// Underlying index is in a prolonged bear when 3Y average is below ~8%.
	m.CategoryBearish = m.Rolling3YAvg > 0 && m.Rolling3YAvg < 8.0
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
