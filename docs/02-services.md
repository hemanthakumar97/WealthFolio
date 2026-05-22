# `internal/services/` — Reference

This document is a per-file reference for the service layer. It is written for AI agents debugging or extending the code. Every claim cites `file:line`.

All services live in package `services` under `/Volumes/Hemanth/Projects/WealthFolio/internal/services/`. They are constructed once in `internal/handlers/router.go:37-67` and shared across requests. Most services hold only a `*pgxpool.Pool` and (optionally) other services they depend on — they are stateless apart from connection pools and HTTP clients.

---

## Service dependency graph

Constructed in `internal/handlers/router.go:37-67` in this exact order:

```
InstrumentService(pool)                                        router.go:37
DuplicateDetector(pool)                                        router.go:38
ImportService(pool, instrSvc, dupSvc)                          router.go:39
FXService(pool)                                                router.go:40
PriceFetcher(pool, fxSvc)                                      router.go:41
PortfolioCalculator(pool, fxSvc)                               router.go:42
SnapshotService(pool, portfolioCalc, fxSvc)                    router.go:43
GmailOAuthHandler(pool, frontendURL, GmailWatcher)             router.go:49  (watcher injected from main)
MarketMoodService(pool)                                        router.go:60
TickertapeService(pool)                                        router.go:61
PreciousMetalsService(pool, fxSvc)                             router.go:62  (note: fxSvc accepted but NOT stored — see quirks below)
BackfillService(pool, sheets, fxSvc, sseBroker emitter)        router.go:64  (constructed inside backfill handler)
SignalService(pool)                                            router.go:65
```

`GmailWatcher` is built in `cmd/server/main.go` (not router.go) so that the job scheduler and the OAuth handler share the same instance. It depends on `ImportService` (which it gets from somewhere outside router.go — agents extending Gmail flow should grep `NewGmailWatcher` to find the construction site).

Score-cache utilities (`score_cache.go`) and metric fetchers (`mf_metrics.go`, `etf_metrics.go`, `stock_metrics.go`) are package-level functions, not service structs — they take `*pgxpool.Pool` as an argument.

---

## `fx.go` — `FXService`

### Purpose
Provides one operation only: USD→INR exchange rate. Caches the rate for 24 h in the `market_cache` table so the rest of the codebase (price fetcher, portfolio calculator, snapshots, backfill, metals) all share a single hit per day. On network failure the service silently returns **84.0 INR** as a fallback so price fetching never breaks the request path (`fx.go:39-41`).

### Key types
- `FXService { pool, client *http.Client (10s timeout) }` (`fx.go:19-29`).

### Public methods
- `NewFXService(pool) *FXService` — `fx.go:24`.
- `USDToINR(ctx) (decimal.Decimal, error)` — `fx.go:33`. Reads `market_cache` row `fx:USD:INR` (`fx.go:14`); on miss/expiry calls `https://api.frankfurter.app/latest?from=USD&to=INR` (`fx.go:16`) and writes back with `expires_at = NOW() + 24h` (`fx.go:69`).

### Caching behavior
| key | TTL | source |
|---|---|---|
| `fx:USD:INR` | 24 h (`fx.go:15`) | Frankfurter API |

### Quirks / gotchas
- **Never returns an error to callers.** On API failure it returns `(84.0, nil)` (`fx.go:39-41`). Callers that check `err != nil` will silently get the fallback.
- The cached value is stored as `{"rate": "84.50"}` — a JSON-encoded string, not a number (`fx.go:68`). Cache reads parse it back with `decimal.NewFromString` (`fx.go:60`).
- A second hardcoded fallback of `84.0` exists in `portfolio_calculator.go:204` and `snapshot_service.go:181` for when `USDToINR` itself returns zero. This is defense-in-depth — currently unreachable because `USDToINR` never returns zero.

---

## `portfolio_calculator.go` — `PortfolioCalculator`

### Purpose
Computes current holdings, closed/partial positions, and a portfolio summary from `transactions` + latest `prices`. **No caching** — every call re-runs the FIFO walk (`portfolio_calculator.go:12-13`). This is the single source of truth for the live dashboard; `SnapshotService` writes a separate, simpler average-cost computation to `portfolio_snapshots` for time-series charts.

### Key types
- `PortfolioCalculator { pool, fx *FXService }` (`portfolio_calculator.go:14-17`).
- `FilterParams { Platform, AssetType, SortBy string }` — `SortBy` ∈ {"value", "pl", "name", "invested"} (`portfolio_calculator.go:24-28`).
- `HoldingInfo` (`portfolio_calculator.go:32-49`) — fields use pointers for `CurrentPrice`/`CurrentValue`/`ProfitLoss`/`ProfitLossPct` so the JSON omits them when no price is available.
- `fifoLot { qty, cost float64 }` (`portfolio_calculator.go:52-55`) — one purchase tranche; `cost` is per-unit, **not** total.
- `instrPosition { units, avgCost float64; platform string }` (`portfolio_calculator.go:57-61`).
- `ClosedPositionInfo` (`portfolio_calculator.go:290-307`) — separate type for closed/partial views.
- `AllocationItem` and `PortfolioSummary` (`portfolio_calculator.go:478-495`).

### Public methods
| method | reads | writes | external |
|---|---|---|---|
| `computeFIFOPositions(ctx)` `portfolio_calculator.go:65` | `transactions` | — | — |
| `Holdings(ctx, FilterParams)` `portfolio_calculator.go:143` | `transactions`, `instruments`, `prices` (LATERAL latest-per-instrument) | — | `fx.USDToINR` |
| `ClosedPositions(ctx, FilterParams)` `portfolio_calculator.go:309` | `transactions`, `instruments` | — | `fx.USDToINR` |
| `Summary(ctx)` `portfolio_calculator.go:497` | (delegates to Holdings) | — | — |

### Critical algorithm: FIFO cost basis

`computeFIFOPositions` (`portfolio_calculator.go:65-141`) walks every transaction in `(instrument_id, transaction_date, id)` order, maintaining a queue of `fifoLot` per instrument:

- **BUY / SWITCH_IN**: append `{qty, cost: amount/qty}` (`portfolio_calculator.go:99-103`).
- **BONUS**: append `{qty, cost: 0}` (`portfolio_calculator.go:104-107`). Bonus units lower the average cost without injecting investment.
- **SELL / SWITCH_OUT**: consume lots from the head with `0.000001` float tolerance (`portfolio_calculator.go:108-119`). Lots that are entirely consumed are dropped; partial consumption decrements `qty` in place.

Final `avgCost = Σ(qty·cost) / Σ(qty)` over surviving lots (`portfolio_calculator.go:128-138`).

#### Worked example
| Date | Type | Qty | Amount | Resulting lots |
|---|---|---|---|---|
| 2024-01 | BUY | 100 | 10 000 | `[{100, 100.0}]` |
| 2024-06 | BUY | 50 | 6 000 | `[{100, 100.0}, {50, 120.0}]` |
| 2024-09 | SELL | 80 | 9 600 | `[{20, 100.0}, {50, 120.0}]` |
| 2024-12 | BONUS | 14 | — | `[{20, 100.0}, {50, 120.0}, {14, 0}]` |

After: `units = 84`, `avgCost = (20·100 + 50·120 + 14·0) / 84 = 8000/84 ≈ 95.24`.

### Critical algorithm: USD currency conversion

