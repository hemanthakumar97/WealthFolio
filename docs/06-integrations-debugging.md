# 06 — External Integrations & Debugging Playbook

This document inventories every third-party service WealthFolio talks to, the
broker file parsers it ships with, the background jobs it runs, and the most
common production failure modes with their fixes.

All file paths are relative to the repository root
(`/Volumes/Hemanth/Projects/WealthFolio`).

---

## Section 1 — External integrations

### 1.1 Yahoo Finance (`query1` / `query2`)
**File:** `internal/services/yahoo_finance.go`

| Function | Endpoint | Method |
|---|---|---|
| `Search` (`yahoo_finance.go:42`) | `https://query2.finance.yahoo.com/v1/finance/search?q=…&quotesCount=…&country=IN|US` | GET |
| `FetchQuote` (`yahoo_finance.go:115`) | `https://query1.finance.yahoo.com/v8/finance/chart/{symbol}?interval=1d&range=5d` | GET |
| `FetchHistory` (`yahoo_finance.go:152`) | `https://query1.finance.yahoo.com/v8/finance/chart/{symbol}?interval=1d&range=max&events=history` | GET |

- **Required headers** (`yahoo_finance.go:33-36`):
  - `User-Agent: Mozilla/5.0 … Chrome/124.0.0.0 Safari/537.36`
  - `Accept: application/json,text/html,*/*`
  - `Accept-Language: en-US,en;q=0.9`
- **Auth:** none (public crumb-less endpoints).
- **Response:** JSON; `Search` reads `quotes[]`, `FetchQuote` reads `chart.result[0].meta.regularMarketPrice`, `FetchHistory` reads `chart.result[0].timestamp[]` + `indicators.adjclose[0].adjclose[]` (falls back to `quote[0].close[]`).
- **Special symbols used for metals/FX:** `GC=F` (gold), `SI=F` (silver), `USDINR=X` (used in `precious_metals.go:113-116`).
- **Caching:** none at this layer. Callers cache (`market_cache` table) via `PreciousMetalsService` etc.
- **Failure modes:**
  - Non-200 → wrapped `HTTP {code}` error (`yahoo_finance.go:57,126,163`).
  - Missing price (`Meta.RegularMarketPrice == 0`) → `no price for {symbol}` (`yahoo_finance.go:145`).
  - Caller fallback differs per service — see `precious_metals.go:118-128` (fallback `usdINR = 84.0`) and `price_fetcher.go:50-52`.
- **Configured via:** No env. Live calls from `PriceFetcher.FetchAll` (cron every 15 min) and `PreciousMetalsService.fetch` (on-demand + 3h cache).

### 1.2 Tickertape — Market Mood Index
**File:** `internal/services/tickertape.go`

- **Endpoint:** `https://api.tickertape.in/mmi/now` (`tickertape.go:73`).
- **Required headers** (`tickertape.go:77-80`) — **all are mandatory** or the API returns 4xx / `success:false`:
  - `User-Agent: Mozilla/5.0 … Chrome/120.0.0.0`
  - `Origin: https://www.tickertape.in`
  - `Referer: https://www.tickertape.in/market-mood-index`
  - `Accept: application/json`
- **Auth:** none.
- **Response shape:** `{"success": bool, "data": {"currentValue": float, "indicator": float}}` (`tickertape.go:92-98`). Value falls back to `indicator` if `currentValue == 0`.
- **Caching:** `market_cache` row keyed `market:mmi` with **3-hour TTL** (`tickertape.go:14-15`). On fetch failure the service returns *stale* cache via `fromCacheAny` (`tickertape.go:59-60`) instead of erroring.
- **Mood classification thresholds** (`tickertape.go:117-130`): ≥75 Extreme Greed, ≥55 Greed, ≥45 Neutral, ≥25 Fear, else Extreme Fear.
- **Configured via:** No env. UI refresh button hits `POST /api/portfolio/market/refresh`.

### 1.3 Precious Metals (Yahoo + AI)
**File:** `internal/services/precious_metals.go`

- **Wraps Yahoo:** Goroutines fan out 4 calls in parallel (`precious_metals.go:113-117`):
  - `GC=F` quote → gold USD/oz
  - `SI=F` quote → silver USD/oz
  - `USDINR=X` quote → FX rate (fallback `84.0` if zero, line 127)
  - `GC=F` history → real 50-DMA. If history fails or has <50 points, falls back to hardcoded `goldMA50Proxy = 3300.0` (`precious_metals.go:25, 134-142`). **This proxy is stale and the source of incorrect "EUPHORIA" alerts** — see §4.2.
