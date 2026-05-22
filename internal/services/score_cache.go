package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const scoreCacheTTL = 24 * time.Hour

// CachedScore is what is stored in the instrument_scores table.
type CachedScore struct {
	InstrumentID int64     `json:"instrument_id"`
	Zero1Score   int       `json:"zero1_score"`
	Kind         string    `json:"kind"` // "mf" | "etf" | "stock"
	MetricsJSON  []byte    `json:"metrics_json"`
	ComputedAt   time.Time `json:"computed_at"`
}

// LoadScore reads a cached score from DB. Returns nil if not found or expired.
func LoadScore(ctx context.Context, pool *pgxpool.Pool, instrumentID int64) (*CachedScore, error) {
	var s CachedScore
	var metricsRaw []byte
	err := pool.QueryRow(ctx, `
		SELECT instrument_id, zero1_score, kind, metrics_json, computed_at
		  FROM instrument_scores
		 WHERE instrument_id = $1
	`, instrumentID).Scan(&s.InstrumentID, &s.Zero1Score, &s.Kind, &metricsRaw, &s.ComputedAt)
	if err != nil {
		return nil, nil // not found
	}
	if time.Since(s.ComputedAt) > scoreCacheTTL {
		return nil, nil // expired
	}
	s.MetricsJSON = metricsRaw
	return &s, nil
}

// SaveScore upserts a score+metrics into the cache.
func SaveScore(ctx context.Context, pool *pgxpool.Pool, instrumentID int64, kind string, score int, metrics any) error {
	b, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO instrument_scores (instrument_id, zero1_score, kind, metrics_json, computed_at)
		     VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (instrument_id)
		DO UPDATE SET zero1_score  = EXCLUDED.zero1_score,
		              kind         = EXCLUDED.kind,
		              metrics_json = EXCLUDED.metrics_json,
		              computed_at  = EXCLUDED.computed_at
	`, instrumentID, score, kind, b)
	return err
}

// ─── Active holdings lookup ───────────────────────────────────────────────────

type activeInstrument struct {
	ID          int64
	AssetType   string
	AMFICode    string
	YahooSymbol string
}

// LoadActiveInstruments returns all instruments that have at least one active holding.
func LoadActiveInstruments(ctx context.Context, pool *pgxpool.Pool) ([]activeInstrument, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT i.id,
		       COALESCE(i.asset_type,''),
		       COALESCE(i.amfi_code,''),
		       COALESCE(i.yahoo_symbol,'')
		  FROM transactions t
		  JOIN instruments i ON t.instrument_id = i.id
		 WHERE t.transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		   AND i.asset_type IN ('MF','ETF','STOCK','US_FUND','METAL','GOLD')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []activeInstrument
	for rows.Next() {
		var a activeInstrument
		if err := rows.Scan(&a.ID, &a.AssetType, &a.AMFICode, &a.YahooSymbol); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ─── Batch refresh ────────────────────────────────────────────────────────────

type RefreshResult struct {
	Total    int      `json:"total"`
	Success  int      `json:"success"`
	Errors   []string `json:"errors,omitempty"`
}

// RefreshAllScores fetches and caches metrics for every active holding.
// MFs are fetched concurrently; ETFs/stocks sequentially (Yahoo crumb constraint).
func RefreshAllScores(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) RefreshResult {
	instruments, err := LoadActiveInstruments(ctx, pool)
	if err != nil {
		return RefreshResult{Errors: []string{"load instruments: " + err.Error()}}
	}

	result := RefreshResult{Total: len(instruments)}
	var mu sync.Mutex

	addErr := func(msg string) {
		mu.Lock()
		result.Errors = append(result.Errors, msg)
		mu.Unlock()
	}
	incSuccess := func() {
		mu.Lock()
		result.Success++
		mu.Unlock()
	}

	// Split by type.
	var mfWork, otherWork []activeInstrument
	for _, a := range instruments {
		if a.AssetType == "MF" && a.AMFICode != "" {
			mfWork = append(mfWork, a)
		} else {
			otherWork = append(otherWork, a)
		}
	}

	// MFs — concurrent batch fetch.
	if len(mfWork) > 0 {
		codes := make([]string, len(mfWork))
		codeToID := make(map[string]int64, len(mfWork))
		for i, a := range mfWork {
			codes[i] = a.AMFICode
			codeToID[a.AMFICode] = a.ID
		}
		metricsMap := FetchMFMetricsBatch(codes, timeout)
		for code, m := range metricsMap {
			id := codeToID[code]
			if err := SaveScore(ctx, pool, id, "mf", m.Zero1Score, m); err != nil {
				addErr(fmt.Sprintf("save MF %d: %s", id, err))
			} else {
				incSuccess()
			}
		}
		// Count failures for MFs that didn't come back.
		for _, a := range mfWork {
			if _, ok := metricsMap[a.AMFICode]; !ok {
				addErr(fmt.Sprintf("fetch MF %d (%s): no data returned", a.ID, a.AMFICode))
			}
		}
	}

	// ETFs and stocks — sequential (Yahoo crumb is per-request).
	for _, a := range otherWork {
		switch {
		case a.AssetType == "ETF" || a.AssetType == "US_FUND" || a.AssetType == "METAL" || a.AssetType == "GOLD" || (a.AssetType == "MF" && a.AMFICode == ""):
			m, err := FetchETFMetrics(ctx, a.ID, a.YahooSymbol, pool, timeout)
			if err != nil {
				addErr(fmt.Sprintf("fetch ETF %d: %s", a.ID, err))
				continue
			}
			if err := SaveScore(ctx, pool, a.ID, "etf", m.Zero1Score, m); err != nil {
				addErr(fmt.Sprintf("save ETF %d: %s", a.ID, err))
				continue
			}
			incSuccess()

		case a.AssetType == "STOCK":
			if a.YahooSymbol == "" {
				addErr(fmt.Sprintf("stock %d: no yahoo_symbol", a.ID))
				continue
			}
			m, err := FetchStockMetrics(ctx, a.ID, a.YahooSymbol, pool, timeout)
			if err != nil {
				addErr(fmt.Sprintf("fetch stock %d: %s", a.ID, err))
				continue
			}
			if err := SaveScore(ctx, pool, a.ID, "stock", m.Zero1Score, m); err != nil {
				addErr(fmt.Sprintf("save stock %d: %s", a.ID, err))
				continue
			}
			incSuccess()
		}
	}

	return result
}
