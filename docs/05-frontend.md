# Frontend (`web/`)

Reference for AI agents adding pages, modifying charts, or debugging queries in the WealthFolio SPA. All paths are relative to `/Volumes/Hemanth/Projects/WealthFolio-v2/`.

---

## 1. Stack & Build

- **Bundler / dev server:** Vite 6 (`web/vite.config.ts`)
- **Framework:** React 19 (`web/package.json:28`) with TypeScript 5.7
- **Router:** TanStack Router v1.95 with the Vite plugin doing **file-based routing** (`web/vite.config.ts:9-13`, `autoCodeSplitting: true`)
- **Server state:** TanStack Query v5 (`web/src/lib/query-client.ts`)
- **Tables:** TanStack Table v8 (used by `_app.holdings.tsx`, `_app.closed-positions.tsx`)
- **Forms:** `react-hook-form` + `zod` via `@hookform/resolvers`
- **Styling:** Tailwind CSS 3.4 (`web/tailwind.config.ts`, `web/src/styles/index.css`), `tailwind-merge` + `clsx` via `cn()` in `web/src/lib/utils.ts`
- **UI primitives:** shadcn/ui (Radix-based) under `web/src/components/ui/`
- **Charts:** Recharts 2.14 (`AreaChart`, `LineChart`, `PieChart`)
- **Icons:** `lucide-react`
- **Toasts:** `sonner` (mounted in `web/src/main.tsx:30`)
- **PWA:** `vite-plugin-pwa` with autoUpdate, SW cache for `/api/portfolio/(summary|holdings)` as StaleWhileRevalidate (`web/vite.config.ts:36-44`)

### Dev workflow

- `cd web && pnpm dev` — Vite on `:5173`, proxies `/api → http://localhost:8000` (`web/vite.config.ts:53-61`)
- `cd web && pnpm build` — emits `web/dist/` (es2022, no sourcemaps, `vite.config.ts:62-66`)
- **Embed step:** `web/dist/` must be manually copied to `internal/web/dist/` for the Go binary to embed the SPA. The `make build` target handles this; ad-hoc `pnpm build` does not.
- `pnpm typecheck` runs `tsc -b` against the project references in `web/tsconfig.json` (`tsconfig.app.json` for `src/`, `tsconfig.node.json` for build config).
- Path alias `@/* → web/src/*` (`web/vite.config.ts:48-52`, mirrored in tsconfig).

---

## 2. Routing

### File-based conventions (TanStack Router)

Routes live under `web/src/routes/`. The plugin generates `web/src/routeTree.gen.ts` — **never edit it manually**; the dev server rewrites it on every save.

| File | URL | Auth |
|---|---|---|
| `__root.tsx` | shell (renders `<Outlet />`) | public |
| `index.tsx` | `/` | redirector |
| `login.tsx` | `/login` | public |
| `setup.tsx` | `/setup` | public (first-run) |
| `_app.tsx` | layout pathless | gated |
| `_app.dashboard.tsx` | `/dashboard` | gated |
| `_app.holdings.tsx` | `/holdings` | gated |
| `_app.transactions.tsx` | `/transactions` | gated |
| `_app.allocations.tsx` | `/allocations` | gated |
| `_app.trends.tsx` | `/trends` | gated |
| `_app.market-mood.tsx` | `/market-mood` | gated |
| `_app.signal.tsx` | `/signal` | gated |
| `_app.closed-positions.tsx` | `/closed-positions` | gated |
| `_app.backfill.tsx` | `/backfill` | gated |
| `_app.categories.tsx` | `/categories` | gated |
| `_app.settings.tsx` | `/settings` | gated |

The `_app` prefix is a **pathless layout route**: `_app.dashboard.tsx` resolves to `/dashboard` (not `/_app/dashboard`) but inherits `_app.tsx`'s component and `beforeLoad`.

### Root shell

`web/src/routes/__root.tsx:8-10` is a `createRootRouteWithContext<{ queryClient }>` that just renders `<Outlet />`. The QueryClient is injected from `web/src/main.tsx:14` so `beforeLoad` hooks can call `context.queryClient.ensureQueryData(...)` if needed.

### Auth gate

`web/src/routes/_app.tsx:34-47`:

```tsx
beforeLoad: async () => {
  try { await authApi.me(); }
  catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      const status = await authApi.status();
      throw redirect({ to: status.needs_setup ? '/setup' : '/login' });
    }
    throw err;
  }
}
```

The redirect is decided per request: if no user exists in the DB → `/setup`; otherwise → `/login`. `index.tsx` does the same check and forwards to `/dashboard` for authed users.

