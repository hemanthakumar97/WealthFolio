# 04 — API Handlers (REST + SSE)

All routes live under `/api`. This document is the authoritative endpoint reference for the Go backend.
Every handler is defined under `internal/handlers/` and wired in [`router.go`](../internal/handlers/router.go).

---

## 1. Routing & Middleware

Router construction: [`internal/handlers/router.go:28-220`](../internal/handlers/router.go).

### Global middleware stack (in order)

Applied at `r := chi.NewRouter()` ([router.go:31-35](../internal/handlers/router.go)):

| Order | Middleware | Purpose |
|---|---|---|
| 1 | `middleware.RequestID` | Generates a unique `X-Request-Id` per request |
| 2 | `middleware.RealIP` | Honours `X-Forwarded-For` / `X-Real-IP` |
| 3 | `middleware.Logger` | Structured request logger to stdout |
| 4 | `middleware.Recoverer` | Catches panics → 500 |
| 5 | `middleware.Timeout(60s)` | Per-request context deadline. **Note**: AI signal endpoints override this via `signalH.withLongTimeout` (5 min) — see [signal.go:30-36](../internal/handlers/signal.go) |

### Route groups

1. **Truly public** (no middleware beyond global): `/api/health`, `/api/gmail/connect`, `/api/gmail/callback`.
2. **Auth bootstrap** (public, inside `/auth`): `/api/auth/status`, `/api/auth/setup`, `/api/auth/login`, `/api/auth/logout`.
3. **Auth-protected `/auth` subgroup**: `r.Use(deps.Signer.Required)` ([router.go:85-91](../internal/handlers/router.go)) — `/me`, `/change-password`, `/profile-picture`.
4. **Main protected group**: Everything else is wrapped in `r.Group(func(r chi.Router) { r.Use(deps.Signer.Required); ... })` ([router.go:94-206](../internal/handlers/router.go)). 401 if the JWT cookie is missing or invalid.
5. **SSE stream** `/api/events`: Wraps `deps.SSEBroker` with `deps.Signer.Required` ([router.go:71-74](../internal/handlers/router.go)).
6. **Static SPA + uploads**: `/uploads/*` serves `UploadDir`; everything else falls through to `deps.SPA` via `r.NotFound` ([router.go:210-217](../internal/handlers/router.go)).

### `auth.Signer.Required` middleware

Implementation: [`internal/auth/middleware.go:27-42`](../internal/auth/middleware.go).
1. Reads cookie named `wf_session` (`auth.CookieName`, [cookie.go:8](../internal/auth/cookie.go)).
2. Parses + verifies the JWT via `Signer.Parse`. On failure → `401 unauthorized` (plain text via `http.Error`, **not** JSON).
3. Injects `auth.Identity{UserID, Email}` into the request context. Handlers retrieve it via `auth.IdentityFrom(r.Context())`.

There is also an `Optional` variant ([middleware.go:45-56](../internal/auth/middleware.go)) — currently unused by the router but available for future routes that want soft-auth.

---

## 2. Authentication Flow

### Cookie

| Field | Value | Source |
|---|---|---|
| Name | `wf_session` | [cookie.go:8](../internal/auth/cookie.go) |
| Path | `/` | |
| `HttpOnly` | `true` | Always |
| `SameSite` | `Lax` | |
| `Secure` | `production` flag (true only when `APP_ENV=production`) | [cookie.go:19](../internal/auth/cookie.go) |
| `MaxAge` / `Expires` | 365 days (`tokenTTL`, [jwt.go:12](../internal/auth/jwt.go)) | |

`auth.SetSessionCookie` writes it after successful login/setup; `auth.ClearSessionCookie` zeroes it on logout.

### JWT contents

Defined in [`internal/auth/jwt.go:14-39`](../internal/auth/jwt.go):

```go
Claims {
  Email   string                      // "email" claim
  Subject string  (RegisteredClaims)  // user id as decimal string
  IssuedAt, ExpiresAt                 // standard
}
```

- Signed with HS256 over `JWT_SECRET`.
- TTL: 365 days.
- `Parse` returns `(userID int64, email string, error)`.

### First-run flow

1. Frontend hits `GET /api/auth/status` → `{ needs_setup: bool }` (true iff `SELECT COUNT(*) FROM users == 0`).
2. If `needs_setup`, frontend POSTs `/api/auth/setup` with `{email, password, username}`. Creates the only user. Auto-issues a session cookie. Returns `201` with the `userResponse`. Subsequent setup attempts → `409 setup already completed` ([auth.go:89-92](../internal/handlers/auth.go)).
3. Otherwise frontend POSTs `/api/auth/login` → bcrypt verifies → session cookie set, `200` with `userResponse`.
4. `GET /api/auth/me` returns the current user (uses identity from cookie).
5. `POST /api/auth/logout` clears the cookie.

