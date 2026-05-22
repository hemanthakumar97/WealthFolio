# 01 — Architecture

Reference document for AI agents working on WealthFolio. Read once, scan for what you need.

---

## 1. Project Purpose

WealthFolio is a **self-hosted, India-focused personal portfolio tracker**. It ingests trade data (Zerodha contract notes, Groww MF, INDmoney, manual entries, and Gmail-driven auto-imports), computes holdings/P&L/XIRR, fetches live prices (Google Finance for Indian equities, Yahoo Finance for US), maintains daily portfolio snapshots, and exposes AI-driven signal scoring for individual instruments. Currency conversion (USD↔INR), precious metals, and Indian market mood (MMI / Tickertape) are first-class concerns. All scheduler times are in `Asia/Kolkata`.

---

## 2. Deployment Model

**Single Go binary** (`bin/wealthfolio`) serves both the REST API and the React SPA.

| Surface | Path | Source |
|---|---|---|
| REST API | `/api/*` | `internal/handlers/router.go` |
| Uploaded files (profile pics) | `/uploads/*` | `http.FileServer` over `cfg.UploadDir` (`internal/handlers/router.go:210-213`) |
| SPA (everything else) | `NotFound` handler | `internal/web/embed.go` via `//go:embed all:dist` |

### Modes

| Mode | API | SPA | How they connect |
|---|---|---|---|
| **Dev** (`make dev`) | `go run ./cmd/server` on `:8000` | `pnpm dev` on `:5173` | Vite dev server proxies `/api/*` → `:8000`; SPA hot-reloads |
| **Prod** (`make build` → `./bin/wealthfolio`) | Same binary | Embedded from `internal/web/dist/` (copied from `web/dist/` by `build-web`) | Chi router's `NotFound` falls through to `web.SPA()` which serves `index.html` for any non-asset, non-`/api/` path |

The SPA handler in `internal/web/embed.go:34-64` explicitly returns 404 for `/api/*` paths (defensive — these should already be matched by chi) and otherwise tries the static asset first, falling back to `index.html` (SPA routing).

If `internal/web/dist/` is empty (running `go run` without building the frontend), `SPA()` serves a small HTML fallback page pointing developers at `pnpm dev` (`internal/web/embed.go:16-26`).

---

## 3. Tech Stack

### Backend (Go 1.23)

| Concern | Library |
|---|---|
| HTTP router | `github.com/go-chi/chi/v5` + standard middleware (RequestID, RealIP, Logger, Recoverer, Timeout 60s) |
| DB driver | `github.com/jackc/pgx/v5` + `pgxpool` — **raw SQL, no sqlc** |
| Migrations | `goose` (SQL files in `internal/db/migrations/*.sql`) |
| Scheduler | `gocron` (`internal/jobs/`) — price fetch, daily snapshot (`SNAPSHOT_HOUR`), market mood (`MARKET_MOOD_HOUR`), Gmail watcher (`GMAIL_WATCHER_HOUR`), FX rates |
| Decimal math | `shopspring/decimal` (transactions, prices, XIRR) |
| Config | `caarlos0/env/v11` (struct-tag env parsing) |
| Auth | bcrypt (passwords) + JWT (HS256, custom `auth.Signer`) in `HttpOnly` cookie |
| SSE | Custom broker in `internal/sse/` for backfill logs and live price events |
| Logging | stdlib `log/slog` with JSON handler (`cmd/server/main.go:25`) |

### Frontend (`web/`, Node 22, pnpm 9)

| Concern | Library |
|---|---|
| Framework | React 19 + TypeScript 5.7 |
| Build | Vite 6 (`vite-plugin-pwa` for PWA, `@vitejs/plugin-react`) |
| Routing | `@tanstack/react-router` 1.95 (file-based; `@tanstack/router-plugin` autogenerates `routeTree.gen.ts`) |
| Data fetching | `@tanstack/react-query` 5 |
| Tables | `@tanstack/react-table` 8 |
| Forms | `react-hook-form` + `zod` + `@hookform/resolvers` |
| UI primitives | Radix UI + shadcn/ui (config in `web/components.json`) |
| Styling | Tailwind 3.4 + `tailwindcss-animate`, `tailwind-merge`, `class-variance-authority` |
| Charts | `recharts` 2.14 |
| Icons | `lucide-react` |
| Toasts | `sonner` |

### Database

**TimescaleDB** (`timescale/timescaledb:latest-pg16` — PostgreSQL 16). Hypertables for time-series (prices, portfolio snapshots). Migrations live in `internal/db/migrations/` (currently `00001_init.sql` through `00020_signal_zero1_score.sql`).

---

## 4. Directory Layout