- **Derived values:**
  - INR price uses `taxDutyFactor = 1.10` (`precious_metals.go:22`) — 10% import duty + GST.
  - `dma50Dist > 12.0` → `isGoldExtended`; `gsr < 52` → `isGSRLow` → status escalates Neutral → CAUTION → EUPHORIA_WATCH → CRITICAL_EUPHORIA (`precious_metals.go:155-184`).
- **AI fallback** (`precious_metals.go:187, 227-292`): calls `callProviderJSON` with config from `app_settings` (provider, key, model). Generates `{alert, drivers[]}` JSON; on any failure (no API key, parse error, timeout 20 s) falls back to rule-based `buildDrivers` and `alertMessage`.
- **Caching:** `market_cache` key `market:metals`, **3-hour TTL** (`precious_metals.go:15-16`). Stale fallback identical to MMI.

### 1.4 niftyindices.com — P/E historical scraping
**File:** `internal/services/market_mood.go` (`SyncMarketData` at line 199)

- **Endpoints:**
  - Session-priming GET: `https://niftyindices.com/reports/historical-data` (`market_mood.go:209`) — required so the server sets bot-check cookies on the HTTP client.
  - Data POST: `https://www.niftyindices.com/Backpage.aspx/getpepbHistoricaldataDBtoString` (`market_mood.go:247`).
- **Required headers** (`market_mood.go:252-257`):
  - `Content-Type: application/json; charset=UTF-8`
  - `User-Agent: Mozilla/5.0 … Chrome/144.0.0.0` (`niftyUA` const at line 230)
  - `Origin: https://niftyindices.com`
  - `Referer: https://niftyindices.com/reports/historical-data`
  - `X-Requested-With: XMLHttpRequest`
  - `Accept: application/json, text/javascript, */*; q=0.01`
- **Payload:** Wrapped twice — outer `{"cinfo": "<inner-json-string>"}` where inner has `{name, startDate (DD-MMM-YYYY), endDate, indexName}` (`market_mood.go:238-244`).
- **Index-name mapping** (`market_mood.go:186-195`): DB names differ from niftyindices API names (e.g. `NIFTY SMALLCAP 50` → `NIFTY SMLCAP 50`).
- **Response:** ASP.NET wrapper `{"d": "<json-string>"}` containing array of `{DATE, pe, pb, divYield}` (`market_mood.go:270-280`).
- **Rate-limit safeguard:** 1-second sleep between indices (`market_mood.go:223-225`).
- **Caching:** Writes daily rows into `market_data` (TimescaleDB hypertable) with `ON CONFLICT (index_name, price_date) DO UPDATE`.
- **Configured via:** Cron job toggled by `enableMarketMood` flag passed to `Scheduler.Register` (`scheduler.go:88`). Manually triggered via market handler.

### 1.5 Frankfurter — USD/INR FX
**File:** `internal/services/fx.go`

- **Endpoint:** `https://api.frankfurter.app/latest?from=USD&to=INR` (`fx.go:16`).
- **Headers:** none.
- **Auth:** none.
- **Response:** `{"rates": {"INR": float}}` (`fx.go:91-100`).
- **Caching:** `market_cache` key `fx:USD:INR`, **24-hour TTL** (`fx.go:14-15`).
- **Failure fallback:** Hardcoded `84.0` returned with `nil` error (`fx.go:39-41`) so price fetching never fully breaks when offline.

### 1.6 Gmail API (OAuth2)
**Files:** `internal/services/gmail_watcher.go`, `internal/handlers/gmail_oauth.go`

- **Endpoints (Google libs):**
  - OAuth: `google.Endpoint` (auth + token URLs from `golang.org/x/oauth2/google`).
  - Gmail v1: list (`Users.Messages.List("me").Q(query).MaxResults(50)`), get (`.Get("me", id).Format("full")`), attachments (`Attachments.Get`).
