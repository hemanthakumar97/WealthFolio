import { useState } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Search,
  TrendingUp,
  TrendingDown,
  Minus,
  Loader2,
  Plus,
  Trash2,
  RefreshCw,
  AlertCircle,
  CheckCircle2,
  BarChart3,
  LineChart,
  ShieldCheck,
  Zap,
} from 'lucide-react';

import { analysisApi, settingsApi, type StockAnalysis } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { usePrivacy } from '@/lib/privacy';

export const Route = createFileRoute('/_app/analysis')({
  component: AnalysisPage,
});

// ── Helpers ───────────────────────────────────────────────────────────────────

const REC_CONFIG: Record<
  string,
  { label: string; color: string; bg: string; icon: typeof TrendingUp }
> = {
  STRONG_BUY:   { label: 'Strong Buy',  color: 'text-emerald-600 dark:text-emerald-400', bg: 'border-emerald-500/30 bg-emerald-500/10', icon: TrendingUp },
  BUY:          { label: 'Buy',         color: 'text-green-600 dark:text-green-400',     bg: 'border-green-500/30 bg-green-500/10',     icon: TrendingUp },
  ACCUMULATE:   { label: 'Accumulate',  color: 'text-lime-600 dark:text-lime-400',       bg: 'border-lime-500/30 bg-lime-500/10',       icon: TrendingUp },
  HOLD:         { label: 'Hold',        color: 'text-yellow-600 dark:text-yellow-400',   bg: 'border-yellow-500/30 bg-yellow-500/10',   icon: Minus },
  REDUCE:       { label: 'Reduce',      color: 'text-orange-600 dark:text-orange-400',   bg: 'border-orange-500/30 bg-orange-500/10',   icon: TrendingDown },
  SELL:         { label: 'Sell',        color: 'text-red-600 dark:text-red-400',          bg: 'border-red-500/30 bg-red-500/10',         icon: TrendingDown },
  NOT_ANALYZED: { label: 'Not Analyzed', color: 'text-muted-foreground',                 bg: 'border-muted/40 bg-muted/20',             icon: Minus },
};

function scoreColor(score: number) {
  if (score >= 70) return 'text-emerald-500';
  if (score >= 50) return 'text-yellow-500';
  return 'text-red-500';
}

function ScoreRing({ score, label }: { score: number; label: string }) {
  const r = 22;
  const circ = 2 * Math.PI * r;
  const dash = (score / 100) * circ;
  const col =
    score >= 70 ? '#10b981' : score >= 50 ? '#eab308' : '#ef4444';
  return (
    <div className="flex flex-col items-center gap-1">
      <svg width={56} height={56} className="-rotate-90">
        <circle cx={28} cy={28} r={r} fill="none" stroke="currentColor" strokeWidth={4} className="text-muted/30" />
        <circle
          cx={28} cy={28} r={r} fill="none"
          stroke={col} strokeWidth={4}
          strokeDasharray={`${dash} ${circ}`}
          strokeLinecap="round"
        />
      </svg>
      <span className={cn('text-xs font-bold -mt-11 z-10 relative', scoreColor(score))}>
        {score}
      </span>
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  );
}

function FundRow({ label, value, unit = '', good, bad }: {
  label: string; value: number; unit?: string; good?: boolean; bad?: boolean;
}) {
  return (
    <div className="flex items-center justify-between py-1.5 border-b border-border/30 last:border-0">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={cn(
        'text-xs font-semibold tabular-nums',
        good ? 'text-emerald-500' : bad ? 'text-red-500' : 'text-foreground',
      )}>
        {value === 0 ? '—' : `${value.toFixed(1)}${unit}`}
      </span>
    </div>
  );
}

function TechBadge({ label, active, positive }: { label: string; active: boolean; positive: boolean }) {
  return (
    <span className={cn(
      'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-medium',
      !active ? 'border-muted/40 bg-muted/20 text-muted-foreground' :
        positive ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' :
          'border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400',
    )}>
      {active && (positive ? <CheckCircle2 className="size-3" /> : <AlertCircle className="size-3" />)}
      {label}
    </span>
  );
}

