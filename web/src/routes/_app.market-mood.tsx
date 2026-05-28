import { useState, useEffect } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
  CartesianGrid,
} from 'recharts';
import {
  RefreshCw,
  TrendingUp,
  TrendingDown,
  Minus,
  Plus,
  ChevronDown,
  ChevronRight,
  AlertTriangle,
  Info,
  CheckCircle,
  XCircle,
  Trash2,
} from 'lucide-react';

import {
  marketApi,
  type MarketMoodResponse,
  type IndexConfigItem,
  type CachedMMI,
  type CachedMetals,
  type MetalsData,
  type PEDataPoint,
  type WatchlistItem,
} from '@/lib/api';
import { formatINR } from '@/lib/format';
import { formatDate } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_app/market-mood')({
  component: MarketMoodPage,
});

// --- Mood chip ---
function MoodChip({ mood }: { mood: 'Green' | 'Yellow' | 'Red' }) {
  const map = {
    Green: {
      label: 'Undervalued',
      cls: 'bg-emerald-500/10 text-emerald-600 border-emerald-500/30',
    },
    Yellow: { label: 'Fair Value', cls: 'bg-amber-500/10 text-amber-600 border-amber-500/30' },
    Red: { label: 'Overvalued', cls: 'bg-rose-500/10 text-rose-600 border-rose-500/30' },
  };
  const { label, cls } = map[mood] ?? map['Yellow'];
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold',
        cls,
      )}
    >
      {mood === 'Green' ? (
        <TrendingDown className="mr-1 size-3" />
      ) : mood === 'Red' ? (
        <TrendingUp className="mr-1 size-3" />
      ) : (
        <Minus className="mr-1 size-3" />
      )}
      {label}
    </span>
  );
}

// --- MMI Gauge ---
const MMI_SEGMENTS = [
  { name: 'Extreme Fear', start: 0,  end: 30,  color: '#10B981' },
  { name: 'Fear',         start: 30, end: 50,  color: '#84CC16' },
  { name: 'Greed',        start: 50, end: 70,  color: '#EAB308' },
  { name: 'Extreme Greed',start: 70, end: 100, color: '#EF4444' },
];

function mmiLabelColor(v: number) {
  if (v < 30) return '#10B981';
  if (v < 50) return '#84CC16';
  if (v < 70) return '#EAB308';
  return '#EF4444';
}

function calcArc(startVal: number, endVal: number, r = 90, cx = 100, cy = 100) {
  const valToDeg = (v: number) => 180 - (v / 100) * 180;
  const toRad = (d: number) => (d * Math.PI) / 180;
  const s = toRad(valToDeg(startVal));
  const e = toRad(valToDeg(endVal));
  const x1 = cx + r * Math.cos(s), y1 = cy - r * Math.sin(s);
  const x2 = cx + r * Math.cos(e), y2 = cy - r * Math.sin(e);
  return `M ${x1} ${y1} A ${r} ${r} 0 0 1 ${x2} ${y2}`;
}