In `Holdings` (`portfolio_calculator.go:220-222`):
```go
if strings.EqualFold(instr.currency, "USD") {
    pos.avgCost *= usdInrFloat
}
```
**Only `avgCost` is multiplied, not `CurrentPrice`.** That works because the prices table stores INR-converted values for USD instruments (`price_fetcher.go:77`, `is_converted = true`). `avgCost` comes from raw USD transaction amounts that were never converted, so it must be converted here. This is load-bearing — if you ever change the price-fetcher to store native-currency prices, you must change this branch too.

In `ClosedPositions` (`portfolio_calculator.go:423-426`), both `costOfSold` and `sellProceeds` are converted (since both came from raw transactions).

### Quirks / gotchas
- **₹500 invested-amount floor.** Holdings with `invested < 500` are silently dropped (`portfolio_calculator.go:225-227`). This hides SIP rounding dust but also any genuinely tiny test holdings.
- **Summary skips priceless holdings.** `Summary` only counts holdings where `CurrentValue != nil` (`portfolio_calculator.go:513-515`). If you BUY an instrument and never backfill prices, it does NOT appear in `total_invested` or `total_current_value`. Without this guard, P&L would be artificially negative for new positions. Note this diverges from `Holdings()` which DOES return the row (just without price fields).
- **PARTIAL vs CLOSED threshold.** In `ClosedPositions`, a position is "CLOSED" if remaining cost-basis `< ₹50` (`portfolio_calculator.go:416-421`), not if unit count is zero. This absorbs SIP redemption rounding.
- **ROI uses cost basis, not invested.** `ROIPct = (sellProceeds − costOfSold) / costOfSold * 100` (`portfolio_calculator.go:452-454`). It does **not** subtract held units' cost.
- `argN` helper at `portfolio_calculator.go:581-592` appears unused — historical dead code.

---

## `snapshot_service.go` — `SnapshotService`

### Purpose
Persists daily portfolio totals into `portfolio_snapshots` (one row per day) and per-instrument breakdowns into `instrument_snapshots`. Two entry points: `CreateDailySnapshot` (cron at midnight, uses live `PortfolioCalculator.Summary`) and `BackfillSnapshots` (one-shot iteration from first transaction date forward).

### Key types
- `SnapshotService { pool, calc *PortfolioCalculator, fx *FXService }` (`snapshot_service.go:17-21`).
- `SnapshotRow` for JSON response (`snapshot_service.go:27-33`).
- `instrDay { InstrumentID, Invested, Value float64 }` — per-day, per-instrument tuple (`snapshot_service.go:169-173`).

### Public methods
| method | reads | writes | external |
|---|---|---|---|
| `CreateDailySnapshot(ctx)` `snapshot_service.go:36` | (via `calc.Summary` → transactions, prices) | `portfolio_snapshots` upsert | — |
| `BackfillSnapshots(ctx)` `snapshot_service.go:83` | `transactions` (for min date), then per-day SQL | `portfolio_snapshots` + `instrument_snapshots` (bulk unnest upsert) | — |
| `computeForDate(ctx, date)` `snapshot_service.go:177` | `transactions`, `prices`, `instruments` | — | `fx.USDToINR` |
| `AllocationHistory(ctx, days)` `snapshot_service.go:233` | `instrument_snapshots`, `instruments` | — | — |
| package-level `XIRRFromTransactions(ctx, pool, currentValue)` `snapshot_service.go:281` | `transactions` | — | — |

### Critical algorithm: `computeForDate`'s invested formula

This is the **divergence point** between live and historical valuations. `computeForDate` does NOT do FIFO — it uses average cost over all buys (`snapshot_service.go:184-212`):

```sql
WITH tx_agg AS (
  SELECT instrument_id,
    SUM(qty if BUY/SWITCH_IN/BONUS) AS units_bought,
    SUM(qty if SELL/SWITCH_OUT)     AS units_sold,
    SUM(amount if BUY/SWITCH_IN)    AS total_invested,  -- BONUS excluded
    SUM(qty if BUY/SWITCH_IN)       AS buy_qty           -- BONUS excluded
  FROM transactions WHERE transaction_date <= $1
  GROUP BY instrument_id
)
SELECT instrument_id,
  CASE WHEN buy_qty > 0
       THEN (total_invested / buy_qty) * (units_bought - units_sold)
       ELSE 0
  END AS invested,                        -- avg cost * held units
  last_price.nav * (units_bought - units_sold) AS value,
  i.currency
FROM tx_agg INNER JOIN last_price ON ... INNER JOIN instruments i ...
WHERE (units_bought - units_sold) > 0.000001
```

The invested formula is `avg_buy_price * net_held_units` where `avg_buy_price = total_invested_amount / total_buy_qty`. This **diverges from the FIFO `Holdings()`** calculation. If you BUY at ₹10 (100 units) then BUY at ₹20 (100 units) then SELL 50 units:

- FIFO sees remaining lots: `[{50, 10}, {100, 20}]` → invested = 500 + 2000 = 2500.
- `computeForDate` sees: avg = 3000/200 = 15, held = 150, invested = 2250.

So **historical-trend P&L can disagree with live dashboard P&L** for any holding that has had a SELL after price changes. This is intentional (simpler & matches typical AMC statements), but agents debugging "why does the trend differ from the dashboard" should check here first.

### Critical algorithm: BONUS handling in computeForDate

`BONUS` units **count** in `units_bought` (`snapshot_service.go:188`) but contribute zero to `total_invested` and zero to `buy_qty` (`snapshot_service.go:190-191`). This dilutes avg cost correctly: for a holding with one ₹100 BUY of 10 units and a BONUS of 10 units, `units_held = 20`, `buy_qty = 10`, `total_invested = 100`, so `invested = (100/10) * 20 = 200` — i.e. the bonus appears as **doubled paper cost basis**. That is wrong economically (free units should not have cost) but is what the SQL does today. Live `Holdings()` handles this correctly via FIFO `cost=0` lots.

### Critical algorithm: XIRR cash flows

`XIRRFromTransactions` (`snapshot_service.go:281-322`) collects BUY/SWITCH_IN as **negative** outflows, SELL/SWITCH_OUT as **positive** inflows, and appends `(now, currentValue)` as the final positive inflow (`snapshot_service.go:306-315`). The returned rate is multiplied by 100 (percentage) on `snapshot_service.go:321`. Non-convergence is swallowed and 0 is returned (`snapshot_service.go:319` — silent failure).

### Quirks / gotchas
- `BackfillSnapshots` swallows per-day errors (`snapshot_service.go:103-104, 130, 162`) and continues — a broken row will appear as "no snapshot for that date" rather than an aborted job.
- Day iteration `for d := firstDate; !d.After(today); d = d.AddDate(0,0,1)` (`snapshot_service.go:100`) is O(days × instruments). For a portfolio that started 5 years ago, this issues 1825 SQL queries.
- `ist()` (`snapshot_service.go:324-326`) is duplicated in `price_fetcher.go:209`.

---

## `price_fetcher.go` — `PriceFetcher`

### Purpose
Live price fetcher run by the cron scheduler every 15 minutes. For each instrument it picks **MFAPI if `amfi_code` is set, else Yahoo if `yahoo_symbol` is set** (`price_fetcher.go:101-122`). USD instruments fetched via Yahoo get multiplied by the live USD/INR rate before storage (`price_fetcher.go:118-120`).