### Profile management

- `PUT /api/auth/me` — partial update of email/username ([auth.go:208-258](../internal/handlers/auth.go)).
- `POST /api/auth/change-password` — verifies current password, then bcrypt-hashes new (`>=6` chars) ([auth.go:266-305](../internal/handlers/auth.go)).
- `POST /api/auth/profile-picture` — multipart `file`, max 5 MB, JPEG/PNG/GIF/WebP. Saved to `${UploadDir}/profile_pictures/<uuid>.<ext>`, served from `/uploads/profile_pictures/...` ([auth.go:308-372](../internal/handlers/auth.go)).

---

## 3. Endpoint Reference

`Auth` column: **No** = public, **Yes** = requires `wf_session` JWT cookie.
All paths are prefixed `/api`.

### 3.1 Health & SSE

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/health` | `HealthHandler.Health` ([health.go:19](../internal/handlers/health.go)) | No | DB ping with 2s timeout | — | `{"status":"ok"}` or 503 `db unhealthy` |
| GET | `/events` | `sse.Broker.ServeHTTP` wrapped by `Signer.Required` ([router.go:71-74](../internal/handlers/router.go)) | Yes | SSE stream — see §4 | — | `text/event-stream` |

### 3.2 Auth

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/auth/status` | `AuthHandler.Status` ([auth.go:58](../internal/handlers/auth.go)) | No | First-run check | — | `{needs_setup: bool, message: "ok"}` |
| POST | `/auth/setup` | `AuthHandler.Setup` ([auth.go:71](../internal/handlers/auth.go)) | No | Create the single user. 409 if any user exists | `{email, password (>=6), username}` | `userResponse` + sets cookie |
| POST | `/auth/login` | `AuthHandler.Login` ([auth.go:128](../internal/handlers/auth.go)) | No | bcrypt verify, set cookie | `{email, password}` | `userResponse` |
| POST | `/auth/logout` | `AuthHandler.Logout` ([auth.go:174](../internal/handlers/auth.go)) | No | Clear cookie | — | `{message:"logged out"}` |
| GET | `/auth/me` | `AuthHandler.Me` ([auth.go:179](../internal/handlers/auth.go)) | Yes | Current user | — | `userResponse` |
| PUT | `/auth/me` | `AuthHandler.UpdateMe` ([auth.go:208](../internal/handlers/auth.go)) | Yes | Partial update | `{email?, username?}` | `userResponse` (409 on email unique conflict) |
| POST | `/auth/change-password` | `AuthHandler.ChangePassword` ([auth.go:266](../internal/handlers/auth.go)) | Yes | | `{current_password, new_password (>=6)}` | `{message:"password updated"}` |
| POST | `/auth/profile-picture` | `AuthHandler.UploadProfilePicture` ([auth.go:308](../internal/handlers/auth.go)) | Yes | multipart `file`, ≤5 MB, JPEG/PNG/GIF/WebP | multipart | `userResponse` with new `profile_picture` path |

`userResponse` = `{id: int64, email: string, username: string?, profile_picture: string?}`

### 3.3 Portfolio

| Method | Path | Handler | Auth | Description | Query / Body | Response |
|---|---|---|---|---|---|---|
| GET | `/portfolio/summary` | `PortfolioHandler.Summary` ([portfolio.go:20](../internal/handlers/portfolio.go)) | Yes | Aggregates current value, invested, profit; appends best-effort XIRR | — | `services.PortfolioSummary` + `xirr: float?` |
| GET | `/portfolio/holdings` | `PortfolioHandler.Holdings` ([portfolio.go:38](../internal/handlers/portfolio.go)) | Yes | Live holdings | `?platform=`, `?asset_type=`, `?sort_by=` | `[]services.HoldingInfo` |
| GET | `/portfolio/closed-positions` | `PortfolioHandler.ClosedPositions` ([portfolio.go:56](../internal/handlers/portfolio.go)) | Yes | Fully exited positions w/ realized P&L | `?platform=`, `?asset_type=` | `[]services.ClosedPositionInfo` |
| POST | `/portfolio/snapshot` | `TrendsHandler.CreateSnapshot` ([trends.go:277](../internal/handlers/trends.go)) | Yes | Manually trigger today's snapshot (also runs nightly via scheduler) | — | snapshot row |

