package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type DuplicateDetector struct {
	pool *pgxpool.Pool
}

func NewDuplicateDetector(pool *pgxpool.Pool) *DuplicateDetector {
	return &DuplicateDetector{pool: pool}
}

type DupCheck struct {
	InstrumentID    int64
	TransactionDate time.Time
	TransactionType string
	Quantity        decimal.Decimal
	Price           decimal.Decimal
	Platform        string
	TradeID         string // preferred: unique per trade execution (e.g. Zerodha trade_id)
	OrderID         string // fallback: broker order id (shared across partial fills)
}

// IsDuplicate returns true if a transaction with effectively identical key fields already exists.
//
// Match rules:
//   - If trade_id is provided: match on (instrument, trade_id, type) — trade_id is unique per
//     execution so partial fills of the same order are correctly treated as distinct transactions.
//   - Else if order_id is provided: match on (instrument, order_id, type).
//   - Otherwise: exact match on (instrument, date, type, platform, qty, price).
func (d *DuplicateDetector) IsDuplicate(ctx context.Context, c DupCheck) (bool, int64, error) {
	if c.TradeID != "" {
		var id int64
		err := d.pool.QueryRow(ctx,
			`SELECT id FROM transactions
			  WHERE instrument_id = $1 AND trade_id = $2 AND transaction_type = $3
			  LIMIT 1`,
			c.InstrumentID, c.TradeID, c.TransactionType,
		).Scan(&id)
		if err == nil {
			return true, id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, 0, fmt.Errorf("dup trade_id check: %w", err)
		}
		return false, 0, nil
	}

	if c.OrderID != "" {
		var id int64
		err := d.pool.QueryRow(ctx,
			`SELECT id FROM transactions
			  WHERE instrument_id = $1 AND order_id = $2 AND transaction_type = $3
			  LIMIT 1`,
			c.InstrumentID, c.OrderID, c.TransactionType,
		).Scan(&id)
		if err == nil {
			return true, id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, 0, fmt.Errorf("dup order_id check: %w", err)
		}
	}

	var id int64
	err := d.pool.QueryRow(ctx,
		`SELECT id FROM transactions
		  WHERE instrument_id = $1
		    AND transaction_date = $2
		    AND transaction_type = $3
		    AND platform = $4
		    AND ABS(quantity - $5) < 0.0001
		    AND ABS(price - $6) < 0.01
		  LIMIT 1`,
		c.InstrumentID, c.TransactionDate, c.TransactionType, c.Platform,
		c.Quantity, c.Price,
	).Scan(&id)
	if err == nil {
		return true, id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, 0, fmt.Errorf("dup exact check: %w", err)
	}
	return false, 0, nil
}
