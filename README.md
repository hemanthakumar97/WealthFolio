# WealthFolio v2

Self-hosted Indian-broker portfolio dashboard. Single Go binary (with embedded React SPA) + TimescaleDB. PWA-first; installable on iOS and Android.

> **Complete.** All eight phases shipped. See `PRD.md` for the full spec.

## Stack

| Layer | Choice |
| --- | --- |
| Backend | Go 1.23, `chi`, `pgx`, `goose`, JWT |
| Frontend | Vite + React 19, TanStack Router/Query, Tailwind + shadcn/ui |
| DB | TimescaleDB (PostgreSQL 16 + hypertables) |
| Deploy | One Go binary (embedded SPA) + TimescaleDB container |

## Run with Docker

```bash
cp .env.example .env
# edit .env: set DB_PASSWORD and JWT_SECRET (openssl rand -hex 64)
mkdir -p data/postgres data/uploads
docker compose up -d
open http://localhost:3000
```

First load shows the setup screen; create the owner account and you're in.

## Local dev (without Docker for app, Docker for db)

```bash
docker compose up -d db
cp .env.example .env

# Backend
make dev-api          # uses .env via your shell — export it first or use direnv

# Frontend (in another terminal)
make dev-web          # Vite on :5173, proxies /api → :8000
```

## Features

| Feature | Details |
|---|---|
| **Multi-broker import** | Zerodha tradebook + Groww MF statement (CSV/XLSX, auto-detected) |
| **Live prices** | Google Finance scraping every 15 min via gocron |
| **Portfolio analytics** | P&L, XIRR, allocation by asset type / platform / category |
| **Trends** | Daily snapshots → TimescaleDB hypertable + CAGG for monthly returns |
| **Categories** | Custom categories with color; instruments linked many-to-many |
| **Allocations** | Target % + SIP targets per instrument and category; distribution calculator |
| **Market Mood** | P/E percentile (Green/Yellow/Red) per index; Tickertape MMI gauge; gold/silver prices |
| **Backfill** | Historical prices from Google Sheet or uploaded CSV; live SSE log stream |
| **Privacy mode** | One-click toggle to mask all amounts in the sidebar |
| **Settings** | Profile update, profile picture upload, password change |
| **PWA** | Installable on iOS/Android; service worker with StaleWhileRevalidate for portfolio data |
| **Self-hosted** | Single Go binary + TimescaleDB; `docker compose up` in one step |

## Make targets

`make help` lists them. Most-used: `dev`, `build`, `docker`, `docker-up`, `migrate`.

## Layout

```
cmd/server/           # entrypoint
internal/
  config/             # env-driven Config struct
  db/migrations/      # goose .sql (00001–00006)
  auth/               # bcrypt, JWT, cookie middleware
  handlers/           # chi handlers — auth, portfolio, transactions,
                      #   instruments, categories, allocations, trends,
                      #   market, backfill, health
  services/           # portfolio_calculator, price_fetcher, gfinance,
                      #   market_mood, tickertape, precious_metals,
                      #   backfill_service, sheets, fx, snapshot, …
  parsers/            # zerodha, groww_mf (CSV/XLSX → NormalizedTransaction)
  sse/                # SSE broker (real-time backfill logs, price events)
  xirr/               # Newton-Raphson IRR
  jobs/               # gocron scheduler (prices, snapshot, market, FX)
  web/                # //go:embed dist
web/                  # Vite + React 19 SPA
  src/routes/         # 11 pages (TanStack Router, code-split)
  src/lib/            # api.ts, sse.ts, privacy.tsx, format.ts
```
