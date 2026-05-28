import { useState, useEffect } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
  PieChart,
  Pie,
  Cell,
} from 'recharts';
import { TrendingUp, TrendingDown, RefreshCw, Loader2, Wallet, Zap, LineChart } from 'lucide-react';

import { portfolioApi, trendsApi } from '@/lib/api';
import type { SnapshotRow } from '@/lib/api';
import { formatINR, formatDate } from '@/lib/format';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { toast } from '@/components/ui/sonner';
import { cn } from '@/lib/utils';
import { usePrivacy, MaskedAmount } from '@/lib/privacy';

export const Route = createFileRoute('/_app/dashboard')({
  component: DashboardPage,
});

const RANGES = [
  { label: '30D', days: 30 },
  { label: '90D', days: 90 },
  { label: '1Y', days: 365 },
  { label: 'All', days: 0 },
] as const;

function DashboardPage() {
  const queryClient = useQueryClient();
  const [rangeDays, setRangeDays] = useState<number>(90);
  const [isMounted, setIsMounted] = useState(false);

  useEffect(() => {
    const timer = setTimeout(() => setIsMounted(true), 150);
    return () => clearTimeout(timer);
  }, []);

  const { data: summary, isLoading: summaryLoading } = useQuery({
    queryKey: ['portfolio', 'summary'],
    queryFn: portfolioApi.summary,
    staleTime: 60_000,
  });

  const { data: trendData, isLoading: trendLoading } = useQuery({
    queryKey: ['trends', 'portfolio', rangeDays],
    queryFn: () => trendsApi.portfolio({ days: rangeDays }),
    staleTime: 60_000,
  });

  const [backfillRunning, setBackfillRunning] = useState(false);

  useEffect(() => {
    if (!backfillRunning) return;
    const id = setInterval(async () => {
      try {
        const s = await trendsApi.backfillStatus();
        if (!s.running) {
          clearInterval(id);
          setBackfillRunning(false);
          if (s.error) {
            toast.error(`Rebuild failed: ${s.error}`);
          } else {
            toast.success(`Rebuilt ${s.snapshots_created} snapshots!`);
            queryClient.invalidateQueries({ queryKey: ['trends'] });
          }
        }
      } catch {
        clearInterval(id);
        setBackfillRunning(false);
      }
    }, 5_000);
    return () => clearInterval(id);
  }, [backfillRunning, queryClient]);

  const backfill = useMutation({
    mutationFn: trendsApi.backfill,
    onSuccess: () => {
      setBackfillRunning(true);
      toast.info('Rebuilding history...');
    },
    onError: () => toast.error('Failed to start rebuild'),
  });

  const pl = summary?.total_profit_loss ?? 0;
  const plPositive = pl >= 0;
  const plPercent = summary?.profit_loss_percent ?? 0;

  // Custom formatted pills/subtexts
  const investedSubtext = (
    <span className="inline-flex items-center gap-1 text-[10px] font-semibold text-muted-foreground/75">
      {summary?.holdings_count ?? 0} active holdings
    </span>
  );

  const valueSubtext = (
    <span className="inline-flex items-center gap-1 text-[10px] font-semibold text-muted-foreground/75">
      {summary?.last_updated ? `As of ${formatDate(summary.last_updated)}` : 'No prices yet'}
    </span>
  );

  const plPercentText = `${plPercent >= 0 ? '+' : ''}${plPercent.toFixed(2)}%`;
  const plSubtext = (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-bold',
        plPositive
          ? 'border-emerald-500/15 bg-emerald-500/10 text-emerald-500'
          : 'border-rose-500/15 bg-rose-500/10 text-rose-500',
      )}
    >
      {plPositive ? <TrendingUp className="size-3" /> : <TrendingDown className="size-3" />}
      {plPercentText}
    </span>
  );

  const xirrText = summary?.xirr != null ? `${summary.xirr.toFixed(2)}%` : '—';
  const xirrSubtext = (
    <span className="inline-flex items-center gap-1 text-[10px] font-semibold text-muted-foreground/75">
      Annualised performance
    </span>
  );

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 border-b border-border/40 pb-5 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="bg-gradient-to-r from-foreground via-foreground/95 to-foreground/80 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
              Overview Dashboard
            </h1>
            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-0.5 text-[10px] font-bold text-emerald-500 shadow-sm shadow-emerald-500/5">
              <span className="relative flex size-1.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"></span>
                <span className="relative inline-flex size-1.5 rounded-full bg-emerald-500"></span>
              </span>
              Live Sync
            </span>
          </div>
          <p className="mt-1 text-sm font-medium text-muted-foreground/90">
            Real-time overview of your wealth, asset distributions, and historical trends.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => backfill.mutate(undefined)}
          disabled={backfill.isPending || backfillRunning}
          className="h-9 shrink-0 gap-2 self-start border-border/60 bg-background/50 font-semibold shadow-sm transition-all duration-300 hover:bg-muted/50 hover:shadow active:scale-95 sm:self-auto"
        >
          {backfill.isPending || backfillRunning ? (
            <Loader2 className="size-4 animate-spin text-primary" />
          ) : (
            <RefreshCw className="size-4 text-muted-foreground transition-transform duration-500 hover:rotate-180" />
          )}
          <span>{backfill.isPending || backfillRunning ? 'Rebuilding...' : 'Rebuild history'}</span>
        </Button>
      </header>

      {/* KPI cards */}
      <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard
          label="Total invested"
          value={summaryLoading ? undefined : (summary?.total_invested ?? 0)}
          sub={investedSubtext}
          color="blue"
          icon={Wallet}
        />
        <KpiCard
          label="Current value"
          value={summaryLoading ? undefined : (summary?.total_current_value ?? 0)}
          sub={valueSubtext}
          color="emerald"
          icon={LineChart}
        />
        <KpiCard
          label="Profit / Loss"
          value={summaryLoading ? undefined : pl}
          sub={plSubtext}
          color={plPositive ? 'emerald' : 'rose'}
          icon={plPositive ? TrendingUp : TrendingDown}
        />
        <KpiCard
          label="XIRR"
          customContent={
            <div className="text-2xl font-extrabold tabular-nums tracking-tight text-violet-500 dark:text-violet-400">
              {summaryLoading ? <span className="text-muted-foreground">—</span> : xirrText}
            </div>
          }
          sub={xirrSubtext}
          color="violet"
          icon={Zap}
        />
      </div>

      {/* Trend chart */}
      <Card className="overflow-hidden border border-border/50 bg-card/45 shadow-sm backdrop-blur-sm transition-all duration-300">
        <CardHeader className="flex flex-col gap-4 border-b border-border/40 pb-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <CardTitle className="text-lg font-bold tracking-tight">
              Portfolio Value Trend
            </CardTitle>
            <CardDescription className="text-xs font-medium text-muted-foreground/80">
              {trendData?.length
                ? `Visualizing ${trendData.length} historical snapshot data points`
                : 'Run "Rebuild history" to populate trend data'}
            </CardDescription>
          </div>
          <div className="flex gap-1.5 self-start rounded-lg border border-border/40 bg-muted/60 p-0.5 sm:self-auto">
            {RANGES.map((r) => (
              <button
                key={r.label}
                onClick={() => setRangeDays(r.days)}
                className={cn(
                  'rounded-md px-3 py-1 text-xs font-bold transition-all duration-200',
                  rangeDays === r.days
                    ? 'bg-background text-foreground shadow-sm'
                    : 'text-muted-foreground/75 hover:bg-muted/40 hover:text-foreground',
                )}
              >
                {r.label}
              </button>
            ))}
          </div>
        </CardHeader>
        <CardContent className="pt-6">
          {trendLoading || !isMounted ? (
            <div className="flex h-[280px] items-center justify-center text-sm font-medium text-muted-foreground/70">
              <Loader2 className="mr-2 size-4 animate-spin text-primary" /> Loading trend
              visualization…
            </div>
          ) : !trendData || trendData.length === 0 ? (
            <div className="flex h-48 flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border/55 bg-muted/10 text-sm font-medium text-muted-foreground/75">
              <p>No trend snapshots found.</p>
              <p className="text-xs text-muted-foreground/60">
                Upload transactions and click "Rebuild history" above.
              </p>
            </div>
          ) : (
            <TrendChart data={trendData} rangeDays={rangeDays} />
          )}
        </CardContent>
      </Card>

      {/* Allocation breakdown */}
      {summary && (summary.by_asset_type?.length ?? 0) > 0 && isMounted && (
        <div className="grid gap-5 md:grid-cols-2">
          <AllocationDonut title="By asset type" items={summary.by_asset_type} />
          <AllocationDonut title="By platform" items={summary.by_platform} />
        </div>
      )}
    </div>
  );
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