- **Scope:** `gmail.GmailReadonlyScope` (`gmail_watcher.go:156`, `gmail_oauth.go:69`).
- **Auth model:** OAuth2 Authorization-Code flow.
  - User-supplied `client_id` + `client_secret` stored in `app_settings` (set via PUT `/api/settings/gmail`, `gmail_oauth.go:110`).
  - `refresh_token` obtained at Callback (`gmail_oauth.go:178`), stored in `app_settings.gmail_refresh_token` (preferred over env `GmailConfig.RefreshToken`).
  - Redirect URI computed from request host: `{scheme}://{Host}/api/gmail/callback` (`gmail_oauth.go:60`). **Must be registered in Google Cloud Console exactly**.
  - State token: `wealthfolio-gmail` (constant, `gmail_oauth.go:22`).
- **Endpoints exposed:**
  - `GET /api/settings/gmail` — Status (`gmail_oauth.go:77`)
  - `PUT /api/settings/gmail` — SaveCredentials
  - `DELETE /api/settings/gmail` — Disconnect
  - `GET /api/gmail/connect` — start OAuth
  - `GET /api/gmail/callback` — finish OAuth
  - `POST /api/gmail/test` — `DryRun` against enabled `email_watch_rules`.
- **Query builder** (`gmail_watcher.go:334-341`): `from:{from_email} {subject_query} after:{YYYY/MM/DD}` with lookback days from `GmailConfig.LookbackDays` (default 7).
- **Dedup:** Each processed message inserted into `email_imports` keyed by `message_id` (`gmail_watcher.go:600-635`). `isProcessed` short-circuits before re-parsing.
- **Configured via env:** `ZERODHA_PDF_PASSWORD` (for PDF decryption). Gmail watcher always runs — no env toggle. Credentials stored in `app_settings`.

### 1.7 Google Sheets — public CSV import
**File:** `internal/services/sheets.go`

- **Endpoint pattern:** Any Sheets URL is rewritten to `https://docs.google.com/spreadsheets/d/{id}/export?format=csv&gid={gid}` via regex extraction (`sheets.go:30-33, 79-81`).
- **Headers:** `User-Agent: Mozilla/5.0 (compatible)` (`sheets.go:47`).
- **Auth:** none — sheet must be publicly accessible (anyone-with-link viewer or "Publish to web").
- **CSV parsing** (`parseSheetCSV`, `sheets.go:88`):
  - Case-insensitive header matching. Date columns: `date|day|trade_date`. Price columns: `close|price|nav|nav_price|close price|closing price`.
  - Multiple date layouts (`sheets.go:123-126`): `2006-01-02`, `02-01-2006`, `01/02/2006`, `Jan 2, 2006`, etc.
  - Numbers with commas stripped (`sheets.go:138`); zero/negative prices skipped.
- **Caching:** none — caller decides persistence.

### 1.8 MFAPI — Indian mutual-fund NAV
**File:** `internal/services/mfapi.go`

- **Base URL:** `https://api.mfapi.in/mf` (`mfapi.go:14`).
- **Search:** `GET /search?q=…` → `[{schemeCode, schemeName}]` (`mfapi.go:30-52`).
- **History:** `GET /{amfiCode}` → `{data: [{date "DD-MM-YYYY", nav "string"}]}` (`mfapi.go:62-102`). **MFAPI returns newest-first** — `price_fetcher.go:106-108` takes `rows[0]` for latest.
- **Headers / Auth:** none.
- **Caching:** none at integration layer; the price-fetcher writes the latest NAV into the `prices` hypertable.

### 1.9 AI providers — Anthropic / OpenAI / Antigravity (Gemini)
**File:** `internal/services/signal_service.go`

`callProviderJSON` (`signal_service.go:677`) dispatches by `cfg.Provider`. All three providers and their streaming equivalents are listed below.

| Provider | Const | Default model | JSON endpoint | Streaming endpoint | Auth header |
|---|---|---|---|---|---|
| Anthropic | `anthropic` (signal_service.go:21) | `claude-sonnet-4-6` (line 34) | `https://api.anthropic.com/v1/messages` (line 695) | same w/ `stream:true` (line 946) | `x-api-key`, `anthropic-version: 2023-06-01` |
| OpenAI | `openai` (line 22) | `gpt-4o` (line 30) | `https://api.openai.com/v1/chat/completions` (line 728) | same w/ `stream:true` (line 1007) | `Authorization: Bearer …` |
| Antigravity (Gemini) | `antigravity` (line 23) | `gemini-3.1-pro-preview` (line 32) | `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key=…` (line 754) | `:streamGenerateContent?alt=sse&key=…` (line 1059) | API key in query string |

