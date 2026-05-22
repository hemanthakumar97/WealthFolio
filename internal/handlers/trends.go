package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

type TrendsHandler struct {
	pool     *pgxpool.Pool
	snapshot *services.SnapshotService
}

func NewTrendsHandler(pool *pgxpool.Pool, snapshot *services.SnapshotService) *TrendsHandler {
	return &TrendsHandler{pool: pool, snapshot: snapshot}
}

// Portfolio returns daily snapshot rows for charting.
// ?days=30|90|365|0  — date range (0 = all)
// ?asset_type=MF     — filter by asset type
// ?instrument_id=1   — filter by specific instrument
func (h *TrendsHandler) Portfolio(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	assetType := strings.TrimSpace(strings.ToUpper(q.Get("asset_type")))
	instrIDStr := strings.TrimSpace(q.Get("instrument_id"))

	type row struct {
		Date          string  `json:"date"`
		Invested      float64 `json:"invested"`
		Value         float64 `json:"value"`
		Profit        float64 `json:"profit"`
		ProfitPercent float64 `json:"profit_percent"`
	}
	out := []row{}

	if assetType != "" || instrIDStr != "" {
		// Filtered: aggregate from instrument_snapshots.
		conds := []string{}
		args := []any{}

		if instrIDStr != "" {
			instrID, err := strconv.ParseInt(instrIDStr, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid instrument_id")
				return
			}
			args = append(args, instrID)
			conds = append(conds, "s.instrument_id = $"+strconv.Itoa(len(args)))
		} else {
			args = append(args, assetType)
			conds = append(conds, "i.asset_type = $"+strconv.Itoa(len(args)))
		}
		if days > 0 {
			args = append(args, days)
			conds = append(conds, "s.snapshot_date >= NOW() - ($"+strconv.Itoa(len(args))+"::int || ' days')::interval")
		}

		sqlQ := `
			SELECT s.snapshot_date::text,
			       SUM(s.invested)::float,
			       SUM(s.value)::float,
			       SUM(s.profit)::float,
			       CASE WHEN SUM(s.invested) > 0 THEN (SUM(s.profit) / SUM(s.invested)) * 100 ELSE 0 END::float
			  FROM instrument_snapshots s
			  JOIN instruments i ON i.id = s.instrument_id
			 WHERE ` + strings.Join(conds, " AND ") + `
			 GROUP BY s.snapshot_date
			 ORDER BY s.snapshot_date`

		rows, err := h.pool.Query(r.Context(), sqlQ, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		for rows.Next() {
			var rr row
			if err := rows.Scan(&rr.Date, &rr.Invested, &rr.Value, &rr.Profit, &rr.ProfitPercent); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out = append(out, rr)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Unfiltered: read pre-aggregated portfolio_snapshots.
	var sqlQ string
	var args []any
	if days > 0 {
		sqlQ = `SELECT snapshot_date::text, total_invested::float, total_value::float,
			           total_profit::float, profit_percentage::float
			      FROM portfolio_snapshots
			     WHERE snapshot_date >= NOW() - ($1::int || ' days')::interval
			     ORDER BY snapshot_date`
		args = []any{days}
	} else {
		sqlQ = `SELECT snapshot_date::text, total_invested::float, total_value::float,
			           total_profit::float, profit_percentage::float
			      FROM portfolio_snapshots
			     ORDER BY snapshot_date`
	}

	rows, err := h.pool.Query(r.Context(), sqlQ, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.Date, &rr.Invested, &rr.Value, &rr.Profit, &rr.ProfitPercent); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, rr)
	}
	writeJSON(w, http.StatusOK, out)
}

// AllocationHistory returns daily breakdown by asset type.
func (h *TrendsHandler) AllocationHistory(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	data, err := h.snapshot.AllocationHistory(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// Benchmark returns historical price data for a major index.
func (h *TrendsHandler) Benchmark(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		symbol = "^NSEI" // Default to Nifty 50
	}
	yahoo := services.NewYahooClient()
	history, err := yahoo.FetchHistory(r.Context(), symbol)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// MonthlyReturns returns monthly performance data.
// ?months=12 (default: last 12 months)
// ?months=0 (all historical months)
func (h *TrendsHandler) MonthlyReturns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	monthsStr := q.Get("months")
	months := 12
	if monthsStr != "" {
		if m, err := strconv.Atoi(monthsStr); err == nil {
			months = m
		}
	}

	var sqlQ string
	var args []any

	if months > 0 {
		// Fetch one extra month for LAG
		sqlQ = `
		WITH monthly AS (
		  SELECT
		    date_trunc('month', snapshot_date)::date        AS month,
		    last(total_profit,    snapshot_date)::float     AS end_profit,
		    last(total_invested,  snapshot_date)::float     AS invested,
		    last(total_value,     snapshot_date)::float     AS value
		  FROM portfolio_snapshots
		  WHERE snapshot_date >= date_trunc('month', NOW() - ($1::int + 1 || ' months')::interval)
		  GROUP BY 1
		),
		ranked AS (
		  SELECT
		    month, invested, value, end_profit,
		    LAG(end_profit) OVER (ORDER BY month) AS prev_profit,
		    LAG(invested)   OVER (ORDER BY month) AS prev_invested
		  FROM monthly
		)
		SELECT
		  month::text,
		  invested,
		  value,
		  COALESCE(end_profit - prev_profit, 0)::float AS monthly_profit,
		  CASE WHEN COALESCE(prev_invested, 0) > 0
		       THEN ((end_profit - prev_profit) / prev_invested * 100)::float
		       ELSE 0::float
		  END AS monthly_profit_pct
		FROM ranked
		WHERE month >= date_trunc('month', NOW() - ($1::int || ' months')::interval)
		ORDER BY month DESC
		`
		args = []any{months}
	} else {
		// Fetch all history
		sqlQ = `
		WITH monthly AS (
		  SELECT
		    date_trunc('month', snapshot_date)::date        AS month,
		    last(total_profit,    snapshot_date)::float     AS end_profit,
		    last(total_invested,  snapshot_date)::float     AS invested,
		    last(total_value,     snapshot_date)::float     AS value
		  FROM portfolio_snapshots
		  GROUP BY 1
		),
		ranked AS (
		  SELECT
		    month, invested, value, end_profit,
		    LAG(end_profit) OVER (ORDER BY month) AS prev_profit,
		    LAG(invested)   OVER (ORDER BY month) AS prev_invested
		  FROM monthly
		)
		SELECT
		  month::text,
		  invested,
		  value,
		  COALESCE(end_profit - prev_profit, end_profit)::float AS monthly_profit,
		  CASE WHEN COALESCE(prev_invested, 0) > 0
		       THEN ((end_profit - COALESCE(prev_profit, 0)) / prev_invested * 100)::float
		       WHEN invested > 0
		       THEN (end_profit / invested * 100)::float
		       ELSE 0::float
		  END AS monthly_profit_pct
		FROM ranked
		ORDER BY month DESC
		`
	}

	rows, err := h.pool.Query(r.Context(), sqlQ, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type row struct {
		Month         string  `json:"month"`
		Invested      float64 `json:"invested"`
		Value         float64 `json:"value"`
		Profit        float64 `json:"profit"`
		ProfitPercent float64 `json:"profit_percent"`
	}
	out := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Month, &r.Invested, &r.Value, &r.Profit, &r.ProfitPercent); err != nil {
			continue
		}
		out = append(out, r)
	}
	writeJSON(w, http.StatusOK, out)
}

// Backfill re-creates all historical snapshots.
func (h *TrendsHandler) Backfill(w http.ResponseWriter, r *http.Request) {
	count, err := h.snapshot.BackfillSnapshots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "ok",
		"snapshots_created": count,
	})
}

// CreateSnapshot triggers a manual snapshot for today.
func (h *TrendsHandler) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := h.snapshot.CreateDailySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
