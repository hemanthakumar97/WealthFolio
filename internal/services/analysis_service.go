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

// FullAnalysis is the complete result of a fundamental + technical analysis run.
type FullAnalysis struct {
	Symbol    string `json:"symbol"`
	CompanyName string `json:"company_name"`
	Sector    string `json:"sector"`

	// Scores
	CompositeScore   int    `json:"composite_score"`   // 0–100
	FundamentalScore int    `json:"fundamental_score"` // 0–100
	TechnicalScore   int    `json:"technical_score"`   // 0–100
	Recommendation   string `json:"recommendation"`    // STRONG_BUY … SELL

	// Technical
	CurrentPrice float64 `json:"current_price"`
	RSI14        float64 `json:"rsi_14"`
	MACDHisto    float64 `json:"macd_histogram"`
	SMA50        float64 `json:"sma_50"`
	SMA200       float64 `json:"sma_200"`
	AboveSMA50   bool    `json:"above_sma_50"`
	AboveSMA200  bool    `json:"above_sma_200"`
	GoldenCross  bool    `json:"golden_cross"`

	// Fundamental
	TrailingPE       float64 `json:"trailing_pe"`
	PriceToBook      float64 `json:"price_to_book"`
	ROE              float64 `json:"roe"`
	ROCE             float64 `json:"roce"`             // Return on Capital Employed
	RevenueGrowth    float64 `json:"revenue_growth"`
	EarningsGrowth   float64 `json:"earnings_growth"`
	DebtToEquity     float64 `json:"debt_to_equity"`
	DividendYield    float64 `json:"dividend_yield"`
	MarketCapCr      float64 `json:"market_cap_cr"`
	Week52High       float64 `json:"week_52_high"`
	Week52Low        float64 `json:"week_52_low"`
	PromoterHolding  float64 `json:"promoter_holding"` // % — approx from Yahoo insiders
	FreeCashFlow     float64 `json:"free_cash_flow"`   // in crores

	// Signals
	BuySignals     []string `json:"buy_signals"`
	CautionSignals []string `json:"caution_signals"`

	// Chart
	PriceHistory []ChartPoint `json:"price_history,omitempty"`

	AnalyzedAt time.Time `json:"analyzed_at"`
}

// AnalysisService runs and persists stock analysis.
type AnalysisService struct {
	pool *pgxpool.Pool
}

func NewAnalysisService(pool *pgxpool.Pool) *AnalysisService {
	return &AnalysisService{pool: pool}
}

// Analyze fetches prices + fundamentals for a Yahoo symbol, computes scores,
// persists to stock_analysis, and returns the result.
func (s *AnalysisService) Analyze(ctx context.Context, symbol string) (*FullAnalysis, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("symbol required")
	}

	client := &http.Client{Timeout: 20 * time.Second}

	// 1. Fetch price history from Yahoo chart API.
	navs, err := fetchYahooPrices(ctx, client, symbol)
	if err != nil {
		return nil, fmt.Errorf("fetch prices for %s: %w", symbol, err)
	}

	// 2. Compute technical indicators.
	tech := ComputeTechnicals(navs)

	// 3. Fetch fundamentals from Yahoo quoteSummary.
	fund, err := fetchYahooFundamentals(ctx, client, symbol)
	if err != nil {
		// Non-fatal: proceed with zero fundamental data.
		fund = &StockMetrics{Symbol: symbol}
	}
	if len(navs) > 10 {
		oneYearAgo := time.Now().AddDate(-1, 0, 0)
		cur := navs[len(navs)-1].NAV
		nav1y := valueOnOrBefore(navs, oneYearAgo)
		if nav1y > 0 {
			fund.Return1YPct = roundF((cur/nav1y-1)*100, 2)
		}
	}
	fundScore := computeStockScore(fund)

	// 4. Composite score: 60% fundamental + 40% technical.
	composite := roundF(float64(fundScore)*0.6+float64(tech.Score)*0.4, 0)
	compositeInt := int(composite)

	// 5. Derive recommendation label.
	rec := scoreToRecommendation(compositeInt)

	// 6. Build signal narratives.
	buys, cautions := buildSignals(fund, tech)

	// 7. Sample price history for chart (260 points max).
	var history []ChartPoint
	if len(navs) > 0 {
		since := time.Now().AddDate(-2, 0, 0)
		history = sampleSeries(navs, since, 260)
	}

	fa := &FullAnalysis{
		Symbol:         symbol,
		CompanyName:    fund.CompanyName,
		Sector:         fund.Sector,
		CompositeScore: compositeInt,
		FundamentalScore: fundScore,
		TechnicalScore: tech.Score,
		Recommendation: rec,

		CurrentPrice: tech.CurrentPrice,
		RSI14:        tech.RSI14,
		MACDHisto:    tech.MACDHisto,
		SMA50:        roundF(tech.SMA50, 2),
		SMA200:       roundF(tech.SMA200, 2),
		AboveSMA50:   tech.AboveSMA50,
		AboveSMA200:  tech.AboveSMA200,
		GoldenCross:  tech.GoldenCross,

		TrailingPE:      fund.TrailingPE,
		PriceToBook:     fund.PriceToBook,
		ROE:             fund.ROE,
		ROCE:            fund.ROCE,
		RevenueGrowth:   fund.RevenueGrowth,
		EarningsGrowth:  fund.EarningsGrowth,
		DebtToEquity:    fund.DebtToEquity,
		DividendYield:   fund.DividendYield,
		MarketCapCr:     fund.MarketCapCr,
		Week52High:      fund.Week52High,
		Week52Low:       fund.Week52Low,
		PromoterHolding: fund.PromoterHolding,
		FreeCashFlow:    fund.FreeCashFlowCr,

		BuySignals:     buys,
		CautionSignals: cautions,
		PriceHistory:   history,
		AnalyzedAt:     time.Now(),
	}

	if err := s.persist(ctx, fa, fund); err != nil {
		// Log but don't fail — return data to caller.
		_ = err
	}

	return fa, nil
}

