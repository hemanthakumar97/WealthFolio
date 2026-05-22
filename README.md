# WealthFolio v2

Self-hosted Indian portfolio tracker — single Go binary with embedded React SPA + TimescaleDB. PWA-first; installable on iOS and Android.

## Stack

| Layer | Choice |
|---|---|
| Backend | Go 1.23, chi, pgx, goose, JWT |
| Frontend | React 19, Vite 6, TanStack Router/Query, Tailwind 3, shadcn/ui, Recharts |
| Database | TimescaleDB (PostgreSQL 16 + hypertables) |
| Deploy | Single Go binary (embedded SPA) + TimescaleDB container |

## Quick start (Docker)

```bash
cp .env.example .env
# Edit .env: set DATABASE_URL and JWT_SECRET (openssl rand -hex 64)
mkdir -p data/postgres data/uploads
docker compose up -d
open http://localhost:3000
```

First load shows the setup screen — create the owner account and you're in.

## Local dev

```bash
# Start only the database
docker compose up -d db

cp .env.example .env   # fill in DATABASE_URL and JWT_SECRET

# Backend (terminal 1)
make dev-api           # Go API on :8000

# Frontend (terminal 2)
cd web && pnpm dev     # Vite on :5173 — proxies /api → :8000
```

## Features

| Feature | Details |
|---|---|
| **Multi-broker import** | Zerodha tradebook CSV, Groww MF XLSX, INDMoney XLS, Gmail auto-import |
| **FIFO cost basis** | True FIFO lot tracking with BONUS support; XIRR for annualised return |
| **Live prices** | Google Finance + Yahoo Finance scraping every 15 min |
| **Trends** | Daily snapshots → TimescaleDB hypertable; monthly returns CAGG |
| **Market Mood** | P/E percentile (Green/Yellow/Red) from niftyindices.com; Tickertape MMI gauge |
| **Precious Metals** | Gold/Silver via Yahoo Finance (GC=F, SI=F); GSR + euphoria alerts; AI-generated insights |
| **Signal / AI** | Portfolio analysis and stock signals via Anthropic/OpenAI/Antigravity |
| **Categories** | Custom categories with colour; many-to-many instrument links |
| **Allocations** | Target % per instrument and category; distribution calculator |
| **Backfill** | Historical prices from Google Sheet or uploaded CSV; live SSE log stream |
| **Gmail import** | OAuth2 watcher — auto-imports Groww, Zerodha, INDMoney emails |
| **Privacy mode** | One-click sidebar toggle to mask all amounts |
| **PWA** | Installable on iOS/Android; StaleWhileRevalidate service worker |
| **Self-hosted** | Single binary + TimescaleDB — `docker compose up` in one command |

## Project layout

```
cmd/server/           Entrypoint
internal/
  config/             Env-driven Config struct
  db/migrations/      23 goose SQL migrations (auto-run at startup)
  auth/               bcrypt, JWT, HttpOnly cookie middleware
  handlers/           chi routes — one file per domain
  services/           Business logic: portfolio calculator, price fetcher,
                      snapshot, signal, market mood, precious metals, fx,
                      gmail watcher, import, backfill, yahoo finance, …
  parsers/            Broker file parsers + auto-detector
  sse/                SSE broker (backfill logs, price events, snapshots)
  xirr/               Newton-Raphson XIRR
  jobs/               gocron scheduler (prices, snapshots, market mood, FX)
  web/                //go:embed dist
web/
  src/routes/         11 authenticated pages (TanStack Router, file-based)
  src/lib/            api.ts, sse.ts, privacy.tsx, format.ts
docs/                 Developer wiki (architecture, services, DB, API, frontend)
```

## Make targets

```bash
make dev        # API + frontend in parallel
make build      # Full production build → bin/wealthfolio
make test       # go test ./...
make migrate    # Run pending DB migrations
make docker-up  # Start app + TimescaleDB containers
```

## Docs

Full developer wiki in [`docs/`](./docs/README.md) — architecture, service algorithms, database schema, API reference, and debugging playbook.