`createRouter` in `main.tsx:12-17` sets `defaultPreload: 'intent'` — hovering nav links pre-loads the route bundle and triggers `beforeLoad`.

---

## 3. State

- **Server state:** TanStack Query exclusively. Single `QueryClient` in `web/src/lib/query-client.ts` with defaults `staleTime: 30_000`, `retry: 1`, `refetchOnWindowFocus: false`.
- **Privacy mode:** React context in `web/src/lib/privacy.tsx:8-19` (`PrivacyProvider` + `usePrivacy()`). Boolean `masked` toggled from the sidebar eye icon (`_app.tsx:106-112`). Not persisted — resets on reload.
- **Auth state:** Effectively the `['me']` query in `_app.tsx:65`. Logout clears the entire query cache (`_app.tsx:79-83`).
- **Sidebar collapsed:** Local `useState` synced to `localStorage['sidebar-collapsed']` (`_app.tsx:67-77`).
- **No Redux, Zustand, Jotai, or other global stores.**

---

## 4. API Client (`web/src/lib/api.ts`)

### Core `request<T>()` (`api.ts:11-23`)

```ts
async function request<T>(path, init): Promise<T> {
  // sets Content-Type: application/json unless body is FormData
  // always sends credentials: 'include' (session cookie)
  const res = await fetch(path, { ...init, credentials: 'include', headers });
  if (!res.ok) throw new ApiError(res.status, body.detail ?? res.statusText);
  if (res.status === 204) return undefined as T;
  return res.json();
}
```

- **Auth model:** HttpOnly JWT cookie set by Go (`/api/auth/login`); the frontend never touches the token. `credentials: 'include'` is required for the cookie to ride along (Vite proxy forwards it in dev, same-origin in prod).
- **Error shape:** `ApiError extends Error` with `status: number` and `detail: string` (`api.ts:1-9`). Check `err instanceof ApiError && err.status === 401` to detect logout (see `_app.tsx:39`).
- **FormData uploads** skip the JSON `Content-Type` header so the browser sets the multipart boundary correctly.

### API namespaces (all exported from `api.ts`)

| Namespace | Methods | Notes |
|---|---|---|
| `authApi` (32) | `status`, `setup`, `login`, `logout`, `me`, `updateMe`, `changePassword`, `uploadProfilePicture` | Cookie-based session |
| `healthApi` (54) | `check` | |
| `transactionsApi` (115) | `upload(file, platform?)`, `manual`, `remove`, `history`, `historyItem`, `logs(uploadId)`, `formats` | `upload` accepts FormData |
| `instrumentsApi` (404) | `list({asset_type, search})`, `updateAssetType` | |
| `portfolioApi` (252) | `summary()`, `holdings({platform, asset_type, sort_by})`, `closedPositions({platform, asset_type})` | |
| `trendsApi` (227) | `portfolio({days, asset_type, instrument_id})`, `monthlyReturns(months)`, `allocationHistory(days)`, `benchmark(symbol)`, `backfill()` | |
| `signalApi` (372) | `loadHoldings(risk)`, `generateHoldings({risk_profile, goal, horizon})`, `getInstrumentMetrics(id)`, `refreshScores()`, `getMFMetrics(id)` | AI-driven |
| `aiSettingsApi` (395) | `get`, `put({provider, api_key, model})` | |
| `categoriesApi` (439) | `list`, `create`, `get`, `update`, `remove`, `listInstruments`, `addInstrument`, `removeInstrument` | |
| `allocationsApi` (503) | `overview`, `updateInstrument`, `updateCategory`, `calculateDistribution(amount)` | |
| `marketApi` (615) | `config`, `toggleConfig`, `moods`, `indexHistory(name, period?)`, `mmi`, `metals`, `refresh`, `sync`, `ingestPE`, `watchlist`, `addWatchlist`, `removeWatchlist` | |
| `backfillApi` (689) | `status`, `startFromSheet`, `startFromFile`, `autoSearch`, `startAuto`, `searchMFAPI`, `startFromMFAPI`, `updateAMFICode`, `updateGFinanceSymbol` | |
| `settingsApi` (762) | `gmailStatus`, `gmailDisconnect`, `saveGmailConfig`, `listRules`, `testRun`, `createRule`, `toggleRule`, `deleteRule` | Email-watch admin |

Constants: `ASSET_TYPES` (`api.ts:271-279`) is the canonical dropdown list — reuse it instead of redefining.

