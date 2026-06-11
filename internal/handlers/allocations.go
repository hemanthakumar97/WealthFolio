package handlers

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
	"github.com/hemanthakumar97/wealthfolio/internal/services"
)

type AllocationsHandler struct {
	pool   *pgxpool.Pool
	signal *services.SignalService
}

func NewAllocationsHandler(pool *pgxpool.Pool, signal *services.SignalService) *AllocationsHandler {
	return &AllocationsHandler{pool: pool, signal: signal}
}

// longTimeout replaces the request context with a 5-minute timeout so AI calls
// are not cut off by the global 60s middleware. Mirrors SignalHandler's wrapper.
func longTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// rebalanceBandPct is the tolerance (in percentage points of deviation) within
// which a position is considered "on target" and no buy/sell is suggested.
const rebalanceBandPct = 0.5

// Rebalance action verbs returned to the client.
const (
	rebalanceHold = "HOLD"
	rebalanceSell = "SELL"
	rebalanceBuy  = "BUY"
)

// --- response types ---

type instrumentAllocationResponse struct {
	InstrumentID   int64   `json:"instrument_id"`
	InstrumentName string  `json:"instrument_name"`
	AssetType      string  `json:"asset_type"`
	Currency       string  `json:"currency"`
	AllocCategory  string  `json:"alloc_category"`
	CurrentUnits   float64 `json:"current_units"`
	CurrentPrice   float64 `json:"current_price"`
	CurrentValue   float64 `json:"current_value"`
	CurrentPercent float64 `json:"current_percent"`
	TargetPercent  float64 `json:"target_percent"`
	Deviation      float64 `json:"deviation"`
	// Unitized instruments (ETF/STOCK/METAL) are traded in whole units, so the
	// rebalance suggestion is expressed in units; everything else (MF/US_FUND)
	// is redeemed/invested by amount.
	Unitized        bool    `json:"unitized"`
	HasTarget       bool    `json:"has_target"`
	RebalanceAmount float64 `json:"rebalance_amount"` // +ve => sell/trim ₹, -ve => buy/add ₹
	RebalanceUnits  float64 `json:"rebalance_units"`  // +ve => sell, -ve => buy (unitized only)
	RebalanceAction string  `json:"rebalance_action"` // "SELL" | "BUY" | "HOLD" | ""

	SIPAmount         float64    `json:"sip_amount"`
	CurrentSIPPercent float64    `json:"current_sip_percent"`
	SIPTargetPercent  float64    `json:"sip_target_percent"`
	SIPDeviation      float64    `json:"sip_deviation"`
	UpdatedAt         *time.Time `json:"updated_at"`
}

type categoryAllocationResponse struct {
	AllocCategory    string    `json:"alloc_category"`
	TargetPercent    float64   `json:"target_percent"`
	SIPTargetPercent float64   `json:"sip_target_percent"`
	SIPAmount        *float64  `json:"sip_amount"`
	CurrentValue     float64   `json:"current_value"`
	CurrentPercent   float64   `json:"current_percent"`
	Deviation        float64   `json:"deviation"`
	RebalanceAmount  float64   `json:"rebalance_amount"` // +ve => trim ₹, -ve => add ₹
	RebalanceAction  string    `json:"rebalance_action"` // "SELL" | "BUY" | "HOLD" | ""
	UpdatedAt        time.Time `json:"updated_at"`
}

type allocationOverview struct {
	TotalValue            float64                        `json:"total_value"`
	TotalSIP              float64                        `json:"total_sip"`
	InstrumentAllocations []instrumentAllocationResponse `json:"instrument_allocations"`
	CategoryAllocations   []categoryAllocationResponse   `json:"category_allocations"`
}

// holdingAlloc is the joined view of a currently-held instrument and its
// (optional) allocation plan. Shared by Overview and SeedFromHoldings.
type holdingAlloc struct {
	InstrumentID     int64
	Name             string
	AssetType        string
	Currency         string
	Units            float64
	Price            float64
	Value            float64
	TargetPercent    float64
	SIPAmount        float64
	SIPTargetPercent float64
	AllocCategory    string // effective: explicit row value, else asset-type default
	HasRow           bool   // an instrument_allocations row exists
	UpdatedAt        *time.Time
}

