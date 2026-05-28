package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// PriceFetcher coordinates fetching live prices for all instruments using Yahoo Finance or MFAPI.
type PriceFetcher struct {
	pool *pgxpool.Pool
	fx   *FXService
}

func NewPriceFetcher(pool *pgxpool.Pool, fx *FXService) *PriceFetcher {
	return &PriceFetcher{pool: pool, fx: fx}
}

type FetchResult struct {
	Fetched int
	Failed  int
}

type instrumentStub struct {
	ID          int64
	AMFICode    string
	YahooSymbol string
	Currency    string
}

// FetchAll fetches live prices for all instruments using whichever source is configured:
//   - amfi_code    → MFAPI latest NAV
//   - yahoo_symbol → Yahoo Finance latest close
func (pf *PriceFetcher) FetchAll(ctx context.Context) (FetchResult, error) {
	instruments, err := pf.loadInstruments(ctx)
	if err != nil {
		return FetchResult{}, err
	}
	if len(instruments) == 0 {
		return FetchResult{}, nil
	}

	usdINR, err := pf.fx.USDToINR(ctx)
	if err != nil {
		return FetchResult{}, fmt.Errorf("price fetch: %w", err)
	}

	mfapiClient := NewMFAPIClient()
	yahooClient := NewYahooClient()

	var (
		mu     sync.Mutex
		result FetchResult
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for _, instr := range instruments {
		instr := instr
		g.Go(func() error {
			price, source, err := pf.fetchLatestPrice(gctx, instr, mfapiClient, yahooClient, usdINR)
			if err != nil {
				slog.Warn("price fetch failed", "instrument_id", instr.ID, "err", err)
				mu.Lock(); result.Failed++; mu.Unlock()
				return nil
			}
			if price.IsZero() {
				return nil
			}
			isConverted := strings.EqualFold(instr.Currency, "USD") && source != "MFAPI"
			if err := pf.upsertPriceWithSource(gctx, instr.ID, price, source, isConverted); err != nil {
				slog.Warn("price upsert failed", "instrument_id", instr.ID, "err", err)
				mu.Lock(); result.Failed++; mu.Unlock()
				return nil
			}
			mu.Lock(); result.Fetched++; mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return result, err
	}
	return result, nil
}

func (pf *PriceFetcher) fetchLatestPrice(
	ctx context.Context,
	instr instrumentStub,
	mfapi *MFAPIClient,
	yahoo *YahooClient,
	usdINR decimal.Decimal,
) (decimal.Decimal, string, error) {
	if instr.AMFICode != "" {
		rows, err := mfapi.FetchHistory(ctx, instr.AMFICode)
		if err != nil || len(rows) == 0 {
			return decimal.Zero, "", err
		}
		// MFAPI returns newest first — take the first row.
		nav := decimal.NewFromFloat(rows[0].Close)
		return nav, "MFAPI", nil
	}

	if instr.YahooSymbol != "" {
		rows, err := yahoo.FetchHistory(ctx, instr.YahooSymbol)
		if err != nil || len(rows) == 0 {
			return decimal.Zero, "", err
		}
		// Yahoo returns chronological — last row is the most recent.
		price := decimal.NewFromFloat(rows[len(rows)-1].Close)
		if strings.EqualFold(instr.Currency, "USD") {
			price = price.Mul(usdINR).Round(4)
		}
		return price, "YAHOO", nil
	}

	return decimal.Zero, "", nil
}

// FetchForInstrument fetches a live price for one instrument using whichever source is set.
func (pf *PriceFetcher) FetchForInstrument(ctx context.Context, instrumentID int64) (decimal.Decimal, error) {
	var instr instrumentStub
	instr.ID = instrumentID
	err := pf.pool.QueryRow(ctx,
		`SELECT COALESCE(amfi_code,''), COALESCE(yahoo_symbol,''), currency
		   FROM instruments WHERE id = $1`,
		instrumentID,
	).Scan(&instr.AMFICode, &instr.YahooSymbol, &instr.Currency)
	if err != nil {
		return decimal.Zero, err
	}

	usdINR, err := pf.fx.USDToINR(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("price fetch single: %w", err)
	}

	price, source, err := pf.fetchLatestPrice(ctx, instr, NewMFAPIClient(), NewYahooClient(), usdINR)
	if err != nil || price.IsZero() {
		return decimal.Zero, err
	}

	isConverted := strings.EqualFold(instr.Currency, "USD") && source != "MFAPI"
	if err := pf.upsertPriceWithSource(ctx, instrumentID, price, source, isConverted); err != nil {
		return decimal.Zero, err
	}
	return price, nil
}

// LatestPrice returns the most recent price row for an instrument.
func (pf *PriceFetcher) LatestPrice(ctx context.Context, instrumentID int64) (decimal.Decimal, time.Time, error) {
	var price decimal.Decimal
	var pdate time.Time
	err := pf.pool.QueryRow(ctx,
		`SELECT nav_price, price_date FROM prices
		  WHERE instrument_id = $1
		  ORDER BY price_date DESC LIMIT 1`,
		instrumentID,
	).Scan(&price, &pdate)
	return price, pdate, err
}

// PriceHistory returns all price rows for an instrument, newest first.
func (pf *PriceFetcher) PriceHistory(ctx context.Context, instrumentID int64, limit int) ([]PriceRow, error) {
	if limit <= 0 {
		limit = 365
	}
	rows, err := pf.pool.Query(ctx,
		`SELECT price_date, nav_price, source, is_converted, fetched_at
		   FROM prices
		  WHERE instrument_id = $1
		  ORDER BY price_date DESC
		  LIMIT $2`,
		instrumentID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceRow
	for rows.Next() {
		var p PriceRow
		if err := rows.Scan(&p.PriceDate, &p.NavPrice, &p.Source, &p.IsConverted, &p.FetchedAt); err != nil {
			return nil, err
		}
		p.InstrumentID = instrumentID
		out = append(out, p)
	}
	return out, nil
}

type PriceRow struct {
	InstrumentID int64           `json:"instrument_id"`
	PriceDate    time.Time       `json:"price_date"`
	NavPrice     decimal.Decimal `json:"nav_price"`
	Source       string          `json:"source"`
	IsConverted  bool            `json:"is_converted"`
	FetchedAt    time.Time       `json:"fetched_at"`
}

func (pf *PriceFetcher) upsertPriceWithSource(ctx context.Context, instrumentID int64, price decimal.Decimal, source string, isConverted bool) error {
	today := time.Now().In(time.FixedZone("IST", 5*3600+30*60)).Truncate(24 * time.Hour)
	_, err := pf.pool.Exec(ctx,
		`INSERT INTO prices (instrument_id, price_date, nav_price, source, is_converted)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (instrument_id, price_date) DO UPDATE
		   SET nav_price    = EXCLUDED.nav_price,
		       source       = EXCLUDED.source,
		       is_converted = EXCLUDED.is_converted,
		       fetched_at   = NOW()`,
		instrumentID, today, price, source, isConverted,
	)
	return err
}

func (pf *PriceFetcher) loadInstruments(ctx context.Context) ([]instrumentStub, error) {
	rows, err := pf.pool.Query(ctx,
		`SELECT id,
		        COALESCE(amfi_code, ''),
		        COALESCE(yahoo_symbol, ''),
		        currency
		   FROM instruments
		  WHERE amfi_code    IS NOT NULL AND amfi_code != ''
		     OR yahoo_symbol IS NOT NULL AND yahoo_symbol != ''`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []instrumentStub
	for rows.Next() {
		var i instrumentStub
		if err := rows.Scan(&i.ID, &i.AMFICode, &i.YahooSymbol, &i.Currency); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}
