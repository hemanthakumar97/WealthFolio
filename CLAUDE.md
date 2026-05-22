# CLAUDE.md

WealthFolio — Indian personal portfolio tracker. Single Go binary + embedded React SPA.

## Wiki docs (read before making changes)

| Doc | When to read |
|---|---|
| `docs/01-architecture.md` | Deployment, startup sequence, full env var list, directory map |
| `docs/02-services.md` | Every service — algorithms, caching, gotchas (FIFO, XIRR, snapshots) |
| `docs/03-database.md` | All 23 migrations, every table, hypertables, query patterns |
| `docs/04-api-handlers.md` | Every REST endpoint with auth, request/response shapes |
| `docs/05-frontend.md` | Routing, state, API client, all pages summarised |
| `docs/06-integrations-debugging.md` | External APIs, parsers, background jobs, debugging playbook |

## MANDATORY: Keep docs in sync

**After any change below, update the relevant doc(s) before the task is done. Non-negotiable.**

| Change | Update |
|---|---|
| Env var added/removed/renamed | `CLAUDE.md` env table + `docs/01-architecture.md` |
| Service/method added/removed | `docs/02-services.md` + dependency graph below |
| DB migration or schema change | `docs/03-database.md` |
| API endpoint changed | `docs/04-api-handlers.md` |
| Frontend route/API call/layout | `docs/05-frontend.md` |
| External API, parser, job | `docs/06-integrations-debugging.md` |
| Algorithm change (FIFO, AI, caching) | `docs/02-services.md` + gotchas below |
| Auth/startup change | `docs/01-architecture.md` + this file |

**Before marking done, verify:**
- [ ] Env table below matches `internal/config/config.go` exactly
- [ ] Dependency graph below matches `internal/handlers/router.go`
- [ ] Migration count in `docs/03-database.md` matches `internal/db/migrations/`

---

## Commands

```bash
# Backend
make dev-api       # API on :8000
make dev           # API + frontend
make build         # Full production build
make test          # go test ./...
make migrate       # Run pending migrations

# Frontend (web/)
pnpm dev           # Vite on :5173 (proxies /api → :8000)
pnpm build         # Build → web/dist (then cp -r web/dist internal/web/dist, or use make build)
pnpm typecheck

# Docker
make docker-up     # host :3000 → container :8000
make docker-logs
```

## Env vars (`internal/config/config.go`)

| Var | Default | Notes |
|---|---|---|
| `DATABASE_URL` | required | `postgres://user:pass@host:port/db?sslmode=disable` |
| `JWT_SECRET` | required | 64+ chars |
| `APP_ENV` | `development` | `production` enables secure cookies |
| `TZ` | `Asia/Kolkata` | Scheduler timezone |
| `PORT` | `8000` | |
| `SNAPSHOT_HOUR` | `23` | IST hour for daily portfolio snapshot |
| `MARKET_MOOD_HOUR` | `19` | IST hour for P/E sync (niftyindices.com → `market_data`) |
| `GMAIL_WATCHER_HOUR` | `7` | IST hour to poll Gmail |
| `GMAIL_LOOKBACK_DAYS` | `7` | Days back to scan |
| `ZERODHA_PDF_PASSWORD` | — | Password for Zerodha contract note PDFs |
| `UPLOAD_DIR` | `/data/uploads` | |
| `UPLOAD_MAX_MB` | `10` | |
| `PROFILE_PIC_MAX_MB` | `5` | |

## Service dependency graph (`router.go:37-67`)

```
InstrumentService + DuplicateDetector → ImportService
FXService → PriceFetcher, PortfolioCalculator, SnapshotService, PreciousMetalsService
PortfolioCalculator → SnapshotService
MarketMoodService / TickertapeService / SignalService  (pool only)
Gmail watcher always starts — skips if no credentials in app_settings
```

## Critical gotchas

- **Dashboard P/L ≠ Trends P/L**: dashboard uses FIFO (`portfolio_calculator.go`), snapshots use avg-cost (`snapshot_service.go:computeForDate`). See `docs/02-services.md`.
- **No ORM**: all queries are raw pgx. `make sqlc` target exists but unused.
- **routeTree.gen.ts**: auto-generated — never edit.
- **Frontend build**: `pnpm build` alone is not enough — must copy `web/dist → internal/web/dist` (or `make build`).
- **React 19**, not 18. Vite 6. TanStack Router file-based (`_app.*` = authed layout).
- **AI config**: `ai_provider`/`ai_api_key`/`ai_model` in `app_settings` table, set via Settings UI.
- **Gmail**: refresh token stored in `app_settings.gmail_refresh_token` — set via OAuth UI, not env.
- **cagg_index_pe_stats**: exists in DB but not queried — market mood reads raw `market_data`.
- `transactions-csv/` contains real trade data — **never commit new files here**.
