package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Mood thresholds: below 25th pct → Green (cheap), above 75th pct → Red (expensive).
const (
	MoodGreen  = "Green"
	MoodYellow = "Yellow"
	MoodRed    = "Red"
)

// IndexConfigItem is one row from market_index_config.
type IndexConfigItem struct {
	IndexName   string `json:"index_name"`
	DisplayName string `json:"display_name"`
	Category    string `json:"category"`
	IsActive    bool   `json:"is_active"`
}

// MarketMoodResponse is the computed mood for one index.
type MarketMoodResponse struct {
	IndexName    string  `json:"index_name"`
	DisplayName  string  `json:"display_name"`
	CurrentPE    float64 `json:"current_pe"`
	MedianPE3Yr  float64 `json:"median_pe_3yr"`
	Pct25        float64 `json:"pct_25"`
	Pct75        float64 `json:"pct_75"`
	Mood         string  `json:"mood"`
	Recommendation string `json:"recommendation"`
	DataPoints   int     `json:"data_points"`
	AsOf         string  `json:"as_of"`
	IsActive     bool    `json:"is_active"`
}

// IndexDetailsResponse wraps mood + historical P/E series.
type IndexDetailsResponse struct {
	MarketMoodResponse
	History []PEDataPoint `json:"history"`
}

// PEDataPoint is one day's P/E for charting.
type PEDataPoint struct {
	Date    string  `json:"date"`
	PERatio float64 `json:"pe_ratio"`
}

// MarketMoodService computes mood from market_data.
type MarketMoodService struct {
	pool *pgxpool.Pool
}

func NewMarketMoodService(pool *pgxpool.Pool) *MarketMoodService {
	return &MarketMoodService{pool: pool}
}