// GetLatest returns the most recent analysis from DB without re-running.
func (s *AnalysisService) GetLatest(ctx context.Context, symbol string) (*FullAnalysis, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	row := s.pool.QueryRow(ctx, `
		SELECT company_name, sector, composite_score, fundamental_score, technical_score,
		       recommendation, current_price, rsi_14, macd_histogram, sma_50, sma_200,
		       above_sma_50, above_sma_200, golden_cross,
		       trailing_pe, price_to_book, roe, revenue_growth, earnings_growth,
		       debt_to_equity, dividend_yield, market_cap_cr, week_52_high, week_52_low,
		       buy_signals, caution_signals, analyzed_at
		FROM stock_analysis
		WHERE symbol = $1
		ORDER BY analysis_date DESC
		LIMIT 1
	`, symbol)

	var fa FullAnalysis
	fa.Symbol = symbol
	var buyStr, cauStr string
	err := row.Scan(
		&fa.CompanyName, &fa.Sector, &fa.CompositeScore, &fa.FundamentalScore, &fa.TechnicalScore,
		&fa.Recommendation, &fa.CurrentPrice, &fa.RSI14, &fa.MACDHisto, &fa.SMA50, &fa.SMA200,
		&fa.AboveSMA50, &fa.AboveSMA200, &fa.GoldenCross,
		&fa.TrailingPE, &fa.PriceToBook, &fa.ROE, &fa.RevenueGrowth, &fa.EarningsGrowth,
		&fa.DebtToEquity, &fa.DividendYield, &fa.MarketCapCr, &fa.Week52High, &fa.Week52Low,
		&buyStr, &cauStr, &fa.AnalyzedAt,
	)
	if err != nil {
		return nil, err
	}
	if buyStr != "" {
		fa.BuySignals = strings.Split(buyStr, "|")
	}
	if cauStr != "" {
		fa.CautionSignals = strings.Split(cauStr, "|")
	}
	return &fa, nil
}

