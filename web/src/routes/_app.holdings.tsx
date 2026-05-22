import { useState, useMemo } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  getFilteredRowModel,
  flexRender,
  createColumnHelper,
  type SortingState,
} from '@tanstack/react-table';
import {
  RefreshCw,
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  Loader2,
  Search,
  X,
  Wallet,
  ArrowUpRight,
  Filter,
} from 'lucide-react';

import { portfolioApi, instrumentsApi, ASSET_TYPES } from '@/lib/api';
import type { HoldingInfo } from '@/lib/api';
import { formatINR, formatNumber, formatDateTime } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent } from '@/components/ui/card';
import { toast } from '@/components/ui/sonner';
import { cn } from '@/lib/utils';
import { MaskedAmount } from '@/lib/privacy';

export const Route = createFileRoute('/_app/holdings')({
  component: HoldingsPage,
});

const col = createColumnHelper<HoldingInfo>();

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

function TypeCell({
  instrumentId,
  value,
  onChange,
}: {
  instrumentId: number;
  value: string;
  onChange: (id: number, type: string) => void;
}) {
  return (
    <div className="relative inline-flex items-center">
      <select
        value={value}
        onChange={(e) => onChange(instrumentId, e.target.value)}
        className={cn(
          'cursor-pointer appearance-none rounded-full border bg-transparent px-2.5 py-0.5 pr-5 text-[11px] font-semibold outline-none transition-all focus:ring-1 focus:ring-primary',
          TYPE_COLORS[value] ?? TYPE_COLORS['OTHER'],
        )}
        style={{
          backgroundImage: `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' fill='none' viewBox='0 0 24 24' stroke='%236b7280' stroke-width='3'%3E%3Cpath stroke-linecap='round' stroke-linejoin='round' d='M19.5 8.25l-7.5 7.5-7.5-7.5'/%3E%3C/svg%3E")`,
          backgroundRepeat: 'no-repeat',
          backgroundPosition: 'right 6px center',
          backgroundSize: '8px',
        }}
      >
        {ASSET_TYPES.map((t) => (
          <option key={t.value} value={t.value} className="bg-card font-sans text-foreground">
            {t.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function HoldingsPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [sorting, setSorting] = useState<SortingState>([]);
  const [typeFilter, setTypeFilter] = useState('');
  const [platformFilter, setPlatformFilter] = useState('');
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
                  'flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br text-xs font-semibold tracking-wider shadow-md transition-all duration-300 hover:scale-105',
                  gradient,
                )}
              >
                {initials}
              </div>
              <div className="min-w-0">
                <p
                  className="max-w-[180px] cursor-default truncate font-semibold text-foreground transition-colors hover:text-primary xl:max-w-[280px]"
                  title={info.getValue()}
                >
                  {info.getValue()}
                </p>
                {original.isin && (
                  <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground/80">
                    {original.isin}
                  </p>
                )}
              </div>
            </div>
          );
        },
      }),
      col.accessor('asset_type', {
        header: 'Type',
        cell: (info) => (
          <TypeCell
            instrumentId={info.row.original.instrument_id}
            value={info.getValue()}
            onChange={(info.table.options.meta as any)?.onTypeChange}
          />
        ),
      }),
      col.accessor('total_units', {
        header: 'Units',
        cell: (info) => (
          <span className="font-semibold tabular-nums text-foreground/90">
            {formatNumber(info.getValue())}
          </span>
        ),
        meta: { align: 'right' },
      }),
      col.accessor('avg_buy_price', {
        header: 'Avg Price',
        cell: (info) => (
          <MaskedAmount
            value={info.getValue()}
            format={formatINR}
            className="font-medium tabular-nums text-muted-foreground"
          />
        ),
        meta: { align: 'right' },
      }),
      col.accessor('invested_amount', {
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
      col.accessor('current_price', {
        header: 'CMP',
        cell: (info) => {
          const val = info.getValue();
          const fetched = info.row.original.last_fetched_at;
          return (
            <div className="text-right">
              {val != null ? (
                <MaskedAmount
                  value={val}
                  format={formatINR}
                  className="font-medium tabular-nums text-foreground"
                />
              ) : (
                <span className="font-medium text-muted-foreground">—</span>
              )}
              {fetched && (
                <p className="mt-0.5 text-[9px] font-medium tracking-tight text-muted-foreground/80">
                  {formatDateTime(fetched)}
                </p>
              )}
            </div>
          );
        },
        meta: { align: 'right' },
      }),
      col.accessor('current_value', {
        header: 'Value',
        cell: (info) => {
          const val = info.getValue();
          return val != null ? (
            <MaskedAmount
              value={val}
              format={formatINR}
              className="font-semibold tabular-nums text-foreground"
            />
          ) : (
            <span className="font-medium text-muted-foreground">—</span>
          );
        },
        meta: { align: 'right' },
      }),
      col.accessor('profit_loss', {
        header: 'P&L',
        cell: (info) => {
          const val = info.getValue();
          if (val == null) return <span className="font-medium text-muted-foreground">—</span>;
          const isProfit = val >= 0;
          return (
            <MaskedAmount
              value={val}
              format={formatINR}
              className={cn(
                'font-semibold tabular-nums transition-colors duration-200',
                isProfit
                  ? 'text-emerald-500 dark:text-emerald-400'
                  : 'text-rose-500 dark:text-rose-400',
              )}
            />
          );
        },
        meta: { align: 'right' },
      }),
      col.accessor('profit_loss_percent', {
        header: 'Return',
        cell: (info) => {
          const pct = info.getValue();
          if (pct == null) return <span className="font-medium text-muted-foreground">—</span>;
          const isPositive = pct >= 0;
          return (
            <span
              className={cn(
                'inline-flex items-center gap-0.5 rounded-full px-2 py-0.5 text-xs font-semibold tabular-nums',
                isPositive
                  ? 'border border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                  : 'border border-rose-500/20 bg-rose-500/10 text-rose-600 dark:text-rose-400',
              )}
            >
              {isPositive ? '+' : ''}{pct.toFixed(2)}%
            </span>
          );
        },
        meta: { align: 'right' },
      }),
      col.accessor('platform', {
        header: 'Platform',
        cell: (info) => {
          const val = info.getValue();
          return (
            <span className="inline-flex items-center rounded-md border border-border/50 bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
              {val.charAt(0) + val.slice(1).toLowerCase()}
            </span>
          );
        },
      }),
    ],
    [],
  );

  const { data, isLoading, isError } = useQuery({
    queryKey: ['holdings'],
    queryFn: () => portfolioApi.holdings(),
    staleTime: 60_000,
  });

  const fetchPrices = useMutation({
    mutationFn: () =>
      fetch('/api/instruments/fetch-prices', { method: 'POST', credentials: 'include' }).then((r) =>
        r.json(),
      ),
    onSuccess: (res) => {
      toast.success(`Fetched ${res.fetched} prices${res.failed ? `, ${res.failed} failed` : ''}`);
      queryClient.invalidateQueries({ queryKey: ['holdings'] });
    },
    onError: () => toast.error('Price fetch failed'),
  });

  const updateType = useMutation({
    mutationFn: ({ id, type }: { id: number; type: string }) =>
      instrumentsApi.updateAssetType(id, type),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['holdings'] });
      toast.success('Type updated');
    },
    onError: () => toast.error('Failed to update type'),
  });

  // Derive unique types and platforms from loaded data.
  const availableTypes = useMemo(
    () => [...new Set((data ?? []).map((h) => h.asset_type))].sort(),
    [data],
  );
  const availablePlatforms = useMemo(
    () => [...new Set((data ?? []).map((h) => h.platform))].sort(),
    [data],
  );

  const typeCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    (data ?? []).forEach((h) => {
      counts[h.asset_type] = (counts[h.asset_type] || 0) + 1;
    });
    return counts;
  }, [data]);

  const platformCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    (data ?? []).forEach((h) => {
      counts[h.platform] = (counts[h.platform] || 0) + 1;
    });
    return counts;
  }, [data]);

  const filtered = useMemo(
    () =>
      (data ?? []).filter((h) => {
        if (
          search &&
          !h.instrument_name.toLowerCase().includes(search.toLowerCase()) &&
          !(h.isin ?? '').toLowerCase().includes(search.toLowerCase())
        )
          return false;
        if (typeFilter && h.asset_type !== typeFilter) return false;
        if (platformFilter && h.platform !== platformFilter) return false;
        return true;
      }),
    [data, search, typeFilter, platformFilter],
  );

  const stats = useMemo(() => {
    let totalInvested = 0;
    let totalCurrentValue = 0;
    let totalPL = 0;

    filtered.forEach((h) => {
      totalInvested += h.invested_amount ?? 0;
      totalCurrentValue += h.current_value ?? h.invested_amount ?? 0;
    });

    totalPL = totalCurrentValue - totalInvested;
    const plPercent = totalInvested > 0 ? (totalPL / totalInvested) * 100 : 0;

    return {
      totalInvested,
      totalCurrentValue,
      totalPL,
      plPercent,
      count: filtered.length,
    };
  }, [filtered]);

  const table = useReactTable({
    data: filtered,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    meta: {
      onTypeChange: (id: number, type: string) => updateType.mutate({ id, type }),
    },
  });

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Portfolio
          </p>
          <h1 className="bg-gradient-to-r from-foreground to-foreground/75 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
            Holdings
          </h1>
          <p className="mt-1 text-xs font-medium text-muted-foreground/80">
            {data?.length ?? 0} active positions
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => fetchPrices.mutate()}
          disabled={fetchPrices.isPending}
          className="flex items-center gap-2 rounded-xl border-border/80 text-xs font-medium shadow-sm transition-all hover:bg-card/50"
        >
          <RefreshCw className={cn('size-3.5', fetchPrices.isPending && 'animate-spin')} />
          Refresh prices
        </Button>
      </header>

      {/* KPI Cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard
          label="Total Invested"
          value={stats.totalInvested}
          sub={`${stats.count} positions`}
          color="blue"
          icon={Wallet}
        />
        <KpiCard
          label="Current Value"
          value={stats.totalCurrentValue}
          sub="Updated live"
          color="emerald"
          icon={ArrowUpRight}
        />
        <KpiCard
          label="Net Profit / Loss"
          value={stats.totalPL}
          sub={`${stats.plPercent >= 0 ? '+' : ''}${stats.plPercent.toFixed(2)}%`}
          color={stats.totalPL >= 0 ? 'emerald' : 'rose'}
          trend={stats.totalPL >= 0 ? 'up' : 'down'}
          isPl={true}
        />
        <KpiCard
          label="Active Filters"
          customContent={
            <div className="flex items-baseline gap-1 text-2xl font-bold text-foreground">
              <span>{filtered.length}</span>
              <span className="text-xs font-normal text-muted-foreground">
                of {data?.length ?? 0}
              </span>
            </div>
          }
          sub={typeFilter || platformFilter || search ? 'Filters are active' : 'No filters applied'}
          color="violet"
          icon={Filter}
        />
      </div>

      {/* Search and Filters Section */}
      <Card className="space-y-4 rounded-2xl border border-border/80 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="relative min-w-[280px] max-w-md flex-1">
            <Search className="absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search by name, symbol, or ISIN..."
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

          {(search || typeFilter || platformFilter) && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setSearch('');
                setTypeFilter('');
                setPlatformFilter('');
              }}
              className="flex items-center gap-1.5 rounded-lg text-xs text-rose-500 hover:bg-rose-500/10 hover:text-rose-600"
            >
              <X className="size-3.5" />
              Clear Filters
            </Button>
          )}
        </div>

        <div className="flex flex-col gap-3.5 border-t border-border/40 pt-4">
          {/* Type Filter Pills */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-[65px] text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Type:
            </span>
            <div className="flex flex-wrap gap-1.5">
              <button
                onClick={() => setTypeFilter('')}
                className={cn(
                  'rounded-full border px-3 py-0.5 text-xs font-semibold transition-all duration-200',
                  !typeFilter
                    ? 'border-primary bg-primary text-primary-foreground shadow-sm shadow-primary/20'
                    : 'border-border bg-card/45 text-muted-foreground hover:bg-muted',
                )}
              >
                All <span className="ml-1 opacity-70">({data?.length ?? 0})</span>
              </button>
              {availableTypes.map((t) => {
                const label = ASSET_TYPES.find((a) => a.value === t)?.label ?? t;
                const count = typeCounts[t] ?? 0;
                return (
                  <button
                    key={t}
                    onClick={() => setTypeFilter(typeFilter === t ? '' : t)}
                    className={cn(
                      'flex items-center gap-1.5 rounded-full border px-3 py-0.5 text-xs font-semibold transition-all duration-200',
                      typeFilter === t
                        ? 'border-primary bg-primary text-primary-foreground shadow-sm shadow-primary/20'
                        : 'border-border bg-card/45 text-muted-foreground hover:bg-muted',
                    )}
                  >
                    <span>{label}</span>
                    <span
                      className={cn(
                        'py-0.2 rounded-full px-1.5 text-[10px]',
                        typeFilter === t
                          ? 'bg-primary-foreground/20 text-primary-foreground'
                          : 'border border-border/50 bg-muted text-muted-foreground',
                      )}
                    >
                      {count}
                    </span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* Platform Filter Pills */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-[65px] text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Platform:
            </span>
            <div className="flex flex-wrap gap-1.5">
              <button
                onClick={() => setPlatformFilter('')}
                className={cn(
                  'rounded-full border px-3 py-0.5 text-xs font-semibold transition-all duration-200',
                  !platformFilter
                    ? 'border-primary bg-primary text-primary-foreground shadow-sm shadow-primary/20'
                    : 'border-border bg-card/45 text-muted-foreground hover:bg-muted',
                )}
              >
                All <span className="ml-1 opacity-70">({data?.length ?? 0})</span>
              </button>
              {availablePlatforms.map((p) => {
                const count = platformCounts[p] ?? 0;
                const label = p.charAt(0) + p.slice(1).toLowerCase();
                return (
                  <button
                    key={p}
                    onClick={() => setPlatformFilter(platformFilter === p ? '' : p)}
                    className={cn(
                      'flex items-center gap-1.5 rounded-full border px-3 py-0.5 text-xs font-semibold transition-all duration-200',
                      platformFilter === p
                        ? 'border-primary bg-primary text-primary-foreground shadow-sm shadow-primary/20'
                        : 'border-border bg-card/45 text-muted-foreground hover:bg-muted',
                    )}
                  >
                    <span>{label}</span>
                    <span
                      className={cn(
                        'py-0.2 rounded-full px-1.5 text-[10px]',
                        platformFilter === p
                          ? 'bg-primary-foreground/20 text-primary-foreground'
                          : 'border border-border/50 bg-muted text-muted-foreground',
                      )}
                    >
                      {count}
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        </div>
      </Card>

      {/* Main Table Card */}
      <Card className="overflow-hidden rounded-2xl border border-border/80 bg-card/45 shadow-sm backdrop-blur-sm">
        <CardContent className="p-0">
          {isLoading ? (
            <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
              <Loader2 className="size-5 animate-spin text-primary" /> Loading holdings data…
            </div>
          ) : isError ? (
            <div className="flex flex-col items-center justify-center p-12 text-center text-rose-500">
              <p className="font-semibold">Failed to load holdings.</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Please check server connectivity and try again.
              </p>
            </div>
          ) : filtered.length === 0 ? (
            data?.length === 0 ? (
              <div className="flex flex-col items-center justify-center px-4 py-16 text-center">
                <div className="mb-4 rounded-2xl border border-border/30 bg-muted/60 p-4 text-primary">
                  <Wallet className="size-8 stroke-[1.5]" />
                </div>
                <h3 className="text-lg font-semibold text-foreground">Start your wealth folio</h3>
                <p className="mt-1 max-w-sm text-sm text-muted-foreground">
                  No holdings found. Upload transactions or import your portfolio data to get
                  started.
                </p>
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center px-4 py-16 text-center">
                <div className="mb-4 rounded-2xl border border-border/30 bg-muted/60 p-4 text-muted-foreground/80">
                  <Search className="size-8 stroke-[1.5]" />
                </div>
                <h3 className="text-lg font-semibold text-foreground">No matching positions</h3>
                <p className="mt-1 max-w-sm text-sm text-muted-foreground">
                  We couldn't find any holdings matching your search or filters. Try adjusting them
                  or clear the filters.
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setSearch('');
                    setTypeFilter('');
                    setPlatformFilter('');
                  }}
                  className="mt-4 flex items-center gap-1.5 rounded-xl border-border/80 text-xs shadow-sm"
                >
                  Reset all filters
                </Button>
              </div>
            )
          ) : (
            <div className="scrollbar-thin overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  {table.getHeaderGroups().map((hg) => (
                    <tr key={hg.id} className="border-b border-border/85 bg-muted/20">
                      {hg.headers.map((header) => (
                        <th
                          key={header.id}
                          className={cn(
                            'border-b border-border/70 px-4 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90',
                            (header.column.columnDef.meta as any)?.align === 'right' &&
                              'text-right',
                          )}
                        >
                          {header.column.getCanSort() ? (
                            <button
                              onClick={header.column.getToggleSortingHandler()}
                              className="group inline-flex items-center gap-1 transition-colors hover:text-foreground"
                            >
                              {flexRender(header.column.columnDef.header, header.getContext())}
                              {header.column.getIsSorted() === 'asc' ? (
                                <ArrowUp className="size-3 text-primary" />
                              ) : header.column.getIsSorted() === 'desc' ? (
                                <ArrowDown className="size-3 text-primary" />
                              ) : (
                                <ArrowUpDown className="size-3 opacity-30 transition-opacity group-hover:opacity-70" />
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
                <tbody className="divide-y divide-border/60">
                  {table.getRowModel().rows.map((row) => (
                    <tr
                      key={row.id}
                      className="transition-colors duration-150 last:border-0 hover:bg-muted/40"
                    >
                      {row.getVisibleCells().map((cell) => (
                        <td
                          key={cell.id}
                          className={cn(
                            'px-4 py-3.5 align-middle',
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

function KpiCard({
  label,
  value,
  customContent,
  sub,
  color,
  trend,
  isPl = false,
  icon: Icon,
}: {
  label: string;
  value?: number;
  customContent?: React.ReactNode;
  sub: React.ReactNode;
  color: 'blue' | 'emerald' | 'rose' | 'violet';
  trend?: 'up' | 'down';
  isPl?: boolean;
  icon?: any;
}) {
  const accent = {
    blue: 'text-blue-500 dark:text-blue-400',
    emerald: 'text-emerald-500 dark:text-emerald-400',
    rose: 'text-rose-500 dark:text-rose-400',
    violet: 'text-violet-500 dark:text-violet-400',
  }[color];

  const bgLight = {
    blue: 'bg-blue-500/5',
    emerald: 'bg-emerald-500/5',
    rose: 'bg-rose-500/5',
    violet: 'bg-violet-500/5',
  }[color];

  return (
    <Card className="overflow-hidden border border-border/80 bg-card/65 shadow-sm backdrop-blur-sm transition-all duration-300 hover:-translate-y-0.5 hover:shadow-md">
      <CardContent className="flex items-start justify-between p-5">
        <div className="space-y-2">
          <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
            {label}
          </p>
          {customContent ? (
            customContent
          ) : value !== undefined ? (
            <div className={cn('text-2xl font-bold tabular-nums tracking-tight', accent)}>
              <MaskedAmount
                value={value}
                format={(v) => {
                  if (isPl) {
                    const formatted = formatINR(v);
                    return v >= 0 ? `+${formatted}` : formatted;
                  }
                  return formatINR(v);
                }}
              />
            </div>
          ) : (
            <span className="font-semibold text-muted-foreground">—</span>
          )}
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            {trend === 'up' && <ArrowUp className="size-3 animate-bounce text-emerald-500" />}
            {trend === 'down' && <ArrowDown className="size-3 animate-bounce text-rose-500" />}
            <span className="font-semibold">{sub}</span>
          </div>
        </div>
        {Icon && (
          <div className={cn('rounded-xl border border-border/10 p-2.5 shadow-inner', bgLight)}>
            <Icon className={cn('size-4.5', accent)} />
          </div>
        )}
      </CardContent>
    </Card>
  );
}