- **AIConfig source** (`signal_service.go:41-52`): loaded from `app_settings` rows `ai_provider`, `ai_api_key`, `ai_model` (set via Settings UI).
- **Max tokens:** `signalMaxTokens = 8192` (line 38).
- **JSON enforcement:** OpenAI uses `response_format: {type: json_object}` (line 722); Antigravity uses `responseMimeType: application/json` (line 766); Anthropic relies on prompt instruction.
- **Streaming format:** All three normalised to SSE `data: {"text": "..."}\n\n` chunks via `writeChunk` (line 1130) terminated with `data: [DONE]\n\n`. The Antigravity scanner buffer is increased to 1 MB for large frames (line 1093).
- **Deterministic actions:** Actions (`BUY_MORE | HOLD | SWITCH | BOOK_PROFIT`) are computed in Go from the score (`scoreToAction`, line 630); AI is only allowed to write the prose. Action is overwritten server-side after the LLM responds (line 615-620).

---

## Section 2 — Broker parsers

Five parsers live under `internal/parsers/`. Three are file-based and registered with the auto-detector; two are email-only (called directly by the Gmail watcher).

| Parser | Type | File format | Source file |
|---|---|---|---|
| `ZerodhaParser` | File | Tradebook / P&L CSV or XLSX | `parsers/zerodha.go` |
| `GrowwMFParser` | File | MF transaction statement CSV/XLSX | `parsers/groww_mf.go` |
| `IndMoneyOrderParser` | File | US-stock order report XLS | `parsers/indmoney_order.go` |
| `ZerodhaContractNoteParser` | Email | Encrypted equity contract-note PDF | `parsers/zerodha_contract_note.go` |
| `GrowwEmailParser` | Email | HTML SIP allotment email | `parsers/groww_email.go` |
| `IndMoneyEmailParser` | Email | HTML US-stock SIP confirmation | `parsers/indmoney_email.go` |

### 2.1 Detector logic (`parsers/detector.go`)
- Registry order: `ZerodhaParser`, `GrowwMFParser`, `IndMoneyOrderParser` (`detector.go:8-12`). Most-specific first.
- For each parser, `Detect(rows[i])` is tested on the **first 30 rows** (`detector.go:18-22`) — important because broker files often have account-info preamble before the real header row.
- Each parser's `Detect` uses substring matching on the normalised header line (`normHeader` lowercases, replaces `_`/`-` with spaces, collapses whitespace, `parsers/base.go:53-58`).

| Parser | Required header markers |
|---|---|
| Zerodha | `isin` AND `trade date` AND `quantity` (`zerodha.go:18-23`) |
| Groww MF | `scheme name` AND `units` AND `nav` (`groww_mf.go:17-22`) |
| IndMoney | `stock symbol` AND `broker reference id` AND `order amount` (`indmoney_order.go:21-26`) |

If no parser matches, `ErrNoParser` is returned (`detector.go:28`).

### 2.2 Known quirks

- **Zerodha contract-note PDFs are encrypted with the user's PAN.** Set `ZERODHA_PDF_PASSWORD` in env or expect `PDF decrypt/extract failed` (`gmail_watcher.go:269-270, 453-456`). The parser then walks "Annexure A" in 11-line blocks per trade (`zerodha_contract_note.go:13-39`).
- **Net total in parens** in Zerodha PDFs means "payable" (i.e. BUY). Parsing logic handles this in `extractAnnexureTrades` (`zerodha_contract_note.go`).
- **MFAPI date format** (`02-01-2006` = DD-MM-YYYY) differs from broker statements; only relevant inside `mfapi.go` but matters when reconciling.
- **Groww email** values appear in *sequential single-cell table rows*, not key/value side-by-side — DOM walking is required (`groww_email.go:14-30`). A regex fallback runs if DOM extraction yields zero records.
- **IndMoney email** uses div/span layouts, not tables — the parser strips HTML to plain text and regex-extracts (`indmoney_email.go:30-34`).
- **ISIN → asset-type heuristic** (`base.go:145-155`): `INF…` → MF, `INE…|IND…|IN9…` → STOCK.
- **Currency / amount sign:** `IndMoneyOrderParser` reads `Order Amount ($)` — caller must convert to INR via FX. File-based parsers preserve broker-native currency in `NormalizedTransaction.Currency`.
- **OrderID vs TradeID:** Both fields exist on `NormalizedTransaction` (`base.go:28-29`). Zerodha tradebooks have unique `TradeID` per partial fill while `OrderID` is shared.
- **BONUS rows** carry `qty > 0` with `price = 0`. FIFO logic in `portfolio_calculator.go:104` adds them at zero cost — this is the divergence behind the "Holdings invested-amount mismatch" bug (§4.4).