// ListConfig returns all index config rows.
func (s *MarketMoodService) ListConfig(ctx context.Context) ([]IndexConfigItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT index_name, display_name, category, is_active
		   FROM market_index_config ORDER BY index_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IndexConfigItem
	for rows.Next() {
		var c IndexConfigItem
		if err := rows.Scan(&c.IndexName, &c.DisplayName, &c.Category, &c.IsActive); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ToggleIndex flips is_active for one index.
func (s *MarketMoodService) ToggleIndex(ctx context.Context, indexName string) (*IndexConfigItem, error) {
	var c IndexConfigItem
	err := s.pool.QueryRow(ctx,
		`UPDATE market_index_config
		    SET is_active = NOT is_active
		  WHERE index_name = $1
		  RETURNING index_name, display_name, category, is_active`,
		indexName,
	).Scan(&c.IndexName, &c.DisplayName, &c.Category, &c.IsActive)
	if err != nil {
		return nil, fmt.Errorf("toggle %s: %w", indexName, err)
	}
	return &c, nil
}

// Moods computes mood for all active indices.
func (s *MarketMoodService) Moods(ctx context.Context) ([]MarketMoodResponse, error) {
	configs, err := s.ListConfig(ctx)
	if err != nil {
		return nil, err
	}
	var out []MarketMoodResponse
	for _, cfg := range configs {
		if !cfg.IsActive {
			continue
		}
		mood, err := s.computeMood(ctx, cfg)
		if err != nil {
			continue // skip index with no data
		}
		out = append(out, *mood)
	}
	return out, nil
}

// IndexDetails returns mood + history for charting.
func (s *MarketMoodService) IndexDetails(ctx context.Context, indexName string, period string) (*IndexDetailsResponse, error) {
	var cfg IndexConfigItem
	err := s.pool.QueryRow(ctx,
		`SELECT index_name, display_name, category, is_active
		   FROM market_index_config WHERE index_name = $1`, indexName,
	).Scan(&cfg.IndexName, &cfg.DisplayName, &cfg.Category, &cfg.IsActive)
	if err != nil {
		return nil, fmt.Errorf("index %q not found", indexName)
	}

	mood, err := s.computeMood(ctx, cfg)
	if err != nil {
		mood = &MarketMoodResponse{
			IndexName:   cfg.IndexName,
			DisplayName: cfg.DisplayName,
			IsActive:    cfg.IsActive,
			Mood:        MoodYellow,
		}
	}

	// History window.
	days := 365
	switch period {
	case "1M":
		days = 30
	case "3M":
		days = 90
	case "6M":
		days = 180
	case "3Y":
		days = 3 * 365
	case "5Y":
		days = 5 * 365
	}

	rows, err := s.pool.Query(ctx,
		`SELECT price_date::text, pe_ratio::float
		   FROM market_data
		  WHERE index_name = $1 AND pe_ratio IS NOT NULL
		    AND price_date >= NOW() - ($2 || ' days')::interval
		  ORDER BY price_date ASC`,
		indexName, days,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []PEDataPoint
	for rows.Next() {
		var p PEDataPoint
		if err := rows.Scan(&p.Date, &p.PERatio); err != nil {
			return nil, err
		}
		history = append(history, p)
	}
	return &IndexDetailsResponse{MarketMoodResponse: *mood, History: history}, nil
}

// apiNameMapping maps DB index names to niftyindices.com API names where they differ.
var apiNameMapping = map[string]string{
	"NIFTY SMALLCAP 50":       "NIFTY SMLCAP 50",
	"NIFTY SMALLCAP 100":      "NIFTY SMLCAP 100",
	"NIFTY SMALLCAP 250":      "NIFTY SMLCAP 250",
	"NIFTY MIDSMALLCAP 400":   "NIFTY MIDSML 400",
	"NIFTY OIL & GAS":         "NIFTY OIL GAS",
	"NIFTY FINANCIAL SERVICES": "NIFTY FIN SERVICE",
	"NIFTY CONSUMER DURABLES": "NIFTY CONSR DURBL",
	"NIFTY LARGEMIDCAP 250":   "NIFTY LARGEMID250",
}

// SyncMarketData fetches one year of P/E data for all active indices from
// niftyindices.com and upserts it into market_data.
func (s *MarketMoodService) SyncMarketData(ctx context.Context) error {
	configs, err := s.ListConfig(ctx)
	if err != nil {
		return fmt.Errorf("list config: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// Prime the session so niftyindices.com sets its cookies/bot-check.
	homeReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://niftyindices.com/reports/historical-data", nil)
	homeReq.Header.Set("User-Agent", niftyUA)
	client.Do(homeReq) //nolint:errcheck — best-effort session init

	end := time.Now()
	start := end.AddDate(-1, 0, 0)

	for _, cfg := range configs {
		if err := s.syncIndex(ctx, client, cfg.IndexName, start, end); err != nil {
			slog.Warn("market sync: index failed", "index", cfg.IndexName, "err", err)
		}
		// Polite rate-limit between indices.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}

const niftyUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36"

func (s *MarketMoodService) syncIndex(ctx context.Context, client *http.Client, indexName string, start, end time.Time) error {
	apiName := indexName
	if mapped, ok := apiNameMapping[indexName]; ok {
		apiName = mapped
	}

	inner, _ := json.Marshal(map[string]string{
		"name":      apiName,
		"startDate": start.Format("02-Jan-2006"),
		"endDate":   end.Format("02-Jan-2006"),
		"indexName": indexName,
	})
	payload, _ := json.Marshal(map[string]string{"cinfo": string(inner)})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://www.niftyindices.com/Backpage.aspx/getpepbHistoricaldataDBtoString",
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", niftyUA)
	req.Header.Set("Origin", "https://niftyindices.com")
	req.Header.Set("Referer", "https://niftyindices.com/reports/historical-data")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("niftyindices request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("niftyindices status %d", resp.StatusCode)
	}

	// Response: {"d":"[{\"DATE\":\"01 Jan 2024\",\"pe\":\"22.5\",...}]"}
	var wrapper struct {
		D string `json:"d"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return fmt.Errorf("decode wrapper: %w", err)
	}

	var records []map[string]string
	if err := json.Unmarshal([]byte(wrapper.D), &records); err != nil {
		return fmt.Errorf("decode records: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	// Upsert each record.
	for _, r := range records {
		dt, err := time.Parse("02 Jan 2006", r["DATE"])
		if err != nil {
			continue
		}
		pe := parseOptFloat(r["pe"])
		pb := parseOptFloat(r["pb"])
		div := parseOptFloat(r["divYield"])

		_, err = s.pool.Exec(ctx, `
			INSERT INTO market_data (index_name, price_date, pe_ratio, pb_ratio, div_yield, source)
			VALUES ($1,$2,$3,$4,$5,'NIFTYINDICES')
			ON CONFLICT (index_name, price_date) DO UPDATE
			  SET pe_ratio = EXCLUDED.pe_ratio,
			      pb_ratio = EXCLUDED.pb_ratio,
			      div_yield = EXCLUDED.div_yield`,
			indexName, dt, pe, pb, div)
		if err != nil {
			slog.Warn("market sync: upsert", "index", indexName, "date", dt, "err", err)
		}
	}
	slog.Info("market sync: done", "index", indexName, "records", len(records))
	return nil
}

func parseOptFloat(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// computeMood calculates the mood for one index based on 3-year P/E percentiles.
func (s *MarketMoodService) computeMood(ctx context.Context, cfg IndexConfigItem) (*MarketMoodResponse, error) {
	// Fetch 3 years of P/E data for percentile calculation.
	rows, err := s.pool.Query(ctx,
		`SELECT pe_ratio::float, price_date::text
		   FROM market_data
		  WHERE index_name = $1 AND pe_ratio IS NOT NULL
		    AND price_date >= NOW() - INTERVAL '3 years'
		  ORDER BY price_date DESC`,
		cfg.IndexName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pes []float64
	var latestDate string
	for rows.Next() {
		var pe float64
		var d string
		if err := rows.Scan(&pe, &d); err != nil {
			return nil, err
		}
		pes = append(pes, pe)
		if latestDate == "" {
			latestDate = d
		}
	}

	if len(pes) == 0 {
		return nil, fmt.Errorf("no data for %s", cfg.IndexName)
	}

	currentPE := pes[0] // newest first
	sorted := make([]float64, len(pes))
	copy(sorted, pes)
	sort.Float64s(sorted)

	pct25 := percentile(sorted, 25)
	pct50 := percentile(sorted, 50)
	pct75 := percentile(sorted, 75)

	mood := MoodYellow
	var recommendation string
	switch {
	case currentPE < pct25:
		mood = MoodGreen
		recommendation = "Market appears undervalued. Consider adding to your equity positions."
	case currentPE > pct75:
		mood = MoodRed
		recommendation = "Market appears overvalued. Consider de-risking or pausing fresh equity investments."
	default:
		recommendation = "Market is fairly valued. Continue your SIP and rebalance if needed."
	}

	return &MarketMoodResponse{
		IndexName:      cfg.IndexName,
		DisplayName:    cfg.DisplayName,
		CurrentPE:      currentPE,
		MedianPE3Yr:    pct50,
		Pct25:          pct25,
		Pct75:          pct75,
		Mood:           mood,
		Recommendation: recommendation,
		DataPoints:     len(pes),
		AsOf:           latestDate,
		IsActive:       cfg.IsActive,
	}, nil
}

// percentile computes the p-th percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p / 100) * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// IngestPE adds a P/E data point for an index.
func (s *MarketMoodService) IngestPE(ctx context.Context, indexName string, date time.Time, pe, pb, divYield *float64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO market_data (index_name, price_date, pe_ratio, pb_ratio, div_yield, source)
		 VALUES ($1, $2, $3, $4, $5, 'MANUAL')
		 ON CONFLICT (index_name, price_date) DO UPDATE SET
		     pe_ratio  = COALESCE(EXCLUDED.pe_ratio, market_data.pe_ratio),
		     pb_ratio  = COALESCE(EXCLUDED.pb_ratio, market_data.pb_ratio),
		     div_yield = COALESCE(EXCLUDED.div_yield, market_data.div_yield)`,
		indexName, date, pe, pb, divYield,
	)
	return err
}
