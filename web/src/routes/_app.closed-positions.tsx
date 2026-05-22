import { useState, useMemo } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  flexRender,
  createColumnHelper,
  type SortingState,
} from '@tanstack/react-table';
import {
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  Loader2,
  Wallet,
  TrendingUp,
  TrendingDown,
  ArrowUpRight,
  Search,
  X,
  History,
} from 'lucide-react';

import { portfolioApi } from '@/lib/api';
import type { ClosedPositionInfo } from '@/lib/api';
import { formatINR, formatNumber, formatDate } from '@/lib/format';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { usePrivacy, MaskedAmount } from '@/lib/privacy';

export const Route = createFileRoute('/_app/closed-positions')({
  component: ClosedPositionsPage,
});

const TYPE_COLORS: Record<string, string> = {
  MF: 'border-blue-500/30 text-blue-600 dark:text-blue-400 bg-blue-500/5 hover:bg-blue-500/10',
  ETF: 'border-violet-500/30 text-violet-600 dark:text-violet-400 bg-violet-500/5 hover:bg-violet-500/10',
  STOCK:
    'border-emerald-500/30 text-emerald-600 dark:text-emerald-400 bg-emerald-500/5 hover:bg-emerald-500/10',
  US_FUND:
    'border-orange-500/30 text-orange-600 dark:text-orange-400 bg-orange-500/5 hover:bg-orange-500/10',
  METAL:
    'border-yellow-500/30 text-yellow-600 dark:text-yellow-400 bg-yellow-500/5 hover:bg-yellow-500/10',
  BOND: 'border-amber-500/30 text-amber-600 dark:text-amber-400 bg-amber-500/5 hover:bg-amber-500/10',
  OTHER: 'border-gray-500/30 text-gray-500 dark:text-gray-400 bg-gray-500/5 hover:bg-gray-500/10',
};

const TYPE_AVATAR_THEMES: Record<string, string> = {
  MF: 'from-blue-500 to-indigo-600 shadow-blue-500/10 text-white',
  ETF: 'from-violet-500 to-purple-600 shadow-violet-500/10 text-white',
  STOCK: 'from-emerald-500 to-teal-600 shadow-emerald-500/10 text-white',
  US_FUND: 'from-orange-500 to-amber-600 shadow-orange-500/10 text-white',
  METAL: 'from-yellow-400 to-amber-500 shadow-yellow-500/10 text-amber-950',
  BOND: 'from-rose-400 to-pink-500 shadow-rose-500/10 text-white',
  OTHER: 'from-slate-400 to-slate-600 shadow-slate-500/10 text-white',
};

function getInstrumentInitials(name: string) {
  const clean = name.replace(/[^a-zA-Z0-9 ]/g, '').trim();
  const parts = clean.split(' ').filter(Boolean);
  if (parts.length === 0) return '??';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[1][0]).toUpperCase();
}

