import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
  Cell,
} from 'recharts';
import { X, AlertTriangle, CheckCircle2, Info, RefreshCw } from 'lucide-react';
import { signalApi, type MFMetrics } from '@/lib/api';
import { cn } from '@/lib/utils';

interface Props {
  instrumentId: number;
  instrumentName: string;
  onClose: () => void;
}

// ─── Tiny helpers ─────────────────────────────────────────────────────────────

function fmt(n: number, d = 2) {
  return n.toFixed(d);
}

function scoreVerdict(s: number) {
  if (s >= 65)
    return {
      label: 'Strong fund',
      color: 'text-emerald-400',
      bar: 'bg-emerald-500',
      hex: '#10b981',
    };
  if (s >= 48)
    return { label: 'Adequate fund', color: 'text-amber-400', bar: 'bg-amber-500', hex: '#f59e0b' };
  return { label: 'Weak fund', color: 'text-red-400', bar: 'bg-red-500', hex: '#ef4444' };
}

function catBench(cat: string) {
  if (cat === 'Small Cap') return 18;
  if (cat === 'Mid Cap') return 17;
  if (cat === 'Large Cap') return 14;
  if (cat === 'ELSS') return 17;
  return 15;
}

function betaLabel(b: number) {
  if (!b) return 'Unknown';
  if (b <= 0.75) return 'Defensive';
  if (b <= 0.85) return 'Low';
  if (b <= 0.95) return 'Moderate';
  if (b <= 1.05) return 'Market-linked';
  return 'High';
}

function deriveScores(m: MFMetrics) {
  const roll = (() => {
    let s = 0;
    s +=
      m.rolling_3y_avg_pct >= 28
        ? 12
        : m.rolling_3y_avg_pct >= 22
          ? 9
          : m.rolling_3y_avg_pct >= 16
            ? 6
            : 3;
    s +=
      m.consistency_1y_pct >= 95
        ? 10
        : m.consistency_1y_pct >= 80
          ? 7
          : m.consistency_1y_pct >= 70
            ? 5
            : 2;
    s +=
      m.rolling_3y_min_pct > 5
        ? 8
        : m.rolling_3y_min_pct > 0
          ? 6
          : m.rolling_3y_min_pct > -5
            ? 4
            : 1;
    return s;
  })();
  const sharpe =
    m.sharpe_1y >= 1
      ? 15
      : m.sharpe_1y >= 0.5
        ? 12
        : m.sharpe_1y >= 0
          ? 9
          : m.sharpe_1y >= -0.3
            ? 7
            : m.sharpe_1y >= -0.7
              ? 5
              : 3;
  const bench = catBench(m.category);
  const diff = m.std_dev_1y_pct - bench;
  const stddev = diff <= -3 ? 15 : diff <= 0 ? 12 : diff <= 2 ? 9 : diff <= 4 ? 6 : 3;
  const beta = !m.beta
    ? 5
    : m.beta <= 0.75
      ? 10
      : m.beta <= 0.85
        ? 8
        : m.beta <= 0.95
          ? 6
          : m.beta <= 1.05
            ? 4
            : 2;
  let aum = 5;
  if (m.aum_cr > 0) {
    if (m.category === 'Small Cap')
      aum = m.aum_cr < 5000 ? 5 : m.aum_cr < 20000 ? 10 : m.aum_cr < 40000 ? 7 : 4;
    else if (m.category === 'Mid Cap')
      aum = m.aum_cr < 2000 ? 5 : m.aum_cr < 30000 ? 10 : m.aum_cr < 60000 ? 8 : 5;
    else aum = m.aum_cr < 500 ? 5 : m.aum_cr < 80000 ? 10 : 9;
  }
  const ter = !m.ter_pct
    ? 5
    : m.ter_pct < 0.5
      ? 10
      : m.ter_pct < 0.7
        ? 8
        : m.ter_pct < 1
          ? 6
          : m.ter_pct < 1.5
            ? 4
            : 2;
  return { roll, sharpe, stddev, beta, aum, ter };
}

// ─── Chart tooltip styles ─────────────────────────────────────────────────────