// ── Watchlist sidebar ─────────────────────────────────────────────────────────

function WatchlistItem({
  fa,
  active,
  onClick,
  onRemove,
}: {
  fa: StockAnalysis;
  active: boolean;
  onClick: () => void;
  onRemove: () => void;
}) {
  const rec = REC_CONFIG[fa.recommendation] ?? REC_CONFIG.NOT_ANALYZED;
  const Icon = rec.icon;
  return (
    <button
      onClick={onClick}
      className={cn(
        'group w-full flex items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-all',
        active ? 'bg-primary/10 ring-1 ring-primary/20' : 'hover:bg-muted/40',
      )}
    >
      <div className="flex-1 min-w-0">
        <p className="text-xs font-bold truncate">{fa.symbol}</p>
        <p className="text-xs text-muted-foreground truncate">{fa.company_name || '—'}</p>
      </div>
      {fa.recommendation !== 'NOT_ANALYZED' && (
        <div className="flex flex-col items-end gap-1">
          <span className={cn('text-xs font-bold', scoreColor(fa.composite_score))}>
            {fa.composite_score}
          </span>
          <span className={cn('text-[10px] font-semibold', rec.color)}>{rec.label}</span>
        </div>
      )}
      <button
        onClick={(e) => { e.stopPropagation(); onRemove(); }}
        className="opacity-0 group-hover:opacity-100 p-0.5 rounded text-muted-foreground hover:text-red-500 transition-opacity"
      >
        <Trash2 className="size-3" />
      </button>
    </button>
  );
}

// ── Main analysis panel ───────────────────────────────────────────────────────