// --- KPI Card ---
function KpiCard({
  label,
  value,
  customContent,
  sub,
  color,
  icon: Icon,
}: {
  label: string;
  value?: number;
  customContent?: React.ReactNode;
  sub?: React.ReactNode;
  color: 'blue' | 'emerald' | 'rose' | 'violet';
  icon?: any;
}) {
  const accent = {
    blue: 'text-blue-500 dark:text-blue-400',
    emerald: 'text-emerald-500 dark:text-emerald-400',
    rose: 'text-rose-500 dark:text-rose-400',
    violet: 'text-violet-500 dark:text-violet-400',
  }[color];

  const bgLight = {
    blue: 'bg-blue-500/5 border-blue-500/10 text-blue-500 dark:text-blue-400',
    emerald: 'bg-emerald-500/5 border-emerald-500/10 text-emerald-500 dark:text-emerald-400',
    rose: 'bg-rose-500/5 border-rose-500/10 text-rose-500 dark:text-rose-400',
    violet: 'bg-violet-500/5 border-violet-500/10 text-violet-500 dark:text-violet-400',
  }[color];

  const glowShadow = {
    blue: 'hover:shadow-blue-500/5',
    emerald: 'hover:shadow-emerald-500/5',
    rose: 'hover:shadow-rose-500/5',
    violet: 'hover:shadow-violet-500/5',
  }[color];

  return (
    <Card
      className={cn(
        'overflow-hidden border border-border/50 bg-card/45 shadow-sm backdrop-blur-sm transition-all duration-300 hover:-translate-y-1 hover:shadow-md',
        glowShadow,
      )}
    >
      <CardContent className="flex items-start justify-between p-6">
        <div className="flex-1 space-y-1.5">
          <p className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/80">
            {label}
          </p>
          {customContent ? (
            customContent
          ) : value !== undefined ? (
            <div className={cn('text-2xl font-extrabold tabular-nums tracking-tight', accent)}>
              <MaskedAmount value={value} format={formatINR} />
            </div>
          ) : (
            <span className="font-semibold text-muted-foreground">—</span>
          )}
          {sub && (
            <div className="mt-1 text-[10px] font-semibold leading-normal text-muted-foreground/75">
              {sub}
            </div>
          )}
        </div>
        {Icon && (
          <div
            className={cn(
              'ml-3 flex flex-shrink-0 items-center justify-center rounded-xl border p-2.5 shadow-inner',
              bgLight,
            )}
          >
            <Icon className="size-4.5" />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

const col = createColumnHelper<ClosedPositionInfo>();

function ClosedPositionsPage() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<'ALL' | 'PARTIAL' | 'CLOSED'>('ALL');
  const [sorting, setSorting] = useState<SortingState>([{ id: 'exit_date', desc: true }]);
  const { masked } = usePrivacy();

  const { data, isLoading, isError } = useQuery({
    queryKey: ['closed-positions'],
    queryFn: () => portfolioApi.closedPositions(),
    staleTime: 60_000,
  });

  const filtered = useMemo(
    () =>
      (data ?? []).filter((p) => {
        if (
          search &&
          !p.instrument_name.toLowerCase().includes(search.toLowerCase()) &&
          !p.asset_type.toLowerCase().includes(search.toLowerCase())
        )
          return false;
        if (statusFilter !== 'ALL' && p.status !== statusFilter) return false;
        return true;
      }),
    [data, search, statusFilter],
  );

  const totals = useMemo(() => {
    const pl = filtered.reduce((sum, p) => sum + p.realized_profit_loss, 0);
    const invested = filtered.reduce((sum, p) => sum + p.total_invested, 0);
    return { pl, invested, pct: invested !== 0 ? (pl / invested) * 100 : 0 };
  }, [filtered]);

  const columns = useMemo(
    () => [
      col.accessor('instrument_name', {
        header: 'Instrument',
        cell: (info) => {
          const original = info.row.original;
          const initials = getInstrumentInitials(info.getValue());
          const type = original.asset_type;
          const gradient = TYPE_AVATAR_THEMES[type] ?? TYPE_AVATAR_THEMES['OTHER'];
          return (
            <div className="flex items-center gap-3">
              <div
                className={cn(
                  'flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br text-xs font-bold tracking-wider shadow-md transition-all duration-300 hover:scale-105',
                  gradient,
                )}
              >
                {initials}
              </div>
              <div className="min-w-0">
                <p
                  className="max-w-[160px] cursor-default truncate font-semibold text-foreground transition-colors hover:text-primary xl:max-w-[260px]"
                  title={info.getValue()}
                >
                  {info.getValue()}
                </p>
                {original.categories && original.categories.length > 0 && (
                  <p className="max-w-[160px] truncate text-[10px] font-medium tracking-wide text-muted-foreground/80 xl:max-w-[260px]">
                    {original.categories.join(' · ')}
                  </p>
                )}
              </div>
            </div>
          );
        },
      }),
      col.accessor('status', {
        header: 'Status',
        cell: (info) => {
          const isPartial = info.getValue() === 'PARTIAL';
          return (
            <Badge
              variant="outline"
              className={cn(
                'rounded-full border px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider shadow-sm transition-all duration-300',
                isPartial
                  ? 'border-amber-500/25 bg-amber-500/10 text-amber-500 shadow-amber-500/5'
                  : 'border-emerald-500/25 bg-emerald-500/10 text-emerald-500 shadow-emerald-500/5',
              )}
            >
              {isPartial ? 'Partial' : 'Closed'}
            </Badge>
          );
        },
      }),
      col.accessor('asset_type', {
        header: 'Type',
        cell: (info) => {
          const val = info.getValue();
          return (
            <span
              className={cn(
                'inline-flex items-center rounded-full border px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider',
                TYPE_COLORS[val] ?? TYPE_COLORS['OTHER'],
              )}
            >
              {val}
            </span>
          );
        },
      }),
      col.accessor('total_units_bought', {
        header: 'Bought',
        cell: (info) => (
          <span className="font-semibold tabular-nums text-foreground/90">
            {masked ? '••••' : formatNumber(info.getValue())}
          </span>
        ),
        meta: { align: 'right' },
      }),
      col.accessor('total_units_sold', {
        header: 'Sold',
        cell: (info) => (
          <span className="font-semibold tabular-nums text-foreground/90">
            {masked ? '••••' : formatNumber(info.getValue())}
          </span>
        ),
        meta: { align: 'right' },
      }),
      col.accessor('avg_buy_price', {
        header: 'Avg Buy',
        cell: (info) => (
          <MaskedAmount
            value={info.getValue()}
            format={formatINR}
            className="font-medium tabular-nums text-muted-foreground"
          />
        ),
        meta: { align: 'right' },
      }),
      col.accessor('avg_sell_price', {
        header: 'Avg Sell',
        cell: (info) => (
          <MaskedAmount
            value={info.getValue()}
            format={formatINR}
            className="font-medium tabular-nums text-foreground/90"
          />
        ),
        meta: { align: 'right' },
      }),
      col.accessor('total_invested', {
        header: 'Invested',
        cell: (info) => (
          <MaskedAmount
            value={info.getValue()}
            format={formatINR}
            className="font-semibold tabular-nums text-foreground"
          />
        ),
        meta: { align: 'right' },
      }),
      col.accessor('total_sold_value', {
        header: 'Proceeds',
        cell: (info) => (
          <MaskedAmount
            value={info.getValue()}
            format={formatINR}
            className="font-semibold tabular-nums text-foreground"
          />
        ),
        meta: { align: 'right' },
      }),
      col.accessor('realized_profit_loss', {
        header: 'Realized P&L',
        cell: (info) => {
          const v = info.getValue();
          const pct = info.row.original.roi_percent;
          const isProfit = v >= 0;
          return (
            <div className="flex flex-col items-end gap-0.5 text-right">
              <MaskedAmount
                value={v}
                format={formatINR}
                className={cn(
                  'font-semibold tabular-nums',
                  isProfit
                    ? 'text-emerald-500 dark:text-emerald-400'
                    : 'text-rose-500 dark:text-rose-400',
                )}
              />
              <span
                className={cn(
                  'py-0.2 inline-flex items-center gap-0.5 rounded-full border px-1.5 text-[10px] font-bold tabular-nums',
                  isProfit
                    ? 'border-emerald-500/15 bg-emerald-500/10 text-emerald-500'
                    : 'border-rose-500/15 bg-rose-500/10 text-rose-500',
                )}
              >
                {isProfit ? '+' : ''}{pct.toFixed(2)}%
              </span>
            </div>
          );
        },
        meta: { align: 'right' },
      }),
      col.accessor('exit_date', {
        header: 'Exit Date',
        cell: (info) => {
          const val = info.getValue();
          return (
            <span className="text-xs font-medium text-muted-foreground/90">
              {val ? formatDate(val) : '—'}
            </span>
          );
        },
      }),
      col.accessor('platform', {
        header: 'Platform',
        cell: (info) => {
          const val = info.getValue();
          return (
            <span className="inline-flex items-center rounded-md border border-border/50 bg-muted px-2 py-0.5 text-xs font-semibold tracking-wide text-muted-foreground">
              {val.charAt(0).toUpperCase() + val.slice(1).toLowerCase()}
            </span>
          );
        },
      }),
    ],
    [masked],
  );

  const table = useReactTable({
    data: filtered,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-2 border-b border-border/40 pb-5 md:flex-row md:items-center md:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="bg-gradient-to-r from-foreground via-foreground/95 to-foreground/80 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
              Closed Positions
            </h1>
            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-0.5 text-[10px] font-bold text-emerald-500 shadow-sm shadow-emerald-500/5">
              <span className="relative flex size-1.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"></span>
                <span className="relative inline-flex size-1.5 rounded-full bg-emerald-500"></span>
              </span>
              History Logs
            </span>
          </div>
          <p className="mt-1 text-sm font-medium text-muted-foreground/90">
            Tracks Exited and Partially Exited lots, calculating realized P&L and ROI percentage.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2 self-start md:self-auto">
          <Badge
            variant="outline"
            className="border-emerald-500/35 bg-emerald-500/5 px-2.5 py-1 text-xs font-bold text-emerald-500 hover:bg-emerald-500/10 dark:text-emerald-400"
          >
            {data?.filter((p) => p.status === 'CLOSED').length ?? 0} Closed
          </Badge>
          <Badge
            variant="outline"
            className="border-amber-500/35 bg-amber-500/5 px-2.5 py-1 text-xs font-bold text-amber-500 hover:bg-amber-500/10 dark:text-amber-400"
          >
            {data?.filter((p) => p.status === 'PARTIAL').length ?? 0} Partial Exits
          </Badge>
        </div>
      </header>

      {/* Glassmorphic KPI Cards */}
      {filtered.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <KpiCard
            label="Total Invested (Sold Lots)"
            value={totals.invested}
            sub="Initial cost basis of sold shares/units"
            color="blue"
            icon={Wallet}
          />
          <KpiCard
            label="Realized P&L"
            value={totals.pl}
            sub={
              <span
                className={cn(
                  'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-bold',
                  totals.pl >= 0
                    ? 'border-emerald-500/15 bg-emerald-500/10 text-emerald-500'
                    : 'border-rose-500/15 bg-rose-500/10 text-rose-500',
                )}
              >
                {totals.pl >= 0 ? (
                  <TrendingUp className="size-3" />
                ) : (
                  <TrendingDown className="size-3" />
                )}
                {totals.pl >= 0 ? '+' : ''}{totals.pct.toFixed(2)}%
              </span>
            }
            color={totals.pl >= 0 ? 'emerald' : 'rose'}
            icon={totals.pl >= 0 ? TrendingUp : TrendingDown}
          />
          <KpiCard
            label="Overall Return"
            customContent={
              <div
                className={cn(
                  'text-2xl font-extrabold tabular-nums tracking-tight transition-colors duration-200',
                  totals.pct >= 0
                    ? 'text-emerald-500 dark:text-emerald-400'
                    : 'text-rose-500 dark:text-rose-400',
                )}
              >
                {totals.pct >= 0 ? '+' : ''}{totals.pct.toFixed(2)}%
              </div>
            }
            sub="Weighted ROI on all exited lots"
            color={totals.pct >= 0 ? 'emerald' : 'rose'}
            icon={ArrowUpRight}
          />
        </div>
      )}

      {/* Filters & Search Control Panel */}
      <Card className="space-y-4 rounded-2xl border border-border/80 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="relative min-w-[280px] max-w-md flex-1">
            <Search className="absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search by instrument name or type..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="h-9 rounded-xl border-border/80 bg-card/50 pl-10 pr-9 text-sm shadow-sm transition-colors hover:bg-card focus-visible:bg-card"
            />
            {search && (
              <button
                onClick={() => setSearch('')}
                className="absolute right-3 top-1/2 size-4 -translate-y-1/2 rounded-full p-0.5 text-muted-foreground transition-colors hover:bg-muted"
              >
                <X className="size-3" />
              </button>
            )}
          </div>

          {(search || statusFilter !== 'ALL') && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setSearch('');
                setStatusFilter('ALL');
              }}
              className="flex items-center gap-1.5 rounded-lg text-xs text-rose-500 transition-all hover:bg-rose-500/10 hover:text-rose-600 active:scale-95"
            >
              <X className="size-3.5" />
              Clear Filters
            </Button>
          )}
        </div>

        <div className="flex flex-col gap-3.5 border-t border-border/40 pt-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-[65px] text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Status:
            </span>
            <div className="flex flex-wrap gap-1.5">
              {(
                [
                  { value: 'ALL', label: 'All Positions', count: data?.length ?? 0 },
                  {
                    value: 'CLOSED',
                    label: 'Closed',
                    count: data?.filter((p) => p.status === 'CLOSED').length ?? 0,
                  },
                  {
                    value: 'PARTIAL',
                    label: 'Partially Exited',
                    count: data?.filter((p) => p.status === 'PARTIAL').length ?? 0,
                  },
                ] as const
              ).map((item) => (
                <button
                  key={item.value}
                  onClick={() => setStatusFilter(item.value)}
                  className={cn(
                    'rounded-full border px-3.5 py-1 text-xs font-semibold shadow-sm transition-all duration-200 active:scale-95',
                    statusFilter === item.value
                      ? 'border-primary bg-primary text-primary-foreground shadow-primary/20'
                      : 'border-border bg-card/45 text-muted-foreground hover:bg-muted',
                  )}
                >
                  {item.label} <span className="ml-1 text-[10px] opacity-70">({item.count})</span>
                </button>
              ))}
            </div>
          </div>
        </div>
      </Card>

      {/* Main Table Content */}
      <Card className="overflow-hidden border border-border/50 bg-card/45 shadow-sm backdrop-blur-sm transition-all duration-300">
        <CardContent className="p-0">
          {isLoading ? (
            <div className="flex flex-col items-center justify-center gap-3 py-16 text-muted-foreground">
              <Loader2 className="size-8 animate-spin text-primary" />
              <p className="animate-pulse text-sm font-semibold tracking-wide">
                Loading closed positions...
              </p>
            </div>
          ) : isError ? (
            <div className="flex flex-col items-center justify-center gap-3 rounded-2xl border border-destructive/20 bg-destructive/5 py-16 text-destructive">
              <p className="text-sm font-bold">Failed to load closed positions.</p>
              <p className="text-xs text-muted-foreground">Please try refreshing the page.</p>
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center justify-center rounded-2xl px-4 py-16 text-center">
              <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-muted/60 text-muted-foreground">
                <History className="size-6" />
              </div>
              <h3 className="text-base font-bold text-foreground">No closed positions found</h3>
              <p className="mt-1 max-w-sm text-xs text-muted-foreground/80">
                {data?.length === 0
                  ? 'Exited or partially exited lots will appear here after you sell a holding.'
                  : 'Adjust your search queries or filters to find what you are looking for.'}
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  {table.getHeaderGroups().map((hg) => (
                    <tr key={hg.id} className="border-b border-border/40">
                      {hg.headers.map((header) => (
                        <th
                          key={header.id}
                          className={cn(
                            'border-r border-border/10 px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground/80 last:border-r-0',
                            (header.column.columnDef.meta as any)?.align === 'right' &&
                              'text-right',
                          )}
                        >
                          {header.column.getCanSort() ? (
                            <button
                              onClick={header.column.getToggleSortingHandler()}
                              className="inline-flex items-center gap-1 transition-all duration-200 hover:text-foreground active:scale-95"
                            >
                              {flexRender(header.column.columnDef.header, header.getContext())}
                              {header.column.getIsSorted() === 'asc' ? (
                                <ArrowUp className="size-3 text-primary" />
                              ) : header.column.getIsSorted() === 'desc' ? (
                                <ArrowDown className="size-3 text-primary" />
                              ) : (
                                <ArrowUpDown className="size-3 opacity-40 hover:opacity-100" />
                              )}
                            </button>
                          ) : (
                            flexRender(header.column.columnDef.header, header.getContext())
                          )}
                        </th>
                      ))}
                    </tr>
                  ))}
                </thead>
                <tbody>
                  {table.getRowModel().rows.map((row) => (
                    <tr
                      key={row.id}
                      className="border-b border-border/40 transition-colors duration-150 last:border-0 hover:bg-muted/35"
                    >
                      {row.getVisibleCells().map((cell) => (
                        <td
                          key={cell.id}
                          className={cn(
                            'border-r border-border/10 px-4 py-3.5 last:border-r-0',
                            (cell.column.columnDef.meta as any)?.align === 'right' && 'text-right',
                          )}
                        >
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