// --- Trend chart ---

const inrFormatter = (v: number) =>
  v >= 1_00_00_000
    ? `₹${(v / 1_00_00_000).toFixed(1)}Cr`
    : v >= 1_00_000
      ? `₹${(v / 1_00_000).toFixed(1)}L`
      : `₹${v.toLocaleString('en-IN')}`;

function CustomTrendTooltip({ active, payload }: any) {
  if (active && payload && payload.length) {
    const data = payload[0].payload as SnapshotRow;
    const isProfit = data.profit >= 0;
    const profitColor = isProfit ? 'text-emerald-500 font-semibold' : 'text-rose-500 font-semibold';
    return (
      <div
        className="min-w-[175px] space-y-2 rounded-xl border border-border/60 p-3.5 text-xs shadow-2xl backdrop-blur-md duration-150 animate-in fade-in zoom-in-95"
        style={{ backgroundColor: 'hsl(var(--card))', opacity: 0.95 }}
      >
        <p className="border-b border-border/50 pb-1.5 font-bold text-foreground">
          {new Date(data.date).toLocaleDateString('en-IN', { dateStyle: 'medium' })}
        </p>
        <div className="space-y-1.5 pt-0.5">
          <div className="flex justify-between gap-4">
            <span className="text-muted-foreground/80">Value:</span>
            <span className="font-bold tabular-nums text-foreground">
              <MaskedAmount value={data.value} format={formatINR} />
            </span>
          </div>
          <div className="flex justify-between gap-4">
            <span className="text-muted-foreground/80">Invested:</span>
            <span className="font-semibold tabular-nums text-foreground">
              <MaskedAmount value={data.invested} format={formatINR} />
            </span>
          </div>
          <div className="mt-1.5 flex justify-between gap-4 border-t border-border/50 pt-1.5">
            <span className="font-medium text-muted-foreground/80">Profit/Loss:</span>
            <span className={cn('flex items-center gap-1 tabular-nums', profitColor)}>
              <MaskedAmount value={data.profit} format={formatINR} />
              <span className="text-[10px] font-normal opacity-75">
                ({isProfit ? '+' : ''}{data.profit_percent.toFixed(2)}%)
              </span>
            </span>
          </div>
        </div>
      </div>
    );
  }
  return null;
}