// GetWatchlistAnalyses returns latest saved analyses for all watchlist symbols.
// Symbols without any analysis return nil entries (caller filters).
func (s *AnalysisService) GetWatchlistAnalyses(ctx context.Context) ([]*FullAnalysis, error) {
	rows, err := s.pool.Query(ctx, `SELECT symbol FROM watchlist ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	var symbols []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err == nil {
			symbols = append(symbols, sym)
		}
	}
	rows.Close()

	var result []*FullAnalysis
	for _, sym := range symbols {
		fa, err := s.GetLatest(ctx, sym)
		if err != nil {
			fa = &FullAnalysis{Symbol: sym, Recommendation: "NOT_ANALYZED"}
		}
		result = append(result, fa)
	}
	return result, nil
}

// ─── Persistence ─────────────────────────────────────────────────────────────

func (s *AnalysisService) persist(ctx context.Context, fa *FullAnalysis, fund *StockMetrics) error {
	metricsJSON, _ := json.Marshal(fund)
	buyStr := strings.Join(fa.BuySignals, "|")
	cauStr := strings.Join(fa.CautionSignals, "|")
	today := time.Now().In(ist()).Truncate(24 * time.Hour)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO stock_analysis (
			symbol, analysis_date, company_name, sector,
			composite_score, fundamental_score, technical_score, recommendation,
			rsi_14, macd_histogram, sma_50, sma_200, current_price,
			above_sma_50, above_sma_200, golden_cross,
			trailing_pe, price_to_book, roe, revenue_growth, earnings_growth,
			debt_to_equity, dividend_yield, market_cap_cr, week_52_high, week_52_low,
			buy_signals, caution_signals, metrics_json, analyzed_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,
			$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,NOW()
		)
		ON CONFLICT (symbol, analysis_date) DO UPDATE SET
			company_name=EXCLUDED.company_name, sector=EXCLUDED.sector,
			composite_score=EXCLUDED.composite_score, fundamental_score=EXCLUDED.fundamental_score,
			technical_score=EXCLUDED.technical_score, recommendation=EXCLUDED.recommendation,
			rsi_14=EXCLUDED.rsi_14, macd_histogram=EXCLUDED.macd_histogram,
			sma_50=EXCLUDED.sma_50, sma_200=EXCLUDED.sma_200, current_price=EXCLUDED.current_price,
			above_sma_50=EXCLUDED.above_sma_50, above_sma_200=EXCLUDED.above_sma_200,
			golden_cross=EXCLUDED.golden_cross,
			trailing_pe=EXCLUDED.trailing_pe, price_to_book=EXCLUDED.price_to_book,
			roe=EXCLUDED.roe, revenue_growth=EXCLUDED.revenue_growth,
			earnings_growth=EXCLUDED.earnings_growth, debt_to_equity=EXCLUDED.debt_to_equity,
			dividend_yield=EXCLUDED.dividend_yield, market_cap_cr=EXCLUDED.market_cap_cr,
			week_52_high=EXCLUDED.week_52_high, week_52_low=EXCLUDED.week_52_low,
			buy_signals=EXCLUDED.buy_signals, caution_signals=EXCLUDED.caution_signals,
			metrics_json=EXCLUDED.metrics_json, analyzed_at=NOW()
	`,
		fa.Symbol, today, fa.CompanyName, fa.Sector,
		fa.CompositeScore, fa.FundamentalScore, fa.TechnicalScore, fa.Recommendation,
		fa.RSI14, fa.MACDHisto, fa.SMA50, fa.SMA200, fa.CurrentPrice,
		fa.AboveSMA50, fa.AboveSMA200, fa.GoldenCross,
		fa.TrailingPE, fa.PriceToBook, fa.ROE, fa.RevenueGrowth, fa.EarningsGrowth,
		fa.DebtToEquity, fa.DividendYield, fa.MarketCapCr, fa.Week52High, fa.Week52Low,
		buyStr, cauStr, metricsJSON,
	)
	return err
}

// ─── Yahoo Finance helpers ────────────────────────────────────────────────────

type yahooPriceChartResp struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct{ Code string } `json:"error"`
	} `json:"chart"`
}

// fetchYahooPrices fetches 2-year daily close prices from Yahoo Finance chart API.
func fetchYahooPrices(ctx context.Context, client *http.Client, symbol string) ([]navPoint, error) {
	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?range=2y&interval=1d",
		url.PathEscape(symbol),
	)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("yahoo chart API: HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var cr yahooPriceChartResp
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, err
	}
	if cr.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo chart: %s", cr.Chart.Error.Code)
	}
	if len(cr.Chart.Result) == 0 || len(cr.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, fmt.Errorf("no chart data for %s", symbol)
	}

	res := cr.Chart.Result[0]
	closes := res.Indicators.Quote[0].Close
	var navs []navPoint
	for i, ts := range res.Timestamp {
		if i >= len(closes) || closes[i] == 0 {
			continue
		}
		if math.IsNaN(closes[i]) {
			continue
		}
		navs = append(navs, navPoint{
			Date: time.Unix(ts, 0).UTC(),
			NAV:  closes[i],
		})
	}
	sortNavPoints(navs)
	return navs, nil
}

