import { useEffect, useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  AreaChart, Area, BarChart, Bar, XAxis, YAxis,
  Tooltip, ResponsiveContainer, ReferenceLine, Cell,
} from 'recharts';
import {
  X, AlertTriangle, CheckCircle2, Info, RefreshCw,
  TrendingUp, TrendingDown, Pencil, Loader2,
} from 'lucide-react';
import { signalApi, type MFMetrics, type ETFMetrics, type StockMetrics } from '@/lib/api';
import { cn } from '@/lib/utils';

interface Props {
  instrumentId: number;
  instrumentName: string;
  assetType?: string;
  onClose: () => void;
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

function fmt(n: number, d = 2) { return n?.toFixed(d) ?? '—'; }

function fmtAxisDate(d: string) {
  return new Date(d).toLocaleDateString('en-IN', { month: 'short', year: '2-digit' });
}

const tooltipStyle = {
  contentStyle: { background: '#18181b', border: '1px solid #27272a', borderRadius: 8, fontSize: 11 },
  labelStyle: { color: '#a1a1aa' },
  itemStyle: { color: '#e4e4e7' },
};

function scoreVerdict(s: number) {
  if (s >= 80) return { label: 'Strong Buy', color: 'text-emerald-400', bar: 'bg-emerald-500', hex: '#10b981' };
  if (s >= 65) return { label: 'Hold',       color: 'text-blue-400',    bar: 'bg-blue-500',    hex: '#3b82f6' };
  if (s >= 50) return { label: 'Switch',     color: 'text-orange-400',  bar: 'bg-orange-500',  hex: '#f97316' };
  return           { label: 'Sell',       color: 'text-red-400',     bar: 'bg-red-500',     hex: '#ef4444' };
}

// ─── Shared sub-components ────────────────────────────────────────────────────

function Sk({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded bg-white/8', className)} />;
}
function LoadingSkeleton() {
  return (
    <div className="mx-auto w-[80vw] space-y-5 p-6">
      <Sk className="h-5 w-40" />
      <Sk className="h-20 w-full" />
      <div className="space-y-2">{[1,2,3,4,5,6].map(i => <Sk key={i} className="h-4 w-full" />)}</div>
      <Sk className="h-48 w-full" />
      <Sk className="h-36 w-full" />
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-border px-6 py-5">
      <p className="mb-4 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</p>
      {children}
    </section>
  );
}

function ScoreRow({ label, score, max, detail }: { label: string; score: number; max: number; detail: string }) {
  const pct = (score / max) * 100;
  const color = pct >= 70 ? 'bg-emerald-500' : pct >= 45 ? 'bg-amber-500' : 'bg-red-500';
  return (
    <div className="flex items-center gap-3">
      <span className="w-28 shrink-0 text-xs text-muted-foreground">{label}</span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-white/8">
        <div className={cn('h-full rounded-full', color)} style={{ width: `${pct}%` }} />
      </div>
      <span className="w-12 shrink-0 text-right text-xs tabular-nums">
        {score}<span className="text-muted-foreground">/{max}</span>
      </span>
      <span className="w-28 shrink-0 text-right text-[10px] text-muted-foreground truncate">{detail}</span>
    </div>
  );
}

function Stat({ label, value, sub, color }: { label: string; value: string; sub?: string; color?: string }) {
  return (
    <div className="rounded-lg border border-white/8 bg-white/[0.03] px-3 py-2.5">
      <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">{label}</p>
      <p className={cn('mt-0.5 text-lg font-semibold tabular-nums', color ?? 'text-foreground')}>{value}</p>
      {sub && <p className="mt-0.5 text-[10px] text-muted-foreground/70">{sub}</p>}
    </div>
  );
}

function NavChart({ data, color }: { data: { d: string; v: number }[]; color: string }) {
  if (!data?.length) return null;
  const min = Math.min(...data.map(p => p.v));
  const max = Math.max(...data.map(p => p.v));
  return (
    <div className="h-44 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="priceGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%"  stopColor={color} stopOpacity={0.25} />
              <stop offset="95%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <XAxis dataKey="d" tickFormatter={fmtAxisDate} tick={{ fontSize: 10, fill: '#71717a' }}
            interval={Math.floor(data.length / 5)} tickLine={false} axisLine={false} />
          <YAxis domain={[min * 0.97, max * 1.01]} tickFormatter={v => `₹${v.toFixed(0)}`}
            tick={{ fontSize: 10, fill: '#71717a' }} tickLine={false} axisLine={false} width={52} />
          <Tooltip {...tooltipStyle}
            labelFormatter={d => new Date(d).toLocaleDateString('en-IN', { day: 'numeric', month: 'short', year: 'numeric' })}
            formatter={(v: number) => [`₹${v.toFixed(2)}`, 'Price']} />
          <Area type="monotone" dataKey="v" stroke={color} strokeWidth={1.5}
            fill="url(#priceGrad)" dot={false} activeDot={{ r: 3, fill: color }} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

function RollingChart({ data }: { data: { d: string; v: number }[] }) {
  if (!data?.length) return null;
  return (
    <div className="h-36 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }} barSize={2}>
          <XAxis dataKey="d" tickFormatter={fmtAxisDate} tick={{ fontSize: 10, fill: '#71717a' }}
            interval={Math.floor(data.length / 5)} tickLine={false} axisLine={false} />
          <YAxis tickFormatter={v => `${v.toFixed(0)}%`}
            tick={{ fontSize: 10, fill: '#71717a' }} tickLine={false} axisLine={false} width={38} />
          <ReferenceLine y={0} stroke="#52525b" strokeWidth={1} />
          <Tooltip {...tooltipStyle}
            labelFormatter={d => new Date(d).toLocaleDateString('en-IN', { month: 'short', year: 'numeric' })}
            formatter={(v: number) => [`${v.toFixed(2)}%`, 'Rolling 1Y CAGR']} />
          <Bar dataKey="v" radius={[1,1,0,0]}>
            {data.map((p, i) => <Cell key={i} fill={p.v >= 0 ? '#10b981' : '#ef4444'} fillOpacity={0.8} />)}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function DrawdownChart({ data }: { data: { d: string; v: number }[] }) {
  if (!data?.length) return null;
  return (
    <div className="h-32 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="ddGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%"  stopColor="#ef4444" stopOpacity={0.3} />
              <stop offset="95%" stopColor="#ef4444" stopOpacity={0.05} />
            </linearGradient>
          </defs>
          <XAxis dataKey="d" tickFormatter={fmtAxisDate} tick={{ fontSize: 10, fill: '#71717a' }}
            interval={Math.floor(data.length / 4)} tickLine={false} axisLine={false} />
          <YAxis tickFormatter={v => `${v.toFixed(0)}%`}
            tick={{ fontSize: 10, fill: '#71717a' }} tickLine={false} axisLine={false} width={38} />
          <ReferenceLine y={0} stroke="#52525b" strokeWidth={1} />
          <Tooltip {...tooltipStyle}
            labelFormatter={d => new Date(d).toLocaleDateString('en-IN', { month: 'short', year: 'numeric' })}
            formatter={(v: number) => [`${v.toFixed(2)}%`, 'Drawdown']} />
          <Area type="monotone" dataKey="v" stroke="#ef4444" strokeWidth={1}
            fill="url(#ddGrad)" dot={false} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

// ─── Overall score header (shared) ───────────────────────────────────────────

function OverallScore({ score, verdict, gaps, availableMax }: {
  score: number;
  verdict: ReturnType<typeof scoreVerdict>;
  gaps?: string[];
  availableMax?: number;
}) {
  const hasGaps = gaps && gaps.length > 0;
  return (
    <section className="px-6 py-5">
      <div className="flex items-end justify-between mb-3">
        <div>
          <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Overall Score</p>
          <p className={cn('mt-1 text-5xl font-bold tabular-nums tracking-tight', verdict.color)}>
            {score}<span className="text-2xl text-muted-foreground">/100</span>
          </p>
          {hasGaps && (
            <p className="mt-1 text-[10px] text-muted-foreground/70">
              Scored on {availableMax ?? '—'} available pts · gaps: {gaps!.join(', ')}
            </p>
          )}
        </div>
        <p className={cn('rounded-full border px-3 py-1 text-sm font-semibold', verdict.color,
          score >= 80 ? 'border-emerald-500/30 bg-emerald-500/8' : score >= 65 ? 'border-blue-500/30 bg-blue-500/8' : score >= 50 ? 'border-orange-500/30 bg-orange-500/8' : 'border-red-500/30 bg-red-500/8')}>
          {verdict.label}
        </p>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-white/8">
        <div className={cn('h-full rounded-full', verdict.bar)} style={{ width: `${score}%` }} />
      </div>
      {hasGaps && (
        <div className="mt-3 flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-400" />
          <p className="text-[11px] text-amber-400/80">
            <span className="font-semibold">Data gaps:</span> {gaps!.join(' · ')} — not in our database. Score calculated from {availableMax} available points only, then normalised to 100.
          </p>
        </div>
      )}
    </section>
  );
}

// ─── MF / ETF body ────────────────────────────────────────────────────────────

function catBench(cat: string) {
  if (cat === 'Small Cap') return 18;
  if (cat === 'Mid Cap')   return 17;
  if (cat === 'Large Cap') return 14;
  if (cat === 'ELSS')      return 17;
  return 15;
}

function betaLabel(b: number) {
  if (!b)      return 'Unknown';
  if (b <= 0.75) return 'Defensive';
  if (b <= 0.85) return 'Low';
  if (b <= 0.95) return 'Moderate';
  if (b <= 1.05) return 'Market-linked';
  return 'High';
}

// pillarPct returns 0–100 for a pillar score / max, or null if no max.
function pillarPct(score: number, max: number) {
  return max > 0 ? Math.round((score / max) * 100) : null;
}

function pillarColor(pct: number | null) {
  if (pct === null) return 'text-muted-foreground';
  if (pct >= 75) return 'text-emerald-400';
  if (pct >= 50) return 'text-amber-400';
  return 'text-red-400';
}

function pillarBarColor(pct: number | null) {
  if (pct === null) return 'bg-white/20';
  if (pct >= 75) return 'bg-emerald-500/70';
  if (pct >= 50) return 'bg-amber-500/70';
  return 'bg-red-500/60';
}

function MFETFBody({ m, isETF }: { m: MFMetrics; isETF: boolean }) {
  const verdict = scoreVerdict(m.zero1_score);
  const bench   = catBench(m.category);

  return (
    <>
      <OverallScore score={m.zero1_score} verdict={verdict} gaps={m.data_gaps} availableMax={m.available_max} />

      <Section title="Score Breakdown">
        {!m.pillars ? (
          <div className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2.5 text-[11px] text-amber-500">
            <AlertTriangle className="size-3.5 shrink-0" />
            Score breakdown uses the new 5-pillar formula — click Refresh to recalculate.
          </div>
        ) : (() => {
          const p = m.pillars;
          const etfM = isETF ? (m as unknown as import('@/lib/api').ETFMetrics) : null;
          const pillars = [
            {
              label: 'Returns Quality',
              score: p.return_quality, max: p.return_quality_max,
              metrics: [
                !isETF && m.alpha !== 0 && { label: 'Alpha (3Y)', detail: `${m.alpha > 0 ? '+' : ''}${fmt(m.alpha, 2)}%`, note: m.alpha >= 3 ? 'Strong outperformance' : m.alpha >= 0 ? 'In line' : 'Underperforming' },
                !isETF && m.cat_rank_3y > 0 && { label: 'Category Rank 3Y', detail: `#${m.cat_rank_3y} in ${m.category}`, note: m.cat_rank_3y <= 10 ? 'Top quartile' : m.cat_rank_3y <= 20 ? 'Above avg' : 'Below avg' },
                { label: 'Rolling 3Y CAGR', detail: `${fmt(m.rolling_3y_avg_pct)}%`, note: m.rolling_3y_avg_pct >= 18 ? 'Strong' : m.rolling_3y_avg_pct >= 12 ? 'Decent' : 'Weak' },
                { label: 'Positive 1Y Periods', detail: `${fmt(m.consistency_1y_pct, 1)}%`, note: m.consistency_1y_pct >= 78 ? 'Reliable' : m.consistency_1y_pct >= 65 ? 'Moderate' : 'Unreliable' },
                { label: 'Worst 3Y Period', detail: `${fmt(m.rolling_3y_min_pct)}%`, note: m.rolling_3y_min_pct > 0 ? 'Always positive' : m.rolling_3y_min_pct > -10 ? 'Minor dip' : 'Significant loss' },
              ].filter(Boolean),
            },
            {
              label: 'Risk-Adjusted',
              score: p.risk_adjusted, max: p.risk_adjusted_max,
              metrics: [
                { label: isETF ? 'Sharpe (1Y)' : (m.groww_sharpe ? 'Sharpe (3Y)' : 'Sharpe (1Y)'), detail: fmt(m.groww_sharpe || m.sharpe_1y, 3), note: (m.groww_sharpe || m.sharpe_1y) >= 0.5 ? 'Good' : (m.groww_sharpe || m.sharpe_1y) >= 0 ? 'Acceptable' : 'Weak' },
                !isETF && m.sortino_ratio !== 0 && { label: 'Sortino Ratio', detail: fmt(m.sortino_ratio, 3), note: m.sortino_ratio >= 1.5 ? 'Excellent' : m.sortino_ratio >= 1 ? 'Good' : 'Acceptable' },
                { label: 'Max Drawdown 3Y', detail: `−${fmt(m.max_drawdown_3y_pct)}%`, note: m.max_drawdown_3y_pct <= 25 ? 'Controlled' : m.max_drawdown_3y_pct <= 35 ? 'Moderate' : 'High' },
                { label: 'Volatility Control', detail: (() => { const sd = m.groww_std_dev || m.std_dev_1y_pct; const ref = m.std_dev_5y_median || catBench(m.category); return `${fmt(sd)}% / ${fmt(ref)}%`; })(), note: (() => { const sd = m.groww_std_dev || m.std_dev_1y_pct; const ref = m.std_dev_5y_median || catBench(m.category); const r = sd/ref; return r <= 0.95 ? 'Below norm' : r <= 1.10 ? 'Normal' : 'Elevated'; })() },
              ].filter(Boolean),
            },
            isETF && p.consistency_max > 0 ? {
              label: 'Index Tracking',
              score: p.consistency, max: p.consistency_max,
              metrics: [
                etfM?.tracking_error_pct != null && etfM.tracking_error_pct > 0
                  ? { label: 'Tracking Error', detail: `${fmt(etfM.tracking_error_pct, 2)}%`, note: etfM.tracking_error_pct < 0.3 ? 'Excellent' : etfM.tracking_error_pct < 0.5 ? 'Good' : 'Poor replication' }
                  : { label: 'Tracking Error', detail: 'N/A', note: 'Not available' },
              ],
            } : !isETF ? {
              label: 'Consistency',
              score: p.consistency, max: p.consistency_max,
              metrics: [
                { label: 'Positive 1Y Periods', detail: `${fmt(m.consistency_1y_pct, 1)}%`, note: m.consistency_1y_pct >= 78 ? 'Reliable' : m.consistency_1y_pct >= 65 ? 'Moderate' : 'Unreliable' },
                { label: 'Worst 3Y Period', detail: `${fmt(m.rolling_3y_min_pct)}%`, note: m.rolling_3y_min_pct > 0 ? 'Always positive' : m.rolling_3y_min_pct > -10 ? 'Minor dip' : 'Significant loss' },
                { label: 'Volatility Control', detail: (() => { const sd = m.groww_std_dev || m.std_dev_1y_pct; const ref = m.std_dev_5y_median || catBench(m.category); return `${fmt(sd)}% / ${fmt(ref)}%`; })(), note: (() => { const sd = m.groww_std_dev || m.std_dev_1y_pct; const ref = m.std_dev_5y_median || catBench(m.category); const r = sd/ref; return r <= 0.95 ? 'Below norm' : r <= 1.10 ? 'Normal' : 'Elevated'; })() },
              ],
            } : null,
            {
              label: isETF ? 'ETF Operations' : 'Fund Operations',
              score: p.fund_ops, max: p.fund_ops_max,
              metrics: [
                m.ter_pct > 0 && { label: isETF ? 'Expense Ratio' : 'TER', detail: `${m.ter_pct}%`, note: isETF ? (m.ter_pct < 0.2 ? 'Excellent' : m.ter_pct < 0.4 ? 'Good' : m.ter_pct < 0.7 ? 'Acceptable' : 'High') : (m.ter_pct < 0.5 ? 'Excellent' : m.ter_pct < 0.7 ? 'Good' : m.ter_pct < 1 ? 'Acceptable' : 'High') },
                m.beta > 0 && { label: 'Beta', detail: fmt(m.beta, 2), note: betaLabel(m.beta) },
                m.aum_cr > 0 && { label: 'AUM', detail: `₹${(m.aum_cr/1000).toFixed(0)}K Cr`, note: m.category === 'Small Cap' && m.aum_cr > 40000 ? 'Capacity concern' : 'OK' },
              ].filter(Boolean),
            },
            !isETF && p.validation_max > 0 ? {
              label: 'Groww Rating',
              score: p.validation, max: p.validation_max,
              metrics: [
                { label: 'Star Rating', detail: `${'★'.repeat(m.groww_rating)}${'☆'.repeat(5 - m.groww_rating)}`, note: m.groww_rating >= 4 ? 'Top rated' : m.groww_rating === 3 ? 'Average' : 'Below avg' },
              ],
            } : null,
          ].filter(Boolean) as { label: string; score: number; max: number; metrics: { label: string; detail: string; note: string }[] }[];

          return (
            <div className="space-y-4">
              {pillars.map(pillar => {
                const pct = pillarPct(pillar.score, pillar.max);
                return (
                  <div key={pillar.label} className="rounded-lg border border-white/8 bg-white/[0.02] overflow-hidden">
                    {/* Pillar header */}
                    <div className="flex items-center justify-between px-4 py-2.5 bg-white/[0.03] border-b border-white/8">
                      <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{pillar.label}</span>
                      <div className="flex items-center gap-3">
                        <div className="h-1.5 w-20 overflow-hidden rounded-full bg-white/10">
                          <div className={cn('h-full rounded-full transition-all', pillarBarColor(pct))}
                            style={{ width: `${pct ?? 0}%` }} />
                        </div>
                        <span className={cn('text-sm font-bold tabular-nums min-w-[3rem] text-right', pillarColor(pct))}>
                          {pillar.score}<span className="text-xs font-normal text-muted-foreground">/{pillar.max}</span>
                        </span>
                      </div>
                    </div>
                    {/* Metric rows */}
                    <div className="divide-y divide-white/5">
                      {pillar.metrics.map((metric: { label: string; detail: string; note: string }) => (
                        <div key={metric.label} className="flex items-center justify-between px-4 py-2">
                          <span className="text-xs text-muted-foreground">{metric.label}</span>
                          <div className="flex items-center gap-2 text-right">
                            <span className="text-xs text-muted-foreground/60">{metric.note}</span>
                            <span className="text-xs font-semibold tabular-nums text-foreground min-w-[3.5rem] text-right">{metric.detail}</span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          );
        })()}
      </Section>

      <Section title={`${isETF ? 'Price' : 'NAV'} Growth — 5 Years`}>
        <NavChart data={m.nav_history} color={verdict.hex} />
        <div className="mt-2 grid grid-cols-3 gap-2">
          <Stat label="1Y Return"  value={`${fmt(m.rolling_1y_avg_pct)}%`} sub="Avg rolling 1Y" color={m.rolling_1y_avg_pct > 12 ? 'text-emerald-400' : undefined} />
          <Stat label="3Y Return"  value={`${fmt(m.rolling_3y_avg_pct)}%`} sub="Avg rolling 3Y" color={m.rolling_3y_avg_pct > 15 ? 'text-emerald-400' : undefined} />
          <Stat label="Min 3Y"     value={`${fmt(m.rolling_3y_min_pct)}%`} sub="Worst 3Y period"
            color={m.rolling_3y_min_pct > 0 ? 'text-emerald-400' : m.rolling_3y_min_pct > -5 ? 'text-amber-400' : 'text-red-400'} />
        </div>
      </Section>

      <Section title="Rolling 1Y Returns — Consistency">
        <div className="mb-3 flex items-center justify-between">
          <p className="text-xs text-muted-foreground">Each bar = 1-year CAGR from that date. Green = positive.</p>
          <span className={cn('text-sm font-semibold tabular-nums',
            m.consistency_1y_pct >= 90 ? 'text-emerald-400' : m.consistency_1y_pct >= 75 ? 'text-amber-400' : 'text-red-400')}>
            {fmt(m.consistency_1y_pct, 1)}% positive
          </span>
        </div>
        <RollingChart data={m.roll_1y_series} />
      </Section>

      <Section title="Drawdown from Peak — 3 Years">
        <div className="mb-3 flex items-center justify-between">
          <p className="text-xs text-muted-foreground">How far the {isETF ? 'ETF' : 'fund'} fell from its previous high at each point.</p>
          <span className="text-sm font-semibold tabular-nums text-red-400">−{fmt(m.max_drawdown_3y_pct)}% max</span>
        </div>
        <DrawdownChart data={m.drawdown_series} />
      </Section>

      <Section title="Risk Metrics">
        <div className="grid grid-cols-2 gap-2 mb-4">
          {m.groww_sharpe ? (
            <Stat label="Sharpe Ratio (3Y)" value={fmt(m.groww_sharpe, 3)}
              sub={m.groww_sharpe >= 0.5 ? 'Good' : m.groww_sharpe >= 0 ? 'Acceptable' : 'Below risk-free'}
              color={m.groww_sharpe >= 0.5 ? 'text-emerald-400' : m.groww_sharpe >= 0 ? 'text-amber-400' : 'text-red-400'} />
          ) : (
            <Stat label="Sharpe Ratio (1Y)" value={fmt(m.sharpe_1y, 3)}
              sub={m.sharpe_1y >= 0.5 ? 'Good' : m.sharpe_1y >= 0 ? 'Acceptable' : 'Market correction'}
              color={m.sharpe_1y >= 0.5 ? 'text-emerald-400' : undefined} />
          )}
          <Stat label="Max Drawdown (3Y)" value={`−${fmt(m.max_drawdown_3y_pct)}%`} sub="Peak to trough"
            color={m.max_drawdown_3y_pct > 30 ? 'text-red-400' : m.max_drawdown_3y_pct > 20 ? 'text-amber-400' : undefined} />
        </div>
        {(m.alpha !== 0 || m.sortino_ratio !== 0) && (
          <div className="grid grid-cols-2 gap-2 mb-4">
            {m.alpha !== 0 && (
              <Stat label="Alpha (3Y)" value={`${m.alpha > 0 ? '+' : ''}${fmt(m.alpha, 2)}%`}
                sub={m.alpha >= 2 ? 'Outperforming' : m.alpha >= 0 ? 'In line' : 'Underperforming'}
                color={m.alpha >= 2 ? 'text-emerald-400' : m.alpha >= 0 ? 'text-amber-400' : 'text-red-400'} />
            )}
            {m.sortino_ratio !== 0 && (
              <Stat label="Sortino Ratio" value={fmt(m.sortino_ratio, 3)}
                sub={m.sortino_ratio >= 1.5 ? 'Excellent' : m.sortino_ratio >= 1 ? 'Good' : m.sortino_ratio >= 0.5 ? 'Acceptable' : 'Weak'}
                color={m.sortino_ratio >= 1 ? 'text-emerald-400' : m.sortino_ratio >= 0.5 ? 'text-amber-400' : 'text-red-400'} />
            )}
          </div>
        )}

        {(() => {
          // Use Groww's std dev when available; fall back to our computed 1Y value.
          // Compare against the fund's own 5Y median (what the score engine uses),
          // falling back to catBench only when std_dev_5y_median is unavailable.
          const sdVal   = (m.groww_std_dev && m.groww_std_dev > 0) ? m.groww_std_dev : m.std_dev_1y_pct;
          const refVal  = m.std_dev_5y_median > 0 ? m.std_dev_5y_median : bench;
          const refLabel = m.std_dev_5y_median > 0 ? 'own 5Y median' : `${m.category} avg`;
          const sdLabel  = m.groww_std_dev > 0 ? 'Groww ann.' : '1Y ann.';
          return (
            <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3 mb-2">
              <div className="flex items-center justify-between mb-2">
                <p className="text-xs text-muted-foreground">Std Dev ({sdLabel}) vs {refLabel}</p>
                <div className="flex items-center gap-1.5">
                  {sdVal < refVal
                    ? <CheckCircle2 className="size-3.5 text-emerald-400" />
                    : <AlertTriangle className="size-3.5 text-amber-400" />}
                  <span className="text-sm font-semibold tabular-nums">
                    {fmt(sdVal)}%<span className="text-xs text-muted-foreground"> / {fmt(refVal)}%</span>
                  </span>
                </div>
              </div>
              <div className="relative h-2 overflow-hidden rounded-full bg-white/8">
                <div className="absolute h-full rounded-full bg-blue-500/60" style={{ width: `${Math.min(100,(sdVal/30)*100)}%` }} />
                <div className="absolute top-0 h-full w-px bg-amber-400/80" style={{ left: `${Math.min(100,(refVal/30)*100)}%` }} />
              </div>
              <div className="mt-1 flex justify-between text-[10px] text-muted-foreground/60">
                <span>Fund: {fmt(sdVal)}%</span>
                <span className="text-amber-400/70">▲ {refLabel}: {fmt(refVal)}%</span>
              </div>
            </div>
          );
        })()}

        {m.beta > 0 && (
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <div className="flex items-center justify-between mb-2">
              <p className="text-xs text-muted-foreground">Beta vs benchmark</p>
              <span className="text-sm font-semibold tabular-nums">
                {fmt(m.beta, 2)}<span className="ml-1.5 text-[11px] text-muted-foreground">{betaLabel(m.beta)}</span>
              </span>
            </div>
            <div className="relative h-2 overflow-hidden rounded-full bg-white/8">
              <div className="absolute h-full rounded-full bg-violet-500/60" style={{ width: `${Math.min(100,(m.beta/1.5)*100)}%` }} />
              <div className="absolute top-0 h-full w-px bg-white/25" style={{ left: '66.6%' }} />
            </div>
            <div className="mt-1 flex justify-between text-[10px] text-muted-foreground/60">
              <span>0 Defensive</span><span className="text-white/25">▲ Market 1.0</span><span>1.5 High</span>
            </div>
          </div>
        )}
      </Section>

      <Section title={isETF ? 'ETF Quality' : 'Fund Quality'}>
        <div className="grid grid-cols-2 gap-3 mb-3">
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">AUM</p>
            {m.aum_cr > 0 ? (
              <>
                <p className={cn('mt-1 text-xl font-semibold tabular-nums',
                  (m.category === 'Small Cap' && m.aum_cr > 40000) ? 'text-amber-400' : 'text-foreground')}>
                  ₹{(m.aum_cr/1000).toFixed(0)}K Cr
                </p>
                {(m.category === 'Small Cap' && m.aum_cr > 40000)
                  ? <div className="mt-1.5 flex items-center gap-1"><AlertTriangle className="size-3 text-amber-400" /><p className="text-[10px] text-amber-400">Capacity concern</p></div>
                  : <div className="mt-1.5 flex items-center gap-1"><CheckCircle2 className="size-3 text-emerald-400" /><p className="text-[10px] text-emerald-400">Good size for category</p></div>}
              </>
            ) : <p className="mt-1 text-sm text-muted-foreground">Unknown</p>}
          </div>
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">{isETF ? 'Expense Ratio' : 'TER (Expense Ratio)'}</p>
            {m.ter_pct > 0 ? (
              <>
                <p className={cn('mt-1 text-xl font-semibold tabular-nums',
                  m.ter_pct < 0.5 ? 'text-emerald-400' : m.ter_pct < 1 ? 'text-foreground' : 'text-amber-400')}>
                  {m.ter_pct}%
                </p>
                <p className="mt-1 text-[10px] text-muted-foreground">
                  {m.ter_pct < 0.2 ? 'Excellent · Low-cost ETF' : m.ter_pct < 0.5 ? 'Good' : m.ter_pct < 1 ? 'Acceptable' : 'High'}
                </p>
              </>
            ) : <p className="mt-1 text-sm text-muted-foreground">Unknown</p>}
          </div>
        </div>

        {/* Groww Rating + Exit Load */}
        {(m.groww_rating > 0 || m.exit_load) && (
          <div className="grid grid-cols-2 gap-3 mb-3">
            {m.groww_rating > 0 && (
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Groww Rating</p>
                <p className="mt-1 text-xl font-semibold text-amber-400 tracking-wide">
                  {'★'.repeat(m.groww_rating)}
                  <span className="text-white/20">{'★'.repeat(5 - m.groww_rating)}</span>
                </p>
                <p className="mt-0.5 text-[10px] text-muted-foreground">{m.groww_rating >= 4 ? 'Top rated' : m.groww_rating === 3 ? 'Average' : 'Below average'}</p>
              </div>
            )}
            {m.exit_load && (
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Exit Load</p>
                <p className="mt-1 text-sm font-medium text-foreground leading-snug">{m.exit_load}</p>
              </div>
            )}
          </div>
        )}

        {/* Category comparison */}
        {m.cat_return_3y !== 0 && (
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground mb-2">
              vs {m.category} Category Average
            </p>
            <div className="grid grid-cols-3 gap-3">
              {[
                { label: '1Y', fund: m.rolling_1y_avg_pct, cat: m.cat_return_1y, rank: m.cat_rank_1y },
                { label: '3Y', fund: m.rolling_3y_avg_pct, cat: m.cat_return_3y, rank: m.cat_rank_3y },
                { label: '5Y', fund: 0, cat: m.cat_return_5y, rank: m.cat_rank_5y },
              ].map(({ label, fund, cat, rank }) => (
                <div key={label} className="text-center">
                  <p className="text-[10px] text-muted-foreground mb-1">{label}</p>
                  {fund !== 0 && (
                    <p className={cn('text-sm font-semibold tabular-nums', fund > cat ? 'text-emerald-400' : 'text-red-400')}>
                      {fmt(fund)}%
                    </p>
                  )}
                  {cat !== 0 && <p className="text-[10px] text-muted-foreground">Cat avg {fmt(cat)}%</p>}
                  {rank > 0 && <p className="text-[10px] text-amber-400/80">Rank #{rank}</p>}
                </div>
              ))}
            </div>
          </div>
        )}
      </Section>

      <section className="px-6 pb-6 pt-4">
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
          <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            {isETF
              ? 'Score computed using Zero1 6-metric framework adapted for ETFs. Price data from Yahoo Finance. AUM/TER from Yahoo quoteSummary.'
              : 'Score uses 10 metrics: Rolling returns, Sharpe, StdDev, Beta, AUM, TER, Alpha, Sortino, Category Rank, and Groww Rating. NAV data from mfapi.in · Live metrics from Groww.'}
            {' '}Not financial advice.
          </p>
        </div>
      </section>
    </>
  );
}

function ETFBody({ m }: { m: ETFMetrics }) {
  const verdict = scoreVerdict(m.zero1_score);
  const trackingError = m.tracking_error_pct ?? 0;

  return (
    <>
      <OverallScore score={m.zero1_score} verdict={verdict} gaps={m.data_gaps} availableMax={m.available_max} />

      <Section title="Fund Quality">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <Stat label="AUM"
            value={m.aum_cr > 0 ? `₹${m.aum_cr >= 1000 ? (m.aum_cr/1000).toFixed(1)+'K' : m.aum_cr.toFixed(0)} Cr` : '—'}
            sub={m.aum_cr >= 5000 ? 'High liquidity' : m.aum_cr >= 500 ? 'Adequate' : 'Small fund'}
            color={m.aum_cr >= 5000 ? 'text-emerald-400' : m.aum_cr >= 500 ? undefined : 'text-amber-400'} />
          <Stat label="Expense Ratio"
            value={m.ter_pct > 0 ? `${m.ter_pct.toFixed(2)}%` : '—'}
            sub={m.cat_ter_pct && m.cat_ter_pct > 0 ? `Cat avg ${m.cat_ter_pct.toFixed(2)}%` : 'Annual cost drag'}
            color={m.ter_pct > 0 && m.ter_pct < 0.2 ? 'text-emerald-400' : m.ter_pct < 0.5 ? undefined : 'text-amber-400'} />
          <Stat label="Tracking Error"
            value={trackingError > 0 ? `${trackingError.toFixed(2)}%` : '—'}
            sub={m.cat_tracking_error_pct && m.cat_tracking_error_pct > 0 ? `Cat avg ${m.cat_tracking_error_pct.toFixed(2)}%` : 'Index replication quality'}
            color={trackingError > 0 && trackingError < 0.3 ? 'text-emerald-400' : trackingError < 1 ? undefined : 'text-red-400'} />
        </div>
        {((m.cat_ter_pct ?? 0) > 0 || (m.cat_tracking_error_pct ?? 0) > 0) && (
          <div className="mt-3 grid grid-cols-2 gap-3">
            {(m.cat_ter_pct ?? 0) > 0 && (
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-3 py-2.5">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Category Exp Ratio</p>
                <p className="mt-0.5 text-base font-semibold tabular-nums">{m.cat_ter_pct!.toFixed(2)}%</p>
                {m.ter_pct > 0 && (
                  <p className={cn('mt-0.5 text-[10px]', m.ter_pct <= m.cat_ter_pct! ? 'text-emerald-400' : 'text-amber-400')}>
                    {m.ter_pct <= m.cat_ter_pct! ? '✓ Below category avg' : '↑ Above category avg'}
                  </p>
                )}
              </div>
            )}
            {(m.cat_tracking_error_pct ?? 0) > 0 && (
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-3 py-2.5">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Category Tracking Err</p>
                <p className="mt-0.5 text-base font-semibold tabular-nums">{m.cat_tracking_error_pct!.toFixed(2)}%</p>
                {trackingError > 0 && (
                  <p className={cn('mt-0.5 text-[10px]', trackingError <= m.cat_tracking_error_pct! ? 'text-emerald-400' : 'text-amber-400')}>
                    {trackingError <= m.cat_tracking_error_pct! ? '✓ Better than category' : '↑ Worse than category'}
                  </p>
                )}
              </div>
            )}
          </div>
        )}
      </Section>

      {/* Valuation — shown for commodity/sectoral ETFs that have PE/PB data */}
      {((m.ttm_pe ?? 0) > 0 || (m.pb_ratio ?? 0) > 0 || (m.div_yield ?? 0) > 0) && (
        <Section title="Valuation">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            {(m.ttm_pe ?? 0) > 0 && (
              <Stat label="TTM PE"
                value={`${m.ttm_pe!.toFixed(2)}×`}
                sub={(m.ind_pe ?? 0) > 0 ? `Index avg ${m.ind_pe!.toFixed(2)}×` : undefined}
                color={(m.ind_pe ?? 0) > 0 && m.ttm_pe! < m.ind_pe! ? 'text-emerald-400' : undefined} />
            )}
            {(m.pb_ratio ?? 0) > 0 && (
              <Stat label="PB Ratio"
                value={`${m.pb_ratio!.toFixed(2)}×`}
                sub={(m.ind_pb ?? 0) > 0 ? `Index avg ${m.ind_pb!.toFixed(2)}×` : undefined}
                color={(m.ind_pb ?? 0) > 0 && m.pb_ratio! < m.ind_pb! ? 'text-emerald-400' : undefined} />
            )}
            {(m.div_yield ?? 0) > 0 && (
              <Stat label="Dividend Yield"
                value={`${m.div_yield!.toFixed(2)}%`}
                sub={(m.ind_dy ?? 0) > 0 ? `Index avg ${m.ind_dy!.toFixed(2)}%` : undefined} />
            )}
          </div>
        </Section>
      )}

      <Section title="Score Breakdown">
        {m.pillars ? (() => {
          const p = m.pillars;
          const rows = [
            { label: 'Returns Quality', score: p.return_quality, max: p.return_quality_max, detail: m.rolling_3y_avg_pct ? `3Y avg ${m.rolling_3y_avg_pct.toFixed(1)}%` : '—' },
            { label: 'Risk-Adjusted',   score: p.risk_adjusted,  max: p.risk_adjusted_max,  detail: m.sharpe_1y ? `Sharpe ${m.sharpe_1y.toFixed(2)}` : '—' },
            { label: 'Index Tracking',  score: p.consistency,    max: p.consistency_max,    detail: m.tracking_error_pct ? `TE ${m.tracking_error_pct.toFixed(2)}%` : 'No data' },
            { label: 'Fund Ops',        score: p.fund_ops,       max: p.fund_ops_max,       detail: m.ter_pct ? `TER ${m.ter_pct.toFixed(2)}%` : '—' },
          ].filter(r => r.max > 0);
          return <div className="space-y-3">{rows.map(r => <ScoreRow key={r.label} {...r} />)}</div>;
        })() : (
          <p className="text-[11px] text-muted-foreground">Click Refresh to compute score breakdown.</p>
        )}
      </Section>

      <Section title="Return Metrics">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="1Y Avg Return" value={m.rolling_1y_avg_pct ? `${m.rolling_1y_avg_pct.toFixed(1)}%` : '—'}
            color={m.rolling_1y_avg_pct && m.rolling_1y_avg_pct >= 12 ? 'text-emerald-400' : m.rolling_1y_avg_pct && m.rolling_1y_avg_pct >= 6 ? undefined : 'text-red-400'} />
          <Stat label="3Y Avg Return" value={m.rolling_3y_avg_pct ? `${m.rolling_3y_avg_pct.toFixed(1)}%` : '—'}
            color={m.rolling_3y_avg_pct && m.rolling_3y_avg_pct >= 12 ? 'text-emerald-400' : m.rolling_3y_avg_pct && m.rolling_3y_avg_pct >= 6 ? undefined : 'text-red-400'} />
          <Stat label="Consistency" value={m.consistency_1y_pct ? `${m.consistency_1y_pct.toFixed(0)}%` : '—'}
            sub="1Y positive periods" color={m.consistency_1y_pct && m.consistency_1y_pct >= 75 ? 'text-emerald-400' : undefined} />
          <Stat label="Beta" value={m.beta ? m.beta.toFixed(2) : '—'}
            sub={m.beta ? (m.beta <= 0.8 ? 'Defensive' : m.beta <= 1.1 ? 'Market-linked' : 'Aggressive') : undefined} />
        </div>
      </Section>

      <Section title="Risk">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <Stat label="Sharpe 1Y" value={m.sharpe_1y ? m.sharpe_1y.toFixed(2) : '—'}
            color={m.sharpe_1y && m.sharpe_1y >= 1 ? 'text-emerald-400' : m.sharpe_1y && m.sharpe_1y >= 0.5 ? undefined : 'text-red-400'} />
          <Stat label="Max Drawdown 3Y" value={m.max_drawdown_3y_pct ? `−${m.max_drawdown_3y_pct.toFixed(1)}%` : '—'}
            color={m.max_drawdown_3y_pct && m.max_drawdown_3y_pct > 35 ? 'text-red-400' : m.max_drawdown_3y_pct && m.max_drawdown_3y_pct > 20 ? 'text-amber-400' : undefined} />
          <Stat label="Std Dev 1Y" value={m.std_dev_1y_pct ? `${m.std_dev_1y_pct.toFixed(1)}%` : '—'}
            sub="Annualised volatility" />
        </div>
      </Section>

      {(m.nav_history?.length ?? 0) > 0 && (
        <Section title="Price History — 5 Years">
          <NavChart data={m.nav_history!} color={verdict.hex} />
        </Section>
      )}
      {(m.roll_1y_series?.length ?? 0) > 0 && (
        <Section title="Rolling 1Y Returns">
          <RollingChart data={m.roll_1y_series!} />
        </Section>
      )}
      {(m.drawdown_series?.length ?? 0) > 0 && (
        <Section title="Drawdown from Peak — 3 Years">
          <DrawdownChart data={m.drawdown_series!} />
        </Section>
      )}

      <section className="px-6 pb-6 pt-4">
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
          <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            AUM / TER / Tracking Error from Tickertape. Price history from Yahoo Finance via local prices table. Not financial advice.
          </p>
        </div>
      </section>
    </>
  );
}

// ─── Stock body ───────────────────────────────────────────────────────────────

function pctColor(v: number, good: number, ok: number) {
  if (v >= good) return 'text-emerald-400';
  if (v >= ok)   return 'text-amber-400';
  return 'text-red-400';
}

function GaugeBar({ value, max, color }: { value: number; max: number; color: string }) {
  const pct = Math.min(100, Math.max(0, (value / max) * 100));
  return (
    <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-white/8">
      <div className={cn('h-full rounded-full', color)} style={{ width: `${pct}%` }} />
    </div>
  );
}

function FinancialTable({ rows, cols }: { rows: string[]; cols: { label: string; values: (string | number)[] }[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-white/8">
            <th className="py-2 text-left text-[10px] font-medium uppercase tracking-wider text-muted-foreground w-36">Metric</th>
            {rows.map(r => <th key={r} className="py-2 text-right text-[10px] font-medium uppercase tracking-wider text-muted-foreground px-2">{r}</th>)}
          </tr>
        </thead>
        <tbody>
          {cols.map(col => (
            <tr key={col.label} className="border-b border-white/5">
              <td className="py-2.5 text-[11px] text-muted-foreground">{col.label}</td>
              {col.values.map((v, i) => (
                <td key={i} className="py-2.5 text-right text-[11px] tabular-nums font-medium px-2">
                  {typeof v === 'number' ? (v === 0 ? '—' : v < 0 ? `(${Math.abs(v).toLocaleString()})` : v.toLocaleString()) : v}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ShareholdingBar({ label, pct, color }: { label: string; pct: number; color: string }) {
  return (
    <div className="flex items-center gap-3">
      <span className="w-20 shrink-0 text-[11px] text-muted-foreground">{label}</span>
      <div className="h-2 flex-1 overflow-hidden rounded-full bg-white/8">
        <div className={cn('h-full rounded-full', color)} style={{ width: `${Math.min(100, pct)}%` }} />
      </div>
      <span className="w-12 shrink-0 text-right text-[11px] tabular-nums font-medium">{pct > 0 ? `${pct.toFixed(1)}%` : '—'}</span>
    </div>
  );
}

function StockBody({ m, instrumentId }: { m: StockMetrics; instrumentId: number }) {
  const [tab, setTab] = useState<'overview' | 'income' | 'balance' | 'cashflow' | 'shareholding'>('overview');
  const [financialsEnabled, setFinancialsEnabled] = useState(false);
  const verdict = scoreVerdict(m.zero1_score);
  const medianPE = sectorMedianPE(m.sector);

  // Lazy-load financials only when a financial tab is first clicked.
  const { data: fin, isLoading: finLoading, error: finError } = useQuery({
    queryKey: ['stock-financials', instrumentId],
    queryFn: () => signalApi.getStockFinancials(instrumentId),
    enabled: financialsEnabled,
    staleTime: 10 * 60_000,
    retry: 1,
  });

  function openTab(id: typeof tab) {
    if (id !== 'overview') setFinancialsEnabled(true);
    setTab(id);
  }

  const valScore = (() => {
    const peRatio = m.trailing_pe > 0 ? m.trailing_pe / medianPE : -1;
    let s = peRatio < 0 ? 8 : peRatio < 0.75 ? 12 : peRatio < 1 ? 10 : peRatio < 1.3 ? 7 : peRatio < 1.8 ? 4 : 1;
    s += m.price_to_book <= 0 ? 4 : m.price_to_book < 1.5 ? 8 : m.price_to_book < 3 ? 6 : m.price_to_book < 6 ? 3 : 1;
    return s;
  })();
  const profScore = (() => {
    let s = m.roe >= 20 ? 10 : m.roe >= 15 ? 8 : m.roe >= 10 ? 5 : m.roe > 0 ? 2 : 0;
    s += m.profit_margin >= 20 ? 10 : m.profit_margin >= 12 ? 8 : m.profit_margin >= 6 ? 5 : m.profit_margin > 0 ? 2 : 0;
    return s;
  })();
  const growthScore = (() => {
    let s = m.revenue_growth >= 20 ? 8 : m.revenue_growth >= 12 ? 6 : m.revenue_growth >= 5 ? 4 : m.revenue_growth > 0 ? 2 : 0;
    s += m.earnings_growth >= 20 ? 7 : m.earnings_growth >= 12 ? 5 : m.earnings_growth >= 5 ? 3 : m.earnings_growth > 0 ? 1 : 0;
    return s;
  })();
  const healthScore = (() => {
    let s = m.debt_to_equity <= 0 ? 5 : m.debt_to_equity < 30 ? 8 : m.debt_to_equity < 70 ? 6 : m.debt_to_equity < 120 ? 4 : m.debt_to_equity < 200 ? 2 : 0;
    s += m.current_ratio <= 0 ? 3 : m.current_ratio >= 2 ? 7 : m.current_ratio >= 1.5 ? 5 : m.current_ratio >= 1 ? 3 : 0;
    return s;
  })();
  const returnScore = (() => {
    let s = m.return_1y_pct >= 25 ? 6 : m.return_1y_pct >= 12 ? 5 : m.return_1y_pct >= 0 ? 3 : 0;
    s += m.max_drawdown_3y_pct < 20 ? 4 : m.max_drawdown_3y_pct < 35 ? 3 : m.max_drawdown_3y_pct < 50 ? 1 : 0;
    return s;
  })();
  const sizeScore = (() => {
    let s = m.market_cap_cr >= 20000 ? 6 : m.market_cap_cr >= 5000 ? 5 : m.market_cap_cr >= 500 ? 3 : 1;
    s += m.beta <= 0 ? 2 : m.beta <= 0.8 ? 4 : m.beta <= 1.1 ? 3 : m.beta <= 1.4 ? 2 : 1;
    return s;
  })();

  const tabs = [
    { id: 'overview' as const, label: 'Overview' },
    { id: 'income' as const, label: 'Income' },
    { id: 'balance' as const, label: 'Balance Sheet' },
    { id: 'cashflow' as const, label: 'Cash Flow' },
    { id: 'shareholding' as const, label: 'Shareholding' },
  ];

  return (
    <>
      <OverallScore score={m.zero1_score} verdict={verdict} gaps={m.data_gaps} availableMax={m.available_max} />

      {(m.sector || m.industry) && (
        <section className="border-t border-border px-6 py-3">
          <div className="flex items-center gap-3 flex-wrap">
            {m.sector && <span className="rounded border border-blue-500/30 bg-blue-500/8 px-2 py-0.5 text-[10px] font-medium text-blue-400">{m.sector}</span>}
            {m.industry && <span className="text-xs text-muted-foreground">{m.industry}</span>}
            {m.market_cap_cr > 0 && <span className="text-xs text-muted-foreground">₹{(m.market_cap_cr/1000).toFixed(1)}K Cr</span>}
          </div>
        </section>
      )}

      <div className="border-b border-border px-6">
        <div className="flex gap-1 overflow-x-auto pb-0">
          {tabs.map(t => (
            <button key={t.id} onClick={() => openTab(t.id)}
              className={cn('shrink-0 border-b-2 px-3 py-2.5 text-[11px] font-medium transition-colors',
                tab === t.id ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'
              )}>
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {tab === 'overview' && (
        <>
          <Section title="Score Breakdown">
            <div className="space-y-3">
              <ScoreRow label="Valuation"    score={valScore}    max={20} detail={m.trailing_pe > 0 ? `PE ${fmt(m.trailing_pe, 1)}×` : 'PE N/A'} />
              <ScoreRow label="Profitability" score={profScore}  max={20} detail={m.roe > 0 ? `ROE ${fmt(m.roe, 1)}%` : 'N/A'} />
              <ScoreRow label="Growth"       score={growthScore} max={15} detail={m.revenue_growth !== 0 ? `Rev ${fmt(m.revenue_growth, 1)}%` : 'N/A'} />
              <ScoreRow label="Fin. Health"  score={healthScore} max={15} detail={m.debt_to_equity > 0 ? `D/E ${fmt(m.debt_to_equity, 1)}` : 'Zero debt'} />
              <ScoreRow label="Returns"      score={returnScore} max={10} detail={`1Y ${fmt(m.return_1y_pct, 1)}%`} />
              <ScoreRow label="Size/Quality" score={sizeScore}   max={10} detail={m.beta > 0 ? `β ${fmt(m.beta, 2)}` : 'β N/A'} />
            </div>
          </Section>

          <Section title="Valuation">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              <Stat label="TTM PE"
                value={m.trailing_pe > 0 ? `${fmt(m.trailing_pe, 1)}×` : 'N/A'}
                sub={m.sector_pe ? `Sector ${m.sector_pe.toFixed(0)}×` : undefined}
                color={m.trailing_pe > 0 && m.sector_pe && m.trailing_pe < m.sector_pe ? 'text-emerald-400' : undefined} />
              <Stat label="PB Ratio"
                value={m.price_to_book > 0 ? `${fmt(m.price_to_book, 2)}×` : 'N/A'}
                sub={m.sector_pb ? `Sector ${m.sector_pb.toFixed(1)}×` : undefined}
                color={m.price_to_book > 0 && m.sector_pb && m.price_to_book < m.sector_pb ? 'text-emerald-400' : undefined} />
              <Stat label="Dividend Yield"
                value={m.dividend_yield > 0 ? `${fmt(m.dividend_yield, 2)}%` : 'N/A'}
                sub={m.sector_div_yield ? `Sector ${m.sector_div_yield.toFixed(1)}%` : undefined}
                color={m.dividend_yield > 0 && m.sector_div_yield && m.dividend_yield > m.sector_div_yield ? 'text-emerald-400' : undefined} />
              <Stat label="Forward PE"  value={m.forward_pe > 0 ? `${fmt(m.forward_pe, 1)}×` : 'N/A'} />
              <Stat label="PEG Ratio"   value={m.peg_ratio > 0 ? fmt(m.peg_ratio, 2) : 'N/A'}
                sub={m.peg_ratio > 0 ? (m.peg_ratio < 1 ? 'Undervalued' : 'Fair/Premium') : undefined}
                color={m.peg_ratio > 0 && m.peg_ratio < 1 ? 'text-emerald-400' : undefined} />
              {m.sector && <Stat label="Sector PE" value={m.sector_pe ? `${m.sector_pe.toFixed(0)}×` : '—'} sub="Benchmark" />}
            </div>
            {m.week_52_high > 0 && m.week_52_low > 0 && (
              <div className="mt-3 rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">52-Week Range</p>
                <div className="flex items-center gap-3">
                  <span className="text-xs text-red-400">₹{fmt(m.week_52_low, 0)}</span>
                  <div className="relative h-2 flex-1 overflow-hidden rounded-full bg-white/8">
                    <div className="absolute h-full rounded-full bg-gradient-to-r from-red-500/60 to-emerald-500/60" style={{ width: '100%' }} />
                  </div>
                  <span className="text-xs text-emerald-400">₹{fmt(m.week_52_high, 0)}</span>
                </div>
              </div>
            )}
          </Section>

          <Section title="Profitability">
            <div className="grid grid-cols-3 gap-3">
              <Stat label="ROE" value={m.roe !== 0 ? `${fmt(m.roe, 1)}%` : 'N/A'} sub="Return on equity" color={pctColor(m.roe, 20, 10)} />
              <Stat label="Net Margin" value={m.profit_margin !== 0 ? `${fmt(m.profit_margin, 1)}%` : 'N/A'} sub="Profit margin" color={pctColor(m.profit_margin, 15, 5)} />
              <Stat label="Op. Margin" value={m.operating_margin !== 0 ? `${fmt(m.operating_margin, 1)}%` : 'N/A'} sub="Operating margin" color={pctColor(m.operating_margin, 20, 10)} />
            </div>
          </Section>

          <Section title="Growth (YoY)">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Revenue Growth</p>
                <div className="mt-1 flex items-center gap-2">
                  {m.revenue_growth >= 0 ? <TrendingUp className="size-4 shrink-0 text-emerald-400" /> : <TrendingDown className="size-4 shrink-0 text-red-400" />}
                  <span className={cn('text-lg font-semibold tabular-nums', pctColor(m.revenue_growth, 15, 5))}>
                    {m.revenue_growth !== 0 ? `${m.revenue_growth > 0 ? '+' : ''}${fmt(m.revenue_growth, 1)}%` : 'N/A'}
                  </span>
                </div>
                <div className="mt-2"><GaugeBar value={Math.max(0, m.revenue_growth)} max={40} color={pctColor(m.revenue_growth, 15, 5).replace('text-', 'bg-')} /></div>
              </div>
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Earnings Growth</p>
                <div className="mt-1 flex items-center gap-2">
                  {m.earnings_growth >= 0 ? <TrendingUp className="size-4 shrink-0 text-emerald-400" /> : <TrendingDown className="size-4 shrink-0 text-red-400" />}
                  <span className={cn('text-lg font-semibold tabular-nums', pctColor(m.earnings_growth, 15, 5))}>
                    {m.earnings_growth !== 0 ? `${m.earnings_growth > 0 ? '+' : ''}${fmt(m.earnings_growth, 1)}%` : 'N/A'}
                  </span>
                </div>
                <div className="mt-2"><GaugeBar value={Math.max(0, m.earnings_growth)} max={40} color={pctColor(m.earnings_growth, 15, 5).replace('text-', 'bg-')} /></div>
              </div>
            </div>
          </Section>

          <Section title="Financial Health">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Debt / Equity</p>
                <p className={cn('mt-1 text-xl font-semibold tabular-nums', m.debt_to_equity <= 0 ? 'text-emerald-400' : m.debt_to_equity < 70 ? 'text-foreground' : 'text-red-400')}>
                  {m.debt_to_equity > 0 ? fmt(m.debt_to_equity, 1) : 'Zero debt'}
                </p>
                <p className="mt-0.5 text-[10px] text-muted-foreground">
                  {m.debt_to_equity <= 0 ? 'Debt-free' : m.debt_to_equity < 30 ? 'Conservative' : m.debt_to_equity < 70 ? 'Moderate' : m.debt_to_equity < 120 ? 'Elevated' : 'High leverage'}
                </p>
              </div>
              <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
                <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Current Ratio</p>
                <p className={cn('mt-1 text-xl font-semibold tabular-nums', m.current_ratio >= 2 ? 'text-emerald-400' : m.current_ratio >= 1 ? 'text-amber-400' : 'text-red-400')}>
                  {m.current_ratio > 0 ? fmt(m.current_ratio, 2) : 'N/A'}
                </p>
                <p className="mt-0.5 text-[10px] text-muted-foreground">
                  {m.current_ratio >= 2 ? 'Strong liquidity' : m.current_ratio >= 1.5 ? 'Healthy' : m.current_ratio >= 1 ? 'Adequate' : 'Tight liquidity'}
                </p>
              </div>
            </div>
          </Section>

          {(m.price_history?.length ?? 0) > 0 && (
            <Section title="Price History — 5 Years">
              <NavChart data={m.price_history!} color={verdict.hex} />
              <div className="mt-2 grid grid-cols-3 gap-2">
                <Stat label="1Y Return" value={`${m.return_1y_pct >= 0 ? '+' : ''}${fmt(m.return_1y_pct, 1)}%`}
                  color={m.return_1y_pct >= 0 ? 'text-emerald-400' : 'text-red-400'} />
                <Stat label="Max Drawdown 3Y" value={`−${fmt(m.max_drawdown_3y_pct, 1)}%`}
                  color={m.max_drawdown_3y_pct > 35 ? 'text-red-400' : m.max_drawdown_3y_pct > 20 ? 'text-amber-400' : undefined} />
                <Stat label="Beta" value={m.beta > 0 ? fmt(m.beta, 2) : 'N/A'}
                  sub={m.beta > 0 ? (m.beta < 0.8 ? 'Defensive' : m.beta < 1.1 ? 'Market-linked' : 'Aggressive') : undefined} />
              </div>
            </Section>
          )}

          {(m.drawdown_series?.length ?? 0) > 0 && (
            <Section title="Drawdown from Peak — 3 Years">
              <DrawdownChart data={m.drawdown_series!} />
            </Section>
          )}

          <section className="px-6 pb-6 pt-4">
            <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
              <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              <p className="text-[11px] leading-relaxed text-muted-foreground">
                Fundamental data from Yahoo Finance quoteSummary. Sector medians are approximate. Not financial advice.
              </p>
            </div>
          </section>
        </>
      )}

      {tab !== 'overview' && (
        finLoading ? (
          <div className="flex items-center gap-3 px-6 py-10 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin shrink-0" />
            Loading financial data from Yahoo Finance…
          </div>
        ) : finError ? (
          <div className="px-6 py-10 text-sm text-destructive">
            Failed to load: {(finError as Error).message}
          </div>
        ) : fin ? (
          <>
            {tab === 'income' && (
              <Section title="Income Statement (₹ Crores)">
                <FinancialTable
                  rows={fin.years.map(f => f.year)}
                  cols={[
                    { label: 'Revenue', values: fin.years.map(f => f.revenue_cr) },
                    { label: 'EBITDA', values: fin.years.map(f => f.ebitda_cr ?? 0) },
                    { label: 'Net Income', values: fin.years.map(f => f.net_income_cr) },
                    { label: 'Net Margin', values: fin.years.map(f => f.revenue_cr > 0 && f.net_income_cr ? `${((f.net_income_cr / f.revenue_cr) * 100).toFixed(1)}%` : '—') },
                  ]}
                />
              </Section>
            )}
            {tab === 'balance' && (
              <Section title="Balance Sheet (₹ Crores)">
                <FinancialTable
                  rows={fin.years.map(f => f.year)}
                  cols={[
                    { label: 'Total Equity', values: fin.years.map(f => f.equity_cr ?? 0) },
                    { label: 'Total Debt', values: fin.years.map(f => f.total_debt_cr ?? 0) },
                    { label: 'Cash', values: fin.years.map(f => f.cash_cr ?? 0) },
                    { label: 'Net Debt', values: fin.years.map(f => (f.total_debt_cr ?? 0) - (f.cash_cr ?? 0)) },
                  ]}
                />
              </Section>
            )}
            {tab === 'cashflow' && (
              <Section title="Cash Flow (₹ Crores)">
                <FinancialTable
                  rows={fin.years.map(f => f.year)}
                  cols={[
                    { label: 'Operating CF', values: fin.years.map(f => f.op_cf_cr ?? 0) },
                    { label: 'CapEx', values: fin.years.map(f => f.capex_cr ?? 0) },
                    { label: 'Free Cash Flow', values: fin.years.map(f => f.free_cf_cr ?? 0) },
                  ]}
                />
              </Section>
            )}
            {tab === 'shareholding' && (
              <Section title="Shareholding Pattern">
                <div className="space-y-3">
                  <ShareholdingBar label="Promoter" pct={fin.promoter_pct} color="bg-blue-500" />
                  <ShareholdingBar label="FII" pct={fin.fii_pct} color="bg-emerald-500" />
                  <ShareholdingBar label="DII" pct={fin.dii_pct} color="bg-amber-500" />
                  <ShareholdingBar label="Public" pct={fin.public_pct} color="bg-purple-500" />
                </div>
                <p className="mt-3 text-[10px] text-muted-foreground">
                  Source: Yahoo Finance (insiders = promoters; institutions = FII+DII combined).
                </p>
              </Section>
            )}
          </>
        ) : null
      )}
    </>
  );
}

// ─── Sector median PE (mirrors Go sectorPE map) ───────────────────────────────

function sectorMedianPE(sector: string): number {
  const map: Record<string, number> = {
    'Technology': 28, 'Financial Services': 14, 'Healthcare': 30,
    'Consumer Defensive': 45, 'Consumer Cyclical': 25, 'Communication Services': 22,
    'Industrials': 22, 'Energy': 12, 'Basic Materials': 15, 'Real Estate': 30, 'Utilities': 20,
  };
  return map[sector] ?? 22;
}

// ─── Main panel ───────────────────────────────────────────────────────────────

export function InstrumentAnalysisPanel({ instrumentId, instrumentName, onClose }: Props) {
  const qc = useQueryClient();
  const [showSlugEditor, setShowSlugEditor] = useState(false);
  const [slugInput, setSlugInput] = useState('');

  const { data, isLoading, error } = useQuery({
    queryKey: ['instrument-metrics', instrumentId],
    queryFn: () => signalApi.getInstrumentMetrics(instrumentId),
    staleTime: 5 * 60_000,
    retry: 1,
  });

  // Refresh bypasses the server-side cache (?force=true) and re-fetches live
  // from AMFI / Yahoo Finance. Uses a mutation so isPending is reliable.
  const refreshMutation = useMutation({
    mutationFn: () => signalApi.getInstrumentMetrics(instrumentId, true),
    onSuccess: (fresh) => {
      qc.setQueryData(['instrument-metrics', instrumentId], fresh);
    },
  });

  // Save manually entered Groww slug then force-refresh.
  const saveSlugMutation = useMutation({
    mutationFn: (slug: string) => signalApi.saveGrowwSlug(instrumentId, slug),
    onSuccess: () => {
      setShowSlugEditor(false);
      refreshMutation.mutate();
    },
  });

  const isRefreshing = refreshMutation.isPending;

  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [onClose]);

  const kindLabel = data?.kind === 'stock' ? 'Stock Analysis' : data?.kind === 'etf' ? 'ETF / Fund Analysis' : 'Full Analysis';

  return (
    <div className="fixed inset-0 z-50 flex">
      <div className="flex w-full flex-col bg-background">
        {/* Header */}
        <div className="flex shrink-0 items-start justify-between border-b border-border px-6 py-4">
          <div className="min-w-0 flex-1 pr-4">
            <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              {kindLabel}
            </p>
            <h2 className="mt-1 text-base font-semibold leading-snug">{instrumentName}</h2>
            {data?.kind === 'stock' && (
              <div className="mt-2 flex items-center gap-2 flex-wrap">
                <span className="rounded border border-emerald-500/30 bg-emerald-500/8 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                  STOCK
                </span>
                <span className="text-xs text-muted-foreground">{data.metrics.sector}</span>
                {data.metrics.zero1_score > 0 && (
                  <span className={cn('text-xs font-semibold', scoreVerdict(data.metrics.zero1_score).color)}>
                    {data.metrics.zero1_score}/90 · {scoreVerdict(data.metrics.zero1_score).label}
                  </span>
                )}
                {data.computed_at && !isRefreshing && (
                  <span className="text-[10px] text-muted-foreground/50">
                    · Updated {new Date(data.computed_at).toLocaleString('en-IN', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' })}
                  </span>
                )}
              </div>
            )}
            {(data?.kind === 'mf' || data?.kind === 'etf') && (
              <div className="mt-2 flex items-center gap-2 flex-wrap">
                <span className="rounded border border-blue-500/30 bg-blue-500/8 px-2 py-0.5 text-[10px] font-medium text-blue-400">
                  {data.kind === 'etf' ? 'ETF' : data.metrics.category}
                </span>
                <span className={cn('text-xs font-semibold', scoreVerdict(data.metrics.zero1_score).color)}>
                  {data.metrics.zero1_score}/90 · {scoreVerdict(data.metrics.zero1_score).label}
                </span>
                {data.computed_at && !isRefreshing && (
                  <span className="text-[10px] text-muted-foreground/50">
                    · Updated {new Date(data.computed_at).toLocaleString('en-IN', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' })}
                  </span>
                )}
              </div>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <button onClick={() => refreshMutation.mutate()} disabled={isRefreshing}
              title={isRefreshing ? 'Fetching live data…' : 'Refresh data'}
              className="grid h-8 w-8 place-items-center rounded-lg border border-border text-muted-foreground hover:border-primary/40 hover:text-primary transition-colors disabled:opacity-40">
              <RefreshCw className={cn('size-3.5', isRefreshing && 'animate-spin')} />
            </button>
            <button onClick={onClose}
              className="grid h-8 w-8 place-items-center rounded-lg border border-border text-muted-foreground hover:border-foreground/30 hover:text-foreground transition-colors">
              <X className="size-4" />
            </button>
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto">
          <div className="mx-auto w-[80vw]">
            {isLoading && <LoadingSkeleton />}

            {isRefreshing && (
              <div className="flex items-center gap-3 rounded-lg border border-primary/20 bg-primary/5 mx-4 mt-4 px-4 py-3 text-sm text-primary">
                <RefreshCw className="size-4 animate-spin shrink-0" />
                <span>Fetching live data from {data?.kind === 'mf' ? 'AMFI' : 'Yahoo Finance'} — this takes 10–20 seconds…</span>
              </div>
            )}

            {refreshMutation.isError && (
              <div className="flex items-center gap-3 rounded-lg border border-destructive/30 bg-destructive/5 mx-4 mt-4 px-4 py-3 text-sm text-destructive">
                <AlertTriangle className="size-4 shrink-0" />
                <span>{refreshMutation.error instanceof Error ? refreshMutation.error.message : 'Refresh failed'}</span>
              </div>
            )}

            {error && (
              <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
                <AlertTriangle className="size-8 text-amber-400" />
                <p className="text-sm font-medium">Failed to load metrics</p>
                <p className="text-xs text-muted-foreground">{(error as Error).message}</p>
              </div>
            )}

            {data?.kind === 'mf' && <MFETFBody m={data.metrics} isETF={false} />}
            {data?.kind === 'etf' && <ETFBody m={data.metrics as ETFMetrics} />}
            {data?.kind === 'stock' && <StockBody m={data.metrics} instrumentId={instrumentId} />}

            {/* Data sources footer */}
            {data && (
              <div className="border-t border-border/30 px-6 py-4 mt-2 space-y-2">
                {/* Groww live data status */}
                {data.kind === 'mf' && !data.metrics.groww_data_ok && (
                  <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2 space-y-2">
                    <div className="flex items-start gap-2">
                      <AlertTriangle className="size-3.5 text-amber-500 shrink-0 mt-0.5" />
                      <p className="text-[11px] text-amber-600 dark:text-amber-400 flex-1">
                        Groww URL not found — AUM, TER, Beta, Alpha, Sortino &amp; ranks use hardcoded fallback. Set the correct Groww URL to get live data.
                      </p>
                    </div>
                    {showSlugEditor ? (
                      <div className="flex gap-2 items-center">
                        <input
                          className="flex-1 rounded border border-white/15 bg-white/5 px-2 py-1 text-[11px] text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:border-amber-400/50"
                          placeholder="Paste Groww URL or slug — e.g. quant-small-cap-fund-direct-growth"
                          value={slugInput}
                          onChange={e => setSlugInput(e.target.value)}
                          onKeyDown={e => { if (e.key === 'Enter' && slugInput.trim()) saveSlugMutation.mutate(slugInput.trim()); if (e.key === 'Escape') setShowSlugEditor(false); }}
                          autoFocus
                        />
                        <button
                          onClick={() => slugInput.trim() && saveSlugMutation.mutate(slugInput.trim())}
                          disabled={!slugInput.trim() || saveSlugMutation.isPending}
                          className="rounded bg-amber-500/20 border border-amber-500/30 px-2 py-1 text-[11px] text-amber-400 hover:bg-amber-500/30 disabled:opacity-50"
                        >
                          {saveSlugMutation.isPending ? 'Saving…' : 'Save & Refresh'}
                        </button>
                        <button onClick={() => setShowSlugEditor(false)} className="text-[11px] text-muted-foreground hover:text-foreground">Cancel</button>
                      </div>
                    ) : (
                      <button
                        onClick={() => { setSlugInput(''); setShowSlugEditor(true); }}
                        className="flex items-center gap-1.5 text-[11px] text-amber-500 hover:text-amber-400"
                      >
                        <Pencil className="size-3" /> Set Groww URL manually
                      </button>
                    )}
                    {saveSlugMutation.isError && (
                      <p className="text-[10px] text-red-400">{String(saveSlugMutation.error)}</p>
                    )}
                  </div>
                )}
                {data.kind === 'mf' && data.metrics.groww_data_ok && (
                  <div className="flex items-center justify-between text-[11px] text-emerald-600 dark:text-emerald-400">
                    <div className="flex items-center gap-2">
                      <CheckCircle2 className="size-3.5 shrink-0" />
                      Live data from Groww — AUM, TER, Beta, Alpha, Sortino, category rank &amp; rating
                    </div>
                    <button
                      onClick={() => { setSlugInput(data.metrics.groww_slug ?? ''); setShowSlugEditor(true); }}
                      className="flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground ml-3"
                      title="Change Groww URL"
                    >
                      <Pencil className="size-3" /> Edit
                    </button>
                  </div>
                )}
                {/* Slug editor also accessible when data_ok=true */}
                {data.kind === 'mf' && data.metrics.groww_data_ok && showSlugEditor && (
                  <div className="flex gap-2 items-center">
                    <input
                      className="flex-1 rounded border border-white/15 bg-white/5 px-2 py-1 text-[11px] text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:border-emerald-400/50"
                      placeholder="Paste Groww URL or slug"
                      value={slugInput}
                      onChange={e => setSlugInput(e.target.value)}
                      onKeyDown={e => { if (e.key === 'Enter' && slugInput.trim()) saveSlugMutation.mutate(slugInput.trim()); if (e.key === 'Escape') setShowSlugEditor(false); }}
                      autoFocus
                    />
                    <button
                      onClick={() => slugInput.trim() && saveSlugMutation.mutate(slugInput.trim())}
                      disabled={!slugInput.trim() || saveSlugMutation.isPending}
                      className="rounded bg-white/8 border border-white/15 px-2 py-1 text-[11px] text-foreground hover:bg-white/12 disabled:opacity-50"
                    >
                      {saveSlugMutation.isPending ? 'Saving…' : 'Save & Refresh'}
                    </button>
                    <button onClick={() => setShowSlugEditor(false)} className="text-[11px] text-muted-foreground hover:text-foreground">Cancel</button>
                  </div>
                )}
                {data?.sources && Object.keys(data.sources).length > 0 && (
                  <div>
                    <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground/60 mb-2">
                      Data Sources
                    </p>
                    <div className="space-y-1">
                      {Object.entries(data.sources).map(([label, href]) => (
                        <div key={label} className="flex items-start gap-1.5">
                          <span className={cn(
                            'mt-0.5 size-1.5 rounded-full shrink-0',
                            href ? 'bg-emerald-500' : 'bg-amber-400'
                          )} />
                          {href ? (
                            <a
                              href={href}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="text-[11px] text-muted-foreground hover:text-primary transition-colors underline underline-offset-2 leading-snug"
                            >
                              {label} ↗
                            </a>
                          ) : (
                            <span className="text-[11px] text-amber-600 dark:text-amber-400 leading-snug">
                              {label}
                            </span>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
