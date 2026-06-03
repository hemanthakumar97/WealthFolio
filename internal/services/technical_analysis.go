package services

import "math"

// TechnicalResult holds all computed indicators from a price series.
type TechnicalResult struct {
	CurrentPrice float64
	SMA50        float64
	SMA200       float64
	EMA20        float64
	BBUpper      float64
	BBLower      float64
	RSI14        float64
	MACDLine     float64
	MACDSignal   float64
	MACDHisto    float64
	AboveSMA50   bool
	AboveSMA200  bool
	GoldenCross  bool // SMA50 > SMA200
	Score        int  // 0–100 technical score
}

// ComputeTechnicals derives all technical indicators from a sorted (asc) price series.
// Requires at least 200 data points for full accuracy; returns best-effort with fewer.
func ComputeTechnicals(navs []navPoint) TechnicalResult {
	if len(navs) < 14 {
		return TechnicalResult{}
	}

	prices := make([]float64, len(navs))
	for i, n := range navs {
		prices[i] = n.NAV
	}

	cur := prices[len(prices)-1]
	tr := TechnicalResult{CurrentPrice: cur}

	tr.SMA50 = sma(prices, 50)
	tr.SMA200 = sma(prices, 200)
	tr.EMA20 = ema(prices, 20)
	tr.BBUpper, tr.BBLower = bollingerBands(prices, 20, 2.0)
	tr.RSI14 = rsi(prices, 14)
	tr.MACDLine, tr.MACDSignal, tr.MACDHisto = macd(prices, 12, 26, 9)

	if tr.SMA50 > 0 {
		tr.AboveSMA50 = cur > tr.SMA50
	}
	if tr.SMA200 > 0 {
		tr.AboveSMA200 = cur > tr.SMA200
	}
	if tr.SMA50 > 0 && tr.SMA200 > 0 {
		tr.GoldenCross = tr.SMA50 > tr.SMA200
	}

	tr.Score = technicalScore(tr)
	return tr
}

// technicalScore converts indicators into 0–100 score.
func technicalScore(tr TechnicalResult) int {
	score := 0

	// Trend (40 pts): being above key MAs and golden cross
	if tr.AboveSMA200 {
		score += 20
	}
	if tr.AboveSMA50 {
		score += 12
	}
	if tr.GoldenCross {
		score += 8
	}

	// RSI (30 pts): oversold is bullish, overbought is bearish
	switch {
	case tr.RSI14 > 0 && tr.RSI14 < 30:
		score += 30 // oversold → strong buy signal
	case tr.RSI14 >= 30 && tr.RSI14 < 50:
		score += 22
	case tr.RSI14 >= 50 && tr.RSI14 < 65:
		score += 15
	case tr.RSI14 >= 65 && tr.RSI14 < 75:
		score += 8
	default:
		score += 0 // overbought (>75) = caution
	}

	// MACD momentum (30 pts)
	switch {
	case tr.MACDHisto > 0 && tr.MACDLine > 0:
		score += 30 // bullish crossover with positive momentum
	case tr.MACDHisto > 0:
		score += 20 // improving but still below zero
	case tr.MACDHisto < 0 && tr.MACDHisto > -0.5:
		score += 10 // slightly negative
	default:
		score += 0 // bearish
	}

	if score > 100 {
		score = 100
	}
	return score
}

// ─── Indicator implementations ────────────────────────────────────────────────

// sma computes the simple moving average of the last `period` values.
func sma(prices []float64, period int) float64 {
	if len(prices) < period {
		period = len(prices)
	}
	if period == 0 {
		return 0
	}
	window := prices[len(prices)-period:]
	sum := 0.0
	for _, p := range window {
		sum += p
	}
	return sum / float64(period)
}

// ema computes exponential moving average using a simple seed (first-period SMA).
func ema(prices []float64, period int) float64 {
	if len(prices) < period {
		return sma(prices, len(prices))
	}
	k := 2.0 / float64(period+1)
	// Seed with SMA of first `period` values.
	val := sma(prices[:period], period)
	for _, p := range prices[period:] {
		val = p*k + val*(1-k)
	}
	return val
}

// rsi computes Wilder's RSI for `period` using all available price data.
func rsi(prices []float64, period int) float64 {
	if len(prices) < period+1 {
		return 50 // not enough data
	}
	var gains, losses float64
	for i := 1; i <= period; i++ {
		d := prices[i] - prices[i-1]
		if d > 0 {
			gains += d
		} else {
			losses -= d
		}
	}
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	for i := period + 1; i < len(prices); i++ {
		d := prices[i] - prices[i-1]
		if d > 0 {
			avgGain = (avgGain*float64(period-1) + d) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) - d) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return roundF(100-(100/(1+rs)), 2)
}

// macd returns (macdLine, signalLine, histogram) using EMA(fast), EMA(slow), EMA(signal).
func macd(prices []float64, fast, slow, signal int) (line, sig, histo float64) {
	if len(prices) < slow+signal {
		return 0, 0, 0
	}
	fastEMA := ema(prices, fast)
	slowEMA := ema(prices, slow)
	line = fastEMA - slowEMA

	// Compute MACD line series for signal EMA
	if len(prices) < slow+signal {
		return line, line, 0
	}
	macdSeries := make([]float64, len(prices)-slow+1)
	k := 2.0 / float64(fast + 1)
	kSlow := 2.0 / float64(slow + 1)
	f := sma(prices[:fast], fast)
	s := sma(prices[:slow], slow)
	macdSeries[0] = f - s
	for i := slow; i < len(prices); i++ {
		f = prices[i]*k + f*(1-k)
		s = prices[i]*kSlow + s*(1-kSlow)
		macdSeries[i-slow+1] = f - s
	}

	sig = ema(macdSeries, signal)
	histo = roundF(macdSeries[len(macdSeries)-1]-sig, 4)
	line = roundF(macdSeries[len(macdSeries)-1], 4)
	sig = roundF(sig, 4)
	return line, sig, histo
}

// bollingerBands returns (upper, lower) band using a 20-period SMA ± stddev multiplier.
func bollingerBands(prices []float64, period int, mult float64) (upper, lower float64) {
	if len(prices) < period {
		return 0, 0
	}
	window := prices[len(prices)-period:]
	mean := sma(window, period)
	var variance float64
	for _, p := range window {
		d := p - mean
		variance += d * d
	}
	stddev := math.Sqrt(variance / float64(period))
	return roundF(mean+mult*stddev, 2), roundF(mean-mult*stddev, 2)
}