// yahooExtendedResp extends the basic quoteSummary response with extra modules.
type yahooExtendedResp struct {
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
				PriceToBook           yahooFloat `json:"priceToBook"`
				PEGRatio              yahooFloat `json:"pegRatio"`
				ProfitMargins         yahooFloat `json:"profitMargins"`
				HeldPercentInsiders   yahooFloat `json:"heldPercentInsiders"`
			} `json:"defaultKeyStatistics"`
			FinancialData struct {
				ReturnOnEquity   yahooFloat `json:"returnOnEquity"`
				DebtToEquity     yahooFloat `json:"debtToEquity"`
				CurrentRatio     yahooFloat `json:"currentRatio"`
				RevenueGrowth    yahooFloat `json:"revenueGrowth"`
				EarningsGrowth   yahooFloat `json:"earningsGrowth"`
				OperatingMargins yahooFloat `json:"operatingMargins"`
				FreeCashflow     yahooFloat `json:"freeCashflow"`
				TotalDebt        yahooFloat `json:"totalDebt"`
				Ebitda           yahooFloat `json:"ebitda"`
			} `json:"financialData"`
			Price struct {
				LongName           string     `json:"longName"`
				ShortName          string     `json:"shortName"`
				RegularMarketPrice yahooFloat `json:"regularMarketPrice"`
			} `json:"price"`
			AssetProfile struct {
				Sector   string `json:"sector"`
				Industry string `json:"industry"`
			} `json:"assetProfile"`
			MajorHoldersBreakdown struct {
				InsidersPercentHeld yahooFloat `json:"insidersPercentHeld"`
			} `json:"majorHoldersBreakdown"`
			BalanceSheetHistory struct {
				BalanceSheetStatements []struct {
					TotalAssets               yahooFloat `json:"totalAssets"`
					TotalCurrentLiabilities   yahooFloat `json:"totalCurrentLiabilities"`
				} `json:"balanceSheetStatements"`
			} `json:"balanceSheetHistory"`
		} `json:"result"`
		Error *struct{ Code string } `json:"error"`
	} `json:"finance"`
}