### Key types
- `PriceFetcher { pool, fx *FXService }` (`price_fetcher.go:16-23`).
- `FetchResult { Fetched, Failed int }` (`price_fetcher.go:25-28`).
- `instrumentStub { ID, AMFICode, YahooSymbol, Currency }` (`price_fetcher.go:30-35`).
- `PriceRow` for history responses (`price_fetcher.go:199-206`).

### Public methods
| method | reads | writes | external |
|---|---|---|---|
| `FetchAll(ctx)` `price_fetcher.go:40` | `instruments` | `prices` upsert per instrument | MFAPI, Yahoo Finance, FX |
| `FetchForInstrument(ctx, id)` `price_fetcher.go:128` | `instruments` | `prices` upsert | MFAPI / Yahoo |
| `LatestPrice(ctx, id)` `price_fetcher.go:158` | `prices` | — | — |
| `PriceHistory(ctx, id, limit)` `price_fetcher.go:171` | `prices` | — | — |

### Quirks / gotchas
- **Concurrency limit = 10** (`price_fetcher.go:63`). Bigger pool than 10 doesn't help.
- `is_converted` is `true` only for USD instruments fetched from Yahoo (`price_fetcher.go:77`). MFAPI is always in INR (`price_fetcher.go:108`) so even US-domiciled funds fetched via AMFI are flagged not-converted.
- **Today's date is in IST** (`price_fetcher.go:209`). On servers in other timezones the upsert can write tomorrow's date.
- The function loads only instruments that have `amfi_code` OR `yahoo_symbol` set (`price_fetcher.go:230-231`). Pure manual instruments are silently ignored.

---

## `mfapi.go` — `MFAPIClient`

### Purpose
Thin wrapper for `https://api.mfapi.in/mf` — used by `PriceFetcher` and `BackfillService`. No DB writes; pure HTTP.

### Public methods
- `Search(ctx, query, limit)` `mfapi.go:30` — `/search?q=...` returns `[]MFAPIMatch { SchemeCode, SchemeName }`.
- `FetchHistory(ctx, amfiCode)` `mfapi.go:62` — `/{code}` returns full NAV history; date format `02-01-2006` (DD-MM-YYYY). Sorts by Date ascending? **NO — MFAPI returns newest-first**, and this function does not re-sort. `PriceFetcher.fetchLatestPrice` relies on `rows[0]` being newest (`price_fetcher.go:106-107`). Callers that need oldest-first must sort.

### Quirks / gotchas
- `"N.A."` NAV strings are skipped (`mfapi.go:89`), as are non-positive NAVs.
- 15 s timeout (`mfapi.go:21`).

---

## `yahoo_finance.go` — `YahooClient`

### Purpose
Yahoo Finance client used everywhere a US ticker is needed (live price, history, search, precious-metals quotes). Pure HTTP, no DB.

### Public methods
- `Search(ctx, query, limit, usOnly)` `yahoo_finance.go:42` — `/v1/finance/search`. When `usOnly=false` (default for Indian users), if **any** `.NS`/`.BO` matches exist they are returned and US matches discarded (`yahoo_finance.go:106-108`). This is load-bearing for the autocomplete UX.
- `FetchQuote(ctx, symbol)` `yahoo_finance.go:115` — single regular-market-price via `/v8/finance/chart?range=5d`. Used by `PreciousMetalsService` for gold/silver/USD-INR.
- `FetchHistory(ctx, symbol)` `yahoo_finance.go:152` — full daily history via `/v8/finance/chart?range=max`. **Prefers adjusted close** (`yahoo_finance.go:197-201`), so corporate actions are factored in. Returns `[]SheetRow` (chronological order — oldest first).

### Quirks / gotchas
- No crumb-handling here — those calls are for the public `/chart` and `/search` endpoints which don't require a crumb. The crumb workflow lives in `stock_metrics.go:fetchYahooCrumb` and is only needed for `/v10/finance/quoteSummary`.
- 20 s timeout (`yahoo_finance.go:17`).

---

## `sheets.go` — `SheetsService`

### Purpose
Downloads a public Google Sheet (any share/edit/pub URL) as CSV, parses Date+Close columns into `[]SheetRow`. Used by `BackfillService` for manual price-history uploads. Also exposes `ParseUploadedCSV` for direct file uploads.

### Key types
- `SheetRow { Date time.Time, Close float64 }` (`sheets.go:15-18`).

### Public methods
- `FetchCSV(ctx, sheetURL)` `sheets.go:37` — extracts spreadsheet ID + gid via regex (`sheets.go:30,33`) and fetches `https://docs.google.com/spreadsheets/d/{id}/export?format=csv&gid={gid}`.
- `ParseUploadedCSV(r io.Reader)` `sheets.go:63`.

### Quirks / gotchas
- **Flexible date parsing.** Tries 7 formats: `2006-01-02`, `02-01-2006`, `01/02/2006`, `2/1/2006`, `02/01/2006`, `Jan 2, 2006`, `2 Jan 2006` (`sheets.go:123-126`). Note `01/02/2006` (US) and `02/01/2006` (EU/IN) are both attempted — ambiguous dates like `04/05/2024` will parse as the first matching format (US, → 5 Apr).
- Commas in numbers are stripped (`sheets.go:138`).
- Header column names are case-insensitive; accepted Date headers: `date`, `day`, `trade_date`; Close: `close`, `price`, `nav`, `nav_price`, `close price`, `closing price` (`sheets.go:104-113`).

---

## `market_mood.go` — `MarketMoodService`

### Purpose
Computes a "cheap/fair/expensive" mood for each Nifty index by comparing today's P/E against its 3-year P/E distribution. Syncs raw P/E data from niftyindices.com, persists in `market_data` (TimescaleDB hypertable). Drives the `/portfolio/market/moods` endpoint.

### Key types
- `IndexConfigItem` (`market_mood.go:27-32`) ← `market_index_config` row.
- `MarketMoodResponse` (`market_mood.go:35-47`) — current PE, 25th/50th/75th percentile, computed `Mood` ∈ {Green, Yellow, Red}.
- `IndexDetailsResponse` adds `History []PEDataPoint`.
- `MoodGreen`/`MoodYellow`/`MoodRed` constants (`market_mood.go:20-24`).

### Public methods
| method | reads | writes | external |
|---|---|---|---|
| `ListConfig(ctx)` `market_mood.go:71` | `market_index_config` | — | — |
| `ToggleIndex(ctx, name)` `market_mood.go:91` | — | `market_index_config` | — |
| `Moods(ctx)` `market_mood.go:107` | `market_data` | — | — |
| `IndexDetails(ctx, name, period)` `market_mood.go:127` | `market_index_config`, `market_data` | — | — |
| `SyncMarketData(ctx)` `market_mood.go:199` | `market_index_config` | `market_data` upsert per (index, date) | niftyindices.com (POST) |
| `IngestPE(ctx, name, date, pe, pb, divYield)` `market_mood.go:410` | — | `market_data` upsert (source=MANUAL) | — |

### Critical algorithm: mood percentile

`computeMood` (`market_mood.go:324-392`):
1. Load 3 years of P/E ratios, newest-first (`market_mood.go:326-332`).
2. `currentPE = pes[0]`.
3. Sort ascending; compute linearly-interpolated 25th/50th/75th percentile via `percentile()` (`market_mood.go:395-407`).
4. **Below 25th pct → Green** ("undervalued, add"); **above 75th pct → Red** ("overvalued, de-risk"); otherwise Yellow ("fairly valued, continue SIP") — `market_mood.go:368-377`.

