# 03 — Database

This document describes the WealthFolio persistence layer end-to-end. It is intended for AI agents who may need to:

- debug a slow or wrong query against TimescaleDB,
- design a new goose migration without breaking compression / continuous-aggregate policies,
- trace a piece of data from a parser row through `transactions` → FIFO → `instrument_snapshots`.

All claims are cited to specific migration files in `internal/db/migrations/`.

---

## 1. Database flavour: TimescaleDB on PostgreSQL 16

The application runs on **TimescaleDB**, an extension on top of PostgreSQL 16. The Timescale extension is created in the very first migration:

```sql
CREATE EXTENSION IF NOT EXISTS timescaledb;
```
— `00001_init.sql`

### Why hypertables

Three tables in the schema are time-series-shaped and benefit from Timescale's automatic partitioning into time-bucketed *chunks*:

| Hypertable            | Time column      | Chunk interval | Compression policy   | Created in                  |
|-----------------------|------------------|----------------|----------------------|-----------------------------|
| `prices`              | `price_date`     | 30 days        | after 90 days, segmentby `instrument_id` | `00003_prices.sql` |
| `portfolio_snapshots` | `snapshot_date`  | 90 days        | after 180 days       | `00004_snapshots.sql`       |
| `market_data`         | `price_date`     | 90 days        | after 180 days, segmentby `index_name` | `00006_market_mood.sql` |

Hypertables give us:

1. **Cheap range queries.** A query for "last 30 days of NAV for instrument 42" hits one or two chunks, not the full table.
2. **Per-chunk compression.** Old NAV history (> 90 days) is automatically columnar-compressed segmented by `instrument_id` (see `00003_prices.sql` lines 21–25), giving 10×+ space savings without sacrificing query performance because Timescale's compressed-chunk planner is segment-aware.
3. **Continuous aggregates (CAGGs)** — see §3.

`instrument_snapshots` (`00011_instrument_snapshots.sql`) is intentionally **not** a hypertable: it is a regular partitioned-by-PK table because it is rebuilt frequently and is small (rows = N_instruments × N_days).

---

## 2. Migration mechanism