function AnalysisPanel({ symbol }: { symbol: string }) {
  const { masked } = usePrivacy();
  const [running, setRunning] = useState(false);

  const { data: fa, refetch, isLoading } = useQuery<StockAnalysis>({
    queryKey: ['analysis', symbol],
    queryFn: () => analysisApi.get(symbol),
    retry: false,
  });

  const runMutation = useMutation({
    mutationFn: () => analysisApi.run(symbol),
    onSuccess: () => refetch(),
  });

  const handleRun = async () => {
    setRunning(true);
    try { await runMutation.mutateAsync(); } finally { setRunning(false); }
  };

  if (isLoading) {
    return (
      <div className="flex flex-col items-center gap-3 py-16 text-muted-foreground">
        <Loader2 className="size-6 animate-spin" />
        <span className="text-sm">Loading…</span>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-bold">{fa?.company_name || symbol}</h2>
          <p className="text-xs text-muted-foreground">{symbol}{fa?.sector ? ` · ${fa.sector}` : ''}</p>
        </div>
        <Button size="sm" variant="outline" onClick={handleRun} disabled={running} className="gap-1.5 shrink-0">
          {running ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
          {fa ? 'Re-analyse' : 'Analyse Now'}
        </Button>
      </div>

      {runMutation.isError && (
        <p className="flex items-center gap-1.5 text-xs text-destructive">
          <AlertCircle className="size-3.5" />
          {runMutation.error instanceof Error ? runMutation.error.message : 'Analysis failed'}
        </p>
      )}

      {fa && (
        <>
          {/* Score + recommendation */}
          <div className="rounded-2xl border border-border/40 bg-card/40 p-4">
            <div className="flex items-center gap-6 mb-4">
              <ScoreRing score={fa.composite_score} label="Overall" />
              <ScoreRing score={fa.fundamental_score} label="Fundamental" />
              <ScoreRing score={fa.technical_score} label="Technical" />
              <div className="flex-1" />
              {(() => {
                const rec = REC_CONFIG[fa.recommendation] ?? REC_CONFIG.HOLD;
                const RecIcon = rec.icon;
                return (
                  <span className={cn(
                    'inline-flex items-center gap-1.5 rounded-xl border px-4 py-2 text-sm font-bold',
                    rec.bg, rec.color,
                  )}>
                    <RecIcon className="size-4" /> {rec.label}
                  </span>
                );
              })()}
            </div>

            {/* Price strip */}
            {fa.current_price > 0 && (
              <div className="grid grid-cols-4 gap-2 text-center">
                {[
                  { label: 'Price', value: masked ? '••••' : `₹${fa.current_price.toFixed(2)}` },
                  { label: '52W High', value: masked ? '••••' : `₹${fa.week_52_high.toFixed(0)}` },
                  { label: '52W Low', value: masked ? '••••' : `₹${fa.week_52_low.toFixed(0)}` },
                  { label: 'Mkt Cap', value: fa.market_cap_cr > 0 ? `₹${fa.market_cap_cr.toFixed(0)}Cr` : '—' },
                ].map((m) => (
                  <div key={m.label} className="rounded-lg bg-muted/30 py-2">
                    <p className="text-xs text-muted-foreground">{m.label}</p>
                    <p className="text-sm font-bold tabular-nums">{m.value}</p>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Technical signals */}
          <div className="rounded-2xl border border-border/40 bg-card/40 p-4 space-y-3">
            <div className="flex items-center gap-2 mb-2">
              <LineChart className="size-4 text-primary" />
              <span className="text-sm font-semibold">Technical Indicators</span>
            </div>
            <div className="flex flex-wrap gap-2">
              <TechBadge label="Above 200 DMA" active={true} positive={fa.above_sma_200} />
              <TechBadge label="Above 50 DMA" active={true} positive={fa.above_sma_50} />
              <TechBadge label={fa.golden_cross ? 'Golden Cross ✓' : 'Death Cross'} active={true} positive={fa.golden_cross} />
              <TechBadge
                label={fa.rsi_14 > 0 ? `RSI ${fa.rsi_14.toFixed(0)}${fa.rsi_14 < 35 ? ' — Oversold' : fa.rsi_14 > 70 ? ' — Overbought' : ''}` : 'RSI N/A'}
                active={fa.rsi_14 > 0}
                positive={fa.rsi_14 > 0 && fa.rsi_14 < 70}
              />
              <TechBadge label={`MACD ${fa.macd_histogram >= 0 ? '▲ Bullish' : '▼ Bearish'}`} active={true} positive={fa.macd_histogram >= 0} />
            </div>
            <div className="grid grid-cols-2 gap-2 mt-2 text-xs">
              {[
                { k: '50-DMA', v: fa.sma_50 > 0 ? `₹${fa.sma_50.toFixed(2)}` : '—' },
                { k: '200-DMA', v: fa.sma_200 > 0 ? `₹${fa.sma_200.toFixed(2)}` : '—' },
              ].map((x) => (
                <div key={x.k} className="flex justify-between rounded-lg bg-muted/20 px-3 py-1.5">
                  <span className="text-muted-foreground">{x.k}</span>
                  <span className="font-semibold tabular-nums">{masked ? '••••' : x.v}</span>
                </div>
              ))}
            </div>
          </div>

          {/* Fundamental metrics */}
          <div className="rounded-2xl border border-border/40 bg-card/40 p-4">
            <div className="flex items-center gap-2 mb-3">
              <BarChart3 className="size-4 text-primary" />
              <span className="text-sm font-semibold">Fundamental Metrics</span>
            </div>
            <div className="grid grid-cols-2 gap-x-6">
              <div>
                <FundRow label="Trailing P/E" value={fa.trailing_pe} good={fa.trailing_pe > 0 && fa.trailing_pe < 20} bad={fa.trailing_pe > 40} />
                <FundRow label="Price / Book" value={fa.price_to_book} good={fa.price_to_book > 0 && fa.price_to_book < 2} bad={fa.price_to_book > 6} />
                <FundRow label="ROE" value={fa.roe} unit="%" good={fa.roe > 15} bad={fa.roe > 0 && fa.roe < 8} />
                <FundRow label="ROCE" value={fa.roce} unit="%" good={fa.roce > 20} bad={fa.roce > 0 && fa.roce < 10} />
                <FundRow label="Promoter Holding" value={fa.promoter_holding} unit="%" good={fa.promoter_holding > 50} bad={fa.promoter_holding > 0 && fa.promoter_holding < 25} />
              </div>
              <div>
                <FundRow label="Revenue Growth" value={fa.revenue_growth} unit="%" good={fa.revenue_growth > 15} bad={fa.revenue_growth < 0} />
                <FundRow label="Earnings Growth" value={fa.earnings_growth} unit="%" good={fa.earnings_growth > 15} bad={fa.earnings_growth < -5} />
                <FundRow label="Debt / Equity" value={fa.debt_to_equity} good={fa.debt_to_equity >= 0 && fa.debt_to_equity < 30} bad={fa.debt_to_equity > 150} />
                <FundRow label="Dividend Yield" value={fa.dividend_yield} unit="%" good={fa.dividend_yield > 2} />
                <FundRow label="Free Cash Flow" value={fa.free_cash_flow} unit="Cr" good={fa.free_cash_flow > 0} bad={fa.free_cash_flow < 0} />
              </div>
            </div>
          </div>

          {/* Buy / Caution signals */}
          {(fa.buy_signals?.length > 0 || fa.caution_signals?.length > 0) && (
            <div className="grid grid-cols-2 gap-3">
              {fa.buy_signals?.length > 0 && (
                <div className="rounded-xl border border-emerald-500/20 bg-emerald-500/5 p-3 space-y-1.5">
                  <div className="flex items-center gap-1.5 text-xs font-semibold text-emerald-600 dark:text-emerald-400 mb-2">
                    <CheckCircle2 className="size-3.5" /> Buy Signals ({fa.buy_signals.length})
                  </div>
                  {fa.buy_signals.map((s, i) => (
                    <p key={i} className="text-xs text-muted-foreground flex items-start gap-1.5">
                      <span className="select-none font-black text-emerald-500 mt-0.5">•</span>{s}
                    </p>
                  ))}
                </div>
              )}
              {fa.caution_signals?.length > 0 && (
                <div className="rounded-xl border border-orange-500/20 bg-orange-500/5 p-3 space-y-1.5">
                  <div className="flex items-center gap-1.5 text-xs font-semibold text-orange-600 dark:text-orange-400 mb-2">
                    <AlertCircle className="size-3.5" /> Caution Signals ({fa.caution_signals.length})
                  </div>
                  {fa.caution_signals.map((s, i) => (
                    <p key={i} className="text-xs text-muted-foreground flex items-start gap-1.5">
                      <span className="select-none font-black text-orange-500 mt-0.5">•</span>{s}
                    </p>
                  ))}
                </div>
              )}
            </div>
          )}

          <p className="text-right text-xs text-muted-foreground">
            Analysed {new Date(fa.analyzed_at).toLocaleString('en-IN', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' })}
          </p>
        </>
      )}

      {!fa && !running && (
        <div className="rounded-xl border border-dashed border-border/40 p-8 text-center text-sm text-muted-foreground">
          No analysis yet. Click <span className="font-semibold text-foreground">Analyse Now</span> to run fundamental + technical analysis.
        </div>
      )}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

function AnalysisPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<string | null>(null);

  const { data: watchlist = [], refetch: refetchWatchlist } = useQuery<StockAnalysis[]>({
    queryKey: ['analysis-watchlist'],
    queryFn: analysisApi.watchlist,
  });

  const addMutation = useMutation({
    mutationFn: async (sym: string) => {
      await settingsApi.addWatchlist(sym);
      return analysisApi.run(sym);
    },
    onSuccess: (fa) => {
      qc.invalidateQueries({ queryKey: ['analysis-watchlist'] });
      setSelected(fa.symbol);
      setSearch('');
    },
  });

  const removeMutation = useMutation({
    mutationFn: (sym: string) => settingsApi.removeWatchlist(sym),
    onSuccess: () => {
      refetchWatchlist();
    },
  });

  const handleAdd = () => {
    const sym = search.trim().toUpperCase();
    if (!sym) return;
    addMutation.mutate(sym);
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-4 border-b border-border/40 pb-5">
        <div className="grid h-12 w-12 place-items-center rounded-2xl border border-primary/20 bg-gradient-to-tr from-primary/20 to-primary/5 text-primary shadow-lg shadow-primary/5">
          <Zap className="size-6" />
        </div>
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight">Stock Analyser</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Fundamental + technical analysis with actionable buy/sell recommendations for any Indian stock.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-[18rem_1fr] gap-6">
        {/* Watchlist sidebar */}
        <aside className="space-y-3">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider px-1">Watchlist</p>
          {/* Add stock */}
          <div className="flex gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground" />
              <Input
                placeholder="RELIANCE.NS"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleAdd()}
                className="pl-8 text-xs h-8"
              />
            </div>
            <Button
              size="sm"
              className="h-8 px-3"
              onClick={handleAdd}
              disabled={!search.trim() || addMutation.isPending}
            >
              {addMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />}
            </Button>
          </div>

          {addMutation.isError && (
            <p className="text-xs text-destructive flex items-center gap-1">
              <AlertCircle className="size-3" />
              {addMutation.error instanceof Error ? addMutation.error.message : 'Failed'}
            </p>
          )}

          <div className="space-y-0.5">
            {watchlist.length === 0 ? (
              <p className="text-xs text-muted-foreground px-3 py-4">
                Add a stock symbol (e.g. RELIANCE.NS) to begin.
              </p>
            ) : (
              watchlist.map((fa) => (
                <WatchlistItem
                  key={fa.symbol}
                  fa={fa}
                  active={selected === fa.symbol}
                  onClick={() => setSelected(fa.symbol)}
                  onRemove={() => {
                    removeMutation.mutate(fa.symbol);
                    if (selected === fa.symbol) setSelected(null);
                  }}
                />
              ))
            )}
          </div>

          {/* Legend */}
          <div className="mt-4 rounded-xl border border-border/30 p-3 space-y-1.5">
            <p className="text-xs font-semibold text-muted-foreground mb-2">Recommendation key</p>
            {Object.entries(REC_CONFIG)
              .filter(([k]) => k !== 'NOT_ANALYZED')
              .map(([k, v]) => {
                const Icon = v.icon;
                return (
                  <div key={k} className="flex items-center gap-2">
                    <Icon className={cn('size-3', v.color)} />
                    <span className={cn('text-xs font-medium', v.color)}>{v.label}</span>
                  </div>
                );
              })}
          </div>
        </aside>

        {/* Analysis panel */}
        <div className="rounded-2xl border border-border/40 bg-card/30 p-5 backdrop-blur-sm min-h-[500px]">
          {selected ? (
            <AnalysisPanel symbol={selected} />
          ) : (
            <div className="flex flex-col items-center justify-center h-full gap-4 py-20 text-muted-foreground">
              <ShieldCheck className="size-12 opacity-20" />
              <div className="text-center space-y-1">
                <p className="font-semibold text-foreground">Select a stock to analyse</p>
                <p className="text-sm">Add a symbol to your watchlist and click it to run a full analysis.</p>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* How it works */}
      <div className="rounded-xl border border-primary/10 bg-primary/5 p-4 text-xs text-muted-foreground">
        <p className="mb-2 font-semibold text-foreground">How analysis works</p>
        <div className="grid grid-cols-3 gap-4">
          <div>
            <p className="font-medium text-foreground mb-1">Fundamental Score (60%)</p>
            <p>P/E vs sector, P/B, ROE, ROCE, revenue &amp; earnings growth, debt/equity, promoter holding, dividend yield — sourced from Yahoo Finance.</p>
          </div>
          <div>
            <p className="font-medium text-foreground mb-1">Technical Score (40%)</p>
            <p>RSI-14, MACD(12,26,9), 50/200-DMA trend, golden/death cross — computed from 2-year daily price history.</p>
          </div>
          <div>
            <p className="font-medium text-foreground mb-1">Recommendation</p>
            <p>Strong Buy ≥78 · Buy ≥63 · Accumulate ≥48 · Hold ≥35 · Reduce ≥20 · Sell &lt;20. Re-run daily for fresh signals.</p>
          </div>
        </div>
      </div>
    </div>
  );
}
