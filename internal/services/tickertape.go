package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const mmiCacheKey = "market:mmi"
const mmiCacheTTL = 3 * time.Hour

// MMIData holds the parsed Tickertape Market Mood Index value.
type MMIData struct {
	Value float64 `json:"value"`
	Mood  string  `json:"mood"`
}

// CachedMMI is the envelope stored in market_cache.
type CachedMMI struct {
	Data      MMIData   `json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TickertapeService scrapes Tickertape for the Market Mood Index.
type TickertapeService struct {
	pool   *pgxpool.Pool
	client *http.Client
}

func NewTickertapeService(pool *pgxpool.Pool) *TickertapeService {
	return &TickertapeService{
		pool:   pool,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// GetMMI returns the cached MMI data, refreshing if stale.
func (t *TickertapeService) GetMMI(ctx context.Context) (*CachedMMI, error) {
	if cached, ok := t.fromCache(ctx); ok {
		return cached, nil
	}
	return t.refresh(ctx)
}

// Refresh forces a fetch from Tickertape and updates the cache.
func (t *TickertapeService) Refresh(ctx context.Context) (*CachedMMI, error) {
	return t.refresh(ctx)
}

func (t *TickertapeService) refresh(ctx context.Context) (*CachedMMI, error) {
	data, err := t.fetchMMI(ctx)
	if err != nil {
		// Return stale cache if available rather than failing.
		if cached, ok := t.fromCacheAny(ctx); ok {
			return cached, nil
		}
		return nil, err
	}
	result := &CachedMMI{Data: *data, UpdatedAt: time.Now()}
	t.toCache(ctx, result)
	return result, nil
}

// fetchMMI calls https://api.tickertape.in/mmi/now with the Origin/Referer
// headers that the Tickertape web app sends, matching the old working behavior.
func (t *TickertapeService) fetchMMI(ctx context.Context) (*MMIData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.tickertape.in/mmi/now", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://www.tickertape.in")
	req.Header.Set("Referer", "https://www.tickertape.in/market-mood-index")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tickertape fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tickertape: status %d", resp.StatusCode)
	}

	var raw struct {
		Success bool `json:"success"`
		Data    struct {
			CurrentValue float64 `json:"currentValue"`
			Indicator    float64 `json:"indicator"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("tickertape decode: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("tickertape: success=false")
	}

	value := raw.Data.CurrentValue
	if value == 0 {
		value = raw.Data.Indicator
	}
	if value == 0 {
		return nil, fmt.Errorf("tickertape: MMI value missing")
	}

	return &MMIData{Value: value, Mood: classifyMMI(value)}, nil
}

func classifyMMI(v float64) string {
	switch {
	case v >= 75:
		return "Extreme Greed"
	case v >= 55:
		return "Greed"
	case v >= 45:
		return "Neutral"
	case v >= 25:
		return "Fear"
	default:
		return "Extreme Fear"
	}
}

func (t *TickertapeService) fromCache(ctx context.Context) (*CachedMMI, bool) {
	var raw []byte
	err := t.pool.QueryRow(ctx,
		`SELECT cache_value FROM market_cache
		  WHERE cache_key = $1 AND expires_at > NOW()`,
		mmiCacheKey,
	).Scan(&raw)
	if err != nil {
		return nil, false
	}
	var c CachedMMI
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, false
	}
	return &c, true
}

// fromCacheAny reads stale cache (ignores expiry).
func (t *TickertapeService) fromCacheAny(ctx context.Context) (*CachedMMI, bool) {
	var raw []byte
	err := t.pool.QueryRow(ctx,
		`SELECT cache_value FROM market_cache WHERE cache_key = $1`,
		mmiCacheKey,
	).Scan(&raw)
	if err != nil {
		return nil, false
	}
	var c CachedMMI
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, false
	}
	return &c, true
}

func (t *TickertapeService) toCache(ctx context.Context, c *CachedMMI) {
	raw, _ := json.Marshal(c)
	_, _ = t.pool.Exec(ctx,
		`INSERT INTO market_cache (cache_key, cache_value, expires_at, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (cache_key) DO UPDATE SET
		     cache_value = EXCLUDED.cache_value,
		     expires_at  = EXCLUDED.expires_at,
		     updated_at  = NOW()`,
		mmiCacheKey, raw, time.Now().Add(mmiCacheTTL),
	)
}

// MoodLabel converts a Tickertape sentiment string to a display label.
func MoodLabel(s string) string {
	return strings.TrimSpace(s)
}