Migrations are managed by **[goose](https://github.com/pressly/goose)** and run **automatically at boot** from `cmd/server/main.go` via `internal/db/migrate.go`:

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
    cfg := pool.Config().ConnConfig
    sqlDB := stdlib.OpenDB(*cfg)
    defer sqlDB.Close()
    goose.SetBaseFS(migrationsFS)
    goose.SetDialect("postgres")
    return goose.UpContext(ctx, sqlDB, "migrations")
}
```
— `internal/db/migrate.go`

Migrations are SQL files (no Go migrations) embedded into the binary via `go:embed migrations/*.sql`. They are applied in lexicographic order; the file-naming convention is `NNNNN_description.sql`.

Each file uses goose annotations:

```sql
-- +goose Up
-- +goose StatementBegin
...
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
...
-- +goose StatementEnd
```

`MigrationStatus()` in the same file exposes a `*sql.DB` for diagnostics endpoints.

### Migration ledger

| #   | File                                       | Purpose                                                                 |
|-----|--------------------------------------------|-------------------------------------------------------------------------|
| 001 | `00001_init.sql`                           | Enable `timescaledb` extension; create `users` table + email index.     |
| 002 | `00002_transactions.sql`                   | Create `instruments`, `upload_history`, `transactions`, `import_logs`. Includes the dedup index `idx_transactions_dedup` on `(instrument_id, transaction_date, transaction_type, platform)`. |
| 003 | `00003_prices.sql`                         | Create `prices` hypertable (30-day chunks, compression after 90d, segment by `instrument_id`) and the `market_cache` key-value store. |
| 004 | `00004_snapshots.sql`                      | Create `portfolio_snapshots` hypertable (90-day chunks, compression after 180d) plus the `cagg_monthly_returns` continuous aggregate. |
| 005 | `00005_categories.sql`                     | Create `categories`, `instrument_categories`, `instrument_allocations`, `category_allocations`. Seeds the five canonical alloc categories (EQUITY, GOLD, DEBT, US_EQUITY, OTHERS). |
| 006 | `00006_market_mood.sql`                    | Create `market_data` hypertable + `cagg_index_pe_stats` CAGG + `market_index_config` (seeded with 5 Indian indices) + `watchlist` table. |
| 007 | `00007_trade_id.sql`                       | Add `trade_id` column to `transactions` + partial unique index `(instrument_id, trade_id, transaction_type) WHERE trade_id IS NOT NULL` for stricter dedup. |
| 008 | `00008_yahoo_symbol.sql`                   | Add `instruments.yahoo_symbol` for US fund price fetching via Yahoo.    |
| 009 | `00009_us_fund_type.sql`                   | Extend `instruments.asset_type` CHECK to include `US_FUND`.              |
| 010 | `00010_metal_type.sql`                     | Rename asset_type `GOLD` → `METAL`; update CHECK accordingly.            |
| 011 | `00011_instrument_snapshots.sql`           | Create the daily `instrument_snapshots` table (non-hypertable, PK `(snapshot_date, instrument_id)`). |
| 012 | `00012_drop_gfinance_symbol.sql`           | Drop the now-unused `instruments.gfinance_symbol` column + index.        |
| 013 | `00013_email_imports.sql`                  | Create `email_imports` to dedupe Gmail-driven imports by `message_id`.   |
| 014 | `00014_app_settings.sql`                   | Create `app_settings` key/value table (AI provider, investor profile, etc.). |
| 015 | `00015_email_watch_rules.sql`              | Create `email_watch_rules` and seed 3 defaults (Groww MF, Zerodha contract notes, IndMoney US). |
| 016 | `00016_tqqq_split_adjustment.sql`          | One-time data migration: apply 2-for-1 TQQQ split to pre-2025-11-20 transactions (idempotent via price > 100 check). |
| 017 | `00017_migrate_gemini_to_antigravity.sql`  | One-line data migration: `UPDATE app_settings SET value='antigravity' WHERE key='ai_provider' AND value='gemini'`. |
| 018 | `00018_signal_results.sql`                 | Create `signal_results` table (per `(instrument_id, risk_profile)`) for AI BUY/HOLD/SELL outputs. |
| 019 | `00019_ai_prompts.sql`                     | Create `ai_prompts` table; seed 9 default prompts (`signals_*`, `holdings_*`, `stock_*` × {conservative, moderate, aggressive}). |
| 020 | `00020_signal_zero1_score.sql`             | Add `signal_results.zero1_score INT NOT NULL DEFAULT 0`.                 |
| 021 | `00021_instrument_scores.sql`              | Create `instrument_scores` table — per-instrument 0–1 score + metrics JSON. |
| 022 | `00022_signal_rich_output.sql`             | Add `signal_results.key_points` and `signal_results.qualitative_note` columns. |
| 023 | `00023_fix_usfund_currency.sql`            | Data fix: set `currency='USD'` for any `US_FUND` instrument still incorrectly tagged INR. |

To add a new migration, drop a file `internal/db/migrations/NNNNN_name.sql` with the next sequential number — `go:embed` will pick it up at compile time and goose will apply it at next startup.

---

## 3. Schema reference (table by table)

> Conventions: `NUMERIC(p,s)` types are exact decimals (money or fund units); `BIGSERIAL` IDs are PG-generated `bigint`. All `created_at` / `updated_at` default to `NOW()`.

### `users` — `00001_init.sql`
Authentication identities. JWT auth wraps this; one row per real human.

| Column            | Type           | Notes                          |
|-------------------|----------------|--------------------------------|
| `id`              | BIGSERIAL PK   |                                |
| `email`           | TEXT UNIQUE    | Indexed (`idx_users_email`).   |
| `username`        | TEXT           |                                |
| `profile_picture` | TEXT           | Path under `UPLOAD_DIR`.       |
| `password_hash`   | TEXT           | bcrypt.                        |
| `created_at`, `updated_at` | TIMESTAMPTZ |                          |

### `instruments` — `00002_transactions.sql` (+ `00008`, `00009`, `00010`, `00012`)
The canonical security/fund master. Every transaction, price, snapshot, category mapping, signal, and score keys off `instrument_id`.

| Column         | Type           | Notes                                                                                                              |
|----------------|----------------|--------------------------------------------------------------------------------------------------------------------|
| `id`           | BIGSERIAL PK   |                                                                                                                    |
| `name`         | TEXT NOT NULL  | Indexed.                                                                                                           |
| `isin`         | TEXT UNIQUE    |                                                                                                                    |
| `amfi_code`    | TEXT           | Indexed; for Indian mutual funds.                                                                                  |
| `asset_type`   | TEXT CHECK     | One of `MF, ETF, STOCK, BOND, METAL, OTHER, US_FUND` after `00009`/`00010`. (Original list was `MF/ETF/STOCK/BOND/GOLD/OTHER`.) |
| `currency`     | TEXT CHECK     | `INR` or `USD`. Default `INR`. `00023` retro-fixes US_FUND rows.                                                  |
| `exchange`     | TEXT           |                                                                                                                    |
| `yahoo_symbol` | TEXT           | Added in `00008` for Yahoo Finance scraping (used for US_FUND).                                                    |

`gfinance_symbol` existed in `00002` and was dropped in `00012` once Google Finance scraping switched to ISIN/name-based lookup.

### `upload_history` — `00002_transactions.sql`
One row per uploaded broker file (Zerodha CSV, Groww XLSX, etc.) or per Gmail-driven import. Parent of `transactions.upload_id` and `import_logs.upload_id`.

Key columns: `status` ∈ `PENDING/PROCESSING/COMPLETED/FAILED/PARTIAL`, `records_total`, `records_imported`, `records_duplicates`, `records_errors`, `error_details JSONB`. Indexed on `uploaded_at DESC`.

### `transactions` — `00002_transactions.sql` (+ `00007`)
Immutable per-trade log. The source of truth for FIFO computation.

| Column                | Type           | Notes                                                                                  |
|-----------------------|----------------|----------------------------------------------------------------------------------------|
| `id`                  | BIGSERIAL PK   |                                                                                        |
| `instrument_id`       | BIGINT FK      | `ON DELETE CASCADE`.                                                                   |
| `transaction_date`    | DATE           | Indexed.                                                                                |
| `transaction_type`    | TEXT CHECK     | `BUY/SELL/SWITCH_IN/SWITCH_OUT/DIVIDEND/BONUS/SPLIT`.                                  |
| `quantity`            | NUMERIC(18,6)  |                                                                                        |
| `price`               | NUMERIC(18,6)  | In instrument's native currency.                                                       |
| `amount`              | NUMERIC(18,2)  | Total monetary value (gross).                                                          |
| `platform`            | TEXT CHECK     | `GROWW/ZERODHA/INDMONEY/MANUAL`.                                                       |
| `upload_id`           | BIGINT FK      | Nullable (manual entry).                                                               |
| `order_id`            | TEXT           | Indexed.                                                                                |
| `trade_id`            | TEXT           | Added in `00007`. Unique per `(instrument_id, trade_id, transaction_type)` when set.   |
| `original_data`       | JSONB          | Raw parsed payload from the broker file/email — kept for auditing.                     |
| `created_at`          | TIMESTAMPTZ    |                                                                                        |

Indexes: `instrument_id`, `transaction_date`, `platform`, `order_id`, dedup composite `(instrument_id, transaction_date, transaction_type, platform)`, and partial unique on `trade_id`.

### `prices` — `00003_prices.sql` (hypertable)
Daily NAVs / closing prices.

PK `(instrument_id, price_date)`. Columns: `nav_price NUMERIC(18,6)`, `source TEXT` (default `GFINANCE`, also `YAHOO`, `AMFI`, `MANUAL`), `is_converted BOOLEAN` (true if value was FX-converted before storage), `fund_name TEXT`, `fetched_at TIMESTAMPTZ`.

- Hypertable: chunk interval 30 days on `price_date`.
- Compression: enabled, segmented by `instrument_id`, policy `add_compression_policy('prices', INTERVAL '90 days')`.
- Extra index: `idx_prices_instrument_id (instrument_id, price_date DESC)` — used by the latest-price `LATERAL JOIN` (see §5).

### `portfolio_snapshots` — `00004_snapshots.sql` (hypertable)
One row per day capturing aggregate portfolio state. PK `(snapshot_date)`.

Columns: `total_invested`, `total_value`, `total_profit` (all NUMERIC(18,2)), `profit_percentage NUMERIC(8,4)`, `snapshot_details JSONB` (per-instrument breakdown blob used by the dashboard).

- Hypertable: chunk interval 90 days.
- Compression: enabled, policy 180 days.
- Upserted daily by the scheduler (`internal/jobs/`) via `snapshot_service.RecordToday()`.

#### CAGG: `cagg_monthly_returns` — `00004_snapshots.sql`
Continuous materialized view that down-samples `portfolio_snapshots` to one row per calendar month using Timescale's `last(value, time_col)` aggregate:

```sql
SELECT time_bucket('1 month', snapshot_date) AS month,
       last(total_invested,    snapshot_date)::float AS invested,
       last(total_value,       snapshot_date)::float AS value,
       last(total_profit,      snapshot_date)::float AS profit,
       last(profit_percentage, snapshot_date)::float AS profit_percent
FROM portfolio_snapshots
GROUP BY month
```

Refresh policy: every 1 hour, with `start_offset='3 months'` and `end_offset='1 day'` (today's partial day excluded). Used by the Trends page to render the monthly-returns chart without scanning every snapshot row.

### `instrument_snapshots` — `00011_instrument_snapshots.sql`
Per-instrument per-day rollup. PK `(snapshot_date, instrument_id)`. Plain table (not a hypertable). Columns: `invested`, `value`, `profit`, `profit_percent`.

Indexes: `(snapshot_date)`, `(instrument_id)`.

Populated by `SnapshotService.BackfillSnapshots()` and `SnapshotService.RecordToday()`. Used by the per-holding history charts.

### `market_data` — `00006_market_mood.sql` (hypertable)
Index-level fundamentals time series for the Market Mood feature.

PK `(index_name, price_date)`. Columns: `pe_ratio`, `pb_ratio`, `div_yield`, `source` (default `MANUAL`; also `TICKERTAPE` from the scraper).

- Hypertable: 90-day chunks on `price_date`.
- Compression: segmented by `index_name`, policy 180 days.

#### CAGG: `cagg_index_pe_stats` — `00006_market_mood.sql`
Daily average P/E per index, materialized from `market_data`:

```sql
SELECT index_name,
       time_bucket('1 day', price_date) AS day,
       avg(pe_ratio) AS pe_ratio
FROM market_data
GROUP BY index_name, day
```

Refresh: hourly, 5-year window. Currently the `market_mood.go` service reads raw `market_data` and computes percentiles in Go (`computeMood` at `internal/services/market_mood.go:323`), so the CAGG is provisioned but not yet on the read path.

### `market_index_config` — `00006_market_mood.sql`
Lookup of tracked indices. PK `index_name`. Seeded with: `NIFTY 50, NIFTY 500, SENSEX, NIFTY MIDCAP 100, NIFTY SMALLCAP 100`. `is_active BOOLEAN` lets the UI toggle indices off without losing history.

### `market_cache` — `00003_prices.sql`
Generic JSONB key-value cache with TTL. PK `cache_key`. Stores scraped MMI, precious-metal spot prices, FX rates. The fetcher checks `expires_at > NOW()` before refresh.

### `watchlist` — `00006_market_mood.sql`
User-saved symbols (free-form text — **no FK to `instruments`**, by design, so you can watch stocks you haven't bought). Columns: `id`, `symbol UNIQUE`, `created_at`.

### `categories` / `instrument_categories` — `00005_categories.sql`
Many-to-many tagging. `categories(id, name UNIQUE, description, color)`. Junction `instrument_categories(instrument_id, category_id, weight NUMERIC(5,4) DEFAULT 1.0)` with `UNIQUE(instrument_id, category_id)`. Weight allows fractional bucketing (e.g. a hybrid fund 60% to EQUITY, 40% to DEBT).

### `instrument_allocations` — `00005_categories.sql`
Per-instrument target allocation + SIP plan. UNIQUE on `instrument_id`. Columns: `target_percent`, `sip_amount`, `sip_target_percent`, `alloc_category` (CHECK ∈ EQUITY/GOLD/DEBT/US_EQUITY/OTHERS).

### `category_allocations` — `00005_categories.sql`
Top-level asset-class targets. PK on `alloc_category` (the same 5-value enum). Seeded with all 5 rows at migration time so the UI never has to handle missing categories.

### `instrument_scores` — `00021_instrument_scores.sql`
Zero1 quality score per instrument. PK `instrument_id`. `zero1_score INT`, `kind TEXT` (`mf | etf | stock`), `metrics_json JSONB` (the inputs that produced the score), `computed_at`.

### `signal_results` — `00018_signal_results.sql` (+ `00020`, `00022`)
LLM-generated buy/hold/sell signals. PK `(instrument_id, risk_profile)` so one signal per holding per profile.

| Column             | Type        | Source migration         |
|--------------------|-------------|--------------------------|
| `instrument_id`    | BIGINT FK   | `00018`                  |
| `risk_profile`     | TEXT CHECK  | `conservative/moderate/aggressive` (`00018`) |
| `action`           | TEXT        | `BUY_MORE/HOLD/PARTIAL_SELL/BOOK_PROFIT` |
| `confidence`       | INT         | `00018`                  |
| `reason`, `tax_note` | TEXT      | `00018`                  |
| `zero1_score`      | INT         | added in `00020`         |
| `key_points`       | TEXT        | added in `00022`         |
| `qualitative_note` | TEXT        | added in `00022`         |
| `generated_at`     | TIMESTAMPTZ |                          |

Index: `(risk_profile, generated_at DESC)`.

### `ai_prompts` — `00019_ai_prompts.sql`
Editable LLM prompt templates. PK `key`. Seeds 9 prompts: `signals_{conservative,moderate,aggressive}`, `holdings_{...}`, `stock_{...}`. Placeholders like `{{goal}}`, `{{horizon}}`, `{{investor_name}}` are substituted in Go.

### `app_settings` — `00014_app_settings.sql`
Generic settings store. PK `key TEXT`, `value TEXT`. Examples: `ai_provider` (`antigravity` after `00017`), investor name/goal/horizon.

### `email_imports` — `00013_email_imports.sql`
Dedup ledger for the Gmail watcher. PK `id`. `message_id TEXT UNIQUE` is the Gmail message id — guarantees we never re-import the same email. `status` ∈ `OK/SKIPPED/ERROR`, FK `upload_id → upload_history.id`.

### `email_watch_rules` — `00015_email_watch_rules.sql`
Configurable Gmail filters → parser mappings. Each row carries `from_email`, `subject_query` (raw Gmail query fragment), and `parser_type` (`groww_mf | zerodha_contract_note | indmoney_us`). Seeded with 3 defaults.

### `import_logs` — `00002_transactions.sql`
Per-row outcome of a single upload. FK to `upload_history`. `status` ∈ `IMPORTED/DUPLICATE/ERROR`. JSONB `details` carries the parsed row when it errored.

---

## 4. Time-series patterns

### Hypertable usage in practice
- **`prices`** is the hottest read table. Almost every dashboard query joins it via `LATERAL` to grab the latest NAV per instrument (see §5). The 30-day chunk interval keeps per-chunk row counts in the thousands rather than millions and lets the compression policy reclaim disk on the long tail.
- **`portfolio_snapshots`** is small (one row per day) but benefits from CAGG-backed Trends queries.
- **`market_data`** is sparse (a few hundred rows per index per year) but enables the Mood feature without burdening the rest of the schema.

### How `last()` aggregates power Trends
Timescale's `last(value, ts)` returns the value of `value` from the row with the highest `ts` in the group. The `cagg_monthly_returns` view leverages this to materialize end-of-month portfolio state in a single bucket — see the SQL in §3. Because it's a CAGG, queries against it never touch raw `portfolio_snapshots` for finalized months; the refresh policy only re-materializes the trailing 3 months window each hour.

The same `last()` pattern would let us add a `cagg_daily_holdings` later — but as of `00023` the `instrument_snapshots` table is used directly because we want per-day, not per-bucket, rows.

---

## 5. Common query patterns

### 5.1 Latest price per instrument (`LATERAL JOIN`)
The canonical pattern used by `PortfolioCalculator.BuildHoldings` (`internal/services/portfolio_calculator.go:168`):

```sql
SELECT i.id, i.name, i.isin, i.asset_type, i.currency,
       lp.nav_price, lp.price_date::text, lp.fetched_at
  FROM instruments i
  LEFT JOIN LATERAL (
    SELECT nav_price::float, price_date, fetched_at
      FROM prices
     WHERE instrument_id = i.id
     ORDER BY price_date DESC
     LIMIT 1
  ) lp ON true
 WHERE i.id = ANY($1)
```

This is the right pattern under Timescale because `(instrument_id, price_date DESC)` index → only the most recent chunk for each instrument is touched.

### 5.2 FIFO walk (in Go)
FIFO is **not** done in SQL — it's done in Go by `computeFIFOPositions()` (`internal/services/portfolio_calculator.go:63`). The function streams `transactions ORDER BY instrument_id, transaction_date` and maintains a per-instrument lot queue. The same logic appears in `ClosedPositions` (`portfolio_calculator.go:316`) which separately tracks `costOfSold` for realized-gain reporting. There is currently no plan to push this into SQL because the lot bookkeeping (bonus, split adjustments via `00016`) is easier to reason about in Go.

### 5.3 Snapshot upsert
`snapshot_service.RecordToday()` writes a single portfolio row:

```sql
INSERT INTO portfolio_snapshots
  (snapshot_date, total_invested, total_value, total_profit, profit_percentage, snapshot_details)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (snapshot_date) DO UPDATE
  SET total_invested    = EXCLUDED.total_invested,
      total_value       = EXCLUDED.total_value,
      total_profit      = EXCLUDED.total_profit,
      profit_percentage = EXCLUDED.profit_percentage,
      snapshot_details  = EXCLUDED.snapshot_details
```

And the per-instrument bulk version uses array unnest to insert N rows in one statement (`snapshot_service.go:151`):

```sql
INSERT INTO instrument_snapshots (snapshot_date, instrument_id, invested, value, profit, profit_percent)
SELECT $1, unnest($2::bigint[]), unnest($3::numeric[]), unnest($4::numeric[]),
       unnest($5::numeric[]), unnest($6::numeric[])
ON CONFLICT (snapshot_date, instrument_id) DO UPDATE
  SET invested = EXCLUDED.invested, value = EXCLUDED.value,
      profit = EXCLUDED.profit, profit_percent = EXCLUDED.profit_percent
```

### 5.4 Percentile P/E for Market Mood
`market_mood.computeMood()` (`internal/services/market_mood.go:323`) fetches the trailing 3 years of `pe_ratio` from `market_data`, sorts in Go, and computes p25/p50/p75 manually via `percentile()` at line 394. The CAGG `cagg_index_pe_stats` is provisioned but the percentile math is currently done client-side; a future optimization is to push percentile calculation server-side via `percentile_cont(0.25) WITHIN GROUP (ORDER BY pe_ratio)` against the CAGG.

### 5.5 Holdings aggregation
Holdings are not aggregated in SQL — they are built by `PortfolioCalculator.BuildHoldings` which:
1. Runs FIFO in Go.
2. Joins `instruments` with latest `prices` via the LATERAL pattern in §5.1.
3. Applies `FXService.USDToINR` for any `currency='USD'` instrument (see `00023` for why this is critical).

### 5.6 Transaction dedup
Two layers:
- **Hard** unique: partial index `(instrument_id, trade_id, transaction_type) WHERE trade_id IS NOT NULL` from `00007`.
- **Soft** dedup index: `(instrument_id, transaction_date, transaction_type, platform)` from `00002`, consulted by `internal/services/duplicate_detector.go` before insert.

---

## 6. No sqlc

Although `Makefile` defines a `sqlc` target, **sqlc is not used**. Every query is raw `pgx`:

- `pool.Query(ctx, sql, args...)`, `pool.QueryRow`, `pool.Exec`.
- Manual `rows.Scan(...)` into Go structs.
- No generated types.

Reason: the schema is small (~20 tables) and queries are mostly hand-tuned for Timescale chunk pruning. The cost of teaching sqlc about Timescale's `LATERAL`/`time_bucket`/`last()` would exceed the benefit. If you add a new query, write it inline in `internal/services/*.go` next to its caller — do not introduce sqlc partway.

---

## 7. Connection setup

Configured in `internal/db/db.go`:

```go
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
    cfg, err := pgxpool.ParseConfig(databaseURL)
    cfg.MaxConns        = 10
    cfg.MinConns        = 1
    cfg.MaxConnLifetime = time.Hour
    cfg.MaxConnIdleTime = 10 * time.Minute
    pool, _ := pgxpool.NewWithConfig(ctx, cfg)
    // 5s ping...
    return pool, nil
}
```

- **Driver**: `github.com/jackc/pgx/v5/pgxpool` (no `database/sql` in the hot path).
- **Pool sizing**: max 10 / min 1. Adequate for a single-user app; bump `MaxConns` if you ever multi-tenant this.
- **Connection reuse**: 1h max lifetime + 10m idle timeout means short-lived TLS handshakes and recovery from transient network drops.

### `DATABASE_URL` format
Standard libpq/pgx URL. Example from `.env.example`:

```
DATABASE_URL=postgres://wealthfolio:change-me-to-something-long@db:5432/wealthfolio?sslmode=disable
```

In Docker Compose, `@db:5432` resolves to the TimescaleDB container. Locally, replace with `@localhost:5432` (after `docker compose up -d db`). For managed Postgres (Neon, RDS, etc.) you'll typically want `sslmode=require`.

Config is parsed by `caarlos0/env` into `internal/config/config.go` — `DatabaseURL` is marked `required`, so a missing env var fails the server at boot before any query runs.
