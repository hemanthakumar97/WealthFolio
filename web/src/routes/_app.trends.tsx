import { useState, useEffect } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  AreaChart,
  Area,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from 'recharts';
import {
  RefreshCw,
  Loader2,
  Wallet,
  TrendingUp,
  TrendingDown,
  ShieldAlert,
  Activity,
  Award,
  BarChart3,
  Calendar,
  Filter,
  Sparkles,
} from 'lucide-react';

import { trendsApi, instrumentsApi, ASSET_TYPES } from '@/lib/api';
import type { Instrument } from '@/lib/api';
import { formatINR } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { toast } from '@/components/ui/sonner';
import { cn } from '@/lib/utils';
import { MaskedAmount } from '@/lib/privacy';

export const Route = createFileRoute('/_app/trends')({
  component: TrendsPage,
});

const RANGES = [
  { label: '30D', days: 30 },
  { label: '90D', days: 90 },
  { label: '6M', days: 180 },
  { label: '1Y', days: 365 },
  { label: 'All', days: 0 },
] as const;

const inrFmt = (v: number) =>
  v >= 1e7
    ? `₹${(v / 1e7).toFixed(1)}Cr`
    : v >= 1e5
      ? `₹${(v / 1e5).toFixed(1)}L`
      : `₹${v.toLocaleString('en-IN')}`;

function stdDev(vals: number[]) {
  if (vals.length < 2) return 0;
  const mean = vals.reduce((a, b) => a + b, 0) / vals.length;
  const variance = vals.reduce((a, b) => a + Math.pow(b - mean, 2), 0) / (vals.length - 1);
  return Math.sqrt(variance);
}

function spansMultipleYears(rows: { date: string }[]) {
  const years = new Set(rows.map((r) => r.date.slice(0, 4)));
  return years.size > 1;
}