Percentile interpolation uses `(p/100) * (n-1)` (`market_mood.go:399`) with linear blend between floor/ceil indices. Example with 5 sorted values `[10, 12, 14, 16, 20]`, 25th percentile: `idx = 0.25 * 4 = 1.0` → exact element `[1] = 12`.

### Critical algorithm: niftyindices.com scraping

`SyncMarketData` (`market_mood.go:199-228`) primes the session by GET-ing `/reports/historical-data` to receive cookies (`market_mood.go:208-211`, error ignored), then for each index POSTs to `/Backpage.aspx/getpepbHistoricaldataDBtoString`. The payload is a JSON-string-inside-JSON: `{"cinfo": "{\"name\":\"NIFTY 50\",...}"}` (`market_mood.go:238-244`). Headers `Origin`, `Referer`, `X-Requested-With` are mandatory (`market_mood.go:253-257`) — without them the server returns 403. The response is `{"d": "[{...}]"}` requiring two-stage JSON decode (`market_mood.go:270-280`). Rate-limited at 1 s between indices (`market_mood.go:221-225`).

`apiNameMapping` (`market_mood.go:186-195`) maps friendly DB names to the abbreviated names that the niftyindices API actually accepts (`SMLCAP` instead of `SMALLCAP`, etc.).

### Quirks / gotchas
- `SyncMarketData` fetches only the **last 365 days** every run (`market_mood.go:213-214`). Long history is only built up by repeated daily runs.
- `IndexDetails` defaults to a 365-day history; `period` parameter accepts `1M`/`3M`/`6M`/`3Y`/`5Y` (`market_mood.go:149-160`).
- `parseOptFloat` returns `nil` for `""` or `"-"` — both legitimate "no data" markers from niftyindices.

---

## `tickertape.go` — `TickertapeService`

### Purpose
Scrapes the Tickertape Market Mood Index (MMI) — a 0–100 sentiment gauge — and labels it Extreme Fear / Fear / Neutral / Greed / Extreme Greed. Used as a sanity check alongside the Nifty mood.

### Public methods
- `GetMMI(ctx)` `tickertape.go:43` — cache-first.
- `Refresh(ctx)` `tickertape.go:51` — bypass cache.

### Caching behavior
| key | TTL | source |
|---|---|---|
| `market:mmi` | 3 h (`tickertape.go:15`) | `api.tickertape.in/mmi/now` |

If the fetch fails, **stale cache is returned** (`tickertape.go:57-62`). `classifyMMI` thresholds: ≥75 Extreme Greed, ≥55 Greed, ≥45 Neutral, ≥25 Fear, else Extreme Fear (`tickertape.go:117-130`).

### Quirks / gotchas
- Both `currentValue` and `indicator` fields are accepted from the API response (`tickertape.go:94-109`) because Tickertape has changed the field name at least once.
- Mandatory headers `Origin: https://www.tickertape.in`, `Referer: https://www.tickertape.in/market-mood-index` (`tickertape.go:78-79`).

---

## `precious_metals.go` — `PreciousMetalsService`

### Purpose
Fetches live gold/silver/USD-INR from Yahoo Finance and computes derived Indian-retail-relevant indicators: per-10g and per-kg INR prices (with 10% duty proxy), Gold/Silver Ratio (GSR), and 50-day moving-average distance for euphoria detection. Optionally uses the configured AI provider to generate human-readable drivers and an alert message, falling back to rule-based output.

### Key types
- `MetalPrice { USD, INR, Valuation, ChangeLabel, IsExtended }` (`precious_metals.go:27-33`). `Valuation` ∈ {"OVERVALUED", "FAIR DEAL", "UNDERVALUED"}.
- `MetalsAlert { Active, Status, Message }` (`precious_metals.go:35-39`). `Status` ∈ {"NEUTRAL", "CAUTION", "EUPHORIA_WATCH", "CRITICAL_EUPHORIA"}.
- `MetalsData { Gold, Silver, USDINR, GSR, DMA50Dist, Alert, MarketDrivers, UpdatedAt }` (`precious_metals.go:47-56`).
- `CachedMetals { Data *MetalsData, UpdatedAt time.Time }` (`precious_metals.go:59-62`).

### Public methods
- `GetMetals(ctx)` `precious_metals.go:74` — cache-first.
- `Refresh(ctx)` `precious_metals.go:81` — bypass cache.

### Caching behavior
| key | TTL | source |
|---|---|---|
| `market:metals` | 3 h (`precious_metals.go:16`) | Yahoo (GC=F, SI=F, USDINR=X) + AI |

On any fetch failure, stale cache is returned via `fromCacheAny` (`precious_metals.go:88-90`).

### Critical algorithm: INR conversion

```
goldINR10g  = (goldUSD / 31.1034768) * 10 * usdINR * 1.10   (precious_metals.go:145)
silverINR1kg = (goldUSD / 31.1034768) * 1000 * usdINR * 1.10 (precious_metals.go:146)
```
The `1.10` factor (`taxDutyFactor`, `precious_metals.go:22`) is a coarse 10% proxy for Indian import duty + GST.

### Critical algorithm: GSR / DMA euphoria detection

- `GSR = goldUSD / silverUSD` (`precious_metals.go:150-152`). Historical mean ≈ 65; <52 = silver expensive relative to gold, >80 = silver cheap.
- 50-DMA: computed from the last 50 rows of `yahoo.FetchHistory("GC=F")` if available (`precious_metals.go:134-142`); otherwise falls back to hardcoded proxy `3300.0` (`precious_metals.go:26, 134`). **Update this constant manually** if Yahoo history fetch breaks.
- `dma50Dist = (goldUSD - goldMA50) / goldMA50 * 100`.
- Thresholds (`precious_metals.go:156-185`):
  - `isGoldExtended = dma50Dist > 12.0` → gold valuation OVERVALUED.
  - `dma50Dist < -3.0` → gold UNDERVALUED, else FAIR DEAL.
  - `gsr < 52` → silver OVERVALUED, `gsr > 80` → UNDERVALUED.
  - `alertActive = isGSRLow && isGoldExtended`; status escalates Neutral → Caution (gsr<65) → EUPHORIA_WATCH (gsr<52) → CRITICAL_EUPHORIA (gsr<52 and gold extended).

### Critical algorithm: AI drivers

`fetchAIContent` (`precious_metals.go:227-292`) reads `ai_provider`/`ai_api_key`/`ai_model` from `app_settings`, calls `callProviderJSON` (defined in `signal_service.go:677`) with a system prompt that demands strict JSON `{"alert": "...", "drivers": [{title, desc}×3]}`. **20 s timeout via `context.WithTimeout(context.Background(), ...)`** — note the `Background()`, which means request cancellation does NOT propagate (`precious_metals.go:228`). The function lenient-parses by trimming everything before the first `{` and after the last `}` (`precious_metals.go:278-284`). On any failure, returns `(nil, "")` and callers fall back to `buildDrivers` / `alertMessage` rule-based output (`precious_metals.go:188-196`).

### Quirks / gotchas
- **`fxSvc` parameter is unused.** The constructor accepts it (`precious_metals.go:70`) but never stores it (the struct has `pool` and `yahoo` only). USD/INR is fetched via Yahoo `USDINR=X` instead (`precious_metals.go:115`), with a hardcoded fallback of `84.0` (`precious_metals.go:127`) — independent of `FXService`'s fallback. If you change one fallback, change both.
- Goroutines on `precious_metals.go:113-116` use unbuffered channel reads on `precious_metals.go:118` — they will block forever on context cancellation. The 20 s budget assumes Yahoo responds.

