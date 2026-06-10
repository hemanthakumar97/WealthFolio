package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

type InstrumentService struct {
	pool *pgxpool.Pool
}

func NewInstrumentService(pool *pgxpool.Pool) *InstrumentService {
	return &InstrumentService{pool: pool}
}

type InstrumentSpec struct {
	Name      string
	ISIN      string
	AMFICode  string
	AssetType string
	Currency  string
	Exchange  string
}

// FindOrCreate looks up an instrument by ISIN (preferred) or by (name, asset_type), creating it
// if missing. Returns the ID and whether a new row was created.
func (s *InstrumentService) FindOrCreate(ctx context.Context, spec InstrumentSpec) (int64, bool, error) {
	if spec.AssetType == "" {
		spec.AssetType = domain.AssetTypeOther
	}
	if spec.Currency == "" {
		spec.Currency = domain.CurrencyINR
	}
	name := strings.TrimSpace(spec.Name)
	isin := strings.ToUpper(strings.TrimSpace(spec.ISIN))

	if isin != "" {
		var id int64
		err := s.pool.QueryRow(ctx,
			`SELECT id FROM instruments WHERE isin = $1`, isin,
		).Scan(&id)
		if err == nil {
			return id, false, s.maybePatch(ctx, id, spec)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, fmt.Errorf("lookup isin: %w", err)
		}
	}

	if name == "" {
		return 0, false, errors.New("instrument needs a name or ISIN")
	}
	// Fallback: by name + asset_type (exact, case-insensitive).
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM instruments WHERE LOWER(name) = LOWER($1) AND asset_type = $2 LIMIT 1`,
		name, spec.AssetType,
	).Scan(&id)
	if err == nil {
		return id, false, s.maybePatch(ctx, id, spec)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("lookup name: %w", err)
	}

	// US stocks/funds: match by ticker stored in yahoo_symbol (e.g. "TQQQ" → "ProShares UltraPro QQQ").
	if spec.AssetType == domain.AssetTypeUSFund || spec.AssetType == domain.AssetTypeStock {
		err = s.pool.QueryRow(ctx,
			`SELECT id FROM instruments WHERE LOWER(yahoo_symbol) = LOWER($1) AND asset_type = $2 LIMIT 1`,
			name, spec.AssetType,
		).Scan(&id)
		if err == nil {
			slog.Info("instrument: ticker→yahoo_symbol match", "ticker", name, "id", id)
			return id, false, s.maybePatch(ctx, id, spec)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, fmt.Errorf("lookup yahoo_symbol: %w", err)
		}
	}

	// MF: fuzzy name match via pg_trgm — catches renamed funds (e.g. fund house renames).
	// Uses a conservative threshold; logs every match so regressions can be spotted.
	if spec.AssetType == domain.AssetTypeMF {
		err = s.pool.QueryRow(ctx,
			`SELECT id FROM instruments
			  WHERE asset_type = $2
			    AND similarity(LOWER(name), LOWER($1)) > 0.55
			  ORDER BY similarity(LOWER(name), LOWER($1)) DESC
			  LIMIT 1`,
			name, spec.AssetType,
		).Scan(&id)
		if err == nil {
			slog.Info("instrument: fuzzy MF name match", "query_name", name, "matched_id", id)
			return id, false, s.maybePatch(ctx, id, spec)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, fmt.Errorf("lookup similarity: %w", err)
		}
	}

	// Create new instrument.
	yahooSymbol := tickerSymbol(name, spec.AssetType)
	err = s.pool.QueryRow(ctx,
		`INSERT INTO instruments (name, isin, amfi_code, asset_type, currency, exchange, yahoo_symbol)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		name,
		nullable(isin),
		nullable(spec.AMFICode),
		spec.AssetType,
		spec.Currency,
		nullable(spec.Exchange),
		nullable(yahooSymbol),
	).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("insert instrument: %w", err)
	}
	slog.Info("instrument: created new", "name", name, "asset_type", spec.AssetType, "id", id)
	return id, true, nil
}

// tickerSymbol returns the name itself as a ticker when it looks like one (short,
// uppercase, no spaces) for US_FUND/STOCK assets — blank otherwise.
func tickerSymbol(name, assetType string) string {
	if assetType != domain.AssetTypeUSFund && assetType != domain.AssetTypeStock {
		return ""
	}
	trimmed := strings.TrimSpace(name)
	if len(trimmed) > 10 || strings.Contains(trimmed, " ") {
		return ""
	}
	for _, r := range trimmed {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return trimmed
}

// maybePatch fills in optional fields (AMFI / exchange / yahoo_symbol) when the existing row
// is missing them, and upgrades currency from INR→USD when the incoming spec knows better.
func (s *InstrumentService) maybePatch(ctx context.Context, id int64, spec InstrumentSpec) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instruments
		   SET amfi_code    = COALESCE(NULLIF(amfi_code, ''), $2),
		       exchange     = COALESCE(NULLIF(exchange, ''), $3),
		       currency     = CASE WHEN $4 = 'USD' THEN 'USD' ELSE currency END,
		       yahoo_symbol = COALESCE(NULLIF(yahoo_symbol, ''), $5),
		       updated_at   = NOW()
		 WHERE id = $1`,
		id, nullable(spec.AMFICode), nullable(spec.Exchange), spec.Currency,
		nullable(tickerSymbol(spec.Name, spec.AssetType)),
	)
	return err
}

func nullable(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}
