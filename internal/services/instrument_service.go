package services

import (
	"context"
	"errors"
	"fmt"
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
	// Fallback: by name + asset_type.
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

	// Create.
	err = s.pool.QueryRow(ctx,
		`INSERT INTO instruments (name, isin, amfi_code, asset_type, currency, exchange)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		name,
		nullable(isin),
		nullable(spec.AMFICode),
		spec.AssetType,
		spec.Currency,
		nullable(spec.Exchange),
	).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("insert instrument: %w", err)
	}
	return id, true, nil
}

// maybePatch fills in optional fields (AMFI / exchange) when the existing row is missing them,
// and upgrades currency from INR→USD when the incoming spec knows better.
func (s *InstrumentService) maybePatch(ctx context.Context, id int64, spec InstrumentSpec) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instruments
		   SET amfi_code  = COALESCE(NULLIF(amfi_code, ''), $2),
		       exchange   = COALESCE(NULLIF(exchange, ''), $3),
		       currency   = CASE WHEN $4 = 'USD' THEN 'USD' ELSE currency END,
		       updated_at = NOW()
		 WHERE id = $1`,
		id, nullable(spec.AMFICode), nullable(spec.Exchange), spec.Currency,
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