---

## `signal_service.go` — `SignalService`

### Purpose
Build a structured JSON portfolio payload from FIFO-active holdings + scorecard metrics, then call an LLM (Anthropic/OpenAI/Antigravity) to produce per-holding investment signals. Two modes:
1. **Streaming markdown analysis** (`/ai/signal/analyse`, `/ai/signal/holdings` POST as text) — for the chat-style UI.
2. **Structured JSON signals** (`FetchHoldingsSignals`) — one decision per holding with deterministic action computed in Go, AI used only to write the explanation.

### Provider constants
`ProviderAnthropic`, `ProviderOpenAI`, `ProviderAntigravity` (`signal_service.go:20-24`). `DefaultModel` (`signal_service.go:27-36`) — OpenAI→`gpt-4o`, Antigravity→`gemini-3.1-pro-preview`, default→`claude-sonnet-4-6`. `signalMaxTokens = 8192` (`signal_service.go:38`).

### Key types
- `AIConfig { Provider, APIKey, Model }` (`signal_service.go:41-45`).
- `RiskProfile` ∈ {`conservative`, `moderate`, `aggressive`} (`signal_service.go:57-61`).
- `InvestorProfile { Name, Goal, Horizon, RiskProfile }` (`signal_service.go:64-69`). **Name is never sent to the AI** (`signal_service.go:1162` replaces it with `"the investor"`).
- `signalLot { Date, Units, Price, Amount }` (`signal_service.go:82-87`).
- `signalHolding { …, MFMetrics, ETFMetrics, StockMetrics }` (`signal_service.go:89-108`) — exactly one metrics pointer is populated.
- `HoldingSignal { Action, Confidence, Reason, KeyPoints, QualitativeNote, TaxNote, Zero1Score }` (`signal_service.go:113-123`). `Action` ∈ {BUY_MORE, HOLD, SWITCH, BOOK_PROFIT}.
- `HoldingsSignalsResult { Signals, RiskProfile, GeneratedAt }` (`signal_service.go:126-130`).
- `holdingActionMeta { score, action, hasMetrics, hasSTCG }` (`signal_service.go:655-660`).

### Public methods
| method | reads | writes | external |
|---|---|---|---|
| `SaveSignals(ctx, riskProfile, signals)` `signal_service.go:134` | — | `signal_results` (delete-then-upsert per id) | — |
| `LoadSignals(ctx, riskProfile)` `signal_service.go:181` | `signal_results`, `instruments`, `transactions` (active-FIFO subquery) | — | — |
| `BuildPortfolioPayload(ctx)` `signal_service.go:275` | `transactions`, `instruments`, `prices` | — | MFAPI (batch), Yahoo crumb (sequential) |
| `StreamHoldingsAnalysis` `signal_service.go:522` | `ai_prompts` | — | LLM streaming |
| `StreamStockAnalysis` `signal_service.go:533` | `ai_prompts` | — | LLM streaming |
| `FetchHoldingsSignals` `signal_service.go:546` | `ai_prompts` | — | LLM JSON |

### Critical algorithm: deterministic action vs AI reasoning

`FetchHoldingsSignals` (`signal_service.go:546-627`) intentionally **pre-computes the action in Go** before calling the LLM. The flow:
1. For each holding, pick `score` from whichever metrics struct is non-nil (`signal_service.go:551-559`).
2. Detect `hasSTCG`: any purchase lot date within the last 12 months (`signal_service.go:561-569`).
3. Compute `action = scoreToAction(score, riskProfile, hasSTCG)` (`signal_service.go:571`).
4. Send the pre-computed actions to the AI via `buildActionSummary` (`signal_service.go:585-589`) with the system prompt "you must justify these, not change them".
5. **Override anyway**: after parsing the AI response, `wrapper.Signals[i].Action = meta.action` (`signal_service.go:617-619`).

This guarantees that "Signal always reflects the Score" (`signal_service.go:543-545`).

#### `scoreToAction` matrix (`signal_service.go:630-653`)
| score | conservative | moderate | aggressive |
|---|---|---|---|
| ≥80 | HOLD | BUY_MORE | BUY_MORE |
| 65–79 | HOLD | HOLD | HOLD |
| 50–64 (no STCG) | SWITCH | SWITCH | SWITCH |
| 50–64 (STCG) | HOLD | HOLD | SWITCH |
| <50 (no STCG) | BOOK_PROFIT | BOOK_PROFIT | BOOK_PROFIT |
| <50 (STCG conservative) | HOLD | BOOK_PROFIT | BOOK_PROFIT |
| 0 (no metrics) | HOLD | HOLD | HOLD |

### Critical algorithm: LTCG booked-this-FY estimator

`signal_service.go:384-396` runs an approximation:
```sql
SELECT SUM(t.amount - t.quantity * avg_cost.cost)
  FROM transactions t
  JOIN (SELECT instrument_id,
               SUM(amount)/NULLIF(SUM(quantity),0) AS cost
          FROM transactions WHERE type IN ('BUY','SWITCH_IN')
         GROUP BY instrument_id) avg_cost ON ...
 WHERE type IN ('SELL','SWITCH_OUT') AND date >= fyStart
```
This is **average-cost** approximation — not FIFO and not LTCG-vs-STCG aware. Result is clamped at 0 (`signal_service.go:394-396`). Treat the value as an indicator, not a tax filing.

### Quirks / gotchas
- **Holdings without metrics are kept.** `FetchHoldingsSignals` includes holdings with `score = 0` and assigns action HOLD (`signal_service.go:631-632`).
- **`LoadSignals` filters stale results.** The subquery on `signal_service.go:193-205` requires `net_units ≥ 0.5` AND `total_buys_amount > 0` — so signals for fully exited instruments are hidden, but units between 0 and 0.5 are also hidden (rounding artifacts).
- **Holdings filter in `BuildPortfolioPayload`:** `totalUnits < 0.001` or `totalInvested ≤ 0` are skipped (`signal_service.go:414-422`). BONUS-only holdings (with no cost basis) are silently excluded from analysis.
- MF metrics are fetched **concurrently in a batch** (`signal_service.go:481-488`); stocks/ETFs are sequential because Yahoo's crumb cookie is per-process (`signal_service.go:489-500`).
- Holdings can be enriched as ETF (not MF) if they have `yahoo_symbol` but no `amfi_code` (`signal_service.go:474-479`).
- Three streaming functions duplicate boilerplate (`streamAnthropic`/`streamOpenAI`/`streamAntigravity`) — the SSE buffer for Antigravity is bumped to 1 MB at `signal_service.go:1093` to handle Gemini's larger SSE frames.
- `callProviderJSON` (`signal_service.go:677`) is shared with `precious_metals.go` for AI driver generation — both depend on the same provider dispatch.
- `setSSSEHeaders` is a typo retained for stability (`signal_service.go:1124`).

### Prompts
- `promptConservative` (`signal_service.go:1237-1287`), `promptModerate` (`signal_service.go:1291-1345`), `promptAggressive` (`signal_service.go:1349-1411`) — all loaded from DB row `holdings_{profile}` first (`signal_service.go:1148-1149`), with these as fallbacks.
- `buildSignalsJSONPrompt` (`signal_service.go:818-920`) — fallback for structured signals; DB key `signals_{profile}`.
- `buildStockAnalysisPrompt` (`signal_service.go:1168-1233`) — DB key `stock_{profile}`.