const tooltipStyle = {
  contentStyle: {
    background: '#18181b',
    border: '1px solid #27272a',
    borderRadius: 8,
    fontSize: 11,
  },
  labelStyle: { color: '#a1a1aa' },
  itemStyle: { color: '#e4e4e7' },
};

function fmtAxisDate(d: string) {
  const dt = new Date(d);
  return dt.toLocaleDateString('en-IN', { month: 'short', year: '2-digit' });
}

// ─── Skeleton ─────────────────────────────────────────────────────────────────

function Sk({ className }: { className?: string }) {
  return <div className={cn('bg-white/8 animate-pulse rounded', className)} />;
}
function LoadingSkeleton() {
  return (
    <div className="space-y-5 p-6">
      <Sk className="h-5 w-40" />
      <Sk className="h-20 w-full" />
      <div className="space-y-2">
        {[1, 2, 3, 4, 5, 6].map((i) => (
          <Sk key={i} className="h-4 w-full" />
        ))}
      </div>
      <Sk className="h-48 w-full" />
      <Sk className="h-36 w-full" />
    </div>
  );
}

// ─── Section wrapper ──────────────────────────────────────────────────────────

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-border px-6 py-5">
      <p className="mb-4 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
        {title}
      </p>
      {children}
    </section>
  );
}

// ─── Score breakdown row ──────────────────────────────────────────────────────

function ScoreRow({
  label,
  score,
  max,
  detail,
}: {
  label: string;
  score: number;
  max: number;
  detail: string;
}) {
  const pct = (score / max) * 100;
  const color = pct >= 70 ? 'bg-emerald-500' : pct >= 45 ? 'bg-amber-500' : 'bg-red-500';
  return (
    <div className="flex items-center gap-3">
      <span className="w-28 shrink-0 text-xs text-muted-foreground">{label}</span>
      <div className="bg-white/8 h-1.5 flex-1 overflow-hidden rounded-full">
        <div className={cn('h-full rounded-full', color)} style={{ width: `${pct}%` }} />
      </div>
      <span className="w-12 shrink-0 text-right text-xs tabular-nums">
        {score}
        <span className="text-muted-foreground">/{max}</span>
      </span>
      <span className="w-24 shrink-0 truncate text-right text-[10px] text-muted-foreground">
        {detail}
      </span>
    </div>
  );
}

// ─── Stat card ────────────────────────────────────────────────────────────────

function Stat({
  label,
  value,
  sub,
  color,
}: {
  label: string;
  value: string;
  sub?: string;
  color?: string;
}) {
  return (
    <div className="border-white/8 rounded-lg border bg-white/[0.03] px-3 py-2.5">
      <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </p>
      <p className={cn('mt-0.5 text-lg font-semibold tabular-nums', color ?? 'text-foreground')}>
        {value}
      </p>
      {sub && <p className="mt-0.5 text-[10px] text-muted-foreground/70">{sub}</p>}
    </div>
  );
}

// ─── NAV growth chart ─────────────────────────────────────────────────────────