### 2.3 The Gmail-driven email parsers
The email parsers do **not** implement the `Parser` interface (which is `[][]string` based). They are called directly by `GmailWatcher.processWithRule` (`gmail_watcher.go:344-355`) which dispatches on `email_watch_rules.parser_type`:
- `groww_mf` → `GrowwEmailParser.ParseEmail`
- `zerodha_contract_note` → PDF decrypt → `ZerodhaContractNoteParser.ParseContractNote`
- `indmoney_us` → `IndMoneyEmailParser.ParseEmail`

Subject date parsing for Zerodha contract notes happens in `parsers.ParseSubjectDate` (called at `gmail_watcher.go:259, 438`); failure falls back to the email's internal date.

---

## Section 3 — Background jobs (`internal/jobs/scheduler.go`)

All cron expressions run in **`Asia/Kolkata` (IST)** as configured at `scheduler.go:25-29`. The scheduler uses `gocron.WithLocation(ist)`. Hours come from CLI / env, passed in `Register(snapshotHour, marketMoodHour, enableMarketMood, gmailWatcherHour)`.

| Job | Cron | Trigger | What it does | Source |
|---|---|---|---|---|
| Price fetch | `*/15 * * * *` | Every 15 minutes | `PriceFetcher.FetchAll` — concurrent (10-worker errgroup) fetch of latest NAV via MFAPI for AMFI-coded instruments and latest Yahoo close for `yahoo_symbol` instruments. USD prices multiplied by cached USD/INR. Publishes SSE `prices` event. | `scheduler.go:42-61` |
| Daily snapshot | `0 {snapshotHour} * * *` IST | Configurable hour, daily | `SnapshotService.CreateDailySnapshot` — computes per-instrument invested/value via the **avg-cost formula** (`snapshot_service.go:175-188`), inserts into `portfolio_snapshots`. Publishes SSE `snapshot` event. | `scheduler.go:64-85` |
| Market mood sync | `0 {marketMoodHour} * * *` IST | Always runs daily | `MarketMoodService.SyncMarketData` — visits niftyindices home page for session cookies, then POSTs per active index in `market_index_config`. Upserts into `market_data`. | `scheduler.go:88-103` |
| Gmail watcher | `0 {gmailWatcherHour} * * *` IST | Always runs daily; exits early if `app_settings.gmail_refresh_token` is empty | `GmailWatcher.Run` — for each enabled `email_watch_rules` row, lists messages (max 50) matching the rule's Gmail query, skips already-processed, dispatches to the matching email parser, imports via `ImportService.PersistRows`, records to `email_imports`. | `scheduler.go:106-120` |

`formatCron(hour)` at `scheduler.go:133-138` builds the simple `0 H * * *` expression. Invalid hours are clamped to `23`.

---

## Section 4 — Debugging playbook

### 4.1 MMI gauge empty / shows "No data"
- **Symptom:** Frontend MMI gauge stays blank, console shows 4xx, or panel says "stale".
- **Root cause:** Tickertape returns `4xx` or `success:false` if the `Origin` / `Referer` headers are missing or the CDN flags the IP.
- **Where to look:**
  - `internal/services/tickertape.go:77-80` (header set)
  - `internal/services/tickertape.go:88-104` (status + success check)
  - Log line: `tickertape: status %d` (line 89) or `tickertape: success=false` (line 103).
- **Fix / workaround:**
  1. Verify headers are still present (Tickertape sometimes adds new bot checks — try replicating headers from browser devtools).
  2. Call `POST /api/portfolio/market/refresh` to force `Refresh` (`tickertape.go:51`).
  3. Note: on any fetch failure the service auto-returns the **stale** cache (`tickertape.go:58-62`). If MMI is empty even with stale fallback, the `market_cache` row may have been wiped — check `SELECT * FROM market_cache WHERE cache_key='market:mmi'`.