### 3.4 Trends

| Method | Path | Handler | Auth | Description | Query / Body | Response |
|---|---|---|---|---|---|---|
| GET | `/trends/portfolio` | `TrendsHandler.Portfolio` ([trends.go:26](../internal/handlers/trends.go)) | Yes | Daily snapshot series for charts. Filtered branch aggregates `instrument_snapshots`; unfiltered reads `portfolio_snapshots` | `?days=30\|90\|365\|0`, `?asset_type=MF`, `?instrument_id=` | `[{date, invested, value, profit, profit_percent}]` |
| GET | `/trends/allocation-history` | `TrendsHandler.AllocationHistory` ([trends.go:128](../internal/handlers/trends.go)) | Yes | Daily breakdown by asset type | `?days=` | `services.AllocationHistory` |
| GET | `/trends/benchmark` | `TrendsHandler.Benchmark` ([trends.go:139](../internal/handlers/trends.go)) | Yes | Yahoo history for an index | `?symbol=^NSEI` (default Nifty 50) | Yahoo history rows |
| GET | `/trends/monthly-returns` | `TrendsHandler.MonthlyReturns` ([trends.go:156](../internal/handlers/trends.go)) | Yes | Month-over-month profit, computed via TimescaleDB `last()` + `LAG()` | `?months=12` (0 = all) | `[{month, invested, value, profit, profit_percent}]` |
| POST | `/trends/backfill` | `TrendsHandler.Backfill` ([trends.go:264](../internal/handlers/trends.go)) | Yes | Rebuild all historical snapshots | — | `{status:"ok", snapshots_created:int}` |

### 3.5 Transactions

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| POST | `/transactions/` | `TransactionsHandler.Upload` ([transactions.go:65](../internal/handlers/transactions.go)) | Yes | Multipart broker file. `parsers.Detect` auto-detects format; `platform` form field overrides. Max body `uploadMaxMB` | multipart `file` + optional `platform` | `uploadResult` (`upload_id`, counts, error msgs) |
| POST | `/transactions/manual` | `TransactionsHandler.Manual` ([transactions.go:155](../internal/handlers/transactions.go)) | Yes | One-off tx entry. Returns 409 if dup (`DuplicateDetector`) | `{symbol, segment, transaction_date (YYYY-MM-DD), transaction_type, order_id, amount, quantity, platform}` | `{id, message}` |
| GET | `/transactions/history` | `TransactionsHandler.History` ([transactions.go:273](../internal/handlers/transactions.go)) | Yes | Last 100 upload runs | — | `[]uploadHistoryItem` |
| GET | `/transactions/history/{id}` | `TransactionsHandler.HistoryItem` ([transactions.go:305](../internal/handlers/transactions.go)) | Yes | One upload run, includes parsed `errors[]` | — | `uploadDetail` |
| GET | `/transactions/formats` | `TransactionsHandler.Formats` ([transactions.go:369](../internal/handlers/transactions.go)) | Yes | Supported parsers list | — | `parsers.SupportedFormats()` |
| GET | `/transactions/{id}/logs` | `TransactionsHandler.Logs` ([transactions.go:342](../internal/handlers/transactions.go)) | Yes | Per-row import logs for an upload | — | `[]importLogItem` |
| DELETE | `/transactions/{id}` | `TransactionsHandler.Delete` ([transactions.go:242](../internal/handlers/transactions.go)) | Yes | Hard delete a single tx | — | 204 / 404 |

Valid `transaction_type` ([transactions.go:416-423](../internal/handlers/transactions.go)): `BUY`, `SELL`, `SWITCH_IN`, `SWITCH_OUT`, `DIVIDEND`, `BONUS`, `SPLIT`.
Valid `platform`: `ZERODHA`, `GROWW`, `INDMONEY`, `MANUAL`.

### 3.6 Instruments

