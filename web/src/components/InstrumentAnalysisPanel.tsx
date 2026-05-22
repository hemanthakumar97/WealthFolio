import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  AreaChart, Area, BarChart, Bar, XAxis, YAxis,
  Tooltip, ResponsiveContainer, ReferenceLine, Cell,
} from 'recharts';
import {
  X, AlertTriangle, CheckCircle2, Info, RefreshCw,
  TrendingUp, TrendingDown, DollarSign,
} from 'lucide-react';
import { signalApi, type MFMetrics, type StockMetrics } from '@/lib/api';
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

function deriveScores(m: MFMetrics) {
  const roll = (() => {
    let s = 0;
    // Avg 3Y CAGR: 15 pts
    s += m.rolling_3y_avg_pct >= 28 ? 15 : m.rolling_3y_avg_pct >= 22 ? 11 : m.rolling_3y_avg_pct >= 16 ? 7 : m.rolling_3y_avg_pct >= 10 ? 4 : 1;
    // Consistency: 10 pts
    s += m.consistency_1y_pct >= 95 ? 10 : m.consistency_1y_pct >= 80 ? 7 : m.consistency_1y_pct >= 70 ? 5 : m.consistency_1y_pct >= 60 ? 3 : 1;
    // Worst 3Y: 5 pts
    s += m.rolling_3y_min_pct > 5 ? 5 : m.rolling_3y_min_pct > 0 ? 4 : m.rolling_3y_min_pct > -5 ? 2 : m.rolling_3y_min_pct > -10 ? 1 : 0;
    return s;
  })();
  const sharpe = m.sharpe_1y >= 1 ? 15 : m.sharpe_1y >= 0.5 ? 12 : m.sharpe_1y >= 0 ? 8 : m.sharpe_1y >= -0.3 ? 5 : m.sharpe_1y >= -0.7 ? 3 : 0;
  const bench  = catBench(m.category);
  const diff   = m.std_dev_1y_pct - bench;
  const stddev = diff <= -3 ? 15 : diff <= 0 ? 12 : diff <= 2 ? 9 : diff <= 4 ? 5 : 2;
  const beta   = m.beta <= 0 ? 0 : m.beta <= 0.75 ? 10 : m.beta <= 0.85 ? 8 : m.beta <= 0.95 ? 6 : m.beta <= 1.05 ? 4 : 2;
  let aum = 0;
  if (m.aum_cr > 0) {
    if (m.category === 'Small Cap') aum = m.aum_cr < 5000 ? 5 : m.aum_cr < 20000 ? 10 : m.aum_cr < 40000 ? 7 : 3;
    else if (m.category === 'Mid Cap') aum = m.aum_cr < 2000 ? 5 : m.aum_cr < 30000 ? 10 : m.aum_cr < 60000 ? 7 : 4;
    else aum = m.aum_cr < 500 ? 4 : m.aum_cr < 80000 ? 10 : 8;
  }
  const ter = m.ter_pct <= 0 ? 0 : m.ter_pct < 0.5 ? 10 : m.ter_pct < 0.7 ? 8 : m.ter_pct < 1 ? 5 : m.ter_pct < 1.5 ? 3 : 1;
  return { roll, sharpe, stddev, beta, aum, ter };
}

