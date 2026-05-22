package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
		// Return a hardcoded fallback so price fetching doesn't break when offline.
		return decimal.NewFromFloat(84.0), nil
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