| Method | Path | Handler | Auth | Description | Query / Body | Response |
|---|---|---|---|---|---|---|
| GET | `/instruments/` | `InstrumentsHandler.List` ([instruments.go:53](../internal/handlers/instruments.go)) | Yes | LIMIT 500 | `?asset_type=`, `?search=` | `[]instrumentResponse` |
| POST | `/instruments/` | `InstrumentsHandler.Create` ([instruments.go:103](../internal/handlers/instruments.go)) | Yes | | `instrumentInput` (name required) | `instrumentResponse` (201) |
| POST | `/instruments/fetch-prices` | `InstrumentsHandler.FetchPrices` ([instruments.go:331](../internal/handlers/instruments.go)) | Yes | Trigger `PriceFetcher.FetchAll` | — | `{fetched:int, failed:int}` |
| GET | `/instruments/{id}` | `InstrumentsHandler.Get` ([instruments.go:141](../internal/handlers/instruments.go)) | Yes | With txn_count, total_units, invested_amount | — | `instrumentDetail` |
| PUT | `/instruments/{id}` | `InstrumentsHandler.Update` ([instruments.go:187](../internal/handlers/instruments.go)) | Yes | Partial | `instrumentInput` | `instrumentResponse` |
| DELETE | `/instruments/{id}` | `InstrumentsHandler.Delete` ([instruments.go:249](../internal/handlers/instruments.go)) | Yes | | — | 204 / 404 |
| GET | `/instruments/{id}/transactions` | `InstrumentsHandler.Transactions` ([instruments.go:279](../internal/handlers/instruments.go)) | Yes | DESC by date | — | `[]transactionRow` |
| GET | `/instruments/{id}/prices` | `InstrumentsHandler.Prices` ([instruments.go:310](../internal/handlers/instruments.go)) | Yes | Last 365 rows | — | `[]services.PriceRow` |

`instrumentInput`: `{name, isin, amfi_code, asset_type, currency, exchange}`.
Valid `asset_type` ([instruments.go:346-354](../internal/handlers/instruments.go)): `MF`, `ETF`, `STOCK`, `BOND`, `METAL`, `OTHER`, `US_FUND`.

### 3.7 Categories

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/categories/` | `CategoriesHandler.List` ([categories.go:57](../internal/handlers/categories.go)) | Yes | With instrument_count | — | `[]categoryResponse` |
| POST | `/categories/` | `CategoriesHandler.Create` ([categories.go:82](../internal/handlers/categories.go)) | Yes | 409 if name duplicate | `{name, description?, color?}` | `categoryResponse` (201) |
| GET | `/categories/{id}` | `CategoriesHandler.Get` ([categories.go:113](../internal/handlers/categories.go)) | Yes | | — | `categoryResponse` |
| PUT | `/categories/{id}` | `CategoriesHandler.Update` ([categories.go:138](../internal/handlers/categories.go)) | Yes | Description/color can be cleared with `""` | `categoryInput` | `categoryResponse` |
| DELETE | `/categories/{id}` | `CategoriesHandler.Delete` ([categories.go:194](../internal/handlers/categories.go)) | Yes | | — | 204 / 404 |
| GET | `/categories/{id}/instruments` | `CategoriesHandler.ListInstruments` ([categories.go:212](../internal/handlers/categories.go)) | Yes | | — | `[]categoryInstrumentResponse` |
| POST | `/categories/{id}/instruments` | `CategoriesHandler.AddInstrument` ([categories.go:242](../internal/handlers/categories.go)) | Yes | Upsert (weight defaults to 1.0) | `{instrument_id, weight}` | `{instrument_id, category_id, weight}` (201) |
| DELETE | `/categories/{id}/instruments/{instr_id}` | `CategoriesHandler.RemoveInstrument` ([categories.go:278](../internal/handlers/categories.go)) | Yes | | — | 204 / 404 |

### 3.8 Allocations

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/allocations/` | `AllocationsHandler.Overview` ([allocations.go:61](../internal/handlers/allocations.go)) | Yes | Computes current vs. target per instrument and per `alloc_category` | — | `allocationOverview{total_value, total_sip, instrument_allocations[], category_allocations[]}` |
| PUT | `/allocations/instruments/{id}` | `AllocationsHandler.UpdateInstrument` ([allocations.go:198](../internal/handlers/allocations.go)) | Yes | Upsert `instrument_allocations` | `{target_percent?, sip_amount?, sip_target_percent?, alloc_category?}` (all optional pointers) | updated `instrumentAllocationResponse` |
| PUT | `/allocations/categories/{name}` | `AllocationsHandler.UpdateCategory` ([allocations.go:262](../internal/handlers/allocations.go)) | Yes | `name` must be one of `EQUITY/GOLD/DEBT/US_EQUITY/OTHERS` | `{target_percent?, sip_target_percent?, sip_amount?}` | `categoryAllocationResponse` |
| POST | `/allocations/calculate-distribution` | `AllocationsHandler.CalculateDistribution` ([allocations.go:315](../internal/handlers/allocations.go)) | Yes | Split a lump-sum across instruments by current target_percent weights | `{amount > 0}` | `{amount, items[{instrument_id, instrument_name, alloc_category, target_percent, amount}], total_target}` |