All response/request types are exported as interfaces near each namespace. Notable shapes: `PortfolioSummary` (199), `HoldingInfo` (152), `ClosedPositionInfo` (172), `SnapshotRow` (211), `HoldingSignal` (287), `MFMetrics` / `StockMetrics` (307–365), `MarketMoodResponse` (542), `MetalsData` (593), `BackfillStatus` (664).

---

## 5. SSE Client (`web/src/lib/sse.ts`)

Single `useSSE()` hook wraps a long-lived `EventSource('/api/events', { withCredentials: true })`. Auto-reconnects 3 s after error (`sse.ts:30-35`).

**Subscribed event types** (hard-coded `sse.ts:38`):
- `backfill` — progress + log lines from `backfill_service.go`
- `prices` — live price tick fan-out from `price_fetcher`
- `snapshot` — daily snapshot job completion
- `import` — Gmail-watcher import results

Usage pattern:
```ts
const { on } = useSSE();
useEffect(() => on('backfill', (data) => { ... }), [on]);
```

`on(type, handler)` returns an unsubscribe function. JSON is auto-parsed; if parsing fails, the raw string is delivered. Currently consumed in `_app.backfill.tsx` (line 13 import).

---

## 6. Layout (`web/src/routes/_app.tsx`)

A two-column CSS grid (`_app.tsx:88-93`) that animates its `grid-template-columns` between `[16rem 1fr]` and `[3.5rem 1fr]` based on `collapsed`.

- **Sidebar** (`_app.tsx:95-129`):
  - Header row with logo, app name (hidden when collapsed), privacy eye toggle, collapse chevron
  - `navItems` array (49-60) drives `<NavLink>`s — to add a page, add to this array and create the matching `_app.<slug>.tsx`
  - `NavLink` (173-219) renders with `activeProps={{ 'aria-current': 'page' }}` for active styling; supports a `disabled` "soon" state
- **Top bar** (`_app.tsx:133-162`): right-aligned avatar dropdown showing email + Settings link + Sign out
- **Main content** (`_app.tsx:165-167`): `<main className="flex-1 overflow-y-auto px-8 py-8"><Outlet /></main>`

Sign-out clears the entire TanStack Query cache before navigating: `queryClient.clear(); navigate({ to: '/login' });` (`_app.tsx:79-83`).

---

## 7. Route-by-route Summary

All gated routes follow the pattern: `createFileRoute('/_app/<slug>')({ component: <Page> })`. Each page composes its own `useQuery` / `useMutation` calls.

### `/dashboard` — `_app.dashboard.tsx`
- **Queries:** `['portfolio','summary']` → `portfolioApi.summary`, `['trends','portfolio', rangeDays]` → `trendsApi.portfolio({days})` (49-59)
- **Mutation:** `trendsApi.backfill` for snapshot regeneration (61-68)
- **UI:** KPI cards (invested, current value, P/L, XIRR), range picker (30D/90D/1Y/All — `RANGES` at line 32), Recharts `AreaChart` of value-vs-invested, donut `PieChart` of `by_asset_type` allocation
- Uses `MaskedAmount` for all currency values

### `/holdings` — `_app.holdings.tsx`
- **Queries:** `portfolioApi.holdings({platform, asset_type, sort_by})`, `instrumentsApi.list()` for the asset-type editor
- **Mutation:** `instrumentsApi.updateAssetType` (inline edit)
- **UI:** TanStack Table with column helper `col` (line 40), sorting via `getSortedRowModel`, search filter via `getFilteredRowModel`, color-coded asset-type badges (`TYPE_COLORS` at 42, `TYPE_AVATAR_THEMES` at 55), refresh button invalidates `['portfolio']`

### `/transactions` — `_app.transactions.tsx`
- **Queries:** `transactionsApi.history`, `transactionsApi.formats`
- **Mutations:** `transactionsApi.upload` (file), `transactionsApi.manual` (zod-validated form), `transactionsApi.remove`
- **UI:** Tabs `upload` / `manual` / `history`. Upload tab supports CSV/XLSX with optional `platform` override. Manual tab uses `react-hook-form` + `zodResolver`. History tab renders status badges and expandable error logs.

### `/allocations` — `_app.allocations.tsx`
- **Query:** `allocationsApi.overview()` → `AllocationOverview` with `instrument_allocations` and `category_allocations`
- **Mutations:** `updateInstrument`, `updateCategory` (inline editable target % and SIP amount), `calculateDistribution(amount)` for SIP planning
- **UI:** Tabs split between Category view (5 alloc categories: EQUITY / GOLD / DEBT / US_EQUITY / OTHERS) and Instrument view, Recharts `PieChart` for current vs target, deviation badges, copy-to-clipboard distribution result

