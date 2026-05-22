package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/xirr"
)

// SnapshotService creates and backfills daily portfolio snapshots.
type SnapshotService struct {
	pool *pgxpool.Pool
	calc *PortfolioCalculator
	fx   *FXService
}

func NewSnapshotService(pool *pgxpool.Pool, calc *PortfolioCalculator, fx *FXService) *SnapshotService {
	return &SnapshotService{pool: pool, calc: calc, fx: fx}
}

type SnapshotRow struct {
	Date          string  `json:"date"`
	Invested      float64 `json:"invested"`
	Value         float64 `json:"value"`
	Profit        float64 `json:"profit"`
	ProfitPercent float64 `json:"profit_percent"`
}

// CreateDailySnapshot computes today's portfolio value and writes/replaces the snapshot row.
func (s *SnapshotService) CreateDailySnapshot(ctx context.Context) (*SnapshotRow, error) {
	summary, err := s.calc.Summary(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute summary: %w", err)
	}

	today := time.Now().In(ist()).Truncate(24 * time.Hour)
	details, _ := json.Marshal(map[string]any{
		"by_asset_type": summary.ByAssetType,
		"by_platform":   summary.ByPlatform,
	})

	profit := summary.TotalCurrentValue - summary.TotalInvested
	pct := 0.0
	if summary.TotalInvested != 0 {
		pct = (profit / summary.TotalInvested) * 100
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO portfolio_snapshots
		   (snapshot_date, total_invested, total_value, total_profit, profit_percentage, snapshot_details)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (snapshot_date) DO UPDATE
		   SET total_invested    = EXCLUDED.total_invested,
		       total_value       = EXCLUDED.total_value,
		       total_profit      = EXCLUDED.total_profit,
		       profit_percentage = EXCLUDED.profit_percentage,
		       snapshot_details  = EXCLUDED.snapshot_details`,
		today, summary.TotalInvested, summary.TotalCurrentValue, profit, pct, details,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert snapshot: %w", err)
	}

	return &SnapshotRow{
		Date:          today.Format("2006-01-02"),
		Invested:      summary.TotalInvested,
		Value:         summary.TotalCurrentValue,
		Profit:        profit,
		ProfitPercent: pct,
	}, nil
}

// BackfillSnapshots re-creates one snapshot per day from the earliest transaction date
// to today, using price forward-fill for days without a direct price record.
// It is intentionally simple: iterate day by day, compute value from prices available
// on that day or earlier, insert/replace snapshot.
func (s *SnapshotService) BackfillSnapshots(ctx context.Context) (int, error) {
	// Find the first transaction date.
	var firstDateStr *string
	err := s.pool.QueryRow(ctx,
		`SELECT MIN(transaction_date)::text FROM transactions`,
	).Scan(&firstDateStr)
	if err != nil || firstDateStr == nil {
		return 0, nil
	}
	firstDate, err := time.Parse("2006-01-02", *firstDateStr)
	if err != nil {
		return 0, err
	}

	today := time.Now().In(ist()).Truncate(24 * time.Hour)
	created := 0

	for d := firstDate; !d.After(today); d = d.AddDate(0, 0, 1) {
		rows, err := s.computeForDate(ctx, d)
		if err != nil {
			slog.Warn("backfill: compute for date", "date", d.Format("2006-01-02"), "err", err)
			continue
		}

		var totalInvested, totalValue float64
		for _, r := range rows {
			totalInvested += r.Invested
			totalValue += r.Value
		}
		profit := totalValue - totalInvested
		pct := 0.0
		if totalInvested != 0 {
			pct = (profit / totalInvested) * 100
		}

		_, err = s.pool.Exec(ctx,
			`INSERT INTO portfolio_snapshots
			   (snapshot_date, total_invested, total_value, total_profit, profit_percentage)
			 VALUES ($1,$2,$3,$4,$5)
			 ON CONFLICT (snapshot_date) DO UPDATE
			   SET total_invested    = EXCLUDED.total_invested,
			       total_value       = EXCLUDED.total_value,
			       total_profit      = EXCLUDED.total_profit,
			       profit_percentage = EXCLUDED.profit_percentage`,
			d, totalInvested, totalValue, profit, pct,
		)
		if err != nil {
			slog.Warn("backfill: upsert portfolio_snapshot", "date", d.Format("2006-01-02"), "err", err)
		} else {
			created++
		}

		// Bulk-insert per-instrument snapshots.
		if len(rows) > 0 {
			ids := make([]int64, len(rows))
			invs := make([]float64, len(rows))
			vals := make([]float64, len(rows))
			profs := make([]float64, len(rows))
			pcts := make([]float64, len(rows))
			for i, r := range rows {
				ids[i] = r.InstrumentID
				invs[i] = r.Invested
				vals[i] = r.Value
				profs[i] = r.Value - r.Invested
				if r.Invested != 0 {
					pcts[i] = ((r.Value - r.Invested) / r.Invested) * 100
				}
			}
			_, err = s.pool.Exec(ctx, `
				INSERT INTO instrument_snapshots (snapshot_date, instrument_id, invested, value, profit, profit_percent)
				SELECT $1, unnest($2::bigint[]), unnest($3::numeric[]), unnest($4::numeric[]), unnest($5::numeric[]), unnest($6::numeric[])
				ON CONFLICT (snapshot_date, instrument_id) DO UPDATE
				  SET invested       = EXCLUDED.invested,
				      value          = EXCLUDED.value,
				      profit         = EXCLUDED.profit,
				      profit_percent = EXCLUDED.profit_percent`,
				d, ids, invs, vals, profs, pcts,
			)
			if err != nil {
				slog.Warn("backfill: upsert instrument_snapshots", "date", d.Format("2006-01-02"), "err", err)
			}
		}
	}
	return created, nil
}

type instrDay struct {
	InstrumentID int64
	Invested     float64
	Value        float64
}

// computeForDate returns per-instrument (invested, value) as of date,
// only for instruments that have a price on or before that date.
func (s *SnapshotService) computeForDate(ctx context.Context, date time.Time) ([]instrDay, error) {
	usdINR, _ := s.fx.USDToINR(ctx)
	usdInrFloat, _ := usdINR.Float64()
	if usdInrFloat == 0 {
		usdInrFloat = 84.0
	}

	rows, err := s.pool.Query(ctx, `
		WITH tx_agg AS (
		  SELECT
		    t.instrument_id,
		    SUM(CASE WHEN t.transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN t.quantity::float ELSE 0 END) AS units_bought,
		    SUM(CASE WHEN t.transaction_type IN ('SELL','SWITCH_OUT')        THEN t.quantity::float ELSE 0 END) AS units_sold,
		    SUM(CASE WHEN t.transaction_type IN ('BUY','SWITCH_IN')          THEN t.amount::float   ELSE 0 END) AS total_invested,
		    SUM(CASE WHEN t.transaction_type IN ('BUY','SWITCH_IN')          THEN t.quantity::float ELSE 0 END) AS buy_qty
		  FROM transactions t
		  WHERE t.transaction_date <= $1
		  GROUP BY t.instrument_id
		),
		last_price AS (
		  SELECT DISTINCT ON (instrument_id)
		    instrument_id, nav_price::float
		  FROM prices
		  WHERE price_date <= $1
		  ORDER BY instrument_id, price_date DESC
		)
		SELECT
		  ta.instrument_id,
		  CASE WHEN ta.buy_qty > 0 THEN (ta.total_invested / ta.buy_qty) * (ta.units_bought - ta.units_sold) ELSE 0 END as invested,
		  lp.nav_price * (ta.units_bought - ta.units_sold) as value,
		  i.currency
		FROM tx_agg ta
		INNER JOIN last_price lp ON lp.instrument_id = ta.instrument_id
		INNER JOIN instruments i ON i.id = ta.instrument_id
		WHERE (ta.units_bought - ta.units_sold) > 0.000001
	`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []instrDay
	for rows.Next() {
		var r instrDay
		var currency string
		if err := rows.Scan(&r.InstrumentID, &r.Invested, &r.Value, &currency); err != nil {
			return nil, err
		}
		if strings.EqualFold(currency, "USD") {
			r.Invested *= usdInrFloat
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *SnapshotService) AllocationHistory(ctx context.Context, days int) ([]map[string]any, error) {
	sqlQ := `
		SELECT s.snapshot_date::text as date,
		       i.asset_type,
		       SUM(s.value)::float as value
		  FROM instrument_snapshots s
		  JOIN instruments i ON i.id = s.instrument_id
	`
	var args []any
	if days > 0 {
		sqlQ += ` WHERE s.snapshot_date >= NOW() - ($1::int || ' days')::interval `
		args = append(args, days)
	}
	sqlQ += ` GROUP BY 1, 2 ORDER BY 1 ASC `

	rows, err := s.pool.Query(ctx, sqlQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Pivot data: rows (date, type, val) -> map[date] { date: date, Type1: val, Type2: val }
	type dateMap map[string]any
	pivoted := make(map[string]dateMap)
	dates := []string{}

	for rows.Next() {
		var date, assetType string
		var val float64
		if err := rows.Scan(&date, &assetType, &val); err != nil {
			return nil, err
		}
		if _, ok := pivoted[date]; !ok {
			pivoted[date] = dateMap{"date": date}
			dates = append(dates, date)
		}
		pivoted[date][assetType] = val
	}

	out := make([]map[string]any, 0, len(dates))
	for _, d := range dates {
		out = append(out, pivoted[d])
	}
	return out, nil
}

// XIRRFromTransactions computes XIRR using all BUY/SELL transactions plus the
// current portfolio value as the final positive cash flow.
func XIRRFromTransactions(ctx context.Context, pool *pgxpool.Pool, currentValue float64) (float64, error) {
	rows, err := pool.Query(ctx,
		`SELECT transaction_date::text, transaction_type, amount::float
		   FROM transactions
		  WHERE transaction_type IN ('BUY','SELL','SWITCH_IN','SWITCH_OUT')
		  ORDER BY transaction_date`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var flows []xirr.CashFlow
	for rows.Next() {
		var dateStr, txType string
		var amount float64
		if err := rows.Scan(&dateStr, &txType, &amount); err != nil {
			continue
		}
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		switch txType {
		case "BUY", "SWITCH_IN":
			flows = append(flows, xirr.CashFlow{Date: d, Amount: -amount})
		case "SELL", "SWITCH_OUT":
			flows = append(flows, xirr.CashFlow{Date: d, Amount: amount})
		}
	}
	if len(flows) == 0 || currentValue == 0 {
		return 0, nil
	}
	// Current value as final inflow.
	flows = append(flows, xirr.CashFlow{Date: time.Now(), Amount: currentValue})

	rate, err := xirr.Calculate(flows)
	if err != nil {
		return 0, nil // Non-convergence → return 0, not an error to surface
	}
	return rate * 100, nil // Return as percentage
}

func ist() *time.Location {
	return time.FixedZone("IST", 5*3600+30*60)
}