### 3.9 AI Signal

All `/ai/signal/*` endpoints require AI provider configured via `/api/settings/ai`. If not configured → 503 `AI provider not configured`. Three endpoints replace the global 60s timeout with a 5-minute one via `signalH.withLongTimeout` ([signal.go:30](../internal/handlers/signal.go)): `POST holdings`, `POST analyse`, `POST refresh-scores`.

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/ai/signal/holdings` | `SignalHandler.GetHoldings` ([signal.go:39](../internal/handlers/signal.go)) | Yes | Load previously-saved signals from DB | `?risk_profile=conservative\|moderate\|aggressive` (default moderate) | `HoldingsSignalsResult` |
| POST | `/ai/signal/holdings` | `SignalHandler.AnalyseHoldings` ([signal.go:65](../internal/handlers/signal.go)) | Yes (5min) | Run AI analysis on all active holdings, save, return reloaded | `{risk_profile, goal, horizon}` | `HoldingsSignalsResult` (422 if no holdings) |
| POST | `/ai/signal/analyse` | `SignalHandler.AnalyseStock` ([signal.go:119](../internal/handlers/signal.go)) | Yes (5min) | **Streams** raw text from the AI provider directly to the response — see §4.3 | `{query, risk_profile}` | streaming text body |
| GET | `/ai/signal/instrument-metrics/{instrument_id}` | `SignalHandler.GetInstrumentMetrics` ([signal.go:151](../internal/handlers/signal.go)) | Yes | Scorecard for any instrument. 24h cache via `services.LoadScore`; cache hits set `X-Score-Source: cache`. Dispatches to `FetchMFMetrics`/`FetchETFMetrics`/`FetchStockMetrics` based on asset_type | — | `{kind: "mf"\|"etf"\|"stock", metrics: {...}}` |
| POST | `/ai/signal/refresh-scores` | `SignalHandler.RefreshScores` ([signal.go:224](../internal/handlers/signal.go)) | Yes (5min) | Batch recompute & cache scores for all active holdings | — | `services.RefreshAllScoresResult` |
| GET | `/ai/signal/mf-metrics/{instrument_id}` | `SignalHandler.GetMFMetrics` ([signal.go:230](../internal/handlers/signal.go)) | Yes | Backward-compat alias → `GetInstrumentMetrics` | — | same |

### 3.10 AI Prompts

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/ai/prompts/` | `AIPromptsHandler.List` ([ai_prompts.go:26](../internal/handlers/ai_prompts.go)) | Yes | All prompt templates from `ai_prompts` table | — | `[{key, content, updated_at}]` |
| PUT | `/ai/prompts/{key}` | `AIPromptsHandler.Update` ([ai_prompts.go:58](../internal/handlers/ai_prompts.go)) | Yes | Upsert by key | `{content: non-empty string}` | updated item |

### 3.11 Backfill (price history)

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| POST | `/backfill/start` | `BackfillHandler.Start` ([backfill.go:207](../internal/handlers/backfill.go)) | Yes | Sheets URL **or** uploaded file. Logs streamed to SSE `backfill` event | multipart: `instrument_id`, either `sheet_url` or `file` | 202 `{status:"started"}` (409 if a job is already running) |
| GET | `/backfill/status` | `BackfillHandler.Status` ([backfill.go:242](../internal/handlers/backfill.go)) | Yes | Current job state | — | `services.BackfillStatus` |
| GET | `/backfill/auto-search` | `BackfillHandler.AutoSearch` ([backfill.go:26](../internal/handlers/backfill.go)) | Yes | Unified MFAPI/Yahoo lookup. Picks source by asset_type, exchange, currency | `?instrument_id=` | `[]AutoSearchResult{source, code, name, exchange, exch_disp}` |
| POST | `/backfill/start-auto` | `BackfillHandler.StartAuto` ([backfill.go:97](../internal/handlers/backfill.go)) | Yes | Start backfill from MFAPI scheme code or Yahoo symbol | `{instrument_id, source: "mfapi"\|"yahoo", code}` | 202 `{status:"started"}` |
| GET | `/backfill/mfapi/search` | `BackfillHandler.SearchMFAPI` ([backfill.go:122](../internal/handlers/backfill.go)) | Yes | Proxies `api.mfapi.in` search | `?q=` | `[]services.MFAPIMatch` (502 on upstream error) |
| POST | `/backfill/mfapi/start` | `BackfillHandler.StartFromMFAPI` ([backfill.go:170](../internal/handlers/backfill.go)) | Yes | Start via known AMFI code | `{instrument_id, amfi_code}` | 202 |
| PUT | `/backfill/instruments/{id}/amfi-code` | `BackfillHandler.UpdateAMFICode` ([backfill.go:142](../internal/handlers/backfill.go)) | Yes | Save code without starting a job | `{amfi_code}` | `{amfi_code}` |