function TrendChart({ data, rangeDays }: { data: SnapshotRow[]; rangeDays: number }) {
  const { masked } = usePrivacy();
  return (
    <ResponsiveContainer width="100%" height={280}>
      <AreaChart
        key={`chart-${data.length}-${rangeDays}-${masked}`}
        data={data}
        margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
      >
        <defs>
          <linearGradient id="gValue" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="#10b981" stopOpacity={0.2} />
            <stop offset="95%" stopColor="#10b981" stopOpacity={0} />
          </linearGradient>
          <linearGradient id="gInvested" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.15} />
            <stop offset="95%" stopColor="#3b82f6" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border) / 0.3)" />
        <XAxis
          dataKey="date"
          tickLine={false}
          axisLine={false}
          tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground) / 0.85)', fontWeight: 500 }}
          tickFormatter={(d: string) => {
            const date = new Date(d);
            return date.toLocaleDateString('en-IN', { month: 'short', day: 'numeric' });
          }}
          dy={8}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground) / 0.85)', fontWeight: 500 }}
          tickFormatter={inrFormatter}
          width={60}
          dx={-8}
        />
        <Tooltip content={<CustomTrendTooltip />} />
        <Legend
          wrapperStyle={{ fontSize: 11, fontWeight: 600, paddingTop: 16 }}
          formatter={(v) => (
            <span className="text-muted-foreground/90 transition-colors duration-200 hover:text-foreground">
              {v === 'value' ? 'Portfolio Value' : 'Invested Amount'}
            </span>
          )}
        />
        <Area
          type="monotone"
          dataKey="value"
          stroke="#10b981"
          strokeWidth={2}
          fill="url(#gValue)"
          dot={false}
          activeDot={{ r: 5, strokeWidth: 0, fill: '#10b981' }}
          isAnimationActive={true}
          animationDuration={1200}
          animationEasing="ease-in-out"
        />
        <Area
          type="monotone"
          dataKey="invested"
          stroke="#3b82f6"
          strokeWidth={1.5}
          fill="url(#gInvested)"
          dot={false}
          strokeDasharray="4 3"
          activeDot={{ r: 4, strokeWidth: 0, fill: '#3b82f6' }}
          isAnimationActive={true}
          animationDuration={1200}
          animationEasing="ease-in-out"
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

// --- Allocation donut chart ---

const ALLOCATION_COLORS = [
  '#6366f1', // Indigo
  '#10b981', // Emerald
  '#8b5cf6', // Violet
  '#f59e0b', // Amber
  '#3b82f6', // Blue
  '#ec4899', // Pink
  '#f43f5e', // Rose
];