function MFETFBody({ m, isETF }: { m: MFMetrics; isETF: boolean }) {
  const verdict = scoreVerdict(m.zero1_score);
  const scores  = deriveScores(m);
  const bench   = catBench(m.category);

  return (
    <>
      <OverallScore score={m.zero1_score} verdict={verdict} gaps={m.data_gaps} availableMax={m.available_max} />

      <Section title="Score Breakdown">
        <div className="space-y-3">
          <ScoreRow label="Rolling Returns" score={scores.roll}   max={30} detail={`Avg 3Y ${fmt(m.rolling_3y_avg_pct)}%`} />
          <ScoreRow label="Sharpe Ratio"    score={scores.sharpe} max={15} detail={fmt(m.sharpe_1y, 3)} />
          <ScoreRow label="Std Deviation"   score={scores.stddev} max={15} detail={`${fmt(m.std_dev_1y_pct)}% ann.`} />
          <ScoreRow label="Beta"            score={scores.beta}   max={10} detail={m.beta ? fmt(m.beta, 2) : 'Unknown'} />
          <ScoreRow label="AUM"             score={scores.aum}    max={10} detail={m.aum_cr ? `₹${(m.aum_cr/1000).toFixed(0)}K Cr` : 'Unknown'} />
          <ScoreRow label={isETF ? 'Expense Ratio' : 'TER'} score={scores.ter} max={10} detail={m.ter_pct ? `${m.ter_pct}%` : 'Unknown'} />
        </div>
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
          <Stat label="Sharpe Ratio (1Y)" value={fmt(m.sharpe_1y, 3)}
            sub={m.sharpe_1y >= 0.5 ? 'Good' : m.sharpe_1y >= 0 ? 'Acceptable' : 'Market correction'}
            color={m.sharpe_1y >= 0.5 ? 'text-emerald-400' : undefined} />
          <Stat label="Max Drawdown (3Y)" value={`−${fmt(m.max_drawdown_3y_pct)}%`} sub="Peak to trough"
            color={m.max_drawdown_3y_pct > 30 ? 'text-red-400' : m.max_drawdown_3y_pct > 20 ? 'text-amber-400' : undefined} />
        </div>

        <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3 mb-2">
          <div className="flex items-center justify-between mb-2">
            <p className="text-xs text-muted-foreground">Std Dev (1Y ann.) vs {m.category} avg</p>
            <div className="flex items-center gap-1.5">
              {m.std_dev_1y_pct < bench
                ? <CheckCircle2 className="size-3.5 text-emerald-400" />
                : <AlertTriangle className="size-3.5 text-amber-400" />}
              <span className="text-sm font-semibold tabular-nums">
                {fmt(m.std_dev_1y_pct)}%<span className="text-xs text-muted-foreground"> / {bench}%</span>
              </span>
            </div>
          </div>
          <div className="relative h-2 overflow-hidden rounded-full bg-white/8">
            <div className="absolute h-full rounded-full bg-blue-500/60" style={{ width: `${Math.min(100,(m.std_dev_1y_pct/30)*100)}%` }} />
            <div className="absolute top-0 h-full w-px bg-amber-400/80" style={{ left: `${Math.min(100,(bench/30)*100)}%` }} />
          </div>
          <div className="mt-1 flex justify-between text-[10px] text-muted-foreground/60">
            <span>Fund: {fmt(m.std_dev_1y_pct)}%</span>
            <span className="text-amber-400/70">▲ Avg: {bench}%</span>
          </div>
        </div>

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
        <div className="grid grid-cols-2 gap-3">
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
      </Section>

      <section className="px-6 pb-6 pt-4">
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
          <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            {isETF
              ? 'Score computed using Zero1 6-metric framework adapted for ETFs. Price data from Yahoo Finance via our prices table. AUM/TER from Yahoo quoteSummary.'
              : 'Scores computed using the Zero1 by Zerodha 6-metric framework. NAV data from mfapi.in. AUM and TER from AMFI public disclosures (updated quarterly).'}
            {' '}Not financial advice.
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

function StockBody({ m }: { m: StockMetrics }) {
  const verdict = scoreVerdict(m.zero1_score);
  // (score row derivation below uses m directly via inline calcs)

  // Derive per-bucket scores for breakdown rows (mirrors Go computeStockScore logic)
  const medianPE = sectorMedianPE(m.sector);
  const peRatio  = m.trailing_pe > 0 ? m.trailing_pe / medianPE : -1;
  const valScore = (() => {
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

  return (
    <>
      <OverallScore score={m.zero1_score} verdict={verdict} gaps={m.data_gaps} availableMax={m.available_max} />

      {/* Meta info */}
      {(m.sector || m.industry) && (
        <section className="border-t border-border px-6 py-3">
          <div className="flex items-center gap-3 flex-wrap">
            {m.sector && (
              <span className="rounded border border-blue-500/30 bg-blue-500/8 px-2 py-0.5 text-[10px] font-medium text-blue-400">
                {m.sector}
              </span>
            )}
            {m.industry && (
              <span className="text-xs text-muted-foreground">{m.industry}</span>
            )}
            {m.market_cap_cr > 0 && (
              <span className="text-xs text-muted-foreground">
                Market cap: <span className="text-foreground">₹{(m.market_cap_cr/1000).toFixed(1)}K Cr</span>
              </span>
            )}
          </div>
        </section>
      )}

      <Section title="Score Breakdown">
        <div className="space-y-3">
          <ScoreRow label="Valuation"   score={valScore}    max={20} detail={m.trailing_pe > 0 ? `PE ${fmt(m.trailing_pe, 1)}×` : 'PE N/A'} />
          <ScoreRow label="Profitability" score={profScore} max={20} detail={m.roe > 0 ? `ROE ${fmt(m.roe, 1)}%` : 'N/A'} />
          <ScoreRow label="Growth"      score={growthScore} max={15} detail={m.revenue_growth !== 0 ? `Rev ${fmt(m.revenue_growth, 1)}%` : 'N/A'} />
          <ScoreRow label="Fin. Health" score={healthScore} max={15} detail={m.debt_to_equity > 0 ? `D/E ${fmt(m.debt_to_equity, 1)}` : 'Zero debt'} />
          <ScoreRow label="Returns"     score={returnScore} max={10} detail={`1Y ${fmt(m.return_1y_pct, 1)}%`} />
          <ScoreRow label="Size/Quality" score={sizeScore}  max={10} detail={m.beta > 0 ? `β ${fmt(m.beta, 2)}` : 'β N/A'} />
        </div>
      </Section>

      {/* Valuation detail */}
      <Section title="Valuation">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="Trailing PE" value={m.trailing_pe > 0 ? `${fmt(m.trailing_pe, 1)}×` : 'N/A'}
            sub={m.sector ? `vs ${sectorMedianPE(m.sector)}× sector` : undefined}
            color={m.trailing_pe > 0 && m.trailing_pe < sectorMedianPE(m.sector) ? 'text-emerald-400' : undefined} />
          <Stat label="Forward PE"  value={m.forward_pe > 0 ? `${fmt(m.forward_pe, 1)}×` : 'N/A'} />
          <Stat label="Price/Book"  value={m.price_to_book > 0 ? `${fmt(m.price_to_book, 2)}×` : 'N/A'}
            color={m.price_to_book > 0 && m.price_to_book < 3 ? 'text-emerald-400' : undefined} />
          <Stat label="PEG Ratio"   value={m.peg_ratio > 0 ? fmt(m.peg_ratio, 2) : 'N/A'}
            sub={m.peg_ratio > 0 ? (m.peg_ratio < 1 ? 'Undervalued' : 'Fair/Premium') : undefined}
            color={m.peg_ratio > 0 && m.peg_ratio < 1 ? 'text-emerald-400' : undefined} />
        </div>

        {/* 52-week range */}
        {m.week_52_high > 0 && m.week_52_low > 0 && (
          <div className="mt-3 rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">52-Week Range</p>
            <div className="flex items-center gap-3">
              <span className="text-xs text-red-400">₹{fmt(m.week_52_low, 0)}</span>
              <div className="relative h-2 flex-1 overflow-hidden rounded-full bg-white/8">
                <div className="absolute h-full rounded-full bg-gradient-to-r from-red-500/60 to-emerald-500/60"
                  style={{ width: '100%' }} />
              </div>
              <span className="text-xs text-emerald-400">₹{fmt(m.week_52_high, 0)}</span>
            </div>
          </div>
        )}
      </Section>

      {/* Profitability */}
      <Section title="Profitability">
        <div className="grid grid-cols-3 gap-3">
          <Stat label="ROE" value={m.roe !== 0 ? `${fmt(m.roe, 1)}%` : 'N/A'}
            sub="Return on equity" color={pctColor(m.roe, 20, 10)} />
          <Stat label="Net Margin" value={m.profit_margin !== 0 ? `${fmt(m.profit_margin, 1)}%` : 'N/A'}
            sub="Profit margin" color={pctColor(m.profit_margin, 15, 5)} />
          <Stat label="Op. Margin" value={m.operating_margin !== 0 ? `${fmt(m.operating_margin, 1)}%` : 'N/A'}
            sub="Operating margin" color={pctColor(m.operating_margin, 20, 10)} />
        </div>
      </Section>

      {/* Growth */}
      <Section title="Growth (YoY)">
        <div className="grid grid-cols-2 gap-3">
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Revenue Growth</p>
            <div className="mt-1 flex items-center gap-2">
              {m.revenue_growth >= 0
                ? <TrendingUp className="size-4 shrink-0 text-emerald-400" />
                : <TrendingDown className="size-4 shrink-0 text-red-400" />}
              <span className={cn('text-lg font-semibold tabular-nums', pctColor(m.revenue_growth, 15, 5))}>
                {m.revenue_growth !== 0 ? `${m.revenue_growth > 0 ? '+' : ''}${fmt(m.revenue_growth, 1)}%` : 'N/A'}
              </span>
            </div>
            <div className="mt-2 flex items-center gap-2">
              <GaugeBar value={Math.max(0, m.revenue_growth)} max={40} color={pctColor(m.revenue_growth, 15, 5).replace('text-', 'bg-')} />
            </div>
          </div>
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Earnings Growth</p>
            <div className="mt-1 flex items-center gap-2">
              {m.earnings_growth >= 0
                ? <TrendingUp className="size-4 shrink-0 text-emerald-400" />
                : <TrendingDown className="size-4 shrink-0 text-red-400" />}
              <span className={cn('text-lg font-semibold tabular-nums', pctColor(m.earnings_growth, 15, 5))}>
                {m.earnings_growth !== 0 ? `${m.earnings_growth > 0 ? '+' : ''}${fmt(m.earnings_growth, 1)}%` : 'N/A'}
              </span>
            </div>
            <div className="mt-2 flex items-center gap-2">
              <GaugeBar value={Math.max(0, m.earnings_growth)} max={40} color={pctColor(m.earnings_growth, 15, 5).replace('text-', 'bg-')} />
            </div>
          </div>
        </div>
      </Section>

      {/* Financial health */}
      <Section title="Financial Health">
        <div className="grid grid-cols-2 gap-3">
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Debt / Equity</p>
            <p className={cn('mt-1 text-xl font-semibold tabular-nums',
              m.debt_to_equity <= 0 ? 'text-emerald-400' : m.debt_to_equity < 70 ? 'text-foreground' : 'text-red-400')}>
              {m.debt_to_equity > 0 ? fmt(m.debt_to_equity, 1) : 'Zero debt'}
            </p>
            <p className="mt-0.5 text-[10px] text-muted-foreground">
              {m.debt_to_equity <= 0 ? 'Debt-free' : m.debt_to_equity < 30 ? 'Conservative' : m.debt_to_equity < 70 ? 'Moderate' : m.debt_to_equity < 120 ? 'Elevated' : 'High leverage'}
            </p>
          </div>
          <div className="rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Current Ratio</p>
            <p className={cn('mt-1 text-xl font-semibold tabular-nums',
              m.current_ratio >= 2 ? 'text-emerald-400' : m.current_ratio >= 1 ? 'text-amber-400' : 'text-red-400')}>
              {m.current_ratio > 0 ? fmt(m.current_ratio, 2) : 'N/A'}
            </p>
            <p className="mt-0.5 text-[10px] text-muted-foreground">
              {m.current_ratio >= 2 ? 'Strong liquidity' : m.current_ratio >= 1.5 ? 'Healthy' : m.current_ratio >= 1 ? 'Adequate' : 'Tight liquidity'}
            </p>
          </div>
        </div>
      </Section>

      {/* Price chart */}
      {(m.price_history?.length ?? 0) > 0 && (
        <Section title="Price History — 5 Years">
          <NavChart data={m.price_history} color={verdict.hex} />
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

      {/* Drawdown chart */}
      {(m.drawdown_series?.length ?? 0) > 0 && (
        <Section title="Drawdown from Peak — 3 Years">
          <div className="mb-3 flex items-center justify-between">
            <p className="text-xs text-muted-foreground">How far the stock fell from its previous high at each point.</p>
            <span className="text-sm font-semibold tabular-nums text-red-400">−{fmt(m.max_drawdown_3y_pct)}% max</span>
          </div>
          <DrawdownChart data={m.drawdown_series} />
        </Section>
      )}

      {/* Dividend */}
      {m.dividend_yield > 0 && (
        <Section title="Income">
          <div className="flex items-center gap-4 rounded-lg border border-white/8 bg-white/[0.03] px-4 py-3">
            <DollarSign className="size-5 text-emerald-400 shrink-0" />
            <div>
              <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Dividend Yield</p>
              <p className="text-xl font-semibold text-emerald-400">{fmt(m.dividend_yield, 2)}%</p>
            </div>
          </div>
        </Section>
      )}

      <section className="px-6 pb-6 pt-4">
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5">
          <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            Fundamental data from Yahoo Finance quoteSummary. Price history from our local prices table.
            Sector median PE is approximate. Not financial advice.
          </p>
        </div>
      </section>
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
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['instrument-metrics', instrumentId],
    queryFn: () => signalApi.getInstrumentMetrics(instrumentId),
    staleTime: 5 * 60_000,
    retry: 1,
  });

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
              <div className="mt-2 flex items-center gap-2">
                <span className="rounded border border-emerald-500/30 bg-emerald-500/8 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                  STOCK
                </span>
                <span className="text-xs text-muted-foreground">{data.metrics.sector}</span>
                {data.metrics.zero1_score > 0 && (
                  <span className={cn('text-xs font-semibold', scoreVerdict(data.metrics.zero1_score).color)}>
                    {data.metrics.zero1_score}/90 · {scoreVerdict(data.metrics.zero1_score).label}
                  </span>
                )}
              </div>
            )}
            {(data?.kind === 'mf' || data?.kind === 'etf') && (
              <div className="mt-2 flex items-center gap-2">
                <span className="rounded border border-blue-500/30 bg-blue-500/8 px-2 py-0.5 text-[10px] font-medium text-blue-400">
                  {data.kind === 'etf' ? 'ETF' : data.metrics.category}
                </span>
                <span className={cn('text-xs font-semibold', scoreVerdict(data.metrics.zero1_score).color)}>
                  {data.metrics.zero1_score}/90 · {scoreVerdict(data.metrics.zero1_score).label}
                </span>
              </div>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <button onClick={() => refetch()} disabled={isFetching}
              title="Refresh data"
              className="grid h-8 w-8 place-items-center rounded-lg border border-border text-muted-foreground hover:border-primary/40 hover:text-primary transition-colors disabled:opacity-40">
              <RefreshCw className={cn('size-3.5', isFetching && 'animate-spin')} />
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

            {error && (
              <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
                <AlertTriangle className="size-8 text-amber-400" />
                <p className="text-sm font-medium">Failed to load metrics</p>
                <p className="text-xs text-muted-foreground">{(error as Error).message}</p>
              </div>
            )}

            {data?.kind === 'mf' && <MFETFBody m={data.metrics} isETF={false} />}
            {data?.kind === 'etf' && <MFETFBody m={data.metrics} isETF={true} />}
            {data?.kind === 'stock' && <StockBody m={data.metrics} />}
          </div>
        </div>
      </div>
    </div>
  );
}