### 3.12 Market Mood

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/portfolio/market/config` | `MarketHandler.Config` ([market.go:33](../internal/handlers/market.go)) | Yes | Index toggle config | — | `[]services.IndexConfigItem` |
| POST | `/portfolio/market/config/{index_name}/toggle` | `MarketHandler.ToggleConfig` ([market.go:45](../internal/handlers/market.go)) | Yes | Toggle enabled flag. `+` decoded to space | — | updated item / 404 |
| GET | `/portfolio/market/moods` | `MarketHandler.Moods` ([market.go:63](../internal/handlers/market.go)) | Yes | Latest mood per enabled index | — | `[]services.MarketMoodResponse` |
| GET | `/portfolio/market/history/{index_name}` | `MarketHandler.IndexHistory` ([market.go:75](../internal/handlers/market.go)) | Yes | Index P/E/PB/div history | `?period=1M\|6M\|1Y\|5Y\|MAX` (default 1Y) | `services.IndexDetails` |
| GET | `/portfolio/market/mmi` | `MarketHandler.MMI` ([market.go:91](../internal/handlers/market.go)) | Yes | Cached Tickertape Market-Mood-Index | — | `{data, updated_at}` |
| GET | `/portfolio/market/metals` | `MarketHandler.Metals` ([market.go:102](../internal/handlers/market.go)) | Yes | Cached gold/silver | — | `{data, updated_at}` |
| POST | `/portfolio/market/refresh` | `MarketHandler.Refresh` ([market.go:113](../internal/handlers/market.go)) | Yes | Force-refresh MMI + metals caches | — | `{mmi_updated, metals_updated, refreshed_at}` |
| POST | `/portfolio/market/sync` | `MarketHandler.Sync` ([market.go:127](../internal/handlers/market.go)) | Yes | Trigger full mood-data sync (Yahoo + scraping) | — | `{message:"sync triggered"}` |
| POST | `/portfolio/market/ingest-pe` | `MarketHandler.IngestPE` ([market.go:145](../internal/handlers/market.go)) | Yes | Manual P/E entry | `{index_name, date (YYYY-MM-DD), pe_ratio?, pb_ratio?, div_yield?}` | 201 `{message:"ingested"}` |
| GET | `/portfolio/market/watchlist` | `MarketHandler.ListWatchlist` ([market.go:175](../internal/handlers/market.go)) | Yes | | — | `[]watchlistItem` |
| POST | `/portfolio/market/watchlist` | `MarketHandler.AddWatchlist` ([market.go:199](../internal/handlers/market.go)) | Yes | Upsert by symbol | `{symbol}` | `watchlistItem` (201) |
| DELETE | `/portfolio/market/watchlist/{symbol}` | `MarketHandler.RemoveWatchlist` ([market.go:223](../internal/handlers/market.go)) | Yes | | — | 204 / 404 |

### 3.13 Settings (Gmail, Email rules, AI)

| Method | Path | Handler | Auth | Description | Request | Response |
|---|---|---|---|---|---|---|
| GET | `/settings/gmail` | `GmailOAuthHandler.Status` ([gmail_oauth.go:77](../internal/handlers/gmail_oauth.go)) | Yes | OAuth status; masked client secret | — | `{configured, connected, email, client_id, masked_secret}` |
| PUT | `/settings/gmail` | `GmailOAuthHandler.SaveCredentials` ([gmail_oauth.go:110](../internal/handlers/gmail_oauth.go)) | Yes | Client ID required; secret only updated if non-masked | `{client_id, client_secret}` | status payload |
| DELETE | `/settings/gmail` | `GmailOAuthHandler.Disconnect` ([gmail_oauth.go:200](../internal/handlers/gmail_oauth.go)) | Yes | Delete refresh token + email | — | `{disconnected:true}` |
| GET | `/settings/email-rules` | `EmailRulesHandler.List` ([email_rules.go:30](../internal/handlers/email_rules.go)) | Yes | | — | `[]emailWatchRule` |
| POST | `/settings/email-rules` | `EmailRulesHandler.Create` ([email_rules.go:60](../internal/handlers/email_rules.go)) | Yes | parser_type must be `groww_mf\|zerodha_contract_note\|indmoney_us` | `{name, platform, from_email, subject_query, parser_type}` | rule (201) |
| PUT | `/settings/email-rules/{id}` | `EmailRulesHandler.Update` ([email_rules.go:95](../internal/handlers/email_rules.go)) | Yes | Either `enabled` or `name` only | `{enabled?, name?}` | rule |
| DELETE | `/settings/email-rules/{id}` | `EmailRulesHandler.Delete` ([email_rules.go:134](../internal/handlers/email_rules.go)) | Yes | | — | 204 / 404 |
| GET | `/settings/ai` | `AISettingsHandler.Get` ([ai_settings.go:32](../internal/handlers/ai_settings.go)) | Yes | Returns masked key (`first8 + ••••••••`) | — | `{provider, model, masked_key, configured}` |
| PUT | `/settings/ai` | `AISettingsHandler.Put` ([ai_settings.go:51](../internal/handlers/ai_settings.go)) | Yes | provider must be `anthropic\|openai\|antigravity` (or empty). Empty `api_key` preserves stored key (model-only update) | `{provider, api_key, model}` | settings |

### 3.14 Gmail OAuth (public callbacks) + test

| Method | Path | Handler | Auth | Description |
|---|---|---|---|---|
| GET | `/gmail/connect` | `GmailOAuthHandler.Connect` ([gmail_oauth.go:143](../internal/handlers/gmail_oauth.go)) | **No** | Redirects to Google OAuth consent. 400 if creds not configured |
| GET | `/gmail/callback` | `GmailOAuthHandler.Callback` ([gmail_oauth.go:159](../internal/handlers/gmail_oauth.go)) | **No** | Google redirects back here. Exchanges code, persists refresh token + Gmail address, then 302 redirects to `${FrontendURL}/settings?tab=integrations&gmail=connected`. State check: must equal `wealthfolio-gmail` |
| POST | `/gmail/test` | `GmailOAuthHandler.TestRun` ([gmail_oauth.go:41](../internal/handlers/gmail_oauth.go)) | Yes (sits inside the protected group, [router.go:178](../internal/handlers/router.go)) | Dry-run the Gmail watcher — returns what would be imported |

> Note: `Connect`/`Callback` are public because Google needs to redirect to `/api/gmail/callback` without a session cookie. State validation provides CSRF protection.

---

## 4. Server-Sent Events

### 4.1 Broker

Implementation: [`internal/sse/broker.go`](../internal/sse/broker.go). Single-process, in-memory fan-out. `Publish(eventType, data)` is non-blocking — slow consumers are dropped (channel buffer 64).

Endpoint: `GET /api/events`. Auth-required (wrapped by `Signer.Required` at [router.go:72](../internal/handlers/router.go)). Wire format per message:

```
event: <type>
data: <json>