### 4.2 Precious-metals card shows wrong "EUPHORIA" warning
- **Symptom:** Gold valuation says `OVERVALUED` / status `EUPHORIA_WATCH` even though gold isn't near recent highs, OR the dma_50_dist value is impossibly high (e.g. > 100%).
- **Root cause:** `goldMA50Proxy = 3300.0` (`precious_metals.go:25`) is a hardcoded fallback. It's used when the Yahoo history call for `GC=F` fails or returns fewer than 50 rows (`precious_metals.go:134-142`). As gold trends well above $3,300, the proxy makes everything look "extended".
- **Where to look:**
  - `internal/services/precious_metals.go:134-142` — the fallback branch.
  - `internal/services/precious_metals.go:155-156` — `dma50Dist` and `isGoldExtended` use the same variable.
- **Fix:** Ensure the parallel `m.yahoo.FetchHistory(ctx, "GC=F")` call succeeds (`precious_metals.go:116`). If Yahoo is blocking, the call returns an error and the proxy kicks in. Workaround: bump the proxy, or guard the alert so it only triggers when `goldMA50 != goldMA50Proxy`.

### 4.3 Dashboard "Total P/L" ≠ Trends "ALL period" P/L
- **Symptom:** The Holdings dashboard P/L and the Trends-chart ALL-period change don't agree (often the Trends value is lower or even negative when dashboard is positive).
- **Root cause:** Two different cost-basis algorithms.
  - Dashboard / live holdings uses **FIFO** lots (`portfolio_calculator.go:63-108`, `computeFIFOPositions`).
  - Daily snapshots — and therefore Trends — use the **avg-cost formula** with `units_bought` summed in SQL (`snapshot_service.go:175-188`, `computeForDate`).
- **Where to look:**
  - `internal/services/portfolio_calculator.go:316-430` (FIFO walk with `unitsSold`, `costOfSold`).
  - `internal/services/snapshot_service.go:175-188` (avg-cost SQL).
- **Fix:** For consistency the snapshot computation has to be re-expressed as FIFO. As a temporary workaround use the FIFO numbers from `/api/portfolio/holdings` as truth and treat older snapshots as approximate.

### 4.4 Holding shows wrong invested amount (BONUS units)
- **Symptom:** A holding that received a BONUS issue shows an inflated effective average buy price OR `invested: -` in the UI.
- **Root cause:** BONUS rows have `qty > 0` and `price = 0, amount = 0`. FIFO adds them as zero-cost lots (`portfolio_calculator.go:104` / `signal_service.go:323-326` — `fifoActiveLot{... units: qty}` with no amount). The avg-cost SQL in the snapshot includes BONUS units in `units_bought` but adds zero to the invested sum — dilutes avg cost.
- **Where to look:**
  - FIFO path: `internal/services/portfolio_calculator.go:104` (case `"BONUS"`).
  - Avg path: `internal/services/snapshot_service.go:188` (`SUM(... 'BUY','SWITCH_IN','BONUS' ... quantity)`).
  - Signal guard: `internal/services/signal_service.go:421-423` — holdings with `totalInvested <= 0` are *skipped* from the AI signals to avoid the `"invested: -"` UI bug.
- **Fix:** Either exclude BONUS from `units_bought` in `computeForDate` (and rely on FIFO truth) or compute a separate "effective invested" that doesn't include zero-cost units.

### 4.5 "Sync Market Data" returns nothing / 403
- **Symptom:** Manual sync or scheduled `SyncMarketData` finishes with `slog.Warn("market sync: index failed", ...)` for every index. `market_data` not updated.
- **Root cause:** niftyindices.com rejects the data POST without a prior GET to `/reports/historical-data` that primes session cookies. The single shared `http.Client` is what carries the cookie jar — but **`http.Client` does NOT keep cookies by default unless a `Jar` is set**. Verify the priming GET succeeded; if 403/captcha is returned, the headers (`Origin`, `Referer`, `User-Agent`, `X-Requested-With`) may need updating.
- **Where to look:**
  - `internal/services/market_mood.go:207-211` (priming GET).
  - `internal/services/market_mood.go:246-258` (POST headers).
  - Log: `niftyindices status %d` (line 266) or `niftyindices request: %w` (line 261).
- **Fix:** Bump the `User-Agent` (`niftyUA` const at line 230) to the latest Chrome major. Confirm the index name mapping (`apiNameMapping`, lines 186-195) covers any newly enabled `market_index_config` row. If still failing, capture the niftyindices XHR from devtools and diff headers.