### `/trends` — `_app.trends.tsx`
- **Queries:** `trendsApi.portfolio({days, asset_type, instrument_id})`, `trendsApi.monthlyReturns(months)`, `trendsApi.allocationHistory(days)`, `trendsApi.benchmark(symbol)`, `instrumentsApi.list()`
- **UI:** Tabs for Performance / Monthly Returns / Allocation History / Benchmark comparison. Recharts `AreaChart` + `LineChart`. Range picker reused from dashboard (`RANGES` at 52). Asset-type and instrument filters via `<Select>` from shadcn.

### `/market-mood` — `_app.market-mood.tsx`
- **Queries:** `marketApi.moods()`, `marketApi.config()`, `marketApi.indexHistory(name, period)`, `marketApi.mmi()`, `marketApi.metals()`, `marketApi.watchlist()`
- **Mutations:** `marketApi.refresh()`, `marketApi.toggleConfig`, `marketApi.ingestPE`, `marketApi.addWatchlist` / `removeWatchlist`
- **Custom components in-file:** `MoodChip` (51-78) for Green/Yellow/Red valuation, `MMIGauge` (~80+) for the Tickertape Market Mood Index gauge
- **UI:** Per-index PE history Recharts `LineChart` with `ReferenceLine` for 25th/75th percentile, gold/silver metals card with valuation badges and alerts

### `/signal` — `_app.signal.tsx`
- **Queries:** `signalApi.loadHoldings(riskProfile)`, `aiSettingsApi.get()`, `portfolioApi.holdings()`
- **Mutations:** `signalApi.generateHoldings({risk_profile, goal, horizon})`, `signalApi.refreshScores()`
- **UI:** Investor profile selector (conservative/moderate/aggressive), per-holding AI recommendation cards (BUY_MORE / HOLD / SWITCH / PARTIAL_SELL / BOOK_PROFIT) with confidence, key points, qualitative note, tax note, Zero1 score
- **Detail panel:** `InstrumentAnalysisPanel` (web/src/components/InstrumentAnalysisPanel.tsx) shown on row expand — fetches `signalApi.getInstrumentMetrics(id)` and renders MF/ETF metrics (NAV history, rolling returns, drawdown via Recharts) or `StockMetrics` (valuation/profitability/growth grids). `MFAnalysisPanel.tsx` is the MF-specific variant.

### `/closed-positions` — `_app.closed-positions.tsx`
- **Query:** `portfolioApi.closedPositions({platform, asset_type})`
- **UI:** TanStack Table of realized P/L by instrument, with sort + text search, exit-date column

### `/backfill` — `_app.backfill.tsx`
- **Queries:** `backfillApi.status()`, `instrumentsApi.list()`
- **Mutations:** `backfillApi.startFromSheet`, `backfillApi.startFromFile`, `backfillApi.startAuto`, `backfillApi.autoSearch`, `backfillApi.searchMFAPI`, `backfillApi.updateAMFICode`, `backfillApi.updateGFinanceSymbol`
- **SSE:** subscribes to `backfill` events via `useSSE()` for live progress and log tail
- **Components in-file:** `AutoPicker` (30+) — MFAPI search for MF, Yahoo Finance search for ETF/STOCK

### `/categories` — `_app.categories.tsx`
- **Queries:** `categoriesApi.list()`, `categoriesApi.listInstruments(id)`, `instrumentsApi.list()`
- **Mutations:** `create`, `update`, `remove`, `addInstrument`, `removeInstrument`
- **UI:** Color-tagged category cards with preset palette (`PRESET_COLORS` at 29+), inline edit, expandable instrument list, weighted membership

### `/settings` — `_app.settings.tsx`
- **Queries:** `authApi.me()`, `aiSettingsApi.get()`, `settingsApi.gmailStatus()`, `settingsApi.listRules()`
- **Mutations:** `authApi.updateMe`, `authApi.changePassword`, `authApi.uploadProfilePicture`, `aiSettingsApi.put`, `settingsApi.saveGmailConfig`, `settingsApi.gmailDisconnect`, `settingsApi.createRule` / `toggleRule` / `deleteRule`, `settingsApi.testRun`
- **UI:** Tabs for Profile / AI / Gmail / Email Rules. Reads `?tab=` search param via `useSearch` (line 2). Parser type labels for rules (`PARSER_LABELS` at 39).

### `/login` and `/setup` (public)
- `login.tsx` — email/password form posting to `authApi.login`; on success navigates to `/dashboard`
- `setup.tsx` — first-run admin creation via `authApi.setup`; gated by `authApi.status().needs_setup`

---

## 8. Component Patterns