function MMIGauge({ value, mood }: { value: number; mood: string }) {
  const [animated, setAnimated] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setAnimated(true), 120);
    return () => clearTimeout(t);
  }, []);

  const v = Math.max(0, Math.min(100, value));
  const display = animated ? v : 0;
  // -90 = left (0), 0 = up (50), +90 = right (100)
  const needleDeg = display * 1.8 - 90;
  const color = mmiLabelColor(v);

  return (
    <div className="flex flex-col items-center w-full">
      <svg viewBox="0 0 200 108" className="w-full max-w-xs overflow-visible">
        {/* Background track */}
        <path d={calcArc(0, 100)} fill="none" stroke="hsl(var(--muted))" strokeWidth="14" strokeLinecap="round" />
        {/* Coloured segments */}
        {MMI_SEGMENTS.map((seg) => (
          <path
            key={seg.name}
            d={calcArc(seg.start + 0.6, seg.end - 0.6)}
            fill="none"
            stroke={seg.color}
            strokeWidth="14"
            strokeLinecap="butt"
            style={{
              opacity: animated ? (v >= seg.start && v <= seg.end ? 1 : 0.18) : 0.18,
              transition: 'opacity 0.6s ease',
            }}
          />
        ))}
        {/* Needle */}
        <g
          style={{
            transform: `rotate(${needleDeg}deg)`,
            transformOrigin: '100px 100px',
            transition: animated ? 'transform 1.1s cubic-bezier(0.34,1.56,0.64,1)' : 'none',
          }}
        >
          {/* Shadow line for depth */}
          <line x1={100} y1={103} x2={100} y2={20}
            stroke="rgba(0,0,0,0.4)" strokeWidth="4" strokeLinecap="round" />
          {/* Main needle */}
          <line x1={100} y1={103} x2={100} y2={20}
            stroke="white" strokeWidth="2.5" strokeLinecap="round" />
        </g>
        {/* Pivot */}
        <circle cx={100} cy={100} r="6" fill="#1e293b" stroke="#e2e8f0" strokeWidth="2.5" />
        <circle cx={100} cy={100} r="2.5" fill="#e2e8f0" />
      </svg>

      {/* Value + label below arc */}
      <div className="text-center -mt-3">
        <div
          className="text-5xl font-black tabular-nums leading-none"
          style={{ color, transition: 'color 0.4s ease' }}
        >
          {v.toFixed(1)}
        </div>
        <div
          className="text-xs font-bold uppercase tracking-[0.2em] mt-2"
          style={{ color, transition: 'color 0.4s ease' }}
        >
          {mood}
        </div>
      </div>
    </div>
  );
}