### 4.6 Gmail watcher not importing
- **Symptom:** New broker emails arrive but no `email_imports` rows appear; UI shows "Gmail connected" but nothing happens.
- **Root causes & checks (in order):**
  1. **No refresh token:** Watcher always runs but exits early if `app_settings.gmail_refresh_token` is empty — logs `no Gmail refresh token configured`. Connect via Settings → Integrations.
  2. **Missing client credentials:** `gmail_client_id` / `gmail_client_secret` not in `app_settings` (`gmail_watcher.go:144-150`). Set via Settings → Integrations UI.
  3. **Refresh token expired/revoked:** Google revokes tokens unused 6+ months or after a password reset. First API call returns `invalid_grant`. Look for `gmail auth: %w` in logs.
  4. **No enabled rules:** `email_watch_rules.enabled = TRUE` rows missing — `loadRules` returns 0 (`gmail_watcher.go:312-331`).
  5. **All messages already processed:** `email_imports.message_id` already exists → skip only. Re-import requires deleting the row.
- **Fix:**
  - Reconnect via `GET /api/gmail/connect` to refresh the token.
  - Run `POST /api/gmail/test` (`gmail_oauth.go:41`) — dry-run that surfaces parse errors per email.

### 4.7 AI features (signals / stock analysis) not working
- **Symptom:** Holdings signals empty, stock analysis stream hangs or returns `… 401 …`, or precious-metals AI driver section falls back to rule-based bullets.
- **Root causes:**
  - `app_settings.ai_api_key` is empty → `fetchAIContent` exits early (`precious_metals.go:252-254`). Signal endpoints would return whatever wrapped error `callProviderJSON` produces.
  - Wrong `ai_provider` value — anything not `openai` / `antigravity` defaults to Anthropic (`signal_service.go:678-686`).
  - Wrong model for provider (e.g. `gpt-4o` set while provider is `anthropic`) → 4xx from provider API.
- **Where to look:**
  - `internal/services/signal_service.go:677-686` — `callProviderJSON` dispatch.
  - `internal/services/signal_service.go:688-715` — Anthropic call, status & body in error: `anthropic %d: %s`.
  - `internal/services/signal_service.go:718-749` — OpenAI.
  - `internal/services/signal_service.go:752-796` — Antigravity / Gemini.
- **Fix:** Verify settings in `/settings` UI. Inspect server logs for the `%d: %s` error message which contains the provider's response body. Run a curl with the same key to confirm credentials.

### 4.8 Yahoo Finance rate-limiting / blocks
- **Symptom:** Lots of `yahoo quote: HTTP 429` or `HTTP 401` in price-fetch logs; many `Failed` counts every 15 min.
- **Root cause:** Yahoo's `query1` / `query2` endpoints rate-limit aggressively on unknown clients. `PriceFetcher.FetchAll` runs an errgroup with limit 10 (`price_fetcher.go:63`) — bursts of 10 parallel calls every 15 minutes from one IP look bot-ish.
- **Where to look:**
  - `internal/services/yahoo_finance.go:33` — `User-Agent` header.
  - `internal/services/yahoo_finance.go:126, 163` — `HTTP %d` error wrappers.
  - `internal/services/price_fetcher.go:60-90` — concurrent fan-out.
- **Fix:**
  - Refresh `User-Agent` string to a current Chrome.
  - Reduce concurrency: drop `g.SetLimit(10)` to e.g. `3-4`.
  - Add a per-request `time.Sleep(200ms)` between Yahoo calls.
  - Prefer MFAPI (no rate limit) for any Indian MF; only use Yahoo for symbols that *only* exist there.
  - Long-term: consider caching `FetchHistory` (it returns full history; you only need the latest row).

---

### Quick reference — env vars touching integrations
- `DATABASE_URL` — connection string (TimescaleDB).
- `JWT_SECRET` — cookie signer.
- `TZ=Asia/Kolkata` — scheduler timezone.
- Gmail watcher always runs — credentials stored in `app_settings` (no env toggle).
- `ZERODHA_PDF_PASSWORD` — PDF password for Zerodha contract-note decryption.
- `UPLOAD_DIR` — uploaded files / profile pics.

### Quick reference — `market_cache` keys
| Key | TTL | Producer |
|---|---|---|
| `market:mmi` | 3 h | `TickertapeService` |
| `market:metals` | 3 h | `PreciousMetalsService` |
| `fx:USD:INR` | 24 h | `FXService` |
