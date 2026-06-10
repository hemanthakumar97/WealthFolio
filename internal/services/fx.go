package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

const fxCacheKey = "fx:USD:INR"
const fxCacheTTL = 24 * time.Hour
const frankfurterURL = "https://api.frankfurter.app/latest?from=USD&to=INR"

// FXService provides USD→INR conversion, caching the rate in market_cache.
type FXService struct {
	pool   *pgxpool.Pool
	client *http.Client
}

func NewFXService(pool *pgxpool.Pool) *FXService {
	return &FXService{
		pool:   pool,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// USDToINR returns the current USD→INR exchange rate.
// Reads from cache first; fetches from Frankfurter API on miss/expiry.
func (f *FXService) USDToINR(ctx context.Context) (decimal.Decimal, error) {
	if rate, ok := f.fromCache(ctx); ok {
		return rate, nil
	}
	rate, err := f.fetchFrankfurter(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("fx: failed to fetch USD/INR rate: %w", err)
	}
	f.toCache(ctx, rate)
	return rate, nil
}

func (f *FXService) fromCache(ctx context.Context) (decimal.Decimal, bool) {
	var raw []byte
	var expiresAt time.Time
	err := f.pool.QueryRow(ctx,
		`SELECT cache_value, expires_at FROM market_cache WHERE cache_key = $1`,
		fxCacheKey,
	).Scan(&raw, &expiresAt)
	if err != nil || time.Now().After(expiresAt) {
		return decimal.Zero, false
	}
	var obj struct{ Rate string `json:"rate"` }
	if err := json.Unmarshal(raw, &obj); err != nil {
		return decimal.Zero, false
	}
	rate, err := decimal.NewFromString(obj.Rate)
	if err != nil || rate.IsZero() {
		return decimal.Zero, false
	}
	return rate, true
}

func (f *FXService) toCache(ctx context.Context, rate decimal.Decimal) {
	raw, _ := json.Marshal(map[string]string{"rate": rate.String()})
	expires := time.Now().Add(fxCacheTTL)
	_, _ = f.pool.Exec(ctx,
		`INSERT INTO market_cache (cache_key, cache_value, expires_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (cache_key) DO UPDATE
		   SET cache_value = EXCLUDED.cache_value,
		       expires_at  = EXCLUDED.expires_at,
		       updated_at  = NOW()`,
		fxCacheKey, raw, expires,
	)
}

// RateForDate returns the USD→INR rate for a specific date.
// Checks fx_rates table first; fetches from Frankfurter historical API on miss and persists it.
func (f *FXService) RateForDate(ctx context.Context, date time.Time) (decimal.Decimal, error) {
	dateStr := date.Format("2006-01-02")
	var rate decimal.Decimal
	err := f.pool.QueryRow(ctx,
		`SELECT usd_to_inr FROM fx_rates WHERE rate_date = $1`, dateStr,
	).Scan(&rate)
	if err == nil {
		return rate, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, fmt.Errorf("fx_rates lookup: %w", err)
	}
	rate, err = f.fetchFrankfurterDate(ctx, dateStr)
	if err != nil {
		return decimal.Zero, err
	}
	_, _ = f.pool.Exec(ctx,
		`INSERT INTO fx_rates (rate_date, usd_to_inr, source)
		 VALUES ($1, $2, 'frankfurter')
		 ON CONFLICT (rate_date) DO NOTHING`,
		dateStr, rate,
	)
	return rate, nil
}

func (f *FXService) fetchFrankfurterDate(ctx context.Context, dateStr string) (decimal.Decimal, error) {
	url := "https://api.frankfurter.app/" + dateStr + "?from=USD&to=INR"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return decimal.Zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, fmt.Errorf("frankfurter: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return decimal.Zero, err
	}
	inr, ok := body.Rates["INR"]
	if !ok || inr == 0 {
		return decimal.Zero, fmt.Errorf("INR rate missing in response")
	}
	return decimal.NewFromFloat(inr), nil
}

func (f *FXService) fetchFrankfurter(ctx context.Context) (decimal.Decimal, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, frankfurterURL, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return decimal.Zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, fmt.Errorf("frankfurter: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return decimal.Zero, err
	}
	inr, ok := body.Rates["INR"]
	if !ok || inr == 0 {
		return decimal.Zero, fmt.Errorf("INR rate missing in response")
	}
	return decimal.NewFromFloat(inr), nil
}
