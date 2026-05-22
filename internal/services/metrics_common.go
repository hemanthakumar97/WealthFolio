package services

import (
	"math"
	"sort"
	"time"
)

// ChartPoint is a single {date, value} pair for chart series. Short keys keep JSON small.
type ChartPoint struct {
	D string  `json:"d"` // YYYY-MM-DD
	V float64 `json:"v"`
}

// navPoint is an internal date+value pair used for all price-series computations.
type navPoint struct {
	Date time.Time
	NAV  float64
}

// valueOnOrBefore returns the last NAV value on or before target.
func valueOnOrBefore(navs []navPoint, target time.Time) float64 {
	var result float64
	for _, n := range navs {
		if !n.Date.After(target) {
			result = n.NAV
		} else {
			break
		}
	}
	return result
}

// rollingCAGRs computes CAGR over a rolling window of `days` days,
// using the last 2× days worth of data points as the endpoint pool.
func rollingCAGRs(navs []navPoint, days int) []float64 {
	var results []float64
	lookback := days * 2
	startIdx := 0
	if len(navs) > lookback {
		startIdx = len(navs) - lookback
	}
	for i := startIdx; i < len(navs); i++ {
		end := navs[i]
		targetStart := end.Date.AddDate(0, 0, -days)
		startNAV := valueOnOrBefore(navs[:i+1], targetStart)
		if startNAV <= 0 {
			continue
		}
		years := float64(days) / 365.0
		cagr := (math.Pow(end.NAV/startNAV, 1/years) - 1) * 100
		results = append(results, cagr)
	}
	return results
}

// annualisedStdDev computes annualised standard deviation of daily returns since `since`.
func annualisedStdDev(navs []navPoint, since time.Time) float64 {
	var window []navPoint
	for _, n := range navs {
		if !n.Date.Before(since) {
			window = append(window, n)
		}
	}
	if len(window) < 5 {
		return 0
	}
	rets := make([]float64, 0, len(window)-1)
	for i := 1; i < len(window); i++ {
		if window[i-1].NAV > 0 {
			rets = append(rets, (window[i].NAV-window[i-1].NAV)/window[i-1].NAV)
		}
	}
	if len(rets) < 4 {
		return 0
	}
	// Adjust annualisation factor for sparse data (monthly ≈ 12/yr, weekly ≈ 52/yr, daily ≈ 252/yr).
	// Estimate trading periods per year from the actual data density.
	var periodsPerYear float64
	if len(window) >= 2 {
		spanDays := window[len(window)-1].Date.Sub(window[0].Date).Hours() / 24
		if spanDays > 0 {
			periodsPerYear = float64(len(window)-1) / (spanDays / 365.0)
		}
	}
	if periodsPerYear < 1 {
		periodsPerYear = 252
	}
	mean := average(rets)
	var variance float64
	for _, r := range rets {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(rets))
	return math.Sqrt(variance) * math.Sqrt(periodsPerYear) * 100
}

// maxDrawdown computes the maximum drawdown from peak over the window starting at `since`.
func maxDrawdown(navs []navPoint, since time.Time) float64 {
	var peak, maxDD float64
	for _, n := range navs {
		if n.Date.Before(since) {
			continue
		}
		if n.NAV > peak {
			peak = n.NAV
		}
		if peak > 0 {
			dd := (peak - n.NAV) / peak * 100
			if dd > maxDD {
				maxDD = dd
			}
		}
	}
	return maxDD
}

// sampleSeries returns a series of ChartPoints sampled from navs since `since`, capped at maxPoints.
func sampleSeries(navs []navPoint, since time.Time, maxPoints int) []ChartPoint {
	var window []navPoint
	for _, n := range navs {
		if !n.Date.Before(since) {
			window = append(window, n)
		}
	}
	if len(window) == 0 {
		return nil
	}
	step := len(window) / maxPoints
	if step < 1 {
		step = 1
	}
	result := make([]ChartPoint, 0, maxPoints+1)
	for i := 0; i < len(window); i += step {
		result = append(result, ChartPoint{
			D: window[i].Date.Format("2006-01-02"),
			V: roundF(window[i].NAV, 3),
		})
	}
	last := window[len(window)-1]
	if result[len(result)-1].D != last.Date.Format("2006-01-02") {
		result = append(result, ChartPoint{D: last.Date.Format("2006-01-02"), V: roundF(last.NAV, 3)})
	}
	return result
}

