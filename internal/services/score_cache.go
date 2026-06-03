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
	Name        string
	AssetType   string
	AMFICode    string
	YahooSymbol string
	GrowwSlug   string
}

// RefreshProgressEvent is emitted by RefreshAllScores as each fund is processed.
type RefreshProgressEvent struct {
	Type      string `json:"type"`                // fund_start | fund_done | fund_error | scores_done | ai_start | ai_done
	ID        int64  `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	AssetType string `json:"asset_type,omitempty"`
	Score     int    `json:"score,omitempty"`
	Error     string `json:"error,omitempty"`
	Success   int    `json:"success,omitempty"`
	Total     int    `json:"total,omitempty"`
}

// LoadActiveInstruments returns instruments with net-positive holdings — mirrors the
// filter used in the /signal page (total units >= 0.5, cost basis > 0).
func LoadActiveInstruments(ctx context.Context, pool *pgxpool.Pool) ([]activeInstrument, error) {
	rows, err := pool.Query(ctx, `
		SELECT i.id,
		       COALESCE(i.name,''),
		       COALESCE(i.asset_type,''),
		       COALESCE(i.amfi_code,''),
		       COALESCE(i.yahoo_symbol,''),
		       COALESCE(i.groww_slug,'')
		  FROM instruments i
		 WHERE i.asset_type IN ('MF','ETF','STOCK','US_FUND','METAL','GOLD')
		   AND EXISTS (
		         SELECT 1 FROM transactions t
		          WHERE t.instrument_id = i.id
		            AND t.transaction_type IN ('BUY','SWITCH_IN','SELL','SWITCH_OUT','BONUS')
		         HAVING
		           SUM(CASE WHEN t.transaction_type IN ('BUY','SWITCH_IN','BONUS')
		                    THEN t.quantity::float ELSE 0 END)
		         - SUM(CASE WHEN t.transaction_type IN ('SELL','SWITCH_OUT')
		                    THEN t.quantity::float ELSE 0 END) >= 0.5
		           AND SUM(CASE WHEN t.transaction_type IN ('BUY','SWITCH_IN')
		                        THEN t.amount::float ELSE 0 END) > 0
		       )
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []activeInstrument
	for rows.Next() {
		var a activeInstrument
		if err := rows.Scan(&a.ID, &a.Name, &a.AssetType, &a.AMFICode, &a.YahooSymbol, &a.GrowwSlug); err != nil {
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
// All fetches run through a shared semaphore of 3 — avoids rate-limiting Groww/mfapi.
// progressCh receives live events per fund; pass nil to skip progress streaming.
func RefreshAllScores(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration, progressCh chan<- RefreshProgressEvent) RefreshResult {
	emit := func(e RefreshProgressEvent) {
		if progressCh != nil {
			select {
			case progressCh <- e:
			default: // never block if consumer is slow
			}
		}
	}
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

	// Semaphore: max 3 concurrent fetches across all asset types.
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup

	for _, a := range instruments {
		wg.Add(1)
		go func(inst activeInstrument) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			emit(RefreshProgressEvent{Type: "fund_start", ID: inst.ID, Name: inst.Name, AssetType: inst.AssetType})

			var score int
			var fetchErr error

			switch {
			case inst.AssetType == "MF" && inst.AMFICode != "":
				m, err := FetchMFMetrics(inst.AMFICode, timeout, inst.GrowwSlug)
				if err != nil {
					fetchErr = err
				} else if err := SaveScore(ctx, pool, inst.ID, "mf", m.Zero1Score, m); err != nil {
					fetchErr = err
				} else {
					score = m.Zero1Score
				}

			case inst.AssetType == "ETF" || inst.AssetType == "US_FUND" ||
				inst.AssetType == "METAL" || inst.AssetType == "GOLD" ||
				(inst.AssetType == "MF" && inst.AMFICode == ""):
				m, err := FetchETFMetrics(ctx, inst.ID, inst.YahooSymbol, pool, timeout)
				if err != nil {
					fetchErr = err
				} else if err := SaveScore(ctx, pool, inst.ID, "etf", m.Zero1Score, m); err != nil {
					fetchErr = err
				} else {
					score = m.Zero1Score
				}

			case inst.AssetType == "STOCK":
				if inst.YahooSymbol == "" {
					fetchErr = fmt.Errorf("no yahoo_symbol")
				} else if m, err := FetchStockMetrics(ctx, inst.ID, inst.YahooSymbol, pool, timeout); err != nil {
					fetchErr = err
				} else if err := SaveScore(ctx, pool, inst.ID, "stock", m.Zero1Score, m); err != nil {
					fetchErr = err
				} else {
					score = m.Zero1Score
				}
			}

			if fetchErr != nil {
				addErr(fmt.Sprintf("%s %d (%s): %s", inst.AssetType, inst.ID, inst.Name, fetchErr))
				emit(RefreshProgressEvent{Type: "fund_error", ID: inst.ID, Name: inst.Name, AssetType: inst.AssetType, Error: fetchErr.Error()})
			} else {
				incSuccess()
				emit(RefreshProgressEvent{Type: "fund_done", ID: inst.ID, Name: inst.Name, AssetType: inst.AssetType, Score: score})
			}
		}(a)
	}
	wg.Wait()

	mu.Lock()
	emit(RefreshProgressEvent{Type: "scores_done", Success: result.Success, Total: result.Total})
	mu.Unlock()

	return result
}