// --- Index mood card with expandable P/E chart ---
function IndexMoodCard({ index }: { index: MarketMoodResponse }) {
  const [expanded, setExpanded] = useState(false);
  const [period, setPeriod] = useState('1Y');

  const { data: details, isFetching } = useQuery({
    queryKey: ['market', 'history', index.index_name, period],
    queryFn: () => marketApi.indexHistory(index.index_name, period),
    enabled: expanded,
  });

  const moodBorder =
    {
      Green: 'border-l-emerald-500',
      Yellow: 'border-l-amber-500',
      Red: 'border-l-rose-500',
    }[index.mood] ?? 'border-l-border';

  return (
    <div className={cn('rounded-lg border border-l-4 border-border bg-card', moodBorder)}>
      <div className="flex items-center gap-4 px-5 py-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <p className="font-semibold">{index.display_name}</p>
            <MoodChip mood={index.mood} />
          </div>
          {index.as_of && (
            <p className="mt-0.5 text-xs text-muted-foreground">As of {formatDate(index.as_of)}</p>
          )}
        </div>
        <div className="hidden items-center gap-6 text-sm sm:flex">
          <div className="text-right">
            <p className="text-xs text-muted-foreground">Current P/E</p>
            <p className="font-bold tabular-nums">{index.current_pe.toFixed(2)}</p>
          </div>
          <div className="text-right">
            <p className="text-xs text-muted-foreground">Median (3Y)</p>
            <p className="font-semibold tabular-nums text-muted-foreground">
              {index.median_pe_3yr.toFixed(2)}
            </p>
          </div>
          <div className="text-right">
            <p className="text-xs text-muted-foreground">25th / 75th %ile</p>
            <p className="text-xs tabular-nums">
              {index.pct_25.toFixed(1)} / {index.pct_75.toFixed(1)}
            </p>
          </div>
        </div>
        <Button size="icon" variant="ghost" onClick={() => setExpanded((v) => !v)}>
          {expanded ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
        </Button>
      </div>

      {/* Recommendation */}
      {index.recommendation && (
        <div className="border-t border-border px-5 py-2.5">
          <p className="text-sm text-muted-foreground">{index.recommendation}</p>
        </div>
      )}

      {/* Expanded P/E chart */}
      {expanded && (
        <div className="space-y-3 border-t border-border px-5 pb-5 pt-4">
          <div className="flex items-center gap-2">
            {(['1M', '3M', '6M', '1Y', '3Y', '5Y'] as const).map((p) => (
              <button
                key={p}
                onClick={() => setPeriod(p)}
                className={cn(
                  'rounded px-2 py-1 text-xs font-medium transition-colors',
                  period === p
                    ? 'bg-primary text-primary-foreground'
                    : 'text-muted-foreground hover:bg-muted',
                )}
              >
                {p}
              </button>
            ))}
            {isFetching && <RefreshCw className="size-3.5 animate-spin text-muted-foreground" />}
          </div>

          {details?.history && details.history.length > 0 ? (
            <ResponsiveContainer width="100%" height={240}>
              <LineChart data={details.history as PEDataPoint[]} margin={{ top: 8, right: 20, bottom: 8, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#334155" vertical={false} />
                <XAxis
                  dataKey="date"
                  stroke="#94a3b8"
                  tick={{ fontSize: 10, fill: '#94a3b8' }}
                  tickFormatter={(v) => v.slice(0, 7)}
                  interval="preserveStartEnd"
                  tickMargin={6}
                />
                <YAxis
                  stroke="#94a3b8"
                  tick={{ fontSize: 10, fill: '#94a3b8' }}
                  domain={['auto', 'auto']}
                  tickFormatter={(v) => v.toFixed(1)}
                />
                <Tooltip
                  contentStyle={{
                    background: '#1e293b',
                    borderColor: '#334155',
                    borderRadius: '0.5rem',
                  }}
                  labelStyle={{ fontSize: 11, color: '#94a3b8' }}
                  itemStyle={{ color: '#e2e8f0' }}
                  formatter={(v: number) => [v.toFixed(2), 'P/E']}
                />
                {details.pct_25 > 0 && (
                  <ReferenceLine
                    y={details.pct_25}
                    stroke="#10B981"
                    strokeDasharray="4 4"
                    label={{ value: '25th %ile', fill: '#10B981', fontSize: 10 }}
                  />
                )}
                {details.median_pe_3yr > 0 && (
                  <ReferenceLine
                    y={details.median_pe_3yr}
                    stroke="#f59e0b"
                    strokeDasharray="4 4"
                    label={{ value: `Median (${details.median_pe_3yr.toFixed(1)})`, fill: '#f59e0b', fontSize: 10 }}
                  />
                )}
                {details.pct_75 > 0 && (
                  <ReferenceLine
                    y={details.pct_75}
                    stroke="#EF4444"
                    strokeDasharray="4 4"
                    label={{ value: '75th %ile', fill: '#EF4444', fontSize: 10 }}
                  />
                )}
                <Line
                  type="monotone"
                  dataKey="pe_ratio"
                  stroke="#3b82f6"
                  dot={false}
                  strokeWidth={2}
                  activeDot={{ r: 5, strokeWidth: 0, fill: '#60a5fa' }}
                />
              </LineChart>
            </ResponsiveContainer>
          ) : (
            <div className="flex flex-col items-center gap-2 py-8 text-center text-muted-foreground">
              <Info className="size-5" />
              <p className="text-sm">No P/E data yet for this period.</p>
              <p className="text-xs">Use the Settings tab → Ingest P/E to add historical data.</p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// --- Precious Metals Widget ---
function valuationCls(v: string) {
  if (v === 'OVERVALUED') return 'text-rose-400 bg-rose-500/10 border-rose-500/20';
  if (v === 'UNDERVALUED') return 'text-emerald-400 bg-emerald-500/10 border-emerald-500/20';
  return 'text-blue-400 bg-blue-500/10 border-blue-500/20';
}

function gsrHeatmap(gsr: number) {
  if (gsr < 52) return 'bg-rose-500 text-white';
  if (gsr < 65) return 'bg-yellow-500 text-black';
  return 'bg-emerald-500 text-white';
}

function PreciousMetalsWidget({ data }: { data: MetalsData }) {
  const isEuphoria = data.alert.active || data.alert.status.includes('EUPHORIA');
  return (
    <div className={cn(
      'rounded-lg border bg-card p-5 space-y-5',
      isEuphoria ? 'border-rose-500/60 shadow-[0_0_14px_rgba(239,68,68,0.3)]' : 'border-border',
    )}>
      <div className="flex items-start justify-between">
        <div>
          <h3 className="font-semibold flex items-center gap-2">
            Precious Metals
            {isEuphoria && <AlertTriangle className="size-4 text-rose-500" />}
          </h3>
          <p className="text-xs text-muted-foreground">Gold/Silver ratio & valuation alerts</p>
        </div>
        <span className={cn('rounded-full px-2.5 py-0.5 text-xs font-bold', gsrHeatmap(data.gsr))}>
          GSR: {data.gsr}
        </span>
      </div>

      {/* Prices */}
      <div className="grid grid-cols-2 gap-3">
        <div className="rounded-md border border-border bg-muted/30 p-3">
          <p className="text-xs text-muted-foreground mb-1">Gold (10g INR)</p>
          <p className="text-lg font-bold tabular-nums text-yellow-400">
            {formatINR(data.gold.inr)}
          </p>
          <div className="flex items-center justify-between mt-1">
            <span className="text-xs text-muted-foreground">${data.gold.usd.toFixed(0)}/oz</span>
            <span className={cn('text-[10px] font-bold uppercase px-1.5 py-0.5 rounded border', valuationCls(data.gold.valuation))}>
              {data.gold.valuation}
            </span>
          </div>
        </div>
        <div className="rounded-md border border-border bg-muted/30 p-3">
          <p className="text-xs text-muted-foreground mb-1">Silver (1kg INR)</p>
          <p className="text-lg font-bold tabular-nums text-slate-300">
            {formatINR(data.silver.inr)}
          </p>
          <div className="flex items-center justify-between mt-1">
            <span className="text-xs text-muted-foreground">${data.silver.usd.toFixed(2)}/oz</span>
            <span className={cn('text-[10px] font-bold uppercase px-1.5 py-0.5 rounded border', valuationCls(data.silver.valuation))}>
              {data.silver.valuation}
            </span>
          </div>
        </div>
      </div>

      {/* Alert */}
      <div className={cn(
        'rounded-md border p-3 flex items-start gap-2',
        isEuphoria ? 'bg-rose-500/10 border-rose-500/30' : 'bg-muted/30 border-border',
      )}>
        {isEuphoria
          ? <AlertTriangle className="size-4 text-rose-400 mt-0.5 shrink-0" />
          : <Info className="size-4 text-blue-400 mt-0.5 shrink-0" />}
        <div>
          <p className={cn('text-xs font-bold mb-0.5', isEuphoria ? 'text-rose-400' : 'text-blue-400')}>
            {data.alert.status.replace('_', ' ')}
          </p>
          <p className="text-xs text-muted-foreground leading-relaxed">{data.alert.message}</p>
        </div>
      </div>

      {/* Market drivers */}
      {(data.market_drivers?.length ?? 0) > 0 && (
        <div className="border-t border-border pt-3 space-y-1.5">
          <p className="text-xs font-semibold text-muted-foreground flex items-center gap-1.5">
            <TrendingUp className="size-3.5" /> Market Drivers
          </p>
          {data.market_drivers.map((d, i) => (
            <p key={i} className="text-xs text-muted-foreground">
              <span className="font-medium text-foreground">{d.title}:</span> {d.desc}
            </p>
          ))}
        </div>
      )}
    </div>
  );
}

// --- Overview tab ---
function OverviewTab({
  moods,
  mmi,
  metals,
}: {
  moods: MarketMoodResponse[];
  mmi: CachedMMI | null;
  metals: CachedMetals | null;
}) {
  const mmiData = mmi?.data ?? null;
  const metalsData = metals?.data ?? null;

  return (
    <div className="space-y-6">
      {/* MMI + Metals row */}
      <div className="grid gap-4 lg:grid-cols-2">
        {/* MMI card */}
        <div className="rounded-lg border border-border bg-card px-6 py-6">
          <p className="text-sm font-semibold mb-4">Market Mood Index</p>
          {mmiData ? (
            <div className="flex flex-col items-center gap-5">
              <MMIGauge value={mmiData.value} mood={mmiData.mood} />
              {/* Legend */}
              <div className="grid grid-cols-4 gap-2 w-full text-xs">
                {MMI_SEGMENTS.map((seg) => (
                  <div
                    key={seg.name}
                    className="rounded-md border px-2 py-2 text-center"
                    style={{ borderColor: `${seg.color}40`, background: `${seg.color}12` }}
                  >
                    <span className="font-bold block leading-tight" style={{ color: seg.color }}>{seg.name}</span>
                    <span className="text-muted-foreground text-[10px]">{seg.start}–{seg.end}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No data — click Refresh.</p>
          )}
          {mmi?.updated_at && (
            <p className="mt-4 text-xs text-muted-foreground">Updated {formatDate(mmi.updated_at)}</p>
          )}
        </div>

        {/* Metals */}
        {metalsData
          ? <PreciousMetalsWidget data={metalsData} />
          : (
            <div className="rounded-lg border border-border bg-card px-5 py-5 flex items-center justify-center">
              <p className="text-sm text-muted-foreground">No metals data — click Refresh.</p>
            </div>
          )}
      </div>

      {/* Index mood cards */}
      {moods.length > 0 && (
        <div className="space-y-3">
          {moods.map((m) => (
            <IndexMoodCard key={m.index_name} index={m} />
          ))}
        </div>
      )}
    </div>
  );
}

// --- Settings tab ---
function SettingsTab({ config }: { config: IndexConfigItem[] }) {
  const qc = useQueryClient();
  const [watchSymbol, setWatchSymbol] = useState('');

  const toggleMut = useMutation({
    mutationFn: (name: string) => marketApi.toggleConfig(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['market', 'config'] }),
  });

  const syncMut = useMutation({
    mutationFn: () => marketApi.sync(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['market', 'moods'] }),
  });

  const { data: watchlist = [] } = useQuery({
    queryKey: ['market', 'watchlist'],
    queryFn: marketApi.watchlist,
  });

  const addWatchMut = useMutation({
    mutationFn: () => marketApi.addWatchlist(watchSymbol),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['market', 'watchlist'] });
      setWatchSymbol('');
    },
  });

  const removeWatchMut = useMutation({
    mutationFn: (sym: string) => marketApi.removeWatchlist(sym),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['market', 'watchlist'] }),
  });

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      {/* Index Configuration */}
      <div className="rounded-lg border border-border bg-card p-5 space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="font-semibold">Index Configuration</h3>
            <p className="text-xs text-muted-foreground mt-0.5">Toggle which indices show on Overview</p>
          </div>
          <Button size="sm" variant="outline" onClick={() => syncMut.mutate()} disabled={syncMut.isPending}>
            <RefreshCw className={cn('size-3.5', syncMut.isPending && 'animate-spin')} />
            Sync
          </Button>
        </div>
        <div className="divide-y divide-border rounded-md border border-border overflow-hidden">
          {config.map((idx) => (
            <div key={idx.index_name} className="flex items-center justify-between px-4 py-3 hover:bg-muted/30 transition-colors">
              <div>
                <p className="text-sm font-medium">{idx.display_name}</p>
                <p className="text-xs text-muted-foreground">{idx.category}</p>
              </div>
              <button
                onClick={() => toggleMut.mutate(idx.index_name)}
                disabled={toggleMut.isPending}
                title={idx.is_active ? 'Disable' : 'Enable'}
                className="rounded-full p-1 transition-colors hover:bg-muted"
              >
                {idx.is_active
                  ? <CheckCircle className="size-5 text-emerald-500" />
                  : <XCircle className="size-5 text-muted-foreground/40" />
                }
              </button>
            </div>
          ))}
        </div>
      </div>

      {/* Watchlist */}
      <div className="rounded-lg border border-border bg-card p-5 space-y-4">
        <div>
          <h3 className="font-semibold">Watchlist</h3>
          <p className="text-xs text-muted-foreground mt-0.5">
            Track symbols like <code className="rounded bg-muted px-1">NSE:RELIANCE</code> for quick reference
          </p>
        </div>
        <div className="flex gap-2">
          <Input
            placeholder="NSE:SYMBOL or MUTF_IN:…"
            value={watchSymbol}
            onChange={(e) => setWatchSymbol(e.target.value.toUpperCase())}
            className="font-mono text-sm"
            onKeyDown={(e) => { if (e.key === 'Enter' && watchSymbol) addWatchMut.mutate(); }}
          />
          <Button onClick={() => addWatchMut.mutate()} disabled={!watchSymbol || addWatchMut.isPending} size="icon">
            <Plus className="size-4" />
          </Button>
        </div>
        {(watchlist as WatchlistItem[]).length > 0 ? (
          <div className="divide-y divide-border rounded-md border border-border overflow-hidden">
            {(watchlist as WatchlistItem[]).map((w) => (
              <div key={w.id} className="flex items-center justify-between px-4 py-2.5 hover:bg-muted/30 transition-colors">
                <span className="font-mono text-sm">{w.symbol}</span>
                <button
                  onClick={() => removeWatchMut.mutate(w.symbol)}
                  disabled={removeWatchMut.isPending}
                  title="Remove"
                  className="rounded p-1 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                >
                  <Trash2 className="size-3.5" />
                </button>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground py-4 text-center">No symbols yet — add one above.</p>
        )}
      </div>
    </div>
  );
}

// --- Page ---
function MarketMoodPage() {
  const qc = useQueryClient();

  const refreshMut = useMutation({
    mutationFn: () => marketApi.refresh(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['market', 'mmi'] });
      qc.invalidateQueries({ queryKey: ['market', 'metals'] });
    },
  });

  const { data: moods = [] } = useQuery({
    queryKey: ['market', 'moods'],
    queryFn: marketApi.moods,
    staleTime: 5 * 60_000,
  });

  const { data: config = [] } = useQuery({
    queryKey: ['market', 'config'],
    queryFn: marketApi.config,
  });

  const { data: mmi } = useQuery({
    queryKey: ['market', 'mmi'],
    queryFn: marketApi.mmi,
    staleTime: 30 * 60_000,
    retry: false,
  });

  const { data: metals } = useQuery({
    queryKey: ['market', 'metals'],
    queryFn: marketApi.metals,
    staleTime: 30 * 60_000,
    retry: false,
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Market Mood</h1>
          <p className="text-sm text-muted-foreground">
            P/E percentile-based market valuation and sentiment indicators.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => refreshMut.mutate()}
          disabled={refreshMut.isPending}
        >
          <RefreshCw className={cn('size-3.5', refreshMut.isPending && 'animate-spin')} />
          Refresh
        </Button>
      </div>

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-4">
          <OverviewTab
            moods={moods as MarketMoodResponse[]}
            mmi={mmi ?? null}
            metals={metals ?? null}
          />
        </TabsContent>

        <TabsContent value="settings" className="mt-4">
          <SettingsTab config={config as IndexConfigItem[]} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