```
WealthFolio/
├── cmd/server/main.go         Entrypoint: config → DB → migrate → scheduler → router → http.Server
├── internal/
│   ├── auth/                  bcrypt + JWT Signer, cookie middleware (Signer.Required)
│   ├── config/config.go       Config struct, env parsing
│   ├── db/
│   │   ├── db.go              pgxpool.Open
│   │   ├── migrate.go         goose runner
│   │   └── migrations/        00001..00020 SQL files
│   ├── domain/                Shared domain types (transactions, instruments, holdings)
│   ├── handlers/
│   │   ├── router.go          ALL routes wired here (the only place routes are defined)
│   │   ├── auth.go, portfolio.go, transactions.go, instruments.go,
│   │   │ categories.go, allocations.go, market.go, backfill.go,
│   │   │ signal.go, ai_settings.go, ai_prompts.go, gmail_oauth.go,
│   │   │ email_rules.go, trends.go, health.go
│   ├── services/              Business logic — see "Service dependencies" below
│   ├── parsers/               Broker parsers (Zerodha CSV/PDF, Groww MF XLSX, INDmoney, emails)
│   │   └── detector.go        Auto-detects upload format
│   ├── sse/                   SSE broker (backfill logs, price events)
│   ├── jobs/                  gocron scheduler registration
│   ├── xirr/                  Newton-Raphson XIRR
│   └── web/
│       ├── embed.go           //go:embed all:dist + SPA() http.Handler
│       └── dist/              Copied from web/dist by `make build-web` (gitignored)
├── web/
│   ├── src/
│   │   ├── main.tsx           React entrypoint
│   │   ├── routeTree.gen.ts   AUTOGENERATED — do not edit
│   │   ├── routes/            File-based routes (see Conventions)
│   │   ├── components/ui/     shadcn/ui primitives
│   │   ├── lib/
│   │   │   ├── api.ts         Typed fetch wrappers for all endpoints
│   │   │   ├── sse.ts         SSE client
│   │   │   ├── privacy.tsx    Privacy-mode React context (mask amounts)
│   │   │   └── format.ts      Indian number/currency formatting
│   │   └── styles/
│   ├── vite.config.ts         Vite + router plugin + PWA + /api proxy
│   ├── tailwind.config.ts
│   └── package.json
├── docs/                      This directory
├── data/                      Local docker-compose volumes (postgres, uploads)
├── transactions-csv/          Real trade data — NEVER commit new files here
├── docker-compose.yml         App + TimescaleDB
├── Dockerfile                 Multi-stage: node:22-alpine (web) → golang:1.23-alpine (api) → distroless
├── Makefile                   Dev/build/docker targets
├── CLAUDE.md                  Top-level agent instructions
└── PRD.md, README.md, ANTIGRAVITY.md
```

---

## 5. Startup Sequence

When `./bin/wealthfolio` runs, `cmd/server/main.go:run()` executes:

1. **Logger init** — `slog` JSON handler at INFO level (`main.go:25-26`).
2. **Config load** — `config.Load()` parses env via `caarlos0/env`. Fails fast if `DATABASE_URL` or `JWT_SECRET` missing.
3. **Signal context** — `signal.NotifyContext` for SIGINT/SIGTERM graceful shutdown (`main.go:40`).
4. **DB pool** — `db.Open(ctx, cfg.DatabaseURL)` returns `*pgxpool.Pool` (`main.go:43`).
5. **Migrations** — `db.Migrate(ctx, pool)` runs pending goose migrations from `internal/db/migrations/` (`main.go:50`).
6. **Auth signer** — `auth.NewSigner(cfg.JWTSecret)` (`main.go:54`).
7. **SSE broker** — `sse.NewBroker()` (`main.go:55`).
8. **Shared services** instantiated for both HTTP handlers and the Gmail watcher: `InstrumentService`, `DuplicateDetector`, `ImportService` (`main.go:58-60`).
9. **Gmail watcher** — only when `GMAIL_ENABLED=true` (`main.go:63-71`).
10. **Scheduler** — `jobs.New(...)` then `sched.Register(SnapshotHour, MarketMoodHour, EnableMarketMood, GmailWatcherHour)`; `sched.Start()` (`main.go:74-81`). `defer sched.Stop()` on shutdown.
11. **Router** — `handlers.NewRouter(handlers.Deps{...})` wires every route. SPA handler comes from `web.SPA()` (the embedded `//go:embed all:dist`).
12. **HTTP server** — `http.Server` on `:cfg.Port` (default 8000), `ReadHeaderTimeout=10s`, `IdleTimeout=2m`. `ListenAndServe()` in a goroutine (`main.go:99-112`).
13. **Block** on `<-ctx.Done()` until signal; then `srv.Shutdown(10s)` (`main.go:114-121`).

### Router-internal service graph (`internal/handlers/router.go:37-68`)