function CustomPieTooltip({ active, payload }: any) {
  if (active && payload && payload.length) {
    const data = payload[0].payload;
    const invested = data.invested ?? 0;
    const currentValue = data.current_value ?? 0;
    const profit = currentValue - invested;
    const isProfit = profit >= 0;
    const profitPercent = invested > 0 ? (profit / invested) * 100 : 0;
    const profitColor = isProfit ? 'text-emerald-500 font-semibold' : 'text-rose-500 font-semibold';

    return (
      <div
        className="w-[170px] space-y-1.5 rounded-xl border border-border/60 p-3.5 text-xs shadow-2xl backdrop-blur-md duration-150 animate-in fade-in zoom-in-95"
        style={{ backgroundColor: 'hsl(var(--card))', opacity: 0.95 }}
      >
        <p className="mb-1 truncate border-b border-border pb-1 font-bold text-foreground">
          {data.name}
        </p>
        <div className="space-y-1.5">
          <div className="flex justify-between gap-4">
            <span className="text-muted-foreground/80">Value:</span>
            <span className="font-bold tabular-nums text-foreground">
              <MaskedAmount value={currentValue} format={formatINR} />
            </span>
          </div>
          <div className="flex justify-between gap-4">
            <span className="text-muted-foreground/80">Weight:</span>
            <span className="font-semibold tabular-nums text-foreground">
              {data.percentage.toFixed(1)}%
            </span>
          </div>
          <div className="mt-1 flex justify-between gap-4 border-t border-border/50 pt-1">
            <span className="font-medium text-muted-foreground/80">P&L:</span>
            <span className={cn('flex items-center gap-1 font-bold tabular-nums', profitColor)}>
              <MaskedAmount value={profit} format={formatINR} />
              <span className="text-[10px] font-normal opacity-75">
                ({isProfit ? '+' : ''}{profitPercent.toFixed(0)}%)
              </span>
            </span>
          </div>
        </div>
      </div>
    );
  }
  return null;
}

function AllocationDonut({
  title,
  items,
}: {
  title: string;
  items: {
    name: string;
    invested: number;
    current_value: number;
    count: number;
    percentage: number;
  }[];
}) {
  const sorted = [...items].sort((a, b) => b.current_value - a.current_value);
  const totalValue = items.reduce((sum, item) => sum + item.current_value, 0);

  return (
    <Card className="overflow-hidden border border-border/50 bg-card/45 shadow-sm backdrop-blur-sm transition-all duration-300">
      <CardHeader className="border-b border-border/40 pb-3">
        <CardTitle className="text-base font-bold tracking-tight">{title}</CardTitle>
      </CardHeader>
      <CardContent className="pt-6">
        <div className="flex flex-col items-center gap-6 sm:flex-row">
          {/* Donut Chart with central total */}
          <div className="relative h-[200px] w-[200px] shrink-0">
            <div className="pointer-events-none absolute inset-0 z-0 flex flex-col items-center justify-center">
              <span className="text-[9px] font-bold uppercase tracking-widest text-muted-foreground/80">
                Total Value
              </span>
              <span className="mt-0.5 text-sm font-extrabold tabular-nums text-foreground">
                <MaskedAmount value={totalValue} format={inrFormatter} />
              </span>
            </div>
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={sorted}
                  cx="50%"
                  cy="50%"
                  innerRadius={65}
                  outerRadius={90}
                  paddingAngle={3}
                  dataKey="current_value"
                  isAnimationActive={true}
                  animationDuration={800}
                >
                  {sorted.map((item, index) => (
                    <Cell
                      key={`cell-${item.name}`}
                      fill={ALLOCATION_COLORS[index % ALLOCATION_COLORS.length]}
                      stroke="hsl(var(--card))"
                      strokeWidth={2}
                    />
                  ))}
                </Pie>
                <Tooltip
                  content={<CustomPieTooltip />}
                  wrapperStyle={{ zIndex: 100 }}
                  offset={20}
                />
              </PieChart>
            </ResponsiveContainer>
          </div>

          {/* Detailed breakdown list */}
          <div className="max-h-[240px] w-full flex-1 overflow-auto pr-1">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-border/40 text-[10px] uppercase tracking-wider text-muted-foreground/80">
                  <th className="py-2 text-left font-bold">Name</th>
                  <th className="py-2 text-right font-bold">Value</th>
                  <th className="py-2 text-right font-bold">%</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((item, index) => {
                  const color = ALLOCATION_COLORS[index % ALLOCATION_COLORS.length];
                  return (
                    <tr
                      key={item.name}
                      className="border-b border-border/40 transition-colors last:border-0 hover:bg-muted/30"
                    >
                      <td className="flex items-center gap-2 py-2 font-medium text-foreground/90">
                        <span
                          className="size-2.5 shrink-0 rounded-full shadow-sm ring-1 ring-white/5"
                          style={{ backgroundColor: color }}
                        />
                        <span className="max-w-[120px] truncate sm:max-w-none">{item.name}</span>
                      </td>
                      <td className="py-2 text-right font-bold tabular-nums text-foreground">
                        <MaskedAmount value={item.current_value} format={formatINR} />
                      </td>
                      <td className="py-2 text-right font-medium tabular-nums text-muted-foreground/80">
                        {item.percentage.toFixed(1)}%
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
