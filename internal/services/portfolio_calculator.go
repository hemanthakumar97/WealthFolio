package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PortfolioCalculator computes holdings, closed positions, and summary metrics
// from the transactions + prices tables. No caching — always reads latest data.
type PortfolioCalculator struct {
	pool *pgxpool.Pool
	fx   *FXService
}

func NewPortfolioCalculator(pool *pgxpool.Pool, fx *FXService) *PortfolioCalculator {
	return &PortfolioCalculator{pool: pool, fx: fx}
}

// FilterParams are optional query filters.
type FilterParams struct {
	Platform  string
	AssetType string
	SortBy    string // "value" | "pl" | "name" | "invested"
}

// --- Holding -----------------------------------------------------------------

type HoldingInfo struct {
	InstrumentID   int64    `json:"instrument_id"`
	InstrumentName string   `json:"instrument_name"`
	ISIN           *string  `json:"isin"`
	AssetType      string   `json:"asset_type"`
	Currency       string   `json:"currency"`
	TotalUnits     float64  `json:"total_units"`
	AvgBuyPrice    float64  `json:"avg_buy_price"`
	InvestedAmount float64  `json:"invested_amount"`
	CurrentPrice   *float64 `json:"current_price"`
	LastPriceDate  *string  `json:"last_price_date"`  // ISO date string
	LastFetchedAt  *string  `json:"last_fetched_at"` // ISO datetime string
	CurrentValue   *float64 `json:"current_value"`
	ProfitLoss     *float64 `json:"profit_loss"`
	ProfitLossPct  *float64 `json:"profit_loss_percent"`
	Platform       string   `json:"platform"`
	Categories     []string `json:"categories"`
}

// fifoLot represents one purchase lot: remaining units and cost per unit.
type fifoLot struct {
	qty  float64
	cost float64
}

type instrPosition struct {
	units    float64
	avgCost  float64
	platform string
}