---

## `gmail_watcher.go` — `GmailWatcher`

### Purpose
Cron-driven Gmail poller that fetches new emails matching DB-defined rules and dispatches them to specific parsers (Groww allotment HTML, Zerodha encrypted-PDF contract notes, IndMoney US SIP). Each successful parse goes through `ImportService.PersistRows` and is recorded in `email_imports` for idempotency.

### Key types
- `GmailConfig { RefreshToken, LookbackDays, ZerodhaPAN }` (`gmail_watcher.go:23-28`).
- `EmailWatchRule` mirrors the `email_watch_rules` table (`gmail_watcher.go:52-60`).
- `DryRunResult`/`DryRunEmail`/`DryRunTransaction` (`gmail_watcher.go:170-195`) — used by the test endpoint.

### Public methods
| method | reads | writes | external |
|---|---|---|---|
| `Run(ctx)` `gmail_watcher.go:64` | `app_settings` (token, client id/secret), `email_watch_rules`, `email_imports` | `email_imports`, `upload_history`, `import_logs`, `transactions`, `instruments` (via ImportService) | Gmail API |
| `DryRun(ctx)` `gmail_watcher.go:204` | same reads, no writes | — | Gmail API |

### Flow per rule (`gmail_watcher.go:344-355`)
- `groww_mf` → `growwParser.ParseEmail(html)` → `ImportService.PersistRows`.
- `zerodha_contract_note` → find PDF attachment → `ExtractPDFText(bytes, ZERODHA_PAN)` → `zerodhaParser.ParseContractNote(text)` → import.
- `indmoney_us` → HTML parse → import.

### Quirks / gotchas
- **DB token overrides env token** (`gmail_watcher.go:131-139`). UI-configured tokens always win.
- Client ID/Secret are required in `app_settings` (`gmail_watcher.go:145-150`) — the env-only flow does not work.
- `MaxResults(50)` per query in production (`gmail_watcher.go:101`) and `10` in dry-run (`gmail_watcher.go:225`).
- Trade date for Zerodha defaults to the email receive date if subject parsing fails (`gmail_watcher.go:439-442`, `:260-262`).
- Idempotency is on `message_id` (`gmail_watcher.go:601-607`). If you reset `email_imports` you will re-import everything.
- `recordImport` uses `ON CONFLICT (message_id) DO NOTHING` (`gmail_watcher.go:629-631`) — so an "ERROR" status row will NOT be overwritten by a later "OK" retry. Manually delete the row to retry.
- `findPDFAttachment` (`gmail_watcher.go:503-533`) recursively walks MIME parts and detects PDFs by MIME type OR filename suffix.
- `processGrowwMessage` uses `domain.PlatformGroww` constant via the import-service; `processZerodhaMessage` uses `domain.PlatformZerodha`; `processIndMoneyMessage` uses `domain.PlatformINDMoney` (`gmail_watcher.go:391, 471, 570`).

---

## `import_service.go` — `ImportService`

### Purpose
Shared persistence layer for both HTTP CSV uploads and the Gmail watcher. Wraps the `transactions` INSERT in a loop that calls `InstrumentService.FindOrCreate` + `DuplicateDetector.IsDuplicate` + the import-log writer.

### Key types
- `ImportResult { Imported, Duplicates, Errors int; ErrorMsgs []string }` (`import_service.go:16-21`).
- `ImportService { pool, instruments *InstrumentService, duplicates *DuplicateDetector }` (`import_service.go:25-29`).

### Public methods
| method | reads | writes |
|---|---|---|
| `CreateUploadRow` `import_service.go:37` | — | `upload_history` (status=PROCESSING) |
| `FinalizeUploadRow` `import_service.go:52` | — | `upload_history` (final counts) |
| `PersistRows(ctx, uploadID, parsed, parseErrors)` `import_service.go:69` | — | `transactions`, `import_logs`, plus delegates to instrument/dup |

### Quirks / gotchas
- Parse errors are logged with status `ERROR` and counted in `Errors` (`import_service.go:77-82`).
- `uploadedBy = 0` is interpreted as NULL (`import_service.go:39-42`) — used for system Gmail imports.
- `original_data` JSON column stores the raw parsed row for debugging (`import_service.go:124`).

---

## `duplicate_detector.go` — `DuplicateDetector`

### Purpose
Per-transaction dedup check with a 3-tier matching strategy (`duplicate_detector.go:36-39`):
1. **trade_id** + instrument + type — unique per execution.
2. **order_id** + instrument + type — broker order, may span partial fills.
3. Fallback: (instrument, date, type, platform, qty±0.0001, price±0.01).

### Public methods
- `IsDuplicate(ctx, DupCheck) (bool, int64, error)` `duplicate_detector.go:40`.

### Quirks / gotchas
- The fallback tolerances are absolute, not relative (`duplicate_detector.go:81-82`). For low-priced instruments (price < ₹1) the `0.01` price tolerance can match unrelated rows.
- `order_id` matching allows multiple distinct partial fills of the same order to collide as duplicates — but only when no `trade_id` is supplied. Zerodha contract notes always provide `trade_id` to avoid this.

---

## `instrument_service.go` — `InstrumentService`

### Purpose
Find-or-create master instrument records. Lookups prefer ISIN, fall back to `(LOWER(name), asset_type)`.

### Key types
- `InstrumentSpec { Name, ISIN, AMFICode, AssetType, Currency, Exchange }` (`instrument_service.go:23-30`).

### Public methods
- `FindOrCreate(ctx, spec)` `instrument_service.go:34`.

### Quirks / gotchas
- **Currency upgrade is one-way INR→USD.** `maybePatch` (`instrument_service.go:92-103`) only sets `currency = 'USD'` if the incoming spec says USD; it never downgrades. AMFI code and exchange are filled in via COALESCE/NULLIF — once set, they aren't overwritten.
- Defaults: `AssetType=domain.AssetTypeOther`, `Currency=domain.CurrencyINR` (`instrument_service.go:35-40`).

---

## `backfill_service.go` — `BackfillService`

### Purpose
Asynchronous, **singleton** price-history backfill job (only one can run at a time, `backfill_service.go:71`). Four entry points (Google Sheet URL, MFAPI scheme code, Yahoo symbol, uploaded file). Streams log lines to a `BackfillEmitter` (SSE broker) for the UI.

### Key types
- `BackfillStatus` (`backfill_service.go:16-26`).
- `BackfillEmitter` interface `{ Emit(string) }` (`backfill_service.go:29-31`).

### Public methods
- `Status()` `backfill_service.go:58`.
- `RunFromSheet(ctx, instrumentID, sheetURL)` `backfill_service.go:70`.
- `RunFromMFAPI(ctx, instrumentID, amfiCode)` `backfill_service.go:82` — **also persists `amfi_code` to `instruments`** (`backfill_service.go:87-91`).
- `RunFromYahoo(ctx, instrumentID, symbol)` `backfill_service.go:101` — same: persists `yahoo_symbol`.
- `RunFromReader(ctx, instrumentID, r)` `backfill_service.go:118` — reads all bytes first so the HTTP handler can return.