// fetchHoldingAllocations returns one row per currently-held instrument that is
// meaningfully held — net units > heldUnitsThreshold (excludes rounding dust from
// closed positions) AND has a current price (excludes instruments with no NAV,
// which would otherwise show ₹0). Left-joined with its allocation plan.
func (h *AllocationsHandler) fetchHoldingAllocations(ctx context.Context) ([]holdingAlloc, error) {
	rows, err := h.pool.Query(ctx, `
		WITH holdings AS (
			SELECT instrument_id,
			       SUM(CASE
			               WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			               WHEN transaction_type IN ('SELL','SWITCH_OUT')       THEN -quantity
			               ELSE 0
			           END) AS units
			  FROM transactions
			 GROUP BY instrument_id
			HAVING SUM(CASE
			               WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			               WHEN transaction_type IN ('SELL','SWITCH_OUT')       THEN -quantity
			               ELSE 0
			           END) > 0.1
		),
		latest AS (
			SELECT DISTINCT ON (instrument_id) instrument_id, nav_price
			  FROM prices
			 ORDER BY instrument_id, price_date DESC
		)
		SELECT i.id, i.name, i.asset_type, i.currency,
		       h.units::float,
		       lp.nav_price::float                             AS price,
		       (h.units * lp.nav_price)::float                 AS value,
		       COALESCE(ia.target_percent, 0)::float,
		       COALESCE(ia.sip_amount, 0)::float,
		       COALESCE(ia.sip_target_percent, 0)::float,
		       ia.alloc_category,
		       (ia.instrument_id IS NOT NULL)                  AS has_row,
		       ia.updated_at
		  FROM holdings h
		  JOIN instruments i ON i.id = h.instrument_id
		  JOIN latest lp ON lp.instrument_id = h.instrument_id AND lp.nav_price > 0
		  LEFT JOIN instrument_allocations ia ON ia.instrument_id = h.instrument_id
		 ORDER BY value DESC, i.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []holdingAlloc{}
	for rows.Next() {
		var ha holdingAlloc
		var cat *string
		if err := rows.Scan(&ha.InstrumentID, &ha.Name, &ha.AssetType, &ha.Currency,
			&ha.Units, &ha.Price, &ha.Value,
			&ha.TargetPercent, &ha.SIPAmount, &ha.SIPTargetPercent,
			&cat, &ha.HasRow, &ha.UpdatedAt); err != nil {
			return nil, err
		}
		if cat != nil {
			ha.AllocCategory = *cat
		} else {
			ha.AllocCategory = defaultAllocCategory(ha.AssetType)
		}
		out = append(out, ha)
	}
	return out, rows.Err()
}

// --- Overview ---

func (h *AllocationsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	holdings, err := h.fetchHoldingAllocations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Total portfolio value derives from the holdings themselves so the page is
	// always consistent with what's shown, even before any target is set.
	var totalValue, totalSIP float64
	for _, ha := range holdings {
		totalValue += ha.Value
		totalSIP += ha.SIPAmount
	}

	instrAllocs := make([]instrumentAllocationResponse, 0, len(holdings))
	catValueMap := map[string]float64{}
	for _, ha := range holdings {
		a := instrumentAllocationResponse{
			InstrumentID:     ha.InstrumentID,
			InstrumentName:   ha.Name,
			AssetType:        ha.AssetType,
			Currency:         ha.Currency,
			AllocCategory:    ha.AllocCategory,
			CurrentUnits:     ha.Units,
			CurrentPrice:     ha.Price,
			CurrentValue:     ha.Value,
			TargetPercent:    ha.TargetPercent,
			Unitized:         isUnitized(ha.AssetType),
			HasTarget:        ha.HasRow,
			SIPAmount:        ha.SIPAmount,
			SIPTargetPercent: ha.SIPTargetPercent,
			UpdatedAt:        ha.UpdatedAt,
		}
		if totalValue > 0 {
			a.CurrentPercent = a.CurrentValue / totalValue * 100
		}
		a.Deviation = a.CurrentPercent - a.TargetPercent

		// Rebalance suggestion only when the instrument is part of a plan
		// (a row exists). Drift = current − target value; +ve means overweight.
		if a.HasTarget {
			targetValue := a.TargetPercent / 100 * totalValue
			a.RebalanceAmount = a.CurrentValue - targetValue
			if a.Unitized && a.CurrentPrice > 0 {
				a.RebalanceUnits = a.RebalanceAmount / a.CurrentPrice
			}
			switch {
			case math.Abs(a.Deviation) < rebalanceBandPct:
				a.RebalanceAction = rebalanceHold
			case a.RebalanceAmount > 0:
				a.RebalanceAction = rebalanceSell
			default:
				a.RebalanceAction = rebalanceBuy
			}
		}

		catValueMap[ha.AllocCategory] += ha.Value
		instrAllocs = append(instrAllocs, a)
	}

	// SIP percentages are relative to total monthly SIP.
	for i := range instrAllocs {
		if totalSIP > 0 {
			instrAllocs[i].CurrentSIPPercent = instrAllocs[i].SIPAmount / totalSIP * 100
		}
		instrAllocs[i].SIPDeviation = instrAllocs[i].CurrentSIPPercent - instrAllocs[i].SIPTargetPercent
	}

	// Category allocations.
	cRows, err := h.pool.Query(ctx, `
		SELECT ca.alloc_category, ca.target_percent::float, ca.sip_target_percent::float,
		       CASE WHEN ca.sip_amount IS NOT NULL THEN ca.sip_amount::float END,
		       ca.updated_at
		  FROM category_allocations ca
		 ORDER BY ca.alloc_category ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cRows.Close()

	catAllocs := []categoryAllocationResponse{}
	var totalCatTarget float64
	for cRows.Next() {
		var ca categoryAllocationResponse
		if err := cRows.Scan(&ca.AllocCategory, &ca.TargetPercent, &ca.SIPTargetPercent,
			&ca.SIPAmount, &ca.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		ca.CurrentValue = catValueMap[ca.AllocCategory]
		if totalValue > 0 {
			ca.CurrentPercent = ca.CurrentValue / totalValue * 100
		}
		ca.Deviation = ca.CurrentPercent - ca.TargetPercent
		totalCatTarget += ca.TargetPercent
		catAllocs = append(catAllocs, ca)
	}
	if err := cRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Category rebalance suggestions are only meaningful once a plan exists
	// (i.e. some category target has been set).
	planExists := totalCatTarget > 0
	for i := range catAllocs {
		ca := &catAllocs[i]
		if !planExists {
			continue
		}
		targetValue := ca.TargetPercent / 100 * totalValue
		ca.RebalanceAmount = ca.CurrentValue - targetValue
		switch {
		case math.Abs(ca.Deviation) < rebalanceBandPct:
			ca.RebalanceAction = rebalanceHold
		case ca.RebalanceAmount > 0:
			ca.RebalanceAction = rebalanceSell
		default:
			ca.RebalanceAction = rebalanceBuy
		}
	}

	writeJSON(w, http.StatusOK, allocationOverview{
		TotalValue:            totalValue,
		TotalSIP:              totalSIP,
		InstrumentAllocations: instrAllocs,
		CategoryAllocations:   catAllocs,
	})
}

// --- Seed targets from current holdings ---

// SeedFromHoldings populates blank allocation targets and categories from the
// current portfolio mix, giving the user a working baseline they can refine.
// It is safe to run repeatedly: it only fills targets that are still 0 and
// never clobbers an existing plan.
func (h *AllocationsHandler) SeedFromHoldings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	holdings, err := h.fetchHoldingAllocations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var totalValue float64
	for _, ha := range holdings {
		totalValue += ha.Value
	}
	if totalValue <= 0 {
		writeJSON(w, http.StatusOK, map[string]any{"seeded": 0})
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)

	catTarget := map[string]float64{}
	seeded := 0
	for _, ha := range holdings {
		pct := ha.Value / totalValue * 100
		catTarget[ha.AllocCategory] += pct
		// Round to 2dp for a tidy starting point.
		pct = math.Round(pct*100) / 100

		// Insert a plan row if missing; on conflict only fill a still-blank
		// target, but always persist the (effective) category classification.
		_, err := tx.Exec(ctx, `
			INSERT INTO instrument_allocations (instrument_id, target_percent, alloc_category)
			VALUES ($1, $2, $3)
			ON CONFLICT (instrument_id) DO UPDATE SET
			    target_percent = CASE WHEN instrument_allocations.target_percent = 0
			                          THEN EXCLUDED.target_percent
			                          ELSE instrument_allocations.target_percent END,
			    alloc_category = EXCLUDED.alloc_category,
			    updated_at     = NOW()`,
			ha.InstrumentID, pct, ha.AllocCategory)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		seeded++
	}

	// Category targets = summed current mix, again only filling blanks.
	for cat, pct := range catTarget {
		pct = math.Round(pct*100) / 100
		if _, err := tx.Exec(ctx, `
			UPDATE category_allocations
			   SET target_percent = $2, updated_at = NOW()
			 WHERE alloc_category = $1 AND target_percent = 0`,
			cat, pct); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seeded": seeded})
}

// --- AI target suggestions ---

type aiSuggestInput struct {
	RiskProfile string `json:"risk_profile"`
	Horizon     string `json:"horizon"`
}

// AISuggest asks the AI for trend-tilted target percentages (category +
// instrument). It does not persist — the frontend reviews, then calls ApplyTargets.
func (h *AllocationsHandler) AISuggest(w http.ResponseWriter, r *http.Request) {
	var req aiSuggestInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	provider, key, model, err := loadAIConfig(r.Context(), h.pool)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if provider == "" || key == "" {
		writeError(w, http.StatusServiceUnavailable, "AI provider not configured — add your API key in Settings → AI")
		return
	}

	result, err := h.signal.SuggestAllocations(r.Context(),
		services.AIConfig{Provider: provider, APIKey: key, Model: model},
		services.AllocSuggestInput{RiskProfile: req.RiskProfile, Horizon: req.Horizon})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Bulk apply reviewed targets ---

type applyTargetsInput struct {
	CategoryTargets []struct {
		AllocCategory string  `json:"alloc_category"`
		TargetPercent float64 `json:"target_percent"`
	} `json:"category_targets"`
	InstrumentTargets []struct {
		InstrumentID  int64   `json:"instrument_id"`
		TargetPercent float64 `json:"target_percent"`
		AllocCategory string  `json:"alloc_category"`
	} `json:"instrument_targets"`
}

// ApplyTargets writes a reviewed set of category + instrument targets in one
// transaction. Reuses the ::numeric-cast upsert so decimals aren't truncated.
func (h *AllocationsHandler) ApplyTargets(w http.ResponseWriter, r *http.Request) {
	var req applyTargetsInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(r.Context())

	applied := 0
	for _, c := range req.CategoryTargets {
		name := strings.ToUpper(strings.TrimSpace(c.AllocCategory))
		if !isValidAllocCategory(name) {
			continue
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE category_allocations SET target_percent = $1::numeric, updated_at = NOW()
			 WHERE alloc_category = $2`, c.TargetPercent, name); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		applied++
	}
	for _, it := range req.InstrumentTargets {
		cat := strings.ToUpper(strings.TrimSpace(it.AllocCategory))
		if !isValidAllocCategory(cat) {
			cat = domain.AllocOthers
		}
		// On insert, classify with the supplied category; on conflict, only the
		// target changes (an existing category classification is preserved).
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO instrument_allocations (instrument_id, target_percent, alloc_category)
			 VALUES ($1, COALESCE($2::numeric, 0), $3::text)
			 ON CONFLICT (instrument_id) DO UPDATE SET
			     target_percent = COALESCE($2::numeric, instrument_allocations.target_percent),
			     updated_at     = NOW()`,
			it.InstrumentID, it.TargetPercent, cat); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		applied++
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied})
}

// --- Update instrument allocation ---

type updateInstrumentAllocInput struct {
	TargetPercent    *float64 `json:"target_percent"`
	SIPAmount        *float64 `json:"sip_amount"`
	SIPTargetPercent *float64 `json:"sip_target_percent"`
	AllocCategory    *string  `json:"alloc_category"`
}

func (h *AllocationsHandler) UpdateInstrument(w http.ResponseWriter, r *http.Request) {
	instrID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var req updateInstrumentAllocInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Upsert the allocation record. The INSERT path coalesces unset fields to
	// their column defaults so a partial update (e.g. target only) doesn't try
	// to write NULL into the NOT NULL sip/target columns; the UPDATE path keeps
	// existing values for any field the request omitted.
	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO instrument_allocations (instrument_id, target_percent, sip_amount, sip_target_percent, alloc_category)
		 VALUES ($1, COALESCE($2::numeric, 0), COALESCE($3::numeric, 0), COALESCE($4::numeric, 0), COALESCE($5::text, 'OTHERS'))
		 ON CONFLICT (instrument_id) DO UPDATE SET
		     target_percent     = COALESCE($2::numeric, instrument_allocations.target_percent),
		     sip_amount         = COALESCE($3::numeric, instrument_allocations.sip_amount),
		     sip_target_percent = COALESCE($4::numeric, instrument_allocations.sip_target_percent),
		     alloc_category     = COALESCE($5::text, instrument_allocations.alloc_category),
		     updated_at         = NOW()`,
		instrID,
		req.TargetPercent,
		req.SIPAmount,
		req.SIPTargetPercent,
		allocCategoryOrDefault(req.AllocCategory),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return updated row.
	var a instrumentAllocationResponse
	var updatedAt time.Time
	err = h.pool.QueryRow(r.Context(),
		`SELECT ia.instrument_id, i.name, i.asset_type, i.currency, ia.alloc_category,
		        ia.target_percent::float, ia.sip_amount::float, ia.sip_target_percent::float, ia.updated_at
		   FROM instrument_allocations ia
		   JOIN instruments i ON i.id = ia.instrument_id
		  WHERE ia.instrument_id = $1`, instrID,
	).Scan(&a.InstrumentID, &a.InstrumentName, &a.AssetType, &a.Currency, &a.AllocCategory,
		&a.TargetPercent, &a.SIPAmount, &a.SIPTargetPercent, &updatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.UpdatedAt = &updatedAt
	a.Unitized = isUnitized(a.AssetType)
	a.HasTarget = true
	writeJSON(w, http.StatusOK, a)
}

// --- Update category allocation ---

type updateCategoryAllocInput struct {
	TargetPercent    *float64 `json:"target_percent"`
	SIPTargetPercent *float64 `json:"sip_target_percent"`
	SIPAmount        *float64 `json:"sip_amount"`
}

func (h *AllocationsHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	name := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "name")))
	if !isValidAllocCategory(name) {
		writeError(w, http.StatusBadRequest, "invalid allocation category")
		return
	}

	var req updateCategoryAllocInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE category_allocations SET
		     target_percent     = COALESCE($1, target_percent),
		     sip_target_percent = COALESCE($2, sip_target_percent),
		     sip_amount         = COALESCE($3, sip_amount),
		     updated_at         = NOW()
		 WHERE alloc_category = $4`,
		req.TargetPercent, req.SIPTargetPercent, req.SIPAmount, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var ca categoryAllocationResponse
	err = h.pool.QueryRow(r.Context(),
		`SELECT alloc_category, target_percent::float, sip_target_percent::float,
		        CASE WHEN sip_amount IS NOT NULL THEN sip_amount::float END, updated_at
		   FROM category_allocations WHERE alloc_category = $1`, name,
	).Scan(&ca.AllocCategory, &ca.TargetPercent, &ca.SIPTargetPercent, &ca.SIPAmount, &ca.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ca)
}

// --- Distribution calculator ---

type distributionRequest struct {
	Amount float64 `json:"amount"`
}

type distributionItem struct {
	InstrumentID   int64   `json:"instrument_id"`
	InstrumentName string  `json:"instrument_name"`
	AllocCategory  string  `json:"alloc_category"`
	TargetPercent  float64 `json:"target_percent"`
	Amount         float64 `json:"amount"`
}

func (h *AllocationsHandler) CalculateDistribution(w http.ResponseWriter, r *http.Request) {
	var req distributionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT ia.instrument_id, i.name, ia.alloc_category, ia.target_percent::float
		   FROM instrument_allocations ia
		   JOIN instruments i ON i.id = ia.instrument_id
		  WHERE ia.target_percent > 0
		  ORDER BY ia.target_percent DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	items := []distributionItem{}
	var totalTarget float64
	for rows.Next() {
		var d distributionItem
		if err := rows.Scan(&d.InstrumentID, &d.InstrumentName, &d.AllocCategory, &d.TargetPercent); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		totalTarget += d.TargetPercent
		items = append(items, d)
	}

	for i := range items {
		if totalTarget > 0 {
			items[i].Amount = req.Amount * items[i].TargetPercent / totalTarget
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"amount":       req.Amount,
		"items":        items,
		"total_target": totalTarget,
	})
}

// --- helpers ---

func isValidAllocCategory(s string) bool {
	switch s {
	case domain.AllocEquity, domain.AllocMetals, domain.AllocDebt,
		domain.AllocUSEquity, domain.AllocOthers:
		return true
	}
	return false
}

func allocCategoryOrDefault(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.ToUpper(strings.TrimSpace(*s))
	if !isValidAllocCategory(v) {
		v = domain.AllocOthers
	}
	return &v
}

// defaultAllocCategory maps an instrument's asset_type to a sensible default
// allocation bucket, used when the user hasn't classified it explicitly.
func defaultAllocCategory(assetType string) string {
	switch assetType {
	case domain.AssetTypeMetal:
		return domain.AllocMetals
	case domain.AssetTypeUSFund:
		return domain.AllocUSEquity
	case domain.AssetTypeBond:
		return domain.AllocDebt
	case domain.AssetTypeMF, domain.AssetTypeETF, domain.AssetTypeStock:
		return domain.AllocEquity
	default:
		return domain.AllocOthers
	}
}

// isUnitized reports whether an asset is traded in discrete units (so the
// rebalance suggestion is given in units rather than rupees).
func isUnitized(assetType string) bool {
	switch assetType {
	case domain.AssetTypeETF, domain.AssetTypeStock, domain.AssetTypeMetal:
		return true
	}
	return false
}