// computeFIFOPositions walks all transactions in date order per instrument and
// returns each instrument's current FIFO cost basis with estimated charges.
func (c *PortfolioCalculator) computeFIFOPositions(ctx context.Context) (map[int64]instrPosition, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT instrument_id, transaction_type,
		       quantity::float, amount::float, platform
		  FROM transactions
		 WHERE transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		 ORDER BY instrument_id, transaction_date, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type instrState struct {
		lots     []fifoLot
		platform string
	}
	states := map[int64]*instrState{}

	for rows.Next() {
		var instrID int64
		var txType, platform string
		var qty, amount float64
		if err := rows.Scan(&instrID, &txType, &qty, &amount, &platform); err != nil {
			return nil, err
		}
		s, ok := states[instrID]
		if !ok {
			s = &instrState{}
			states[instrID] = s
		}
		s.platform = platform

		switch txType {
		case "BUY", "SWITCH_IN":
			if qty <= 0 {
				continue
			}
			s.lots = append(s.lots, fifoLot{qty: qty, cost: amount / qty})
		case "BONUS":
			if qty > 0 {
				s.lots = append(s.lots, fifoLot{qty: qty, cost: 0})
			}
		case "SELL", "SWITCH_OUT":
			remaining := qty
			for len(s.lots) > 0 && remaining > 0.000001 {
				if s.lots[0].qty <= remaining+0.000001 {
					remaining -= s.lots[0].qty
					s.lots = s.lots[1:]
				} else {
					s.lots[0].qty -= remaining
					remaining = 0
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	positions := make(map[int64]instrPosition, len(states))
	for instrID, s := range states {
		var totalUnits, totalCost float64
		for _, l := range s.lots {
			totalUnits += l.qty
			totalCost += l.qty * l.cost
		}
		if totalUnits > 0.000001 {
			positions[instrID] = instrPosition{
				units:    totalUnits,
				avgCost:  totalCost / totalUnits,
				platform: s.platform,
			}
		}
	}
	return positions, nil
}

func (c *PortfolioCalculator) Holdings(ctx context.Context, f FilterParams) ([]HoldingInfo, error) {
	positions, err := c.computeFIFOPositions(ctx)
	if err != nil {
		return nil, err
	}
	if len(positions) == 0 {
		return nil, nil
	}

	instrIDs := make([]int64, 0, len(positions))
	for id := range positions {
		instrIDs = append(instrIDs, id)
	}

	// Fetch instrument metadata + latest price in one query.
	type instrRow struct {
		id        int64
		name      string
		isin      *string
		assetType string
		currency  string
		navPrice  *float64
		priceDate *string
		fetchedAt *time.Time
	}
	irows, err := c.pool.Query(ctx, `
		SELECT i.id, i.name, i.isin, i.asset_type, i.currency,
		       lp.nav_price, lp.price_date::text, lp.fetched_at
		  FROM instruments i
		  LEFT JOIN LATERAL (
		    SELECT nav_price::float, price_date, fetched_at
		      FROM prices WHERE instrument_id = i.id
		     ORDER BY price_date DESC LIMIT 1
		  ) lp ON true
		 WHERE i.id = ANY($1)
	`, instrIDs)
	if err != nil {
		return nil, err
	}
	defer irows.Close()

	instrMap := make(map[int64]instrRow, len(instrIDs))
	for irows.Next() {
		var r instrRow
		if err := irows.Scan(&r.id, &r.name, &r.isin, &r.assetType, &r.currency,
			&r.navPrice, &r.priceDate, &r.fetchedAt); err != nil {
			return nil, err
		}
		instrMap[r.id] = r
	}
	if err := irows.Err(); err != nil {
		return nil, err
	}

	usdINR, err := c.fx.USDToINR(ctx)
	if err != nil {
		return nil, fmt.Errorf("holdings: %w", err)
	}
	usdInrFloat, _ := usdINR.Float64()

	var out []HoldingInfo
	for instrID, pos := range positions {
		instr, ok := instrMap[instrID]
		if !ok {
			continue
		}
		if f.Platform != "" && !strings.EqualFold(pos.platform, f.Platform) {
			continue
		}
		if f.AssetType != "" && !strings.EqualFold(instr.assetType, f.AssetType) {
			continue
		}

		isUSD := strings.EqualFold(instr.currency, "USD")

		// avg_buy_price returned in native currency (USD for US funds) so it
		// displays correctly alongside the instrument's currency label.
		// investedAmount is always in INR for consistent portfolio totals.
		avgCostINR := pos.avgCost
		if isUSD {
			avgCostINR = pos.avgCost * usdInrFloat
		}
		investedAmount := pos.units * avgCostINR
		if investedAmount < 500 {
			continue
		}

		h := HoldingInfo{
			InstrumentID:   instrID,
			InstrumentName: instr.name,
			ISIN:           instr.isin,
			AssetType:      instr.assetType,
			Currency:       instr.currency,
			TotalUnits:     pos.units,
			AvgBuyPrice:    pos.avgCost, // native currency (USD for US funds)
			InvestedAmount: investedAmount,
			Platform:       pos.platform,
			Categories:     []string{},
		}
		if instr.priceDate != nil {
			h.LastPriceDate = instr.priceDate
		}
		if instr.fetchedAt != nil {
			s := instr.fetchedAt.UTC().Format(time.RFC3339)
			h.LastFetchedAt = &s
		}
		if instr.navPrice != nil && *instr.navPrice > 0 {
			// nav_price is stored in INR for USD instruments (price_fetcher converts on write).
			// Return current_price in native currency; all totals stay in INR.
			displayPrice := *instr.navPrice
			if isUSD && usdInrFloat > 0 {
				displayPrice = displayPrice / usdInrFloat
			}
			h.CurrentPrice = &displayPrice
			cv := (*instr.navPrice) * pos.units // always INR
			h.CurrentValue = &cv
			pl := cv - investedAmount
			h.ProfitLoss = &pl
			if investedAmount != 0 {
				plp := (pl / investedAmount) * 100
				h.ProfitLossPct = &plp
			}
		}
		out = append(out, h)
	}

	// Sort results.
	cv := func(h HoldingInfo) float64 {
		if h.CurrentValue != nil {
			return *h.CurrentValue
		}
		return 0
	}
	pl := func(h HoldingInfo) float64 {
		if h.ProfitLoss != nil {
			return *h.ProfitLoss
		}
		return 0
	}
	switch f.SortBy {
	case "name":
		sort.Slice(out, func(i, j int) bool { return out[i].InstrumentName < out[j].InstrumentName })
	case "invested":
		sort.Slice(out, func(i, j int) bool { return out[i].InvestedAmount > out[j].InvestedAmount })
	case "pl":
		sort.Slice(out, func(i, j int) bool { return pl(out[i]) > pl(out[j]) })
	default:
		sort.Slice(out, func(i, j int) bool { return cv(out[i]) > cv(out[j]) })
	}
	return out, nil
}

// --- Closed / Partial Positions ----------------------------------------------

type ClosedPositionInfo struct {
	InstrumentID     int64    `json:"instrument_id"`
	InstrumentName   string   `json:"instrument_name"`
	AssetType        string   `json:"asset_type"`
	Status           string   `json:"status"` // "CLOSED" or "PARTIAL"
	TotalUnitsBought float64  `json:"total_units_bought"`
	TotalUnitsSold   float64  `json:"total_units_sold"`
	UnitsHeld        float64  `json:"units_held"`
	AvgBuyPrice      float64  `json:"avg_buy_price"`  // FIFO cost of sold units / units sold
	AvgSellPrice     float64  `json:"avg_sell_price"` // sell proceeds / units sold
	TotalInvested    float64  `json:"total_invested"`  // FIFO cost basis of sold units
	TotalSoldValue   float64  `json:"total_sold_value"`
	RealizedPL       float64  `json:"realized_profit_loss"`
	ROIPct           float64  `json:"roi_percent"`
	ExitDate         *string  `json:"exit_date"`
	Platform         string   `json:"platform"`
	Categories       []string `json:"categories"`
}

func (c *PortfolioCalculator) ClosedPositions(ctx context.Context, f FilterParams) ([]ClosedPositionInfo, error) {
	usdINR, err := c.fx.USDToINR(ctx)
	if err != nil {
		return nil, fmt.Errorf("closed positions: %w", err)
	}
	usdInrFloat, _ := usdINR.Float64()

	// Walk all transactions in date order per instrument, tracking FIFO lots and
	// accumulating the cost basis + proceeds for every sell.
	rows, err := c.pool.Query(ctx, `
		SELECT t.instrument_id, t.transaction_type,
		       t.quantity::float, t.amount::float, t.platform,
		       t.transaction_date::text, i.asset_type, i.name, i.currency
		  FROM transactions t
		  JOIN instruments i ON i.id = t.instrument_id
		 WHERE t.transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		 ORDER BY t.instrument_id, t.transaction_date, t.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type realizedState struct {
		name         string
		assetType    string
		currency     string
		platform     string
		lots         []fifoLot // remaining buy lots (FIFO queue)
		unitsBought  float64
		unitsSold    float64
		costOfSold   float64 // FIFO cost basis of all sold units
		sellProceeds float64
		lastSellDate string
	}
	states := map[int64]*realizedState{}

	for rows.Next() {
		var instrID int64
		var txType, platform, txDate, assetType, name, currency string
		var qty, amount float64
		if err := rows.Scan(&instrID, &txType, &qty, &amount, &platform, &txDate, &assetType, &name, &currency); err != nil {
			return nil, err
		}
		s, ok := states[instrID]
		if !ok {
			s = &realizedState{name: name, assetType: assetType, currency: currency}
			states[instrID] = s
		}
		s.platform = platform

		switch txType {
		case "BUY", "SWITCH_IN":
			if qty <= 0 {
				continue
			}
			s.unitsBought += qty
			s.lots = append(s.lots, fifoLot{qty: qty, cost: amount / qty})

		case "BONUS":
			if qty > 0 {
				s.unitsBought += qty
				s.lots = append(s.lots, fifoLot{qty: qty, cost: 0})
			}

		case "SELL", "SWITCH_OUT":
			s.unitsSold += qty
			s.sellProceeds += amount
			s.lastSellDate = txDate
			// consume lots FIFO, accumulating cost basis of sold units
			remaining := qty
			for len(s.lots) > 0 && remaining > 0.000001 {
				if s.lots[0].qty <= remaining+0.000001 {
					s.costOfSold += s.lots[0].qty * s.lots[0].cost
					remaining -= s.lots[0].qty
					s.lots = s.lots[1:]
				} else {
					s.costOfSold += remaining * s.lots[0].cost
					s.lots[0].qty -= remaining
					remaining = 0
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch instrument names for the filter (already have them, but need asset_type for filter)
	var out []ClosedPositionInfo
	for instrID, s := range states {
		if s.unitsSold < 0.000001 {
			continue // never sold anything
		}
		if f.Platform != "" && !strings.EqualFold(s.platform, f.Platform) {
			continue
		}
		if f.AssetType != "" && !strings.EqualFold(s.assetType, f.AssetType) {
			continue
		}

		var unitsHeld, costBasisHeld float64
		for _, l := range s.lots {
			unitsHeld += l.qty
			costBasisHeld += l.qty * l.cost
		}

		// Treat as fully closed if the remaining cost basis is negligible (< ₹50) —
		// this handles SIP quantity rounding artifacts without a hard unit threshold.
		status := "CLOSED"
		if unitsHeld > 0.000001 && costBasisHeld >= 50 {
			status = "PARTIAL"
		}

		if strings.EqualFold(s.currency, "USD") {
			s.costOfSold *= usdInrFloat
			s.sellProceeds *= usdInrFloat
		}

		var avgBuy, avgSell float64
		if s.unitsSold > 0 {
			avgBuy = s.costOfSold / s.unitsSold
			avgSell = s.sellProceeds / s.unitsSold
		}

		cp := ClosedPositionInfo{
			InstrumentID:     instrID,
			InstrumentName:   s.name,
			AssetType:        s.assetType,
			Status:           status,
			TotalUnitsBought: s.unitsBought,
			TotalUnitsSold:   s.unitsSold,
			UnitsHeld:        unitsHeld,
			AvgBuyPrice:      avgBuy,
			AvgSellPrice:     avgSell,
			TotalInvested:    s.costOfSold,
			TotalSoldValue:   s.sellProceeds,
			Platform:         s.platform,
			Categories:       []string{},
		}
		if s.lastSellDate != "" {
			cp.ExitDate = &s.lastSellDate
		}
		cp.RealizedPL = cp.TotalSoldValue - cp.TotalInvested
		if cp.TotalInvested != 0 {
			cp.ROIPct = (cp.RealizedPL / cp.TotalInvested) * 100
		}
		out = append(out, cp)
	}

	// Sort: fully closed first, then by exit date desc
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == "CLOSED"
		}
		di, dj := "", ""
		if out[i].ExitDate != nil {
			di = *out[i].ExitDate
		}
		if out[j].ExitDate != nil {
			dj = *out[j].ExitDate
		}
		return di > dj
	})
	return out, nil
}

// --- Summary -----------------------------------------------------------------

type AllocationItem struct {
	Name         string  `json:"name"`
	Invested     float64 `json:"invested"`
	CurrentValue float64 `json:"current_value"`
	Count        int     `json:"count"`
	Percentage   float64 `json:"percentage"`
}

type PortfolioSummary struct {
	TotalInvested     float64          `json:"total_invested"`
	TotalCurrentValue float64          `json:"total_current_value"`
	TotalProfitLoss   float64          `json:"total_profit_loss"`
	ProfitLossPct     float64          `json:"profit_loss_percent"`
	HoldingsCount     int              `json:"holdings_count"`
	LastUpdated       *string          `json:"last_updated"`
	ByAssetType       []AllocationItem `json:"by_asset_type"`
	ByPlatform        []AllocationItem `json:"by_platform"`
}

func (c *PortfolioCalculator) Summary(ctx context.Context) (PortfolioSummary, error) {
	holdings, err := c.Holdings(ctx, FilterParams{})
	if err != nil {
		return PortfolioSummary{}, err
	}

	s := PortfolioSummary{
		ByAssetType: []AllocationItem{},
		ByPlatform:  []AllocationItem{},
	}
	s.HoldingsCount = len(holdings)
	byType := map[string]*AllocationItem{}
	byPlat := map[string]*AllocationItem{}

	var lastFetch time.Time

	for _, h := range holdings {
		// Only count instruments we have a price for — otherwise invested would be
		// counted without a corresponding current value, making P&L artificially negative.
		if h.CurrentValue == nil {
			continue
		}
		s.TotalInvested += h.InvestedAmount
		s.TotalCurrentValue += *h.CurrentValue

		ensureItem(byType, h.AssetType)
		byType[h.AssetType].Invested += h.InvestedAmount
		if h.CurrentValue != nil {
			byType[h.AssetType].CurrentValue += *h.CurrentValue
		}
		byType[h.AssetType].Count++

		pl := h.Platform
		if pl == "" {
			pl = "MANUAL"
		}
		ensureItem(byPlat, pl)
		byPlat[pl].Invested += h.InvestedAmount
		if h.CurrentValue != nil {
			byPlat[pl].CurrentValue += *h.CurrentValue
		}
		byPlat[pl].Count++

		if h.LastFetchedAt != nil {
			if t, err := time.Parse(time.RFC3339, *h.LastFetchedAt); err == nil && t.After(lastFetch) {
				lastFetch = t
			}
		}
	}

	s.TotalProfitLoss = s.TotalCurrentValue - s.TotalInvested
	ref := s.TotalCurrentValue
	if ref == 0 {
		ref = s.TotalInvested
	}
	if s.TotalInvested != 0 {
		s.ProfitLossPct = (s.TotalProfitLoss / s.TotalInvested) * 100
	}

	for name, item := range byType {
		item.Name = name
		if ref > 0 {
			item.Percentage = (item.CurrentValue / ref) * 100
		}
		s.ByAssetType = append(s.ByAssetType, *item)
	}
	for name, item := range byPlat {
		item.Name = name
		if ref > 0 {
			item.Percentage = (item.CurrentValue / ref) * 100
		}
		s.ByPlatform = append(s.ByPlatform, *item)
	}

	if !lastFetch.IsZero() {
		str := lastFetch.UTC().Format(time.RFC3339)
		s.LastUpdated = &str
	}
	return s, nil
}

func ensureItem(m map[string]*AllocationItem, key string) {
	if _, ok := m[key]; !ok {
		m[key] = &AllocationItem{}
	}
}

func argN(n int) string {
	const digits = "0123456789"
	if n < 10 {
		return string(digits[n])
	}
	s := ""
	for n > 0 {
		s = string(digits[n%10]) + s
		n /= 10
	}
	return s
}