```

A `: ping` heartbeat is sent on connect.

### 4.2 Published event types

| Event type | Source | Payload | When |
|---|---|---|---|
| `backfill` | `sseEmitter.Emit` ([backfill.go:188](../internal/handlers/backfill.go)) | `{log: string}` | During `BackfillService.RunFromSheet/Reader/MFAPI/Yahoo` — each progress line |
| `prices` | `jobs/scheduler.go:56` | `{fetched: int, failed: int}` | After the 15-min scheduled `PriceFetcher.FetchAll` run |
| `snapshot` | `jobs/scheduler.go:80` | `services.PortfolioSnapshot` | After the nightly snapshot job |

### 4.3 Streaming endpoint (not via broker)

`POST /api/ai/signal/analyse` ([signal.go:119](../internal/handlers/signal.go)) streams the AI provider's response **directly to the response writer** via `SignalService.StreamStockAnalysis` → `streamFromProvider` ([signal_service.go:533](../internal/services/signal_service.go)). This is not a fan-out broker event — it's a one-shot stream tied to the request. Errors before any bytes are written fall back to `writeError`.

---

## 5. Error Handling

### Helpers ([internal/handlers/util.go](../internal/handlers/util.go))

```go
writeJSON(w, status, body)   // sets Content-Type, encodes JSON
writeError(w, status, msg)   // writes {"detail": msg}
decodeJSON(r, &dst)          // DisallowUnknownFields() — strict
```

All JSON error responses use the shape `{"detail": "<message>"}`. The one exception is the `Signer.Required` middleware, which uses `http.Error` to write plain text `"unauthorized\n"` for 401s ([middleware.go:31](../internal/auth/middleware.go), [middleware.go:36](../internal/auth/middleware.go)).

### Status code conventions

| Code | Used for |
|---|---|
| 200 | Default success |
| 201 | `Setup`, `Create*`, `AddInstrument`, `AddWatchlist`, `IngestPE` |
| 202 | Async work accepted: all `/backfill/start*` endpoints |
| 204 | All `Delete*` endpoints on success |
| 400 | Invalid body, missing required field, malformed `chi.URLParam`, invalid enum |
| 401 | Missing/invalid JWT cookie (middleware); also `Login` invalid creds, `ChangePassword` current-password mismatch |
| 404 | `pgx.ErrNoRows` or 0 `RowsAffected` on resource lookups/deletes |
| 409 | Setup already completed; unique constraint violations (`email`, category `name`); `Manual` duplicate transaction; backfill job already running (`svc.Run*` returns "job in progress") |
| 422 | `UnprocessableEntity` — semantically valid but rejected: no active holdings (`AnalyseHoldings`), stock missing Yahoo symbol, scorecard not supported for asset type |
| 500 | Generic internal errors — DB query/scan failures, `bcrypt`, file IO |
| 502 | Upstream proxy failure — `SearchMFAPI` upstream error |
| 503 | DB down (`/health`), AI provider not configured (`/ai/signal/*`), Gmail watcher not configured (`/gmail/test`) |

### Common pitfalls

- `decodeJSON` uses `DisallowUnknownFields()` — sending extra JSON keys returns **400 invalid body**. Frontend must match struct exactly.
- `dec.DisallowUnknownFields()` means optional pointer fields (e.g. `target_percent *float64`) must be omitted entirely when not setting, not sent as `null`-only with extra siblings.
- Many handlers respond with `writeError(w, 500, err.Error())` — raw DB errors leak through. Don't rely on these as a stable contract.
- `Update*` handlers use a fragile pattern: pgx `pgx.ErrNoRows` is checked via `err == pgx.ErrNoRows`, not `errors.Is`. If the underlying driver wraps the error this branch is missed.
- 401 from `Signer.Required` is **plain text**, not JSON — front-end must handle both shapes when surfacing auth errors.

---

## 6. Adding a New Endpoint

1. Pick or create a handler file in `internal/handlers/`.
2. Define a `XxxHandler` struct with the deps you need (usually `*pgxpool.Pool` + services).
3. Add a constructor `NewXxxHandler(...)` returning `*XxxHandler`.
4. Add the handler method `func (h *XxxHandler) Foo(w http.ResponseWriter, r *http.Request)`. Use `decodeJSON`, `writeJSON`, `writeError`, `chi.URLParam`, `auth.IdentityFrom`.
5. In `router.go`:
   - Instantiate it inside `NewRouter` (alongside the other `NewXxxHandler(...)` calls, ~line 51-67).
   - Add the route inside the protected group (`r.Group(func(r chi.Router) { r.Use(deps.Signer.Required); ... })`) unless explicitly public.
6. If the endpoint may exceed 60s (e.g. AI/network), wrap it with a long-timeout middleware like `signalH.withLongTimeout` ([signal.go:30](../internal/handlers/signal.go)).
7. If you need to push events to connected clients, inject `*sse.Broker` and call `broker.Publish("your_event", payload)`.

## 7. Debugging Checklist

- **404 from `/api/...`** → not registered in `router.go`. Check spelling and that the route is inside the right `r.Route` block. Anything not matching falls through to `NotFound` → SPA, which returns the index.html with a 200, easy to confuse with a "200 but wrong content" bug.
- **401 unauthorized** → cookie missing/invalid. In dev, ensure Vite proxies `/api` and the browser is on the same origin so `wf_session` is sent. In production, `Secure: true` cookies require HTTPS.
- **400 invalid body** with seemingly correct JSON → likely an extra field; `decodeJSON` uses `DisallowUnknownFields`.
- **Body context cancelled at 60s** for AI/long endpoints → confirm the route is wrapped by `signalH.withLongTimeout` (only `POST /holdings`, `POST /analyse`, `POST /refresh-scores` are).
- **SSE shows no events** → confirm `deps.SSEBroker != nil` at startup; events publish from `internal/jobs/scheduler.go` and `BackfillService` only. Stream endpoint requires JWT cookie just like REST endpoints.
- **Gmail callback returns 400 invalid state** → frontend redirected to `/api/gmail/connect` from a different origin and lost the `state` param; both connect and callback must hit the same backend host.