// drawdownSeries returns the rolling drawdown-from-peak series since `since`, sampled to maxPoints.
func drawdownSeries(navs []navPoint, since time.Time, maxPoints int) []ChartPoint {
	var window []navPoint
	for _, n := range navs {
		if !n.Date.Before(since) {
			window = append(window, n)
		}
	}
	if len(window) == 0 {
		return nil
	}
	type dated struct {
		date time.Time
		dd   float64
	}
	var raw []dated
	var peak float64
	for _, n := range navs {
		if n.NAV > peak {
			peak = n.NAV
		}
		if n.Date.Before(since) {
			continue
		}
		dd := 0.0
		if peak > 0 {
			dd = -((peak - n.NAV) / peak * 100)
		}
		raw = append(raw, dated{n.Date, roundF(dd, 2)})
	}
	if len(raw) == 0 {
		return nil
	}
	step := len(raw) / maxPoints
	if step < 1 {
		step = 1
	}
	result := make([]ChartPoint, 0, maxPoints)
	for i := 0; i < len(raw); i += step {
		result = append(result, ChartPoint{D: raw[i].date.Format("2006-01-02"), V: raw[i].dd})
	}
	return result
}

// rollingCAGRSeries returns a time series of rolling CAGR values with dates, sampled to maxPoints.
func rollingCAGRSeries(navs []navPoint, days int, lookbackDays int) []ChartPoint {
	startIdx := 0
	if len(navs) > lookbackDays {
		startIdx = len(navs) - lookbackDays
	}
	window := navs[startIdx:]

	type dated struct {
		date time.Time
		val  float64
	}
	var raw []dated
	for i, end := range window {
		targetStart := end.Date.AddDate(0, 0, -days)
		startNAV := valueOnOrBefore(navs[:startIdx+i+1], targetStart)
		if startNAV <= 0 {
			continue
		}
		years := float64(days) / 365.0
		cagr := (math.Pow(end.NAV/startNAV, 1/years) - 1) * 100
		raw = append(raw, dated{end.Date, roundF(cagr, 2)})
	}
	if len(raw) == 0 {
		return nil
	}
	step := len(raw) / 260
	if step < 1 {
		step = 1
	}
	result := make([]ChartPoint, 0, 260)
	for i := 0; i < len(raw); i += step {
		result = append(result, ChartPoint{D: raw[i].date.Format("2006-01-02"), V: raw[i].val})
	}
	last := raw[len(raw)-1]
	if result[len(result)-1].D != last.date.Format("2006-01-02") {
		result = append(result, ChartPoint{D: last.date.Format("2006-01-02"), V: last.val})
	}
	return result
}

// sortNavPoints sorts a slice of navPoints by date ascending.
func sortNavPoints(navs []navPoint) {
	sort.Slice(navs, func(i, j int) bool { return navs[i].Date.Before(navs[j].Date) })
}

// medianAnnualStdDev computes the median of yearly annualised std devs over the window.
// Splits the period into 1-year slices and returns the median, giving a stable
// baseline of "normal" volatility for this fund that isn't skewed by one bad year.
func medianAnnualStdDev(navs []navPoint, since time.Time) float64 {
	type yearSlice struct{ from, to time.Time }
	// Build non-overlapping 1-year windows from `since` to now.
	end := time.Now()
	var slices []yearSlice
	cur := since
	for cur.Before(end) {
		next := cur.AddDate(1, 0, 0)
		if next.After(end) {
			next = end
		}
		slices = append(slices, yearSlice{cur, next})
		cur = next
	}
	var devs []float64
	for _, sl := range slices {
		var window []navPoint
		for _, n := range navs {
			if !n.Date.Before(sl.from) && n.Date.Before(sl.to) {
				window = append(window, n)
			}
		}
		if len(window) < 5 { // works with monthly data (~12 pts/yr)
			continue
		}
		dev := annualisedStdDev(window, sl.from)
		if dev > 0 {
			devs = append(devs, dev)
		}
	}
	if len(devs) == 0 {
		return 0
	}
	sort.Float64s(devs)
	mid := len(devs) / 2
	if len(devs)%2 == 0 {
		return (devs[mid-1] + devs[mid]) / 2
	}
	return devs[mid]
}

// ─── Pure math helpers ────────────────────────────────────────────────────────

func average(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func minFloatVal(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func percentPositive(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var count int
	for _, v := range vals {
		if v > 0 {
			count++
		}
	}
	return float64(count) / float64(len(vals)) * 100
}

func roundF(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}