function DateTick({
  x,
  y,
  payload,
  multiYear,
}: {
  x?: number;
  y?: number;
  payload?: { value: string };
  multiYear: boolean;
}) {
  if (!payload?.value) return null;
  const d = new Date(payload.value);
  const dayMonth = d.toLocaleDateString('en-IN', { day: 'numeric', month: 'short' });
  return (
    <g transform={`translate(${x},${y})`}>
      <text
        x={0}
        y={0}
        dy={10}
        textAnchor="middle"
        fontSize={10}
        fill="hsl(var(--muted-foreground))"
        fontWeight={500}
      >
        {dayMonth}
      </text>
      {multiYear && (
        <text
          x={0}
          y={0}
          dy={22}
          textAnchor="middle"
          fontSize={9}
          fill="hsl(var(--muted-foreground))"
          opacity={0.7}
          fontWeight={500}
        >
          {d.getFullYear()}
        </text>
      )}
    </g>
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
  color: 'blue' | 'emerald' | 'rose' | 'violet' | 'amber' | 'slate';
  icon?: any;
}) {
  const accent = {
    blue: 'text-blue-500 dark:text-blue-400',
    emerald: 'text-emerald-500 dark:text-emerald-400',
    rose: 'text-rose-500 dark:text-rose-400',
    violet: 'text-violet-500 dark:text-violet-400',
    amber: 'text-amber-500 dark:text-amber-400',
    slate: 'text-slate-500 dark:text-slate-400',
  }[color];

  const bgLight = {
    blue: 'bg-blue-500/5 border-blue-500/10',
    emerald: 'bg-emerald-500/5 border-emerald-500/10',
    rose: 'bg-rose-500/5 border-rose-500/10',
    violet: 'bg-violet-500/5 border-violet-500/10',
    amber: 'bg-amber-500/5 border-amber-500/10',
    slate: 'bg-slate-500/5 border-slate-500/10',
  }[color];

  const glowShadow = {
    blue: 'hover:shadow-blue-500/5',
    emerald: 'hover:shadow-emerald-500/5',
    rose: 'hover:shadow-rose-500/5',
    violet: 'hover:shadow-violet-500/5',
    amber: 'hover:shadow-amber-500/5',
    slate: 'hover:shadow-slate-500/5',
  }[color];

  return (
    <Card
      className={cn(
        'overflow-hidden border border-border/60 bg-card/45 shadow-sm backdrop-blur-sm transition-all duration-300 hover:-translate-y-1 hover:shadow-md',
        glowShadow,
      )}
    >
      <CardContent className="flex items-start justify-between p-5">
        <div className="flex-1 space-y-1">
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

// --- Custom Tooltips for Recharts ---
function ValueInvestedTooltip({ active, payload, label }: any) {
  if (active && payload && payload.length) {
    const d = payload[0].payload;
    return (
      <div className="rounded-2xl border border-border/80 bg-card/90 p-4 text-xs shadow-xl backdrop-blur-md">
        <div className="mb-2 flex items-center gap-2 border-b border-border/40 pb-1.5 font-bold text-muted-foreground">
          <Calendar className="size-3.5 text-muted-foreground/80" />
          <span>{new Date(label).toLocaleDateString('en-IN', { dateStyle: 'medium' })}</span>
        </div>
        <div className="min-w-[200px] space-y-1.5">
          <div className="flex items-center justify-between gap-4 font-semibold">
            <span className="flex items-center gap-2 text-muted-foreground">
              <span className="size-2 animate-pulse rounded-full bg-[#34d399] shadow-sm" />
              Portfolio Value:
            </span>
            <span className="tabular-nums text-foreground">
              <MaskedAmount value={d.value} format={formatINR} />
            </span>
          </div>
          {d.benchmark !== undefined && d.benchmark !== null && (
            <div className="flex items-center justify-between gap-4 font-semibold">
              <span className="flex items-center gap-2 text-muted-foreground/80">
                <span className="size-2 rounded-full bg-slate-400" />
                Nifty 50 (Norm.):
              </span>
              <span className="tabular-nums text-slate-400">
                <MaskedAmount value={d.benchmark} format={formatINR} />
              </span>
            </div>
          )}
          <div className="flex items-center justify-between gap-4 font-semibold">
            <span className="flex items-center gap-2 text-muted-foreground">
              <span className="size-2 rounded-full bg-[#60a5fa]" />
              Total Invested:
            </span>
            <span className="tabular-nums text-foreground">
              <MaskedAmount value={d.invested} format={formatINR} />
            </span>
          </div>
          <div className="mt-2 flex items-center justify-between gap-4 border-t border-border/40 pt-2 text-xs font-bold">
            <span className="text-muted-foreground">Net P&L:</span>
            <span
              className={cn(
                'flex items-center gap-1.5 tabular-nums',
                d.profit >= 0 ? 'text-emerald-500' : 'text-rose-500',
              )}
            >
              {d.profit >= 0 ? '+' : ''}
              <MaskedAmount value={d.profit} format={formatINR} />
              <span className="text-[10px] font-bold">
                ({d.profit >= 0 ? '+' : ''}{d.profit_percent.toFixed(2)}%)
              </span>
            </span>
          </div>
        </div>
      </div>
    );
  }
  return null;
}

function PandLTooltip({ active, payload, label }: any) {
  if (active && payload && payload.length) {
    const profit = payload[0].value;
    const d = payload[0].payload;
    return (
      <div className="rounded-2xl border border-border/80 bg-card/90 p-4 text-xs shadow-xl backdrop-blur-md">
        <div className="mb-2 flex items-center gap-2 border-b border-border/40 pb-1.5 font-bold text-muted-foreground">
          <Calendar className="size-3.5 text-muted-foreground/80" />
          <span>{new Date(label).toLocaleDateString('en-IN', { dateStyle: 'medium' })}</span>
        </div>
        <div className="flex min-w-[165px] items-center justify-between gap-6 font-bold">
          <span className="flex items-center gap-2 text-muted-foreground">
            <span className="size-2 animate-pulse rounded-full bg-[#a78bfa] shadow-sm" />
            Net P&L:
          </span>
          <span
            className={cn(
              'flex items-center gap-1 tabular-nums',
              profit >= 0 ? 'text-emerald-500' : 'text-rose-500',
            )}
          >
            {profit >= 0 ? '+' : ''}
            <MaskedAmount value={profit} format={formatINR} />
            <span className="text-[10px] font-bold">
              ({profit >= 0 ? '+' : ''}{d.profit_percent.toFixed(2)}%)
            </span>
          </span>
        </div>
      </div>
    );
  }
  return null;
}

function ReturnPercentTooltip({ active, payload, label }: any) {
  if (active && payload && payload.length) {
    const val = payload[0].value;
    return (
      <div className="rounded-2xl border border-border/80 bg-card/90 p-4 text-xs shadow-xl backdrop-blur-md">
        <div className="mb-2 flex items-center gap-2 border-b border-border/40 pb-1.5 font-bold text-muted-foreground">
          <Calendar className="size-3.5 text-muted-foreground/80" />
          <span>{new Date(label).toLocaleDateString('en-IN', { dateStyle: 'medium' })}</span>
        </div>
        <div className="flex min-w-[165px] items-center justify-between gap-6 font-bold">
          <span className="flex items-center gap-2 text-muted-foreground">
            <span className="size-2 animate-pulse rounded-full bg-[#f59e0b] shadow-sm" />
            Period Return:
          </span>
          <span className={cn('tabular-nums', val >= 0 ? 'text-emerald-500' : 'text-rose-500')}>
            {val >= 0 ? '+' : ''}
            {val.toFixed(2)}%
          </span>
        </div>
      </div>
    );
  }
  return null;
}

function DrawdownTooltip({ active, payload, label }: any) {
  if (active && payload && payload.length) {
    const val = payload[0].value;
    return (
      <div className="rounded-2xl border border-border/80 bg-card/90 p-4 text-xs shadow-xl backdrop-blur-md">
        <div className="mb-2 flex items-center gap-2 border-b border-border/40 pb-1.5 font-bold text-muted-foreground">
          <Calendar className="size-3.5 text-muted-foreground/80" />
          <span>{new Date(label).toLocaleDateString('en-IN', { dateStyle: 'medium' })}</span>
        </div>
        <div className="flex min-w-[165px] items-center justify-between gap-6 font-bold">
          <span className="flex items-center gap-2 text-muted-foreground">
            <span className="size-2 animate-pulse rounded-full bg-[#f43f5e] shadow-sm" />
            Drawdown:
          </span>
          <span className="tabular-nums text-rose-500">{val.toFixed(2)}%</span>
        </div>
      </div>
    );
  }
  return null;
}

function AssetAllocationTooltip({ active, payload, label, allocData, assetKeys }: any) {
  if (active && payload && payload.length) {
    const d = allocData.find((ad: any) => ad.date === label);
    const total = assetKeys.reduce((sum: number, k: string) => sum + (Number(d?.[k]) || 0), 0);
    return (
      <div className="rounded-2xl border border-border/80 bg-card/90 p-4 text-xs shadow-xl backdrop-blur-md">
        <div className="mb-2 flex items-center gap-2 border-b border-border/40 pb-1.5 font-bold text-muted-foreground">
          <Calendar className="size-3.5 text-muted-foreground/80" />
          <span>{new Date(label).toLocaleDateString('en-IN', { dateStyle: 'medium' })}</span>
        </div>
        <div className="min-w-[200px] space-y-1.5">
          {payload.map((entry: any, index: number) => {
            const val = Number(d?.[entry.name as string]) || 0;
            const pct = total > 0 ? (val / total) * 100 : 0;
            return (
              <div key={index} className="flex items-center justify-between gap-6 font-semibold">
                <span className="flex items-center gap-2 text-muted-foreground">
                  <span
                    className="size-2 rounded-full shadow-sm"
                    style={{ backgroundColor: entry.color }}
                  />
                  {ASSET_TYPES.find((t) => t.value === entry.name)?.label || entry.name}:
                </span>
                <div className="flex flex-col items-end">
                  <span className="font-semibold tabular-nums text-foreground">
                    <MaskedAmount value={val} format={formatINR} />
                  </span>
                  <span className="select-none text-[9px] font-bold text-muted-foreground/80">
                    {pct.toFixed(1)}%
                  </span>
                </div>
              </div>
            );
          })}
          <div className="mt-2.5 flex items-center justify-between gap-6 border-t border-border/40 pt-2 text-xs font-bold">
            <span className="text-muted-foreground">Total Asset Value:</span>
            <span className="tabular-nums text-foreground">
              <MaskedAmount value={total} format={formatINR} />
            </span>
          </div>
        </div>
      </div>
    );
  }
  return null;
}

// --- Charts component (shared across filter modes) ---
function PortfolioCharts({
  data,
  isLoading,
  isAllTime,
  days,
}: {
  data: any[];
  isLoading: boolean;
  isAllTime: boolean;
  days: number;
}) {
  const [showBenchmark, setShowBenchmark] = useState(false);
  const [allocMode, setAllocMode] = useState<'val' | 'pct'>('val');
  const [isMounted, setIsMounted] = useState(false);

  useEffect(() => {
    const timer = setTimeout(() => setIsMounted(true), 150);
    return () => clearTimeout(timer);
  }, []);

  const { data: benchData = [], isLoading: isBenchLoading } = useQuery({
    queryKey: ['trends', 'benchmark', '^NSEI'],
    queryFn: () => trendsApi.benchmark('^NSEI'),
    enabled: showBenchmark,
    staleTime: 60 * 60 * 1000,
  });

  const { data: allocData = [] } = useQuery({
    queryKey: ['trends', 'allocation-history', days],
    queryFn: () => trendsApi.allocationHistory(days),
    staleTime: 60_000,
  });

  const multiYear = spansMultipleYears(data);
  const xAxisProps = {
    dataKey: 'date' as const,
    tickLine: false,
    axisLine: false,
    tick: (props: any) => <DateTick {...props} multiYear={multiYear} />,
    interval: 'preserveStartEnd' as const,
    height: multiYear ? 40 : 25,
  };

  if (isLoading || !isMounted)
    return (
      <div className="flex h-64 items-center justify-center gap-2.5 text-sm font-semibold text-muted-foreground">
        <Loader2 className="size-5 animate-spin text-violet-500" /> Loading portfolio metrics…
      </div>
    );
  if (data.length === 0)
    return (
      <div className="mx-auto flex h-64 max-w-md flex-col items-center justify-center rounded-2xl border border-dashed border-border/60 bg-card/20 p-6 text-center">
        <ShieldAlert className="mb-2 size-8 stroke-[1.5] text-muted-foreground/60" />
        <p className="text-sm font-bold text-foreground">No historical data found</p>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
          Please click the "Rebuild History" trigger in the upper-right corner to construct your
          portfolio timeline.
        </p>
      </div>
    );

  const first = data[0];
  let maxVal = 0;
  let maxDrawdown = 0;
  const dailyReturns: number[] = [];

  // Helper to find closest benchmark price on or before a date
  const findClosestBenchmark = (targetDate: string) => {
    if (!benchData || benchData.length === 0) return null;
    let match = null;
    for (const b of benchData) {
      if (b.date.slice(0, 10) <= targetDate) {
        match = b;
      } else {
        break;
      }
    }
    return match;
  };

  const processedData = data.map((d, i) => {
    if (d.value > maxVal) maxVal = d.value;
    const drawdown = maxVal > 0 ? ((d.value - maxVal) / maxVal) * 100 : 0;
    if (drawdown < maxDrawdown) maxDrawdown = drawdown;

    if (i > 0) {
      const prev = data[i - 1];
      if (prev.value > 0) {
        const dailyRet = (d.value - (d.invested - prev.invested)) / prev.value - 1;
        dailyReturns.push(dailyRet);
      }
    }

    let period_return_pct = 0;
    if (first) {
      if (isAllTime) {
        period_return_pct = d.profit_percent;
      } else if (d === first) {
        period_return_pct = 0;
      } else {
        const periodProfit = d.profit - first.profit;
        const netContributions = d.invested - first.invested;
        const denominator = first.value + netContributions / 2;
        period_return_pct = denominator > 0 ? (periodProfit / denominator) * 100 : 0;
      }
    }

    // Benchmark normalization
    let benchmarkValue = null;
    if (showBenchmark && benchData.length > 0 && first) {
      const bPoint = findClosestBenchmark(d.date);
      if (bPoint) {
        const firstB = findClosestBenchmark(first.date);
        if (firstB) {
          benchmarkValue = (bPoint.close / firstB.close) * first.value;
        }
      }
    }

    return { ...d, drawdown, period_return_pct, benchmark: benchmarkValue };
  });

  const latest = processedData[processedData.length - 1];
  const periodReturn = latest ? latest.period_return_pct : 0;
  const periodProfit =
    latest && first ? (isAllTime ? latest.profit : latest.profit - first.profit) : 0;

  // Risk metrics
  const vol = stdDev(dailyReturns) * Math.sqrt(252) * 100; // Annualized Volatility
  const riskFreeRate = 0.07; // 7% risk-free rate for India
  const annualizedReturn = ((periodReturn / (days || 365)) * 365) / 100;
  const sharpe = vol > 0 ? (annualizedReturn - riskFreeRate) / (vol / 100) : 0;

  // Benchmark Return Calculation
  let benchReturn = 0;
  if (showBenchmark && benchData.length > 0 && first && latest) {
    const startB = findClosestBenchmark(first.date);
    const endB = findClosestBenchmark(latest.date);
    if (startB && endB) {
      benchReturn = ((endB.close - startB.close) / startB.close) * 100;
    }
  }

  // Extract all asset type keys from allocData for Stacked Area Chart
  const assetKeys = Array.from(
    new Set(allocData.flatMap((d) => Object.keys(d).filter((k) => k !== 'date'))),
  );
  const colors = ['#6366f1', '#14b8a6', '#f59e0b', '#3b82f6', '#f43f5e', '#a78bfa', '#94a3b8'];

  return (
    <div className="space-y-6">
      {/* Primary KPI Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <KpiCard
          label="Current Net Worth"
          value={latest ? latest.value : 0}
          sub="Total market value of assets"
          color="blue"
          icon={Wallet}
        />
        <KpiCard
          label="Period Return"
          customContent={
            <div
              className={cn(
                'flex items-center gap-1.5 text-2xl font-extrabold tabular-nums tracking-tight',
                periodReturn >= 0 ? 'text-emerald-500' : 'text-rose-500',
              )}
            >
              {periodReturn > 0 ? '+' : ''}{periodReturn.toFixed(2)}%
            </div>
          }
          sub={
            <span className="flex items-center gap-1">
              <span className="font-bold text-foreground">
                <MaskedAmount value={periodProfit} format={formatINR} />
              </span>
              <span>{isAllTime ? 'all-time profit' : 'in selected period'}</span>
            </span>
          }
          color={periodReturn >= 0 ? 'emerald' : 'rose'}
          icon={periodReturn >= 0 ? TrendingUp : TrendingDown}
        />
        <KpiCard
          label="Max Drawdown"
          customContent={
            <div className="text-2xl font-extrabold tabular-nums tracking-tight text-rose-500">
              {maxDrawdown.toFixed(2)}%
            </div>
          }
          sub="Peak-to-trough decline ratio"
          color="rose"
          icon={ShieldAlert}
        />
      </div>

      {/* Risk Metrics KPI Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <KpiCard
          label="Volatility (Ann.)"
          customContent={
            <div className="text-xl font-extrabold tabular-nums tracking-tight text-violet-500 dark:text-violet-400">
              {vol.toFixed(2)}%
            </div>
          }
          sub="Annualized standard deviation"
          color="violet"
          icon={Activity}
        />
        <KpiCard
          label="Sharpe Ratio"
          customContent={
            <div className="text-xl font-extrabold tabular-nums tracking-tight text-amber-500 dark:text-amber-400">
              {sharpe.toFixed(2)}
            </div>
          }
          sub="Risk-adjusted performance score"
          color="amber"
          icon={Award}
        />
        <KpiCard
          label="Benchmark (Nifty 50)"
          customContent={
            <div className="flex w-full items-center justify-between text-xl font-extrabold tabular-nums tracking-tight">
              <div className="min-w-0">
                {showBenchmark ? (
                  isBenchLoading ? (
                    <span className="flex items-center gap-1.5 text-xs italic text-muted-foreground/80">
                      <Loader2 className="size-3.5 animate-spin text-violet-500" /> Fetching data...
                    </span>
                  ) : benchReturn !== 0 || benchData.length > 0 ? (
                    <span
                      className={cn(
                        'flex items-center gap-1 text-sm font-bold tabular-nums',
                        benchReturn >= 0 ? 'text-emerald-500' : 'text-rose-500',
                      )}
                    >
                      {benchReturn > 0 ? '+' : ''}{benchReturn.toFixed(2)}%
                    </span>
                  ) : (
                    <span className="text-xs italic text-muted-foreground/70">No data found</span>
                  )
                ) : (
                  <span className="text-xs font-semibold text-muted-foreground">Disabled</span>
                )}
              </div>
              <button
                onClick={() => setShowBenchmark(!showBenchmark)}
                className={cn(
                  'ml-2 cursor-pointer select-none rounded-xl border border-current px-3 py-1 text-[10px] font-bold tracking-wide shadow-inner transition-all',
                  showBenchmark
                    ? 'border-indigo-500/30 bg-indigo-500/10 text-indigo-500'
                    : 'border-border/60 bg-muted/40 text-muted-foreground/80 hover:bg-muted/65 hover:text-foreground',
                )}
              >
                {showBenchmark ? 'Disable' : 'Enable'}
              </button>
            </div>
          }
          sub="Normalized comparative index return"
          color="slate"
          icon={BarChart3}
        />
      </div>

      {/* Primary Area Chart */}
      <Card className="rounded-2xl border border-border/60 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
        <CardHeader className="p-0 pb-4">
          <CardTitle className="text-sm font-bold text-foreground">
            Value vs Invested Capital
          </CardTitle>
          <CardDescription className="text-xs text-muted-foreground">
            Historical portfolio values and cumulative investment ({processedData.length} data
            points)
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0 pt-3">
          <ResponsiveContainer width="100%" height={320}>
            <AreaChart
              key={`${processedData.length}-${days}-${showBenchmark}`}
              data={processedData}
              margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
            >
              <defs>
                <linearGradient id="gV" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#34d399" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#34d399" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="gI" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#60a5fa" stopOpacity={0.15} />
                  <stop offset="95%" stopColor="#60a5fa" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.6} />
              <XAxis {...xAxisProps} />
              <YAxis
                tickLine={false}
                axisLine={false}
                tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground))', fontWeight: 600 }}
                tickFormatter={inrFmt}
                width={70}
              />
              <Tooltip content={<ValueInvestedTooltip />} />
              <Legend
                wrapperStyle={{ fontSize: 11, fontWeight: 600, paddingTop: 12 }}
                formatter={(v) =>
                  v === 'value'
                    ? 'Portfolio Value'
                    : v === 'invested'
                      ? 'Invested Capital'
                      : 'Nifty 50 Index'
                }
              />
              <Area
                type="monotone"
                dataKey="value"
                stroke="#34d399"
                strokeWidth={2.5}
                fill="url(#gV)"
                dot={false}
                activeDot={{ r: 5, stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                isAnimationActive={true}
                animationDuration={1200}
                animationEasing="ease-in-out"
              />
              <Area
                type="monotone"
                dataKey="invested"
                stroke="#60a5fa"
                strokeWidth={2}
                fill="url(#gI)"
                dot={false}
                strokeDasharray="4 3"
                activeDot={{ r: 4, stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                isAnimationActive={true}
                animationDuration={1200}
                animationEasing="ease-in-out"
              />
              {showBenchmark && (
                <Line
                  type="monotone"
                  dataKey="benchmark"
                  stroke="#94a3b8"
                  strokeWidth={1.5}
                  strokeDasharray="4 4"
                  dot={false}
                  isAnimationActive={true}
                  animationDuration={1200}
                  animationEasing="ease-in-out"
                />
              )}
            </AreaChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>

      {/* Grid for P&L and Return % line charts */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card className="rounded-2xl border border-border/60 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
          <CardHeader className="p-0 pb-4">
            <CardTitle className="text-sm font-bold text-foreground">
              Cumulative P&L Over Time
            </CardTitle>
          </CardHeader>
          <CardContent className="p-0 pt-3">
            <ResponsiveContainer width="100%" height={210}>
              <LineChart
                key={`${processedData.length}-${days}`}
                data={processedData}
                margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.6} />
                <XAxis {...xAxisProps} />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  tick={{ fontSize: 9, fill: 'hsl(var(--muted-foreground))', fontWeight: 600 }}
                  tickFormatter={inrFmt}
                  width={60}
                />
                <Tooltip content={<PandLTooltip />} />
                <Line
                  type="monotone"
                  dataKey="profit"
                  stroke="#a78bfa"
                  strokeWidth={2.5}
                  dot={false}
                  activeDot={{ r: 5, stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                  isAnimationActive={true}
                  animationDuration={1200}
                  animationEasing="ease-in-out"
                />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>

        <Card className="rounded-2xl border border-border/60 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
          <CardHeader className="p-0 pb-4">
            <CardTitle className="text-sm font-bold text-foreground">
              Return Rate Over Time
            </CardTitle>
          </CardHeader>
          <CardContent className="p-0 pt-3">
            <ResponsiveContainer width="100%" height={210}>
              <LineChart
                key={`${processedData.length}-${days}`}
                data={processedData}
                margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.6} />
                <XAxis {...xAxisProps} />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  tick={{ fontSize: 9, fill: 'hsl(var(--muted-foreground))', fontWeight: 600 }}
                  tickFormatter={(v: number) => `${v.toFixed(1)}%`}
                  width={55}
                />
                <Tooltip content={<ReturnPercentTooltip />} />
                <Line
                  type="monotone"
                  dataKey="period_return_pct"
                  stroke="#f59e0b"
                  strokeWidth={2.5}
                  dot={false}
                  activeDot={{ r: 5, stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                  isAnimationActive={true}
                  animationDuration={1200}
                  animationEasing="ease-in-out"
                />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </div>

      {/* Drawdown Area Chart */}
      <Card className="rounded-2xl border border-border/60 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
        <CardHeader className="p-0 pb-4">
          <CardTitle className="text-sm font-bold text-foreground">
            Portfolio Drawdown Analysis
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0 pt-3">
          <ResponsiveContainer width="100%" height={200}>
            <AreaChart
              key={`${processedData.length}-${days}`}
              data={processedData}
              margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
            >
              <defs>
                <linearGradient id="gD" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#f43f5e" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#f43f5e" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.6} />
              <XAxis {...xAxisProps} />
              <YAxis
                tickLine={false}
                axisLine={false}
                tick={{ fontSize: 9, fill: 'hsl(var(--muted-foreground))', fontWeight: 600 }}
                tickFormatter={(v: number) => `${v.toFixed(1)}%`}
                width={55}
              />
              <Tooltip content={<DrawdownTooltip />} />
              <Area
                type="monotone"
                dataKey="drawdown"
                stroke="#f43f5e"
                strokeWidth={2}
                fill="url(#gD)"
                dot={false}
                activeDot={{ r: 4, stroke: 'hsl(var(--card))', strokeWidth: 1.5 }}
                isAnimationActive={true}
                animationDuration={1200}
                animationEasing="ease-in-out"
              />
            </AreaChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>

      {/* Asset Allocation Trend Stacked Area */}
      <Card className="rounded-2xl border border-border/60 bg-card/45 p-5 shadow-sm backdrop-blur-sm">
        <CardHeader className="p-0 pb-4">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div>
              <CardTitle className="text-sm font-bold text-foreground">
                Historical Asset Allocation Trend
              </CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                Historical market values breakdown (Invested + Accrued Profits)
              </CardDescription>
            </div>
            <div className="flex rounded-xl border border-border/60 bg-muted/40 p-1 text-[10px] font-bold shadow-inner">
              <button
                onClick={() => setAllocMode('val')}
                className={cn(
                  'cursor-pointer rounded-lg px-3 py-1 transition-all',
                  allocMode === 'val'
                    ? 'bg-card text-foreground shadow-sm'
                    : 'text-muted-foreground/80 hover:text-foreground',
                )}
              >
                Value
              </button>
              <button
                onClick={() => setAllocMode('pct')}
                className={cn(
                  'cursor-pointer rounded-lg px-3 py-1 transition-all',
                  allocMode === 'pct'
                    ? 'bg-card text-foreground shadow-sm'
                    : 'text-muted-foreground/80 hover:text-foreground',
                )}
              >
                % Ratio
              </button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-0 pt-3">
          <ResponsiveContainer width="100%" height={260}>
            <AreaChart
              key={`${allocData.length}-${days}-${allocMode}`}
              data={
                allocMode === 'val'
                  ? allocData
                  : allocData.map((d) => {
                      const total = assetKeys.reduce((sum, k) => sum + (Number(d[k]) || 0), 0);
                      const p: any = { date: d.date };
                      assetKeys.forEach(
                        (k) => (p[k] = total > 0 ? ((Number(d[k]) || 0) / total) * 100 : 0),
                      );
                      return p;
                    })
              }
              margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
            >
              <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.6} />
              <XAxis {...xAxisProps} />
              <YAxis
                tickLine={false}
                axisLine={false}
                tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground))', fontWeight: 600 }}
                tickFormatter={allocMode === 'val' ? inrFmt : (v) => `${v}%`}
                width={70}
              />
              <Tooltip
                content={(props) => (
                  <AssetAllocationTooltip {...props} allocData={allocData} assetKeys={assetKeys} />
                )}
              />
              <Legend
                wrapperStyle={{ fontSize: 11, fontWeight: 600, paddingTop: 12 }}
                formatter={(v) => ASSET_TYPES.find((t) => t.value === v)?.label || v}
              />
              {assetKeys.map((k, i) => (
                <Area
                  key={k}
                  type="monotone"
                  dataKey={k}
                  stackId="1"
                  stroke={colors[i % colors.length]}
                  fill={colors[i % colors.length]}
                  fillOpacity={0.4}
                  isAnimationActive={true}
                  animationDuration={1200}
                  animationEasing="ease-in-out"
                />
              ))}
            </AreaChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
    </div>
  );
}

// --- Monthly Heatmap Matrix ---
function MonthlyHeatmap({ data }: { data: any[] }) {
  if (!data || data.length === 0) return null;

  // Group by year
  const byYear = data.reduce(
    (acc, row) => {
      const year = parseInt(row.month.slice(0, 4));
      const month = parseInt(row.month.slice(5, 7)) - 1; // 0-11
      if (!acc[year]) acc[year] = new Array(12).fill(null);
      acc[year][month] = row;
      return acc;
    },
    {} as Record<number, any[]>,
  );

  const years = Object.keys(byYear)
    .map(Number)
    .sort((a, b) => b - a);
  const months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];

  return (
    <Card className="mb-6 flex flex-col justify-between overflow-hidden rounded-2xl border border-border/60 bg-card/45 shadow-sm backdrop-blur-sm">
      <div className="border-b border-border/40 bg-muted/10 p-6 pb-4">
        <h3 className="text-sm font-bold text-foreground">Monthly Return Heatmap</h3>
        <p className="text-xs text-muted-foreground">
          Historical percentage performance matrix broken down by month
        </p>
      </div>
      <CardContent className="p-0">
        <div className="scrollbar-thin overflow-x-auto">
          <table className="w-full min-w-[700px] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border/40 bg-muted/20">
                <th className="w-20 px-5 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                  Year
                </th>
                {months.map((m) => (
                  <th
                    key={m}
                    className="px-2 py-3.5 text-center text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90"
                  >
                    {m}
                  </th>
                ))}
                <th className="w-24 border-l border-border/40 bg-muted/10 px-4 py-3.5 text-center text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                  Total
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/45">
              {years.map((year) => {
                const yearData = byYear[year];
                let yTotal = 0;
                let hasData = false;
                yearData.forEach((d: any) => {
                  if (d) {
                    yTotal += d.profit_percent;
                    hasData = true;
                  }
                });

                return (
                  <tr key={year} className="transition-colors duration-150 hover:bg-muted/15">
                    <td className="px-5 py-3 font-extrabold text-foreground">{year}</td>
                    {yearData.map((d: any, i: number) => {
                      if (!d)
                        return (
                          <td
                            key={i}
                            className="select-none p-1.5 text-center font-bold text-muted-foreground/35"
                          >
                            -
                          </td>
                        );
                      const pct = d.profit_percent;
                      let cellClass = '';
                      if (pct > 0) {
                        cellClass =
                          'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20 shadow-[0_0_10px_rgba(16,185,129,0.02)]';
                      } else if (pct < 0) {
                        cellClass =
                          'bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/20 shadow-[0_0_10px_rgba(244,63,94,0.02)]';
                      } else {
                        cellClass = 'bg-muted/40 text-muted-foreground border border-border/30';
                      }

                      return (
                        <td key={i} className="p-1">
                          <div
                            className={cn(
                              'cursor-default rounded-xl p-2 text-center text-xs font-bold transition-all duration-200 hover:scale-[1.04]',
                              cellClass,
                            )}
                          >
                            {pct > 0 ? '+' : ''}
                            {pct.toFixed(1)}%
                          </div>
                        </td>
                      );
                    })}
                    <td className="border-l border-border/40 bg-muted/5 p-1 pl-2.5">
                      {hasData ? (
                        <div
                          className={cn(
                            'cursor-default rounded-xl border p-2 text-center text-xs font-black transition-all duration-200',
                            yTotal >= 0
                              ? 'border-emerald-500/20 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400'
                              : 'border-rose-500/20 bg-rose-500/5 text-rose-600 dark:text-rose-400',
                          )}
                        >
                          {yTotal > 0 ? '+' : ''}
                          {yTotal.toFixed(1)}%
                        </div>
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );
}

// --- Page ---
function TrendsPage() {
  const qc = useQueryClient();
  const [days, setDays] = useState(365);
  const [assetType, setAssetType] = useState<string>(''); // '' = all
  const [instrumentId, setInstrumentId] = useState<number | undefined>();

  const { data: instruments = [] } = useQuery({
    queryKey: ['instruments'],
    queryFn: () => instrumentsApi.list(),
    staleTime: 5 * 60_000,
  });

  // Build query params from filters.
  const filterParams = {
    days,
    ...(instrumentId
      ? { instrument_id: instrumentId }
      : assetType
        ? { asset_type: assetType }
        : {}),
  };

  const { data = [], isLoading } = useQuery({
    queryKey: ['trends', 'portfolio', filterParams],
    queryFn: () => trendsApi.portfolio(filterParams),
    staleTime: 60_000,
  });

  const { data: monthly = [] } = useQuery({
    queryKey: ['trends', 'monthly'],
    queryFn: () => trendsApi.monthlyReturns(0),
    staleTime: 60_000,
  });

  const [backfillRunning, setBackfillRunning] = useState(false);
  const [showBackfillOptions, setShowBackfillOptions] = useState(false);
  const [bfInstrumentId, setBfInstrumentId] = useState<string>('0');
  const [bfRewrite, setBfRewrite] = useState(true);

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
            qc.invalidateQueries({ queryKey: ['trends'] });
          }
        }
      } catch {
        clearInterval(id);
        setBackfillRunning(false);
      }
    }, 5_000);
    return () => clearInterval(id);
  }, [backfillRunning, qc]);

  const backfill = useMutation({
    mutationFn: trendsApi.backfill,
    onSuccess: () => {
      setBackfillRunning(true);
      setShowBackfillOptions(false);
      toast.info('Rebuilding history...');
    },
    onError: () => toast.error('Failed to start rebuild'),
  });

  const handleStartBackfill = () => {
    const instrumentId = parseInt(bfInstrumentId, 10);
    backfill.mutate({
      rewrite: instrumentId !== 0 ? true : bfRewrite,
      instrument_id: instrumentId,
    });
  };

  // Derived label for current filter
  const filterLabel = instrumentId
    ? ((instruments as Instrument[]).find((i) => i.id === instrumentId)?.name ?? 'Fund')
    : assetType
      ? (ASSET_TYPES.find((t) => t.value === assetType)?.label ?? assetType)
      : 'All';

  const setFilter = (type: string) => {
    setAssetType(type);
    setInstrumentId(undefined);
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Performance Analytics
          </p>
          <h1 className="bg-gradient-to-r from-foreground to-foreground/75 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
            Trends
          </h1>
        </div>
        <div className="flex flex-col items-end gap-2">
          {!showBackfillOptions ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setShowBackfillOptions(true)}
              disabled={backfill.isPending || backfillRunning}
              className="flex h-10 cursor-pointer items-center gap-2 rounded-xl border-border/60 px-4 text-xs font-bold shadow-sm transition-all duration-300 hover:bg-muted/40"
            >
              <RefreshCw
                className={cn('size-4 text-muted-foreground', (backfill.isPending || backfillRunning) && 'animate-spin')}
              />
              {backfill.isPending || backfillRunning ? 'Rebuilding...' : 'Rebuild History'}
            </Button>
          ) : (
            <div className="flex flex-col gap-3 rounded-xl border border-border/60 bg-background p-3 shadow-sm">
              <p className="text-xs font-semibold text-muted-foreground">Rebuild Options</p>
              <div className="flex flex-wrap items-center gap-2">
                <Select value={bfInstrumentId} onValueChange={setBfInstrumentId}>
                  <SelectTrigger className="h-8 w-52 text-xs">
                    <SelectValue placeholder="All instruments" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">All instruments</SelectItem>
                    {(instruments as Instrument[]).map((i) => (
                      <SelectItem key={i.id} value={String(i.id)}>
                        {i.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {bfInstrumentId === '0' && (
                  <div className="flex overflow-hidden rounded-lg border border-border/60">
                    <button
                      className={cn(
                        'px-3 py-1.5 text-xs font-medium transition-colors',
                        bfRewrite
                          ? 'bg-primary text-primary-foreground'
                          : 'text-muted-foreground hover:text-foreground',
                      )}
                      onClick={() => setBfRewrite(true)}
                    >
                      Rewrite
                    </button>
                    <button
                      className={cn(
                        'border-l border-border/60 px-3 py-1.5 text-xs font-medium transition-colors',
                        !bfRewrite
                          ? 'bg-primary text-primary-foreground'
                          : 'text-muted-foreground hover:text-foreground',
                      )}
                      onClick={() => setBfRewrite(false)}
                    >
                      Skip Existing
                    </button>
                  </div>
                )}
              </div>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  onClick={handleStartBackfill}
                  disabled={backfill.isPending || backfillRunning}
                  className="h-8 text-xs"
                >
                  {backfill.isPending || backfillRunning ? (
                    <>
                      <RefreshCw className="mr-1 size-3 animate-spin" />
                      Rebuilding...
                    </>
                  ) : (
                    'Start'
                  )}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setShowBackfillOptions(false)}
                  className="h-8 text-xs"
                >
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </div>
      </header>

      {/* Tabs */}
      <Tabs defaultValue="overview" className="space-y-6">
        <TabsList className="inline-flex rounded-xl border border-border/45 bg-muted/60 p-1">
          <TabsTrigger
            value="overview"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Portfolio Value
          </TabsTrigger>
          <TabsTrigger
            value="returns"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Monthly Returns
          </TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-0 space-y-5 outline-none">
          {/* Filters card */}
          <Card className="rounded-2xl border border-border/60 bg-card/45 p-4 shadow-sm backdrop-blur-sm">
            <div className="flex flex-wrap items-center gap-4">
              {/* Date ranges */}
              <div className="flex flex-wrap gap-1 rounded-xl border border-border/40 bg-muted/40 p-1 shadow-inner">
                {RANGES.map((r) => (
                  <button
                    key={r.label}
                    onClick={() => setDays(r.days)}
                    className={cn(
                      'cursor-pointer select-none rounded-lg px-3 py-1.5 text-xs font-bold transition-all duration-200',
                      days === r.days
                        ? 'bg-card text-foreground shadow-sm'
                        : 'text-muted-foreground/80 hover:bg-muted/30 hover:text-foreground',
                    )}
                  >
                    {r.label}
                  </button>
                ))}
              </div>

              <div className="hidden h-6 w-px bg-border/80 md:block" />

              {/* Asset Types Filter */}
              <div className="flex flex-wrap gap-1">
                <button
                  onClick={() => setFilter('')}
                  className={cn(
                    'cursor-pointer select-none rounded-xl border px-3.5 py-1.5 text-xs font-bold transition-all duration-200',
                    !assetType && !instrumentId
                      ? 'scale-[1.01] border-transparent bg-gradient-to-r from-violet-600 to-indigo-600 text-white shadow-sm shadow-indigo-500/20'
                      : 'border-border/60 bg-card/50 text-muted-foreground/90 hover:bg-card hover:text-foreground',
                  )}
                >
                  All Assets
                </button>
                {ASSET_TYPES.map((t) => (
                  <button
                    key={t.value}
                    onClick={() => setFilter(t.value)}
                    className={cn(
                      'cursor-pointer select-none rounded-xl border px-3.5 py-1.5 text-xs font-bold transition-all duration-200',
                      assetType === t.value && !instrumentId
                        ? 'scale-[1.01] border-transparent bg-gradient-to-r from-violet-600 to-indigo-600 text-white shadow-sm shadow-indigo-500/20'
                        : 'border-border/60 bg-card/50 text-muted-foreground/90 hover:bg-card hover:text-foreground',
                    )}
                  >
                    {t.label}
                  </button>
                ))}
              </div>

              <div className="hidden h-6 w-px bg-border/80 md:block" />

              {/* Radix Styled Select Fund dropdown */}
              <div className="relative">
                <Select
                  value={instrumentId !== undefined ? String(instrumentId) : 'ALL'}
                  onValueChange={(v) => {
                    if (v === 'ALL') {
                      setInstrumentId(undefined);
                    } else {
                      setInstrumentId(Number(v));
                      setAssetType('');
                    }
                  }}
                >
                  <SelectTrigger className="h-10 w-52 rounded-xl border-border/60 bg-card/60 text-xs font-semibold text-foreground shadow-inner focus:ring-1 focus:ring-violet-500/20">
                    <span className="flex items-center gap-2">
                      <Filter className="size-3.5 text-muted-foreground/80" />
                      <SelectValue placeholder="Filter by Fund" />
                    </span>
                  </SelectTrigger>
                  <SelectContent className="max-h-[280px] rounded-xl border-border/60 bg-card/95 backdrop-blur-md">
                    <SelectItem value="ALL" className="text-xs font-medium">
                      Fund: All
                    </SelectItem>
                    {(instruments as Instrument[]).map((i) => (
                      <SelectItem key={i.id} value={String(i.id)} className="text-xs font-medium">
                        {i.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          </Card>

          {/* Active Filter Pill */}
          {(assetType || instrumentId) && (
            <div className="flex w-fit items-center gap-2 rounded-xl border border-border/40 bg-muted/20 px-3 py-2 text-xs font-semibold text-muted-foreground">
              <span className="size-1.5 animate-pulse rounded-full bg-violet-500" />
              <span>Filtering:</span>
              <span className="rounded-lg border border-violet-500/15 bg-violet-500/10 px-2 py-0.5 font-bold text-violet-600 dark:text-violet-400">
                {filterLabel}
              </span>
              <button
                onClick={() => {
                  setAssetType('');
                  setInstrumentId(undefined);
                }}
                className="cursor-pointer select-none pl-1 text-[10px] font-bold uppercase tracking-wider text-muted-foreground/80 transition-colors hover:text-foreground"
              >
                [ Clear Filters ]
              </button>
            </div>
          )}

          {/* Main Charts Block */}
          <PortfolioCharts data={data} isLoading={isLoading} isAllTime={days === 0} days={days} />
        </TabsContent>

        <TabsContent value="returns" className="mt-0 outline-none">
          <MonthlyHeatmap data={monthly} />

          {/* Monthly logs card */}
          <Card className="flex flex-col justify-between overflow-hidden rounded-2xl border border-border/60 bg-card/45 shadow-sm backdrop-blur-sm">
            <div className="border-b border-border/40 bg-muted/10 p-6 pb-4">
              <h3 className="flex items-center gap-1.5 text-sm font-bold text-foreground">
                <Sparkles className="size-4 text-violet-500" />
                Monthly Return Statements
              </h3>
              <p className="text-xs text-muted-foreground">
                Historical records of invested capital and monthly performance
              </p>
            </div>
            <CardContent className="p-0">
              {monthly.length === 0 ? (
                <div className="flex flex-col items-center justify-center px-4 py-16 text-center text-muted-foreground">
                  <Calendar className="mb-2 size-8 stroke-[1.5] text-muted-foreground/60" />
                  <p className="text-sm font-bold text-foreground">No monthly records found</p>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    Records will appear as you backfill or record transactions.
                  </p>
                </div>
              ) : (
                <div className="scrollbar-thin overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-border/40 bg-muted/20 text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                        <th className="px-5 py-3.5 text-left font-medium">Month</th>
                        <th className="px-4 py-3.5 text-right font-medium">Invested</th>
                        <th className="px-4 py-3.5 text-right font-medium">Value</th>
                        <th className="px-4 py-3.5 text-right font-medium">Monthly P&L</th>
                        <th className="px-5 py-3.5 text-right font-medium">Monthly Return %</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border/40">
                      {monthly.map((m) => (
                        <tr
                          key={m.month}
                          className="transition-colors duration-150 hover:bg-muted/30"
                        >
                          <td className="px-5 py-3.5 font-bold text-foreground">
                            {m.month.slice(0, 7)}
                          </td>
                          <td className="px-4 py-3.5 text-right font-semibold tabular-nums text-foreground/80">
                            <MaskedAmount value={m.invested} format={formatINR} />
                          </td>
                          <td className="px-4 py-3.5 text-right font-semibold tabular-nums text-foreground/80">
                            <MaskedAmount value={m.value} format={formatINR} />
                          </td>
                          <td
                            className={cn(
                              'px-4 py-3.5 text-right font-bold tabular-nums',
                              m.profit >= 0 ? 'text-emerald-500' : 'text-rose-500',
                            )}
                          >
                            {m.profit >= 0 ? '+' : ''}
                            <MaskedAmount value={m.profit} format={formatINR} />
                          </td>
                          <td className="px-5 py-3.5 text-right">
                            <span
                              className={cn(
                                'inline-flex items-center gap-0.5 rounded-full border px-2.5 py-0.5 text-xs font-bold tabular-nums transition-all',
                                m.profit_percent >= 0
                                  ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                                  : 'border-rose-500/20 bg-rose-500/10 text-rose-600 dark:text-rose-400',
                              )}
                            >
                              {m.profit_percent >= 0 ? '+' : ''}
                              {m.profit_percent.toFixed(2)}%
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