function NavChart({ data, color }: { data: { d: string; v: number }[]; color: string }) {
  if (!data?.length) return null;
  const min = Math.min(...data.map((p) => p.v));
  const max = Math.max(...data.map((p) => p.v));
  const domain: [number, number] = [min * 0.97, max * 1.01];

  return (
    <div className="h-44 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="navGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={color} stopOpacity={0.25} />
              <stop offset="95%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <XAxis
            dataKey="d"
            tickFormatter={fmtAxisDate}
            tick={{ fontSize: 10, fill: '#71717a' }}
            interval={Math.floor(data.length / 5)}
            tickLine={false}
            axisLine={false}
          />
          <YAxis
            domain={domain}
            tickFormatter={(v) => `₹${v.toFixed(0)}`}
            tick={{ fontSize: 10, fill: '#71717a' }}
            tickLine={false}
            axisLine={false}
            width={52}
          />
          <Tooltip
            {...tooltipStyle}
            labelFormatter={(d) =>
              new Date(d).toLocaleDateString('en-IN', {
                day: 'numeric',
                month: 'short',
                year: 'numeric',
              })
            }
            formatter={(v: number) => [`₹${v.toFixed(3)}`, 'NAV']}
          />
          <Area
            type="monotone"
            dataKey="v"
            stroke={color}
            strokeWidth={1.5}
            fill="url(#navGrad)"
            dot={false}
            activeDot={{ r: 3, fill: color }}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

// ─── Rolling 1Y returns chart ─────────────────────────────────────────────────

function RollingChart({ data }: { data: { d: string; v: number }[] }) {
  if (!data?.length) return null;
  return (
    <div className="h-36 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }} barSize={2}>
          <XAxis
            dataKey="d"
            tickFormatter={fmtAxisDate}
            tick={{ fontSize: 10, fill: '#71717a' }}
            interval={Math.floor(data.length / 5)}
            tickLine={false}
            axisLine={false}
          />
          <YAxis
            tickFormatter={(v) => `${v.toFixed(0)}%`}
            tick={{ fontSize: 10, fill: '#71717a' }}
            tickLine={false}
            axisLine={false}
            width={38}
          />
          <ReferenceLine y={0} stroke="#52525b" strokeWidth={1} />
          <Tooltip
            {...tooltipStyle}
            labelFormatter={(d) =>
              new Date(d).toLocaleDateString('en-IN', { month: 'short', year: 'numeric' })
            }
            formatter={(v: number) => [`${v.toFixed(2)}%`, 'Rolling 1Y CAGR']}
          />
          <Bar dataKey="v" radius={[1, 1, 0, 0]}>
            {data.map((p, i) => (
              <Cell key={i} fill={p.v >= 0 ? '#10b981' : '#ef4444'} fillOpacity={0.8} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

// ─── Drawdown chart ───────────────────────────────────────────────────────────

function DrawdownChart({ data }: { data: { d: string; v: number }[] }) {
  if (!data?.length) return null;
  return (
    <div className="h-32 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="ddGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#ef4444" stopOpacity={0.3} />
              <stop offset="95%" stopColor="#ef4444" stopOpacity={0.05} />
            </linearGradient>
          </defs>
          <XAxis
            dataKey="d"
            tickFormatter={fmtAxisDate}
            tick={{ fontSize: 10, fill: '#71717a' }}
            interval={Math.floor(data.length / 4)}
            tickLine={false}
            axisLine={false}
          />
          <YAxis
            tickFormatter={(v) => `${v.toFixed(0)}%`}
            tick={{ fontSize: 10, fill: '#71717a' }}
            tickLine={false}
            axisLine={false}
            width={38}
          />
          <ReferenceLine y={0} stroke="#52525b" strokeWidth={1} />
          <Tooltip
            {...tooltipStyle}
            labelFormatter={(d) =>
              new Date(d).toLocaleDateString('en-IN', { month: 'short', year: 'numeric' })
            }
            formatter={(v: number) => [`${v.toFixed(2)}%`, 'Drawdown']}
          />
          <Area
            type="monotone"
            dataKey="v"
            stroke="#ef4444"
            strokeWidth={1}
            fill="url(#ddGrad)"
            dot={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

// ─── Main panel ───────────────────────────────────────────────────────────────

export function MFAnalysisPanel({ instrumentId, instrumentName, onClose }: Props) {
  const {
    data: m,
    isLoading,
    error,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ['mf-metrics', instrumentId],
    queryFn: () => signalApi.getMFMetrics(instrumentId),
    staleTime: 5 * 60_000,
    retry: 1,
  });

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [onClose]);

  const verdict = m ? scoreVerdict(m.zero1_score) : null;
  const scores = m ? deriveScores(m) : null;
  const bench = m ? catBench(m.category) : 0;

  return (
    <div className="fixed inset-0 z-50 flex">
      <div className="flex w-full flex-col bg-background">
        {/* Header */}
        <div className="flex shrink-0 items-start justify-between border-b border-border px-6 py-4">
          <div className="min-w-0 flex-1 pr-4">
            <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              Zero1 by Zerodha · Full Analysis
            </p>
            <h2 className="mt-1 text-base font-semibold leading-snug">{instrumentName}</h2>
            {m && (
              <div className="mt-2 flex items-center gap-2">
                <span className="bg-blue-500/8 rounded border border-blue-500/30 px-2 py-0.5 text-[10px] font-medium text-blue-400">
                  {m.category}
                </span>
                <span className={cn('text-xs font-semibold', verdict?.color)}>
                  {m.zero1_score}/90 · {verdict?.label}
                </span>
              </div>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <button
              onClick={() => refetch()}
              disabled={isFetching}
              title="Fetch latest NAV data from mfapi.in"
              className="grid h-8 w-8 place-items-center rounded-lg border border-border text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary disabled:opacity-40"
            >
              <RefreshCw className={cn('size-3.5', isFetching && 'animate-spin')} />
            </button>
            <button
              onClick={onClose}
              className="grid h-8 w-8 place-items-center rounded-lg border border-border text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
            >
              <X className="size-4" />
            </button>
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto">
          <div className="mx-auto w-[80vw]">
            {isLoading && <LoadingSkeleton />}

            {error && (
              <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
                <AlertTriangle className="size-8 text-amber-400" />
                <p className="text-sm font-medium">Failed to load metrics</p>
                <p className="text-xs text-muted-foreground">
                  mfapi.in may be temporarily unavailable
                </p>
              </div>
            )}

            {m && scores && verdict && (
              <>
                {/* ── Overall score ── */}
                <section className="px-6 py-5">
                  <div className="mb-3 flex items-end justify-between">
                    <div>
                      <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                        Overall Score
                      </p>
                      <p
                        className={cn(
                          'mt-1 text-5xl font-bold tabular-nums tracking-tight',
                          verdict.color,
                        )}
                      >
                        {m.zero1_score}
                        <span className="text-2xl text-muted-foreground">/90</span>
                      </p>
                    </div>
                    <p className="text-right text-[11px] leading-relaxed text-muted-foreground">
                      Rolling · Sharpe · StdDev
                      <br />
                      Beta · AUM · TER
                    </p>
                  </div>
                  <div className="bg-white/8 h-2 overflow-hidden rounded-full">
                    <div
                      className={cn('h-full rounded-full', verdict.bar)}
                      style={{ width: `${Math.round((m.zero1_score / 90) * 100)}%` }}
                    />
                  </div>
                </section>

                {/* ── Score breakdown ── */}
                <Section title="Score Breakdown">
                  <div className="space-y-3">
                    <ScoreRow
                      label="Rolling Returns"
                      score={scores.roll}
                      max={30}
                      detail={`Avg 3Y ${fmt(m.rolling_3y_avg_pct)}%`}
                    />
                    <ScoreRow
                      label="Sharpe Ratio"
                      score={scores.sharpe}
                      max={15}
                      detail={fmt(m.sharpe_1y, 3)}
                    />
                    <ScoreRow
                      label="Std Deviation"
                      score={scores.stddev}
                      max={15}
                      detail={`${fmt(m.std_dev_1y_pct)}% ann.`}
                    />
                    <ScoreRow
                      label="Beta"
                      score={scores.beta}
                      max={10}
                      detail={m.beta ? fmt(m.beta, 2) : 'Unknown'}
                    />
                    <ScoreRow
                      label="AUM"
                      score={scores.aum}
                      max={10}
                      detail={m.aum_cr ? `₹${(m.aum_cr / 1000).toFixed(0)}K Cr` : 'Unknown'}
                    />
                    <ScoreRow
                      label="TER"
                      score={scores.ter}
                      max={10}
                      detail={m.ter_pct ? `${m.ter_pct}%` : 'Unknown'}
                    />
                  </div>
                </Section>

                {/* ── NAV growth chart ── */}
                <Section title="NAV Growth — 5 Years">
                  <NavChart data={m.nav_history} color={verdict.hex} />
                  <div className="mt-2 grid grid-cols-3 gap-2">
                    <Stat
                      label="1Y Return"
                      value={`${fmt(m.rolling_1y_avg_pct)}%`}
                      sub="Avg rolling 1Y"
                      color={m.rolling_1y_avg_pct > 12 ? 'text-emerald-400' : undefined}
                    />
                    <Stat
                      label="3Y Return"
                      value={`${fmt(m.rolling_3y_avg_pct)}%`}
                      sub="Avg rolling 3Y"
                      color={m.rolling_3y_avg_pct > 15 ? 'text-emerald-400' : undefined}
                    />
                    <Stat
                      label="Min 3Y"
                      value={`${fmt(m.rolling_3y_min_pct)}%`}
                      sub="Worst 3Y period"
                      color={
                        m.rolling_3y_min_pct > 0
                          ? 'text-emerald-400'
                          : m.rolling_3y_min_pct > -5
                            ? 'text-amber-400'
                            : 'text-red-400'
                      }
                    />
                  </div>
                </Section>

                {/* ── Rolling 1Y returns chart ── */}
                <Section title="Rolling 1Y Returns — Consistency">
                  <div className="mb-3 flex items-center justify-between">
                    <p className="text-xs text-muted-foreground">
                      Each bar = 1-year CAGR from that date. Green = positive.
                    </p>
                    <span
                      className={cn(
                        'text-sm font-semibold tabular-nums',
                        m.consistency_1y_pct >= 90
                          ? 'text-emerald-400'
                          : m.consistency_1y_pct >= 75
                            ? 'text-amber-400'
                            : 'text-red-400',
                      )}
                    >
                      {fmt(m.consistency_1y_pct, 1)}% positive
                    </span>
                  </div>
                  <RollingChart data={m.roll_1y_series} />
                </Section>

                {/* ── Drawdown chart ── */}
                <Section title="Drawdown from Peak — 3 Years">
                  <div className="mb-3 flex items-center justify-between">
                    <p className="text-xs text-muted-foreground">
                      How far the fund fell from its previous high at each point.
                    </p>
                    <span className="text-sm font-semibold tabular-nums text-red-400">
                      −{fmt(m.max_drawdown_3y_pct)}% max
                    </span>
                  </div>
                  <DrawdownChart data={m.drawdown_series} />
                </Section>

                {/* ── Risk metrics ── */}
                <Section title="Risk Metrics">
                  <div className="mb-4 grid grid-cols-2 gap-2">
                    <Stat
                      label="Sharpe Ratio (1Y)"
                      value={fmt(m.sharpe_1y, 3)}
                      sub={
                        m.sharpe_1y >= 0.5
                          ? 'Good'
                          : m.sharpe_1y >= 0
                            ? 'Acceptable'
                            : 'Market correction'
                      }
                      color={m.sharpe_1y >= 0.5 ? 'text-emerald-400' : undefined}
                    />
                    <Stat
                      label="Max Drawdown (3Y)"
                      value={`−${fmt(m.max_drawdown_3y_pct)}%`}
                      sub="Peak to trough"
                      color={
                        m.max_drawdown_3y_pct > 30
                          ? 'text-red-400'
                          : m.max_drawdown_3y_pct > 20
                            ? 'text-amber-400'
                            : undefined
                      }
                    />
                  </div>

                  {/* Std Dev bar */}
                  <div className="border-white/8 mb-2 rounded-lg border bg-white/[0.03] px-4 py-3">
                    <div className="mb-2 flex items-center justify-between">
                      <p className="text-xs text-muted-foreground">
                        Std Dev (1Y ann.) vs {m.category} avg
                      </p>
                      <div className="flex items-center gap-1.5">
                        {m.std_dev_1y_pct < bench ? (
                          <CheckCircle2 className="size-3.5 text-emerald-400" />
                        ) : (
                          <AlertTriangle className="size-3.5 text-amber-400" />
                        )}
                        <span className="text-sm font-semibold tabular-nums">
                          {fmt(m.std_dev_1y_pct)}%
                          <span className="text-xs text-muted-foreground"> / {bench}%</span>
                        </span>
                      </div>
                    </div>
                    <div className="bg-white/8 relative h-2 overflow-hidden rounded-full">
                      <div
                        className="absolute h-full rounded-full bg-blue-500/60"
                        style={{ width: `${Math.min(100, (m.std_dev_1y_pct / 30) * 100)}%` }}
                      />
                      <div
                        className="absolute top-0 h-full w-px bg-amber-400/80"
                        style={{ left: `${Math.min(100, (bench / 30) * 100)}%` }}
                      />
                    </div>
                    <div className="mt-1 flex justify-between text-[10px] text-muted-foreground/60">
                      <span>Fund: {fmt(m.std_dev_1y_pct)}%</span>
                      <span className="text-amber-400/70">▲ Avg: {bench}%</span>
                    </div>
                  </div>

                  {/* Beta bar */}
                  {m.beta > 0 && (
                    <div className="border-white/8 rounded-lg border bg-white/[0.03] px-4 py-3">
                      <div className="mb-2 flex items-center justify-between">
                        <p className="text-xs text-muted-foreground">Beta vs benchmark</p>
                        <span className="text-sm font-semibold tabular-nums">
                          {fmt(m.beta, 2)}
                          <span className="ml-1.5 text-[11px] text-muted-foreground">
                            {betaLabel(m.beta)}
                          </span>
                        </span>
                      </div>
                      <div className="bg-white/8 relative h-2 overflow-hidden rounded-full">
                        <div
                          className="absolute h-full rounded-full bg-violet-500/60"
                          style={{ width: `${Math.min(100, (m.beta / 1.5) * 100)}%` }}
                        />
                        <div
                          className="absolute top-0 h-full w-px bg-white/25"
                          style={{ left: '66.6%' }}
                        />
                      </div>
                      <div className="mt-1 flex justify-between text-[10px] text-muted-foreground/60">
                        <span>0 Defensive</span>
                        <span className="text-white/25">▲ Market 1.0</span>
                        <span>1.5 High</span>
                      </div>
                    </div>
                  )}
                </Section>

                {/* ── Fund quality ── */}
                <Section title="Fund Quality">
                  <div className="grid grid-cols-2 gap-3">
                    <div className="border-white/8 rounded-lg border bg-white/[0.03] px-4 py-3">
                      <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                        AUM
                      </p>
                      {m.aum_cr > 0 ? (
                        <>
                          <p
                            className={cn(
                              'mt-1 text-xl font-semibold tabular-nums',
                              (m.category === 'Small Cap' && m.aum_cr > 40000) ||
                                (m.category === 'Mid Cap' && m.aum_cr > 60000)
                                ? 'text-amber-400'
                                : 'text-foreground',
                            )}
                          >
                            ₹{(m.aum_cr / 1000).toFixed(0)}K Cr
                          </p>
                          {m.category === 'Small Cap' && m.aum_cr > 40000 ? (
                            <div className="mt-1.5 flex items-center gap-1">
                              <AlertTriangle className="size-3 text-amber-400" />
                              <p className="text-[10px] text-amber-400">Capacity concern</p>
                            </div>
                          ) : (
                            <div className="mt-1.5 flex items-center gap-1">
                              <CheckCircle2 className="size-3 text-emerald-400" />
                              <p className="text-[10px] text-emerald-400">Good size for category</p>
                            </div>
                          )}
                        </>
                      ) : (
                        <p className="mt-1 text-sm text-muted-foreground">Unknown</p>
                      )}
                    </div>

                    <div className="border-white/8 rounded-lg border bg-white/[0.03] px-4 py-3">
                      <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                        TER (Expense Ratio)
                      </p>
                      {m.ter_pct > 0 ? (
                        <>
                          <p
                            className={cn(
                              'mt-1 text-xl font-semibold tabular-nums',
                              m.ter_pct < 0.5
                                ? 'text-emerald-400'
                                : m.ter_pct < 1
                                  ? 'text-foreground'
                                  : 'text-amber-400',
                            )}
                          >
                            {m.ter_pct}%
                          </p>
                          <p className="mt-1 text-[10px] text-muted-foreground">
                            {m.ter_pct < 0.5
                              ? 'Excellent · Direct plan'
                              : m.ter_pct < 0.7
                                ? 'Good · Direct plan'
                                : m.ter_pct < 1
                                  ? 'Acceptable'
                                  : 'High — check if direct'}
                          </p>
                        </>
                      ) : (
                        <p className="mt-1 text-sm text-muted-foreground">Unknown</p>
                      )}
                    </div>
                  </div>
                </Section>

                {/* ── Footer ── */}
                <section className="px-6 pb-6 pt-4">
                  <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
                    <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
                    <p className="text-[11px] leading-relaxed text-muted-foreground">
                      Scores computed using the Zero1 by Zerodha 6-metric framework. NAV data from
                      mfapi.in. AUM and TER from AMFI public disclosures (updated quarterly). Not
                      financial advice.
                    </p>
                  </div>
                </section>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