```
                         pgxpool.Pool
                              │
       ┌──────────┬──────────┼────────────┬──────────────┐
       ▼          ▼          ▼            ▼              ▼
 InstrumentSvc  DupDetector  FXService  MoodSvc/MMI    AISettings/Prompts
       │          │          │ │ │ │
       └──┬───────┘          │ │ │ └─→ PreciousMetalsService
          ▼                  │ │ └───→ SnapshotService ──→ TrendsHandler
       ImportService         │ └─────→ PortfolioCalculator ─→ PortfolioHandler
          │                  └───────→ PriceFetcher ──────→ InstrumentsHandler
          ▼
       TransactionsHandler
```

`SnapshotService` depends on `PortfolioCalculator` + `FXService`. `PortfolioCalculator` depends on `FXService`. All constructed fresh per process in `NewRouter`.

---

## 6. Environment Variables

Source: `internal/config/config.go` + `docker-compose.yml`.

| Var | Required | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | yes | — | pgx connection string (e.g. `postgres://wealthfolio:pw@db:5432/wealthfolio?sslmode=disable`) |
| `JWT_SECRET` | yes | — | HMAC secret for JWT signing; 64+ chars recommended |
| `APP_ENV` | no | `development` | When `production`, `Config.IsProduction()` returns true; enables secure cookies |
| `TZ` | no | `Asia/Kolkata` | Scheduler timezone |
| `PORT` | no | `8000` | HTTP listen port |
| `SNAPSHOT_HOUR` | no | `23` | Hour (IST) for daily portfolio snapshot |
| `ENABLE_MARKET_MOOD` | no | `true` | Toggle market mood scraping job |
| `MARKET_MOOD_HOUR` | no | `19` | Hour (IST) for market mood fetch |
| `GMAIL_ENABLED` | no | `false` | Enable Gmail auto-import watcher |
| `GMAIL_REFRESH_TOKEN` | no | — | OAuth2 refresh token (also persisted in DB via settings) |
| `GMAIL_WATCHER_HOUR` | no | `7` | Hour (IST) for daily Gmail scan |
| `GMAIL_LOOKBACK_DAYS` | no | `7` | How many days back to scan emails |
| `ZERODHA_PAN` | no | — | Password used to decrypt Zerodha contract note PDFs |
| `UPLOAD_MAX_MB` | no | `10` | Max upload size for transaction files |
| `PROFILE_PIC_MAX_MB` | no | `5` | Max upload size for profile pictures |
| `UPLOAD_DIR` | no | `/data/uploads` | Filesystem path served at `/uploads/*` |

`docker-compose.yml` additionally requires `DB_PASSWORD` (compose-only, used to construct `DATABASE_URL` and the `POSTGRES_PASSWORD` for the db service).

---

## 7. Build & Run Commands

From `Makefile`:

| Target | Action |
|---|---|
| `make help` | List all targets |
| `make dev-api` | `go run ./cmd/server` — Go API on `:8000` |
| `make dev-web` | `cd web && pnpm dev` — Vite on `:5173`, proxies `/api` → `:8000` |
| `make dev` | Runs `dev-api` + `dev-web` in parallel via `make -j2`; `trap 'kill 0' INT` cleans both on Ctrl-C |
| `make build-web` | `cd web && pnpm install --frozen-lockfile && pnpm build`, then `rm -rf internal/web/dist && cp -r web/dist internal/web/dist` |
| `make build-api` | `CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/wealthfolio ./cmd/server` |
| `make build` | `build-web` then `build-api` (full production binary) |
| `make migrate` | `go run ./cmd/server -migrate-only` (uses `DATABASE_URL` from env) |
| `make sqlc` | `sqlc generate` — **DEFINED BUT UNUSED**; no sqlc in repo |
| `make tidy` | `go mod tidy` |
| `make fmt` | `gofmt -w .` then `cd web && pnpm format` |
| `make test` | `go test ./...` |
| `make docker` | `docker compose build` |
| `make docker-up` | `docker compose up -d` |
| `make docker-down` | `docker compose down` |
| `make docker-logs` | `docker compose logs -f app` |

### Local dev prerequisites

```bash
docker compose up -d db        # TimescaleDB only on :5433 (mapped to container :5432)
cp .env.example .env           # Fill DB_PASSWORD, JWT_SECRET
# Export .env (or use direnv) before `make dev-api`
```

### Docker image

`Dockerfile` is multi-stage:
1. **`web` stage** (`node:22-alpine`): `pnpm install --frozen-lockfile`, `pnpm build` → `/web/dist`.
2. **`api` stage** (`golang:1.23-alpine`): `go mod download`, copy source, copy `/web/dist` into `internal/web/dist`, `go build -trimpath -ldflags="-w -s"` → `/out/wealthfolio`.
3. **Runtime** (`gcr.io/distroless/static-debian12:nonroot`): copies the binary, `EXPOSE 8000`, `USER nonroot`. ~3MB base.