### Quirks / gotchas
- All `Run*` methods launch `go b.run(context.Background(), ...)` — using `Background()` so the HTTP request context cancellation doesn't kill the job (`backfill_service.go:74, 93, 111, 127`).
- USD conversion uses the live FX rate at backfill time (`backfill_service.go:188-197`). Historical USD/INR is **not** used per-date — every old row gets today's rate. The `is_converted=true` flag is set on each row (`backfill_service.go:202-207`).
- `ON CONFLICT DO NOTHING` (`backfill_service.go:212`): existing rows are not updated; this is "skipped" in the log.
- Snapshot recalculation is logged but not actually triggered (`backfill_service.go:231-234`) — the `go func(){ slog.Info(...) }()` does nothing. To rebuild snapshots after backfill, manually POST `/api/trends/backfill`.

---

## `pdf_extractor.go`

### Purpose
Decrypts password-protected PDFs (pdfcpu AES-256) and extracts plain text (ledongthuc/pdf). Used by Gmail watcher for Zerodha contract notes.

### Public functions
- `ExtractPDFText(pdfBytes, password string) (string, error)` `pdf_extractor.go:17`.

### Quirks / gotchas
- Writes both encrypted and decrypted PDFs to **temp files** (`pdf_extractor.go:22, 34`) — pdfcpu's API does not support in-memory decryption.
- Tries owner+user password first, then user-only (`pdf_extractor.go:41-50`).
- Returns extracted text concatenated with newlines between pages (`pdf_extractor.go:78-81`).

---

## `score_cache.go`

### Purpose
Persists computed scorecard metrics to `instrument_scores` with a 24 h TTL (`score_cache.go:13`). `RefreshAllScores` is the batch job invoked from `/api/ai/signal/refresh-scores`.

### Public functions
| function | reads | writes | external |
|---|---|---|---|
| `LoadScore(ctx, pool, instrumentID)` `score_cache.go:25` | `instrument_scores` | — | — |
| `SaveScore(ctx, pool, instrumentID, kind, score, metrics)` `score_cache.go:44` | — | `instrument_scores` upsert | — |
| `LoadActiveInstruments(ctx, pool)` `score_cache.go:71` | `transactions`, `instruments` | — | — |
| `RefreshAllScores(ctx, pool, timeout)` `score_cache.go:108` | active instruments | `instrument_scores` | MFAPI batch + Yahoo (sequential) |

### Quirks / gotchas
- MFs go through `FetchMFMetricsBatch` (concurrent); ETFs and stocks are sequential (`score_cache.go:163`) because of the Yahoo crumb constraint.
- MFs without `amfi_code` get routed to ETF metrics (`score_cache.go:166`) — fallback path when AMFI code wasn't supplied.

---

## `metrics_common.go`

### Purpose
Shared math helpers used by `mf_metrics.go`, `etf_metrics.go`, `stock_metrics.go`.

### Public types & functions
- `ChartPoint { D string, V float64 }` (`metrics_common.go:10-13`) — JSON keys deliberately short.
- `navPoint { Date, NAV }` (internal, `metrics_common.go:16-19`).
- `valueOnOrBefore(navs, target)` — point-in-time NAV lookup (`metrics_common.go:22`).
- `rollingCAGRs(navs, days)` — windowed CAGR using last `2*days` of data (`metrics_common.go:36`).
- `annualisedStdDev(navs, since)` — std dev of daily returns × √periods-per-year, where periods-per-year is **estimated from data density** (`metrics_common.go:78-87`), defaulting to 252 if span unknown.
- `maxDrawdown(navs, since)` (`metrics_common.go:100`).
- `sampleSeries`/`drawdownSeries`/`rollingCAGRSeries` — downsample to ≤ maxPoints by `step = len/maxPoints` (`metrics_common.go:120, 149, 193`).
- `medianAnnualStdDev(navs, since)` — splits window into 1-year slices, returns median annual std dev (`metrics_common.go:241`). Used as the fund's **own** historical volatility baseline.

---

## `mf_metrics.go` — `FetchMFMetrics`

### Purpose
Compute the Zero1 by Zerodha 6-metric scorecard (rolling returns, Sharpe, std dev, beta, AUM, TER) for one mutual fund, scored 0–100 normalized to available metrics.

### Key data
- `riskFreeRate = 7.0` (`mf_metrics.go:14`) — Indian 91-day T-bill proxy.
- `knownFundData` hardcoded Beta/AUM/TER for 9 popular funds (`mf_metrics.go:42-56`). **Update quarterly.**

### Public functions
- `FetchMFMetrics(amfiCode, timeout)` `mf_metrics.go:70`.
- `FetchMFMetricsBatch(codes, timeout)` `mf_metrics.go:156` — concurrent fan-out (one goroutine per code).

### Critical algorithm: `computeZero1Score`

Total max = 90 raw pts but normalized to 100 over available metrics. Allocation (`mf_metrics.go:209-384`):

| Component | Max pts | Notes |
|---|---|---|
| Rolling 3Y CAGR | 15 | `≥24% → 15`, …, `<6% → 1` |
| 1Y Consistency | 10 | % positive 1Y windows: `≥92% → 10`, … |
| Worst 3Y rolling | 5 | downside protection |
| Sharpe 1Y | 15 | `≥1.0 → 15`, scaled down to `0` |
| Std Dev vs own 5Y median | 15 | **ratio to fund's own history** — `≤0.80 → 15`, … |
| Beta | 10 (gap if missing) | `≤0.75 → 10`, … |
| AUM | 10 (gap if missing) | **category-aware** — small cap >₹40k Cr penalized (capacity concern), `mf_metrics.go:319-352` |
| TER | 10 (gap if missing) | `<0.5% → 10`, …, `≥1.5% → 1` |

`AvailableMax` starts at 60 (rolling + Sharpe + StdDev are always computable) and grows by 10 per available scorecard input. Final = `round(s * 100 / availMax)` (`mf_metrics.go:382-383`). Missing fields are recorded in `DataGaps` and excluded from the denominator so scores aren't artificially depressed.

### Quirks / gotchas
- Requires ≥60 NAV points (`mf_metrics.go:101-103`); otherwise returns error.
- Std-dev scoring compares 1Y vs **own** 5Y median (`mf_metrics.go:277-281`) — handles broad-market volatility spikes fairly. Falls back to `categoryBenchStdDev` if no 5Y data.
- Date format: MFAPI uses `02-01-2006` (DD-MM-YYYY) (`mf_metrics.go:88`).

---

## `etf_metrics.go` — `FetchETFMetrics`

### Purpose
ETF scorecard. Uses the local `prices` table for NAV history (ETFs must be backfilled via Yahoo first) and `knownETFData` map (`etf_metrics.go:72-98`) for Beta/AUM/TER, with Yahoo `quoteSummary` as fallback.

### Public functions
- `FetchETFMetrics(ctx, instrumentID, yahooSymbol, pool, timeout)` `etf_metrics.go:102`.

### Critical algorithm: `computeETFScore` (`etf_metrics.go:225-365`)
Same structure as MF but different thresholds:
- Rolling 3Y CAGR: `≥18% → 15` (lower bar than MFs since ETFs are typically passive).
- AUM bands: `<100 Cr → 2`, `<500 Cr → 5`, `<5000 Cr → 8`, else 10.
- TER: passive funds get higher marks for ultra-low ratios — `<0.1% → 10`, `<0.2% → 9`, `<0.4% → 7`.

### Quirks / gotchas
- Requires ≥20 price rows (`etf_metrics.go:108-110`) — error message tells the user to run backfill.
- `inferETFCategory` uses substring matching against the symbol name (`etf_metrics.go:380-394`) — `contains` (`etf_metrics.go:397-415`) is a hand-rolled `strings.Contains` over multiple substrings.
- Yahoo's `totalAssets` is converted to Crores by `/1e7` (`etf_metrics.go:219`) — works for Indian ETFs in ₹ but treats US ETFs' USD values as if they were ₹. Acceptable because `knownETFData` overrides for US tickers.

