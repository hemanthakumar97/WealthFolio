package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/domain"
)

type AllocationsHandler struct {
	pool *pgxpool.Pool
}

func NewAllocationsHandler(pool *pgxpool.Pool) *AllocationsHandler {
	return &AllocationsHandler{pool: pool}
}

// --- response types ---

type instrumentAllocationResponse struct {
	InstrumentID       int64     `json:"instrument_id"`
	InstrumentName     string    `json:"instrument_name"`
	AllocCategory      string    `json:"alloc_category"`
	CurrentValue       float64   `json:"current_value"`
	CurrentPercent     float64   `json:"current_percent"`
	TargetPercent      float64   `json:"target_percent"`
	Deviation          float64   `json:"deviation"`
	SIPAmount          float64   `json:"sip_amount"`
	CurrentSIPPercent  float64   `json:"current_sip_percent"`
	SIPTargetPercent   float64   `json:"sip_target_percent"`
	SIPDeviation       float64   `json:"sip_deviation"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type categoryAllocationResponse struct {
	AllocCategory     string    `json:"alloc_category"`
	TargetPercent     float64   `json:"target_percent"`
	SIPTargetPercent  float64   `json:"sip_target_percent"`
	SIPAmount         *float64  `json:"sip_amount"`
	CurrentValue      float64   `json:"current_value"`
	CurrentPercent    float64   `json:"current_percent"`
	Deviation         float64   `json:"deviation"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type allocationOverview struct {
	TotalValue             float64                         `json:"total_value"`
	TotalSIP               float64                         `json:"total_sip"`
	InstrumentAllocations  []instrumentAllocationResponse  `json:"instrument_allocations"`
	CategoryAllocations    []categoryAllocationResponse    `json:"category_allocations"`
}

// --- Overview ---

func (h *AllocationsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Calculate total current portfolio value from holdings.
	var totalValue float64
	_ = h.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(p.nav_price * t.units)::float, 0)
		FROM (
			SELECT instrument_id,
			       SUM(CASE
			               WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			               WHEN transaction_type IN ('SELL','SWITCH_OUT')        THEN -quantity
			               ELSE 0
			           END) AS units
			FROM transactions
			GROUP BY instrument_id
			HAVING SUM(CASE
			               WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			               WHEN transaction_type IN ('SELL','SWITCH_OUT')        THEN -quantity
			               ELSE 0
			           END) > 0.001
		) t
		JOIN LATERAL (
			SELECT nav_price FROM prices
			WHERE instrument_id = t.instrument_id
			ORDER BY price_date DESC LIMIT 1
		) p ON TRUE
	`).Scan(&totalValue)

	// Instrument allocations — join with latest price to get current_value.
	iRows, err := h.pool.Query(ctx, `
		SELECT ia.instrument_id, i.name, ia.alloc_category,
		       ia.target_percent::float, ia.sip_amount::float, ia.sip_target_percent::float,
		       ia.updated_at,
		       COALESCE(
		           (SELECT p.nav_price::float *
		                   GREATEST(
		                       (SELECT SUM(CASE
		                                       WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
		                                       WHEN transaction_type IN ('SELL','SWITCH_OUT')        THEN -quantity
		                                       ELSE 0
		                                   END)::float
		                          FROM transactions
		                         WHERE instrument_id = ia.instrument_id), 0
		                   )
		              FROM prices p
		             WHERE p.instrument_id = ia.instrument_id
		             ORDER BY p.price_date DESC LIMIT 1),
		       0) AS current_value
		  FROM instrument_allocations ia
		  JOIN instruments i ON i.id = ia.instrument_id
		 ORDER BY i.name ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer iRows.Close()

	instrAllocs := []instrumentAllocationResponse{}
	var totalSIP float64
	for iRows.Next() {
		var a instrumentAllocationResponse
		if err := iRows.Scan(&a.InstrumentID, &a.InstrumentName, &a.AllocCategory,
			&a.TargetPercent, &a.SIPAmount, &a.SIPTargetPercent, &a.UpdatedAt, &a.CurrentValue); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if totalValue > 0 {
			a.CurrentPercent = a.CurrentValue / totalValue * 100
		}
		a.Deviation = a.CurrentPercent - a.TargetPercent
		totalSIP += a.SIPAmount
		// SIP percentages are relative to total SIP — compute after loop.
		instrAllocs = append(instrAllocs, a)
	}
	// Compute SIP percentages now that totalSIP is known.
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

	// Build a map of category → current_value from instrument_allocations we already have.
	catValueMap := map[string]float64{}
	for _, ia := range instrAllocs {
		catValueMap[ia.AllocCategory] += ia.CurrentValue
	}

	catAllocs := []categoryAllocationResponse{}
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
		catAllocs = append(catAllocs, ca)
	}

	writeJSON(w, http.StatusOK, allocationOverview{
		TotalValue:            totalValue,
		TotalSIP:              totalSIP,
		InstrumentAllocations: instrAllocs,
		CategoryAllocations:   catAllocs,
	})
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

	// Upsert the allocation record.
	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO instrument_allocations (instrument_id, target_percent, sip_amount, sip_target_percent, alloc_category)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (instrument_id) DO UPDATE SET
		     target_percent     = COALESCE($2, instrument_allocations.target_percent),
		     sip_amount         = COALESCE($3, instrument_allocations.sip_amount),
		     sip_target_percent = COALESCE($4, instrument_allocations.sip_target_percent),
		     alloc_category     = COALESCE($5, instrument_allocations.alloc_category),
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
	err = h.pool.QueryRow(r.Context(),
		`SELECT ia.instrument_id, i.name, ia.alloc_category,
		        ia.target_percent::float, ia.sip_amount::float, ia.sip_target_percent::float, ia.updated_at,
		        0::float
		   FROM instrument_allocations ia
		   JOIN instruments i ON i.id = ia.instrument_id
		  WHERE ia.instrument_id = $1`, instrID,
	).Scan(&a.InstrumentID, &a.InstrumentName, &a.AllocCategory,
		&a.TargetPercent, &a.SIPAmount, &a.SIPTargetPercent, &a.UpdatedAt, &a.CurrentValue)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	case domain.AllocEquity, domain.AllocGold, domain.AllocDebt,
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