The compose `app` service maps host `:3000` → container `:8000`.

---

## 8. Key Conventions

### Database access
- **Raw `pgx` queries inline** — no sqlc, no ORM. The `make sqlc` target exists but nothing uses it. Do not introduce sqlc without explicit instruction.
- TimescaleDB hypertables for time-series tables (prices, snapshots).
- Migrations are plain goose SQL (`-- +goose Up` / `-- +goose Down`), numbered `NNNNN_name.sql`.

### Routing (Go)
- **All routes live in `internal/handlers/router.go`** — never define routes in handler files. Handlers expose methods; `router.go` mounts them.
- Auth-protected routes are inside `r.Group(func(r chi.Router) { r.Use(deps.Signer.Required); ... })` at `router.go:94`.
- Public routes: `/api/health`, `/api/auth/{status,setup,login,logout}`, `/api/gmail/{connect,callback}` (`router.go:70-92`).
- SSE: `r.Get("/api/events", ...)` wrapped with `Signer.Required` (`router.go:71-74`).
- Long-running AI endpoints use `signalH.withLongTimeout` middleware to override the global 60s timeout (`router.go:154-157`).
- Uploaded files served via `http.FileServer` at `/uploads/*` (`router.go:210-213`).
- **SPA fallthrough**: `r.NotFound(deps.SPA.ServeHTTP)` at `router.go:216` — any non-API, non-uploads path serves the React app.

### Frontend routing (TanStack Router, file-based)
- Files in `web/src/routes/` define routes. `__root.tsx` is the root layout.
- Files prefixed `_app.` are nested under the authenticated layout (`_app.tsx`). Examples: `_app.dashboard.tsx`, `_app.holdings.tsx`, `_app.transactions.tsx`, `_app.settings.tsx`.
- Public routes (no `_app.` prefix): `index.tsx`, `login.tsx`, `setup.tsx`.
- `routeTree.gen.ts` is regenerated by `@tanstack/router-plugin` during Vite build — **never edit by hand**.

### Authentication
- JWT in `HttpOnly` cookie. `auth.Signer.Required` is the middleware (used as `r.Use(deps.Signer.Required)`).
- `APP_ENV=production` flips secure-cookie flags (`Deps.Production` propagates through `NewAuthHandler`).
- First-run: `/api/auth/setup` (public, one-shot). Normal: `/api/auth/login`.
- Authed user info: `GET /api/auth/me`.

### Embedding & static serving
- `internal/web/embed.go` uses `//go:embed all:dist`. If the `dist/` directory is empty, a fallback HTML page is served — the API still works.
- The SPA handler in `web.SPA()` refuses to serve `/api/*` paths defensively (returns 404), and otherwise tries static-file first, falling back to `index.html` for SPA routes.

### Logging
- `slog` with JSON output. Use `slog.Info/Warn/Error` with key/value pairs (e.g. `slog.Info("server listening", "addr", srv.Addr, "env", cfg.AppEnv)`).

### Service construction
- Services are plain structs with `NewXxxService(pool, deps...)` constructors. Constructed once per process in `cmd/server/main.go` (the shared three: `InstrumentService`, `DuplicateDetector`, `ImportService`) and once per request scope in `handlers.NewRouter`. The shared ones are passed to the Gmail watcher so HTTP uploads and email imports share the same dedup logic.

### Files & dirs to avoid touching
- `internal/web/dist/` — generated by `make build-web`.
- `web/src/routeTree.gen.ts` — generated.
- `transactions-csv/` — real personal data; do not commit new files.
- `client_secret_*.json` at repo root — OAuth credentials (gitignored expectations apply).

### Naming
- The project was migrated from Gemini CLI to **Antigravity CLI** on 2026-05-21 (see migration `00017_migrate_gemini_to_antigravity.sql`). References to "Gemini" in older code/migrations refer to the same tool.

---

## Quick reference: where to look

| You want to... | Open |
|---|---|
| Add a route | `internal/handlers/router.go` |
| Add a handler | `internal/handlers/<domain>.go`, wire in `router.go` |
| Add business logic | `internal/services/<name>.go` |
| Add a parser for a new broker | `internal/parsers/`, update `detector.go` |
| Add a scheduled job | `internal/jobs/` |
| Add a DB migration | `internal/db/migrations/NNNNN_name.sql` (goose format) |
| Add an env var | `internal/config/config.go` + this doc + `docker-compose.yml` if needed |
| Add a frontend page | `web/src/routes/_app.<name>.tsx` (authed) or `web/src/routes/<name>.tsx` (public) |
| Add a typed API client | `web/src/lib/api.ts` |
| Change SPA root layout | `web/src/routes/__root.tsx` and `_app.tsx` |
