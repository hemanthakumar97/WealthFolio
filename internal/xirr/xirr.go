// Package xirr computes the Extended Internal Rate of Return for a series of
// dated cash flows using Newton-Raphson iteration.
package xirr

import (
	"errors"
	"math"
	"time"
)

// CashFlow is a (date, amount) pair.
// Negative amounts are outflows (purchases), positive are inflows (sales, final value).
type CashFlow struct {
	Date   time.Time
	Amount float64
}

var ErrNoConvergence = errors.New("XIRR did not converge")

// Calculate returns the annualized rate of return for the given cash flows.
// Returns ErrNoConvergence if the algorithm fails after maxIter iterations.
func Calculate(flows []CashFlow) (float64, error) {
	if len(flows) < 2 {
		return 0, errors.New("need at least 2 cash flows")
	}
	// Use first flow date as reference.
	t0 := flows[0].Date

	years := func(d time.Time) float64 {
		return d.Sub(t0).Hours() / (365.25 * 24)
	}

	// Newton-Raphson.
	rate := 0.1
	const maxIter = 300
	const tol = 1e-7

	for i := 0; i < maxIter; i++ {
		npv := 0.0
		dnpv := 0.0
		for _, cf := range flows {
			t := years(cf.Date)
			denom := math.Pow(1+rate, t)
			if denom == 0 {
				return 0, ErrNoConvergence
			}
			npv += cf.Amount / denom
			dnpv -= cf.Amount * t / (denom * (1 + rate))
		}
		if math.IsNaN(npv) || math.IsInf(npv, 0) {
			return 0, ErrNoConvergence
		}
		if dnpv == 0 {
			return 0, ErrNoConvergence
		}
		next := rate - npv/dnpv
		if math.Abs(next-rate) < tol {
			return next, nil
		}
		rate = next
		if rate <= -1 {
			rate = -0.999
		}
	}
	return 0, ErrNoConvergence
}