// fetchYahooFundamentals re-uses the existing Yahoo quoteSummary fetcher.
// It constructs a minimal StockMetrics from the response (no DB price history needed).
func fetchYahooFundamentals(ctx context.Context, client *http.Client, symbol string) (*StockMetrics, error) {
	crumb, cookies, err := fetchYahooCrumb(client)
	if err != nil {
		return nil, err
	}

	modules := "summaryDetail,defaultKeyStatistics,financialData,price,assetProfile,majorHoldersBreakdown,balanceSheetHistory"
	qsURL := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=%s&crumb=%s",
		url.PathEscape(symbol),
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
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("quoteSummary: HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var qs yahooExtendedResp
	if err := json.Unmarshal(body, &qs); err != nil {
		return nil, err
	}
	if qs.Finance.Error != nil || len(qs.Finance.Result) == 0 {
		return nil, fmt.Errorf("no quoteSummary result for %s", symbol)
	}
	r := qs.Finance.Result[0]

	sm := &StockMetrics{
		Symbol:          symbol,
		CompanyName:     r.Price.LongName,
		Sector:          r.AssetProfile.Sector,
		Industry:        r.AssetProfile.Industry,
		TrailingPE:      r.SummaryDetail.TrailingPE.Raw,
		ForwardPE:       r.SummaryDetail.ForwardPE.Raw,
		Beta:            r.SummaryDetail.Beta.Raw,
		Week52High:      r.SummaryDetail.FiftyTwoWeekHigh.Raw,
		Week52Low:       r.SummaryDetail.FiftyTwoWeekLow.Raw,
		DividendYield:   r.SummaryDetail.DividendYield.Raw * 100,
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
	if sm.CompanyName == "" {
		sm.CompanyName = r.Price.ShortName
	}
	cap := r.SummaryDetail.MarketCap.Raw
	sm.MarketCapCr = roundF(cap/1e7, 0)

	// Extended fields used only by FullAnalysis (not in Zero1Score).
	sm.PromoterHolding = roundF(r.DefaultKeyStatistics.HeldPercentInsiders.Raw*100, 1)
	sm.FreeCashFlowCr = roundF(r.FinancialData.FreeCashflow.Raw/1e7, 1)
	if len(r.BalanceSheetHistory.BalanceSheetStatements) > 0 {
		bs := r.BalanceSheetHistory.BalanceSheetStatements[0]
		ce := bs.TotalAssets.Raw - bs.TotalCurrentLiabilities.Raw
		if ce > 0 && r.FinancialData.Ebitda.Raw > 0 {
			sm.ROCE = roundF(r.FinancialData.Ebitda.Raw/ce*100, 1)
		}
	}

	return sm, nil
}

// ─── Signal narrative builder ─────────────────────────────────────────────────

func buildSignals(fund *StockMetrics, tech TechnicalResult) (buys, cautions []string) {
	// Fundamental buy signals
	if fund.TrailingPE > 0 {
		med := sectorMedianPE(fund.Sector)
		if fund.TrailingPE < med*0.8 {
			buys = append(buys, fmt.Sprintf("Trades at P/E %.1f — below sector median (%.0f)", fund.TrailingPE, med))
		} else if fund.TrailingPE > med*1.5 {
			cautions = append(cautions, fmt.Sprintf("P/E %.1f is %.0f%% above sector median", fund.TrailingPE, (fund.TrailingPE/med-1)*100))
		}
	}
	if fund.PriceToBook > 0 && fund.PriceToBook < 1.5 {
		buys = append(buys, fmt.Sprintf("P/B %.2f — trading near book value", fund.PriceToBook))
	}
	if fund.ROE > 15 {
		buys = append(buys, fmt.Sprintf("ROE %.1f%% — strong return on equity", fund.ROE))
	} else if fund.ROE > 0 && fund.ROE < 8 {
		cautions = append(cautions, fmt.Sprintf("ROE %.1f%% is below 8%% threshold", fund.ROE))
	}
	if fund.RevenueGrowth > 15 {
		buys = append(buys, fmt.Sprintf("Revenue growing %.1f%% YoY", fund.RevenueGrowth))
	}
	if fund.EarningsGrowth > 15 {
		buys = append(buys, fmt.Sprintf("Earnings growing %.1f%% YoY — strong momentum", fund.EarningsGrowth))
	} else if fund.EarningsGrowth < -5 {
		cautions = append(cautions, fmt.Sprintf("Earnings declined %.1f%% YoY", -fund.EarningsGrowth))
	}
	if fund.DebtToEquity > 0 {
		if fund.DebtToEquity < 30 {
			buys = append(buys, fmt.Sprintf("Low debt-to-equity ratio (%.1f)", fund.DebtToEquity))
		} else if fund.DebtToEquity > 150 {
			cautions = append(cautions, fmt.Sprintf("High debt-to-equity ratio (%.1f)", fund.DebtToEquity))
		}
	}
	if fund.DividendYield > 2 {
		buys = append(buys, fmt.Sprintf("Dividend yield %.2f%%", fund.DividendYield))
	}
	if fund.ROCE > 20 {
		buys = append(buys, fmt.Sprintf("ROCE %.1f%% — strong capital efficiency", fund.ROCE))
	}
	if fund.PromoterHolding > 50 {
		buys = append(buys, fmt.Sprintf("Promoter holding %.1f%% — high skin in the game", fund.PromoterHolding))
	} else if fund.PromoterHolding > 0 && fund.PromoterHolding < 25 {
		cautions = append(cautions, fmt.Sprintf("Low promoter holding (%.1f%%) — reduced alignment", fund.PromoterHolding))
	}

	// Technical buy signals
	if tech.RSI14 > 0 && tech.RSI14 < 35 {
		buys = append(buys, fmt.Sprintf("RSI %.1f — oversold, potential reversal zone", tech.RSI14))
	} else if tech.RSI14 > 70 {
		cautions = append(cautions, fmt.Sprintf("RSI %.1f — overbought territory", tech.RSI14))
	}
	if tech.GoldenCross {
		buys = append(buys, "Golden cross: 50-DMA above 200-DMA — bullish trend")
	} else if tech.SMA50 > 0 && tech.SMA200 > 0 && tech.SMA50 < tech.SMA200 {
		cautions = append(cautions, "Death cross: 50-DMA below 200-DMA — bearish trend")
	}
	if tech.AboveSMA200 {
		buys = append(buys, "Price above 200-DMA — in long-term uptrend")
	} else {
		cautions = append(cautions, "Price below 200-DMA — in long-term downtrend")
	}
	if tech.MACDHisto > 0 {
		buys = append(buys, "MACD histogram positive — bullish momentum")
	} else if tech.MACDHisto < 0 {
		cautions = append(cautions, "MACD histogram negative — bearish momentum")
	}

	// Price position vs 52-week range
	if fund.Week52High > 0 && fund.Week52Low > 0 && tech.CurrentPrice > 0 {
		rangePos := (tech.CurrentPrice - fund.Week52Low) / (fund.Week52High - fund.Week52Low) * 100
		if rangePos < 25 {
			buys = append(buys, fmt.Sprintf("Near 52-week low (%.0f%% of range) — potential value entry", rangePos))
		} else if rangePos > 85 {
			cautions = append(cautions, fmt.Sprintf("Near 52-week high (%.0f%% of range) — limited upside buffer", rangePos))
		}
	}

	return buys, cautions
}

// scoreToRecommendation maps 0–100 composite to label.
func scoreToRecommendation(score int) string {
	switch {
	case score >= 78:
		return "STRONG_BUY"
	case score >= 63:
		return "BUY"
	case score >= 48:
		return "ACCUMULATE"
	case score >= 35:
		return "HOLD"
	case score >= 20:
		return "REDUCE"
	default:
		return "SELL"
	}
}