### shadcn/ui primitives (`web/src/components/ui/`)

Available: `badge`, `button`, `card` (with `CardHeader`/`CardContent`/`CardDescription`/`CardTitle`), `dropdown-menu`, `input`, `label`, `select`, `separator`, `sonner` (toast), `tabs`. All are Radix-based with `cva` variants and the `cn()` helper. Config in `web/components.json`.

When adding shadcn components, copy them under `web/src/components/ui/` and ensure they consume `cn` from `@/lib/utils`.

### Shared cross-page components (`web/src/components/`)

- `InstrumentAnalysisPanel.tsx` — used by `/signal`; switches rendering between MF/ETF and Stock metrics
- `MFAnalysisPanel.tsx` — MF-specific Zero1 metric panel with NAV / rolling / drawdown charts

### Privacy mode (`web/src/lib/privacy.tsx`)

```tsx
<MaskedAmount value={summary?.total_invested} />
// Renders "₹ ••••••" when masked, else ₹1,23,45,678.00 (en-IN, 2 dp)
```

Always wrap currency rendering in `<MaskedAmount>` (or check `usePrivacy().masked`) so the sidebar eye toggle works site-wide. Custom formatter override: `<MaskedAmount value={x} format={formatINR} />`.

### Custom in-route components

- `MoodChip` (`_app.market-mood.tsx:51`) — Green/Yellow/Red valuation pill
- `MMIGauge` (`_app.market-mood.tsx:~80`) — radial gauge for Tickertape MMI value
- `NavLink` (`_app.tsx:173`) — sidebar item with active state + collapsed-tooltip
- `AutoPicker` (`_app.backfill.tsx:30`) — MFAPI/Yahoo symbol picker

### Toasts

`import { toast } from '@/components/ui/sonner'` — usage: `toast.success(...)`, `toast.error(...)`. The `<Toaster>` is mounted once in `main.tsx:30` with `position="top-right" richColors`.

---

## 9. Formatting (`web/src/lib/format.ts`)

All formatters use the `en-IN` locale, which produces Indian lakh/crore digit grouping (2-3-2 pattern: `1,23,45,678` instead of `1,234,5678`).

| Function | Output example | Notes |
|---|---|---|
| `formatINR(n)` | `₹1,23,45,678.00` | `Intl.NumberFormat('en-IN', { style: 'currency', currency: 'INR', maximumFractionDigits: 2 })` |
| `formatNumber(n)` | `1,23,456.7890` | `maximumFractionDigits: 4` — used for unit quantities |
| `formatPercent(n)` | `12.34%` | Divides by 100 first; expects 12.34 not 0.1234 |
| `formatDateTime(iso)` | `21 May 2026, 10:45` | `en-IN`, short month, 2-digit time |
| `formatDate(iso)` | `21 May 2026` | `en-IN`, short month |

All formatters return `'—'` for null/undefined/NaN inputs — safe to pass raw API fields directly.

`MaskedAmount` (in `privacy.tsx:36`) has its own inline formatter that uses `toLocaleString('en-IN', { maximumFractionDigits: 2 })` rather than the `Intl.NumberFormat` instance — pass `format={formatINR}` if you want the exact same output as `formatINR()`.

---

## Quick recipes

**Add a new page:**
1. Create `web/src/routes/_app.<slug>.tsx` with `export const Route = createFileRoute('/_app/<slug>')({ component: ... })`
2. Add an entry to `navItems` in `web/src/routes/_app.tsx:49-60` with a Lucide icon
3. Save — Vite plugin regenerates `routeTree.gen.ts`
4. Use `useQuery` for reads, `useMutation` + `queryClient.invalidateQueries({ queryKey: [...] })` for writes
5. Wrap currency in `<MaskedAmount>`; use `formatINR` / `formatDate` for everything else

**Add a new API endpoint:**
1. Add the namespace method in `web/src/lib/api.ts` next to its peers; export request/response interfaces
2. Use `request<T>(path, init)` — never call `fetch` directly (you'd lose the cookie + error handling)
3. For uploads, pass `FormData` as `body` — `Content-Type` is set automatically

**Add a chart:**
- Recharts is the only chart library. Wrap in `<ResponsiveContainer width="100%" height={...}>`. Match the dashboard's color palette (Tailwind `emerald-*` for positive, `rose-*` for negative).

**Debug a stale query:**
- Default `staleTime` is 30 s. Pages that override it (e.g. dashboard summary at 60 s) do so inline. Use `queryClient.invalidateQueries({ queryKey: ['portfolio'] })` to force a refetch after a mutation — this is the established pattern across all pages.
