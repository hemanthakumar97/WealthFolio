package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const metalsCacheKey = "market:metals"
const metalsCacheTTL = 3 * time.Hour

// troy oz → grams
const troyOzToGrams = 31.1034768

// TAX_DUTY_FACTOR mirrors the old project (10% import duty + GST approximation).
const taxDutyFactor = 1.10

// GOLD_MA_50 is a proxy 50-DMA used for euphoria detection (kept in sync with old project).
const goldMA50Proxy = 3300.0

type MetalPrice struct {
	USD          float64 `json:"usd"`
	INR          float64 `json:"inr"`           // per 10g for gold, per 1kg for silver
	Valuation    string  `json:"valuation"`      // OVERVALUED / FAIR DEAL / UNDERVALUED
	ChangeLabel  string  `json:"change_label"`
	IsExtended   bool    `json:"is_extended,omitempty"`
}

type MetalsAlert struct {
	Active  bool   `json:"active"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type MetalsDriver struct {
	Title string `json:"title"`
	Desc  string `json:"desc"`
}

// MetalsData is the rich payload returned by the precious metals service.
type MetalsData struct {
	Gold          MetalPrice     `json:"gold"`
	Silver        MetalPrice     `json:"silver"`
	USDINR        float64        `json:"usd_inr"`
	GSR           float64        `json:"gsr"`
	DMA50Dist     float64        `json:"dma_50_dist"`
	Alert         MetalsAlert    `json:"alert"`
	MarketDrivers []MetalsDriver `json:"market_drivers"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// CachedMetals is stored in market_cache.
type CachedMetals struct {
	Data      *MetalsData `json:"data"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// PreciousMetalsService fetches gold/silver prices and caches them.
type PreciousMetalsService struct {
	pool   *pgxpool.Pool
	yahoo  *YahooClient
}

func NewPreciousMetalsService(pool *pgxpool.Pool, fx *FXService) *PreciousMetalsService {
	return &PreciousMetalsService{pool: pool, yahoo: NewYahooClient()}
}

func (m *PreciousMetalsService) GetMetals(ctx context.Context) (*CachedMetals, error) {
	if cached, ok := m.fromCache(ctx); ok {
		return cached, nil
	}
	return m.refresh(ctx)
}

func (m *PreciousMetalsService) Refresh(ctx context.Context) (*CachedMetals, error) {
	return m.refresh(ctx)
}

func (m *PreciousMetalsService) refresh(ctx context.Context) (*CachedMetals, error) {
	data, err := m.fetch(ctx)
	if err != nil {
		if cached, ok := m.fromCacheAny(ctx); ok {
			return cached, nil
		}
		return nil, err
	}
	result := &CachedMetals{Data: data, UpdatedAt: time.Now()}
	m.toCache(ctx, result)
	return result, nil
}

func (m *PreciousMetalsService) fetch(ctx context.Context) (*MetalsData, error) {
	type priceResult struct {
		val float64
		err error
	}
	type histResult struct {
		rows []SheetRow
		err  error
	}

	goldCh := make(chan priceResult, 1)
	silverCh := make(chan priceResult, 1)
	fxCh := make(chan priceResult, 1)
	goldHistCh := make(chan histResult, 1)

	go func() { v, e := m.yahoo.FetchQuote(ctx, "GC=F"); goldCh <- priceResult{v, e} }()
	go func() { v, e := m.yahoo.FetchQuote(ctx, "SI=F"); silverCh <- priceResult{v, e} }()
	go func() { v, e := m.yahoo.FetchQuote(ctx, "USDINR=X"); fxCh <- priceResult{v, e} }()
	go func() { rows, e := m.yahoo.FetchHistory(ctx, "GC=F"); goldHistCh <- histResult{rows, e} }()

	g, s, fx, gh := <-goldCh, <-silverCh, <-fxCh, <-goldHistCh
	if g.err != nil {
		return nil, fmt.Errorf("gold quote: %w", g.err)
	}
	if s.err != nil {
		return nil, fmt.Errorf("silver quote: %w", s.err)
	}
	usdINR := fx.val
	if fx.err != nil || usdINR == 0 {
		usdINR = 84.0
	}

	goldUSD := g.val
	silverUSD := s.val

	// Compute real 50-day moving average from history.
	goldMA50 := goldMA50Proxy
	if gh.err == nil && len(gh.rows) >= 50 {
		recent := gh.rows[len(gh.rows)-50:]
		sum := 0.0
		for _, r := range recent {
			sum += r.Close
		}
		goldMA50 = sum / 50
	}

	// INR calculations with import duty factor.
	goldINR10g := (goldUSD / troyOzToGrams) * 10 * usdINR * taxDutyFactor
	silverINR1kg := (silverUSD / troyOzToGrams) * 1000 * usdINR * taxDutyFactor

	// Gold/Silver Ratio.
	gsr := 0.0
	if silverUSD > 0 {
		gsr = goldUSD / silverUSD
	}

	// Distance from real (or fallback proxy) 50-DMA.
	dma50Dist := ((goldUSD - goldMA50) / goldMA50) * 100
	isGoldExtended := dma50Dist > 12.0
	isGSRLow := gsr < 52

	// Valuations.
	goldVal := "FAIR DEAL"
	if isGoldExtended {
		goldVal = "OVERVALUED"
	} else if dma50Dist < -3.0 {
		goldVal = "UNDERVALUED"
	}

	silverVal := "FAIR DEAL"
	if gsr < 52 {
		silverVal = "OVERVALUED"
	} else if gsr > 80 {
		silverVal = "UNDERVALUED"
	}

	// Alert status.
	alertActive := isGSRLow && isGoldExtended
	status := "NEUTRAL"
	if isGSRLow {
		status = "EUPHORIA_WATCH"
		if isGoldExtended {
			status = "CRITICAL_EUPHORIA"
		}
	} else if gsr < 65 {
		status = "CAUTION"
	}

	// Try AI for both drivers and alert message in one call.
	aiDrivers, aiMessage := fetchAIContent(ctx, m.pool, goldUSD, silverUSD, usdINR, gsr, dma50Dist, status)

	drivers := aiDrivers
	if len(drivers) == 0 {
		drivers = buildDrivers(gsr, usdINR, dma50Dist)
	}
	message := aiMessage
	if message == "" {
		message = alertMessage(status, isGoldExtended)
	}

	return &MetalsData{
		Gold: MetalPrice{
			USD:        round2(goldUSD),
			INR:        math.Round(goldINR10g),
			Valuation:  goldVal,
			IsExtended: isGoldExtended,
		},
		Silver: MetalPrice{
			USD:       round2(silverUSD),
			INR:       math.Round(silverINR1kg),
			Valuation: silverVal,
		},
		USDINR:        round2(usdINR),
		GSR:           round2(gsr),
		DMA50Dist:     round2(dma50Dist),
		Alert:         MetalsAlert{Active: alertActive, Status: status, Message: message},
		MarketDrivers: drivers,
		UpdatedAt:     time.Now(),
	}, nil
}

// aiContent is the structured response we expect from the LLM.
type aiContent struct {
	Alert   string        `json:"alert"`
	Drivers []MetalsDriver `json:"drivers"`
}

// fetchAIContent calls the configured LLM and returns an alert message + 3 driver bullets.
// Returns empty values on any failure so callers can fall back to rule-based output.
func fetchAIContent(ctx context.Context, pool *pgxpool.Pool, goldUSD, silverUSD, usdINR, gsr, dma50Dist float64, status string) ([]MetalsDriver, string) {
	aiCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rows, err := pool.Query(aiCtx,
		`SELECT key, value FROM app_settings WHERE key IN ('ai_provider','ai_api_key','ai_model')`)
	if err != nil {
		return nil, ""
	}
	defer rows.Close()
	cfg := AIConfig{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		switch k {
		case "ai_provider":
			cfg.Provider = v
		case "ai_api_key":
			cfg.APIKey = v
		case "ai_model":
			cfg.Model = v
		}
	}
	if cfg.APIKey == "" {
		return nil, ""
	}

	system := `You are a senior precious-metals analyst focused on Indian retail investors.
Return ONLY a single valid JSON object with exactly two keys:
  "alert": one concise actionable sentence (≤25 words) for the current market status,
  "drivers": array of exactly 3 objects each with "title" (≤4 words) and "desc" (≤18 words).
No markdown, no explanation — raw JSON only.`

	user := fmt.Sprintf(`Live market data:
- Gold: $%.2f/oz (%.1f%% vs 50-day MA)
- Silver: $%.2f/oz
- USD/INR: ₹%.2f
- Gold/Silver Ratio: %.2f
- Valuation status: %s

Generate the alert message and 3 market-driver bullets for Indian retail investors.`,
		goldUSD, dma50Dist, silverUSD, usdINR, gsr, status)

	raw, err := callProviderJSON(aiCtx, cfg, system, user)
	if err != nil {
		slog.Warn("metals: AI content generation failed", "err", err)
		return nil, ""
	}

	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	if i := strings.LastIndex(raw, "}"); i >= 0 && i < len(raw)-1 {
		raw = raw[:i+1]
	}

	var result aiContent
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		slog.Warn("metals: AI content parse failed", "raw", raw, "err", err)
		return nil, ""
	}
	return result.Drivers, result.Alert
}

func buildDrivers(gsr, usdINR, dma50Dist float64) []MetalsDriver {
	var d []MetalsDriver
	if gsr < 52 {
		d = append(d, MetalsDriver{"Rotation Signal", fmt.Sprintf("Silver overextended (GSR %.2f). Consider rotating to Gold.", gsr)})
	} else if gsr > 80 {
		d = append(d, MetalsDriver{"Accumulate Signal", fmt.Sprintf("Silver historically cheap (GSR %.2f). Good entry zone.", gsr)})
	} else {
		d = append(d, MetalsDriver{"Fair Value", fmt.Sprintf("GSR %.2f is in healthy equilibrium.", gsr)})
	}
	if usdINR > 90.0 {
		d = append(d, MetalsDriver{"Weak Rupee", fmt.Sprintf("₹%.2f creating artificial floor for local prices.", usdINR)})
	} else {
		d = append(d, MetalsDriver{"Stable Rupee", fmt.Sprintf("USD/INR at ₹%.2f — not amplifying metal prices.", usdINR)})
	}
	if dma50Dist > 12.0 {
		d = append(d, MetalsDriver{"Market Euphoria", fmt.Sprintf("Gold %.1f%% above 50-DMA proxy. High correction risk.", dma50Dist)})
	} else if math.Abs(dma50Dist) < 5.0 {
		d = append(d, MetalsDriver{"Consolidation", "Gold consolidating near its medium-term mean."})
	}
	return d
}

func alertMessage(status string, isGoldExtended bool) string {
	switch status {
	case "CRITICAL_EUPHORIA":
		return "CRITICAL: Both metals overvalued. Consider pausing SIPs and booking profit. Move capital to arbitrage funds."
	case "EUPHORIA_WATCH":
		if isGoldExtended {
			return "Silver bubble (GSR < 52) & Gold overextended. Pause all metal SIPs. Move to liquid funds."
		}
		return "Silver overvalued (GSR < 52). Gold is fair. Switch Silver SIPs to Gold (SGB/ETF). Avoid Silver lumpsum."
	case "CAUTION":
		return "Valuation elevated. Silver outperforming. Pause lumpsums. Continue Gold SIPs."
	default:
		return "Fair value zone. Continue SIPs in Gold & Silver. Good zone for accumulation."
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func (m *PreciousMetalsService) fromCache(ctx context.Context) (*CachedMetals, bool) {
	var raw []byte
	err := m.pool.QueryRow(ctx,
		`SELECT cache_value FROM market_cache WHERE cache_key = $1 AND expires_at > NOW()`,
		metalsCacheKey,
	).Scan(&raw)
	if err != nil {
		return nil, false
	}
	var c CachedMetals
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, false
	}
	return &c, true
}

func (m *PreciousMetalsService) fromCacheAny(ctx context.Context) (*CachedMetals, bool) {
	var raw []byte
	err := m.pool.QueryRow(ctx,
		`SELECT cache_value FROM market_cache WHERE cache_key = $1`, metalsCacheKey,
	).Scan(&raw)
	if err != nil {
		return nil, false
	}
	var c CachedMetals
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, false
	}
	return &c, true
}

func (m *PreciousMetalsService) toCache(ctx context.Context, c *CachedMetals) {
	raw, _ := json.Marshal(c)
	_, _ = m.pool.Exec(ctx,
		`INSERT INTO market_cache (cache_key, cache_value, expires_at, updated_at)
		 VALUES ($1,$2,$3,NOW())
		 ON CONFLICT (cache_key) DO UPDATE SET
		     cache_value = EXCLUDED.cache_value,
		     expires_at  = EXCLUDED.expires_at,
		     updated_at  = NOW()`,
		metalsCacheKey, raw, time.Now().Add(metalsCacheTTL),
	)
}