---

## `stock_metrics.go` — `FetchStockMetrics`

### Purpose
Stock fundamental scorecard using Yahoo's v10 `/quoteSummary` endpoint plus our local price history. Requires a Yahoo crumb (cookie-based anti-bot token introduced 2023).

### Key data
- `sectorPE` median trailing PE per sector (`stock_metrics.go:65-77`).

### Public functions
- `FetchStockMetrics(ctx, instrumentID, yahooSymbol, pool, timeout)` `stock_metrics.go:128`.
- `FetchStockMetricsBatch(...)` `stock_metrics.go:235` — **sequential** to avoid hammering Yahoo with crumb requests.

### Critical algorithm: Yahoo crumb dance

`fetchYahooCrumb` (`stock_metrics.go:556-584`):
1. GET `https://finance.yahoo.com/` to obtain session cookies (`A1`, `A3` etc.).
2. GET `https://query2.finance.yahoo.com/v1/test/getcrumb` with those cookies — body is the crumb token.
3. Pass both crumb (querystring) and cookies (Cookie header) to subsequent `/quoteSummary` calls.

If the crumb is empty or contains "Unauthorized", an error is returned.

### Critical algorithm: `computeStockScore` (`stock_metrics.go:280-546`)

| Category | Max | Components |
|---|---|---|
| Valuation | 20 | P/E vs sector median (8), P/B (6), PEG (6) |
| Profitability | 20 | ROE (8), Net margin (6), Op margin (6) |
| Growth | 15 | Revenue YoY (8), EPS YoY (7) |
| Financial Health | 15 | D/E (10, zero-debt = full marks), Current ratio (5) |
| Returns/Momentum | 10 | 1Y return + 3Y drawdown |
| Size/Quality | 10 | Market cap + Beta |

D/E ≤ 0 is treated as "zero debt or N/A" and awarded full 10 pts (`stock_metrics.go:450-451`) — this is the financials-sector workaround (banks report D/E differently).

### Quirks / gotchas
- `yahooFloat` (`stock_metrics.go:122-125`) handles Yahoo's `{raw, fmt}` number objects.
- Percentage conversions: `dividendYield`, `profitMargins`, `returnOnEquity`, `operatingMargins`, `grossMargins`, `revenueGrowth`, `earningsGrowth` are all multiplied by 100 (`stock_metrics.go:186-197`).
- Market cap converted to Crores by `/1e7` for both `.NS`/`.BO` and other symbols (`stock_metrics.go:201-206`) — comment says "approximate" for non-Indian, since USD market cap divided by 10M is in USD-Crores not ₹.
- Stock fundamentals timeout: 20 s when called from `SignalService` (`signal_service.go:492`).

---

## `internal/xirr/xirr.go` — `Calculate`

### Purpose
Newton-Raphson XIRR. Computes annualized rate where NPV of cash flows = 0.

### Public types & functions
- `CashFlow { Date, Amount float64 }` — negative for outflows (`xirr.go:13-16`).
- `Calculate(flows []CashFlow) (float64, error)` `xirr.go:22`. Returns rate as a fraction (e.g. 0.18 for 18%). `ErrNoConvergence` after 300 iterations or NaN/zero-derivative.

### Algorithm
- Reference date = first flow's date (`xirr.go:27`).
- Time in years uses `365.25 * 24` hours (`xirr.go:29-31`).
- Initial guess: `rate = 0.1` (`xirr.go:34`).
- Iteration:
  ```
  npv  = Σ amount / (1+rate)^t
  dnpv = -Σ amount * t / ((1+rate)^t * (1+rate))
  next = rate - npv/dnpv
  if |next - rate| < 1e-7: return next
  ```
- Floor on `rate <= -1`: bump to `-0.999` (`xirr.go:61-62`) so `1+rate` stays positive.

### Quirks / gotchas
- The caller (`snapshot_service.go:281`) supplies the current portfolio value with `Date: time.Now()` — meaning XIRR includes a partial year. The `365.25` divisor is consistent for partial years.
- Non-convergence is silently swallowed (`snapshot_service.go:319`) — the API returns 0 rather than an error.

---

## Cross-cutting caching summary

| Table / TTL | Owner | Keys |
|---|---|---|
| `market_cache` 24 h | `FXService` (`fx.go:14-15`) | `fx:USD:INR` |
| `market_cache` 3 h | `TickertapeService` (`tickertape.go:14-15`) | `market:mmi` |
| `market_cache` 3 h | `PreciousMetalsService` (`precious_metals.go:15-16`) | `market:metals` |
| `instrument_scores` 24 h | `score_cache.go:13` | per `instrument_id` |
| `market_data` (no TTL — append-only) | `MarketMoodService.SyncMarketData` | per `(index, date)` |
| `prices` (append-only, latest-per-day) | `PriceFetcher`, `BackfillService` | per `(instrument, date)` |
| `portfolio_snapshots` (append-only) | `SnapshotService` | per `date` |

The portfolio calculator, FIFO, holdings, signals — none cache. They re-run the SQL on every call.

---

## Cross-cutting "load-bearing" landmines

1. **`avgCost` in `Holdings` is USD-converted, but `currentPrice` is not** (`portfolio_calculator.go:220-222`) — because the prices table already stores INR-converted USD prices. Symmetric assumption baked into `price_fetcher.go:77`.
2. **`SnapshotService.computeForDate` uses average cost, not FIFO** (`snapshot_service.go:204-205`) — historical chart will disagree with the live dashboard whenever a SELL happened.
3. **BONUS units inflate average cost in `computeForDate`** — see worked example above.
4. **`Summary` skips priceless holdings** but **`Holdings` returns them with null prices** (`portfolio_calculator.go:513-515`). Frontend code expecting `summary.TotalInvested == sum(holdings.InvestedAmount)` will be wrong by however much sits in unpriced instruments.
5. **`FetchHoldingsSignals` overrides AI's action with deterministic Go logic** (`signal_service.go:617-619`) — never debug the AI prompt for an unexpected action; trace `scoreToAction` instead.
6. **Gmail `recordImport` uses `ON CONFLICT DO NOTHING`** (`gmail_watcher.go:629-631`) — error rows are sticky; retries require manual DELETE.
7. **`BackfillService.run` uses `context.Background()`** (`backfill_service.go:74, 93, 111, 127`) — request cancellation will not stop the job. Same for `fetchAIContent` in metals (`precious_metals.go:228`).
8. **`MFAPI` returns newest-first; `Yahoo.FetchHistory` returns oldest-first.** Both are consumed by the price fetcher; `rows[0]` vs `rows[len-1]` is correct in `price_fetcher.go:107` and `:117` respectively.
9. **`PreciousMetalsService` ignores its `fxSvc` constructor parameter** (`precious_metals.go:70-72`) — uses Yahoo `USDINR=X` and its own `84.0` fallback instead.
10. **`fxSvc` fallback `84.0`** is hardcoded in **four** places: `fx.go:40`, `portfolio_calculator.go:204`, `portfolio_calculator.go:313`, `snapshot_service.go:181`, `price_fetcher.go:51, 142`, `precious_metals.go:127`, `backfill_service.go:192`. If you change one, change all.
