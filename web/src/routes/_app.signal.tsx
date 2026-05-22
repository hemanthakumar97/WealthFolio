import { useState, useRef, useCallback, useEffect } from 'react';
import { createFileRoute, Link } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { InstrumentAnalysisPanel } from '@/components/InstrumentAnalysisPanel';
import {
  Sparkles,
  TrendingUp,
  TrendingDown,
  Minus,
  CheckCircle2,
  XCircle,
  Loader2,
  Search,
  AlertTriangle,
  BarChart3,
  RefreshCw,
  ChevronRight,
  Shield,
  Flame,
  Settings2,
  Clock,
  Activity,
  LineChart,
} from 'lucide-react';

import {
  portfolioApi,
  signalApi,
  aiSettingsApi,
  type HoldingSignal,
  type SignalAction,
} from '@/lib/api';
import { formatINR } from '@/lib/format';
import { MaskedAmount } from '@/lib/privacy';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent } from '@/components/ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_app/signal')({
  component: SignalPage,
});

function formatRelative(isoString: string): string {
  const diff = Math.floor((Date.now() - new Date(isoString).getTime()) / 1000);
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

// ─── Types ────────────────────────────────────────────────────────────────────

type RiskProfile = 'conservative' | 'moderate' | 'aggressive';

interface InvestorProfile {
  riskProfile: RiskProfile;
  goal: string;
  horizon: string;
}

type StreamState = 'idle' | 'streaming' | 'done' | 'error';

// ─── Action config ────────────────────────────────────────────────────────────

const ACTION_CFG: Record<
  SignalAction,
  {
    label: string;
    color: string;
    bg: string;
    border: string;
    icon: typeof TrendingUp;
  }
> = {
  BUY_MORE: {
    label: 'Buy More',
    color: 'text-emerald-400',
    bg: 'bg-emerald-500/10',
    border: 'border-emerald-500/25',
    icon: TrendingUp,
  },
  HOLD: {
    label: 'Hold',
    color: 'text-blue-400',
    bg: 'bg-blue-500/10',
    border: 'border-blue-500/25',
    icon: Minus,
  },
  SWITCH: {
    label: 'Switch',
    color: 'text-orange-400',
    bg: 'bg-orange-500/10',
    border: 'border-orange-500/25',
    icon: RefreshCw,
  },
  PARTIAL_SELL: {
    label: 'Partial Sell',
    color: 'text-amber-400',
    bg: 'bg-amber-500/10',
    border: 'border-amber-500/25',
    icon: TrendingDown,
  },
  BOOK_PROFIT: {
    label: 'Book Profit',
    color: 'text-violet-400',
    bg: 'bg-violet-500/10',
    border: 'border-violet-500/25',
    icon: CheckCircle2,
  },
};

const TYPE_COLORS: Record<string, string> = {
  MF:      'border-blue-500/30 text-blue-400 bg-blue-500/5',
  ETF:     'border-violet-500/30 text-violet-400 bg-violet-500/5',
  STOCK:   'border-emerald-500/30 text-emerald-400 bg-emerald-500/5',
  BOND:    'border-amber-500/30 text-amber-400 bg-amber-500/5',
  METAL:   'border-yellow-500/30 text-yellow-400 bg-yellow-500/5',
  GOLD:    'border-yellow-500/30 text-yellow-400 bg-yellow-500/5',
  US_FUND: 'border-sky-500/30 text-sky-400 bg-sky-500/5',
};

// ─── Risk profile config ──────────────────────────────────────────────────────

const PROFILES: Record<
  RiskProfile,
  {
    label: string;
    description: string;
    allocation: string;
    color: string;
    bg: string;
    border: string;
    icon: typeof Shield;
  }
> = {
  conservative: {
    label: 'Conservative',
    description: 'Capital preservation, tax efficiency, stable compounding',
    allocation: '40% Equity · 45% Debt · 15% Gold',
    color: 'text-blue-400',
    bg: 'bg-blue-500/8',
    border: 'border-blue-500/30',
    icon: Shield,
  },
  moderate: {
    label: 'Moderate',
    description: 'Balanced growth with risk management, LTCG optimisation',
    allocation: '65% Equity · 25% Debt · 10% Gold',
    color: 'text-emerald-400',
    bg: 'bg-emerald-500/8',
    border: 'border-emerald-500/30',
    icon: TrendingUp,
  },
  aggressive: {
    label: 'Aggressive',
    description: 'Maximum alpha, full exemption harvest, conviction bets',
    allocation: '80% Equity · 10% Debt · 10% Gold',
    color: 'text-red-400',
    bg: 'bg-red-500/8',
    border: 'border-red-500/30',
    icon: Flame,
  },
};

const SUGGESTIONS = [
  'Reliance Industries',
  'HDFC Bank',
  'Nifty 50 ETF',
  'Bajaj Finance',
  'ITC Ltd',
  'Parag Parikh Flexi Cap Fund',
];

// ─── Confidence bar ───────────────────────────────────────────────────────────

function ConfidenceBar({ value }: { value: number }) {
  const color = value >= 75 ? 'bg-emerald-500' : value >= 60 ? 'bg-amber-500' : 'bg-red-500';
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-muted">
        <div className={cn('h-full rounded-full', color)} style={{ width: `${value}%` }} />
      </div>
      <span className="text-[11px] tabular-nums text-muted-foreground">{value}%</span>
    </div>
  );
}

// ─── Score bar (used in expanded analysis row) ────────────────────────────────

function ScoreBar({ score }: { score: number }) {
  const pct   = score; // /100
  const color = score >= 80 ? 'bg-emerald-500' : score >= 65 ? 'bg-blue-500' : score >= 50 ? 'bg-orange-500' : 'bg-red-500';
  const text  = score >= 80 ? 'text-emerald-400' : score >= 65 ? 'text-blue-400' : score >= 50 ? 'text-orange-400' : 'text-red-400';
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-muted">
        <div className={cn('h-full rounded-full', color)} style={{ width: `${pct}%` }} />
      </div>
      <span className={cn('text-[11px] tabular-nums font-medium', text)}>{score}/100</span>
    </div>
  );
}



function ScoreDetailInRow({ instrumentId, storedScore }: { instrumentId: number; storedScore?: number }) {
  const { data } = useQuery({
    queryKey: ['instrument-metrics', instrumentId],
    queryFn: () => signalApi.getInstrumentMetrics(instrumentId),
    staleTime: 5 * 60_000,
    enabled: false, // only reads from cache; ScoreCell already triggers the fetch
  });

  const score = storedScore || data?.metrics?.zero1_score;
  if (!score) return null;

  return (
    <div className="shrink-0">
      <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Score</p>
      <ScoreBar score={score} />
      <p className="mt-1 text-[10px] text-muted-foreground/60">
        {score >= 80 ? 'Strong Buy' : score >= 65 ? 'Hold' : score >= 50 ? 'Switch' : 'Sell'}
        {' · '}click name to view full breakdown
      </p>
    </div>
  );
}

// ─── Profile selector ─────────────────────────────────────────────────────────

function ProfileSelector({
  profile,
  onChange,
}: {
  profile: InvestorProfile;
  onChange: (p: InvestorProfile) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const cfg = PROFILES[profile.riskProfile];
  const Icon = cfg.icon;

  return (
    <div className="rounded-xl border border-border bg-card/50 p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <p className="text-sm font-medium">Investor Profile</p>
          <span
            className={cn(
              'flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-semibold',
              cfg.color,
              cfg.bg,
              cfg.border,
            )}
          >
            <Icon className="size-3" /> {cfg.label}
          </span>
        </div>
        <button
          onClick={() => setExpanded((v) => !v)}
          className="flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          <Settings2 className="size-3.5" /> {expanded ? 'Collapse' : 'Edit'}
        </button>
      </div>

      {expanded && (
        <div className="mt-4 space-y-4">
          <div>
            <p className="mb-2 text-xs font-medium text-muted-foreground">Risk Profile</p>
            <div className="grid grid-cols-3 gap-2">
              {(Object.entries(PROFILES) as [RiskProfile, typeof PROFILES.moderate][]).map(
                ([key, pcfg]) => {
                  const PIcon = pcfg.icon;
                  const sel = profile.riskProfile === key;
                  return (
                    <button
                      key={key}
                      onClick={() => onChange({ ...profile, riskProfile: key })}
                      className={cn(
                        'flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-all',
                        sel ? cn(pcfg.border, pcfg.bg) : 'border-border hover:bg-muted/30',
                      )}
                    >
                      <div className="flex items-center gap-1.5">
                        <PIcon
                          className={cn('size-3.5', sel ? pcfg.color : 'text-muted-foreground')}
                        />
                        <span
                          className={cn(
                            'text-xs font-semibold',
                            sel ? pcfg.color : 'text-foreground',
                          )}
                        >
                          {pcfg.label}
                        </span>
                      </div>
                      <p className="text-[10px] leading-relaxed text-muted-foreground">
                        {pcfg.description}
                      </p>
                      <p
                        className={cn(
                          'text-[10px] font-medium',
                          sel ? pcfg.color : 'text-muted-foreground/70',
                        )}
                      >
                        {pcfg.allocation}
                      </p>
                    </button>
                  );
                },
              )}
            </div>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-[11px] font-medium text-muted-foreground">
                Investment Goal
              </label>
              <Input
                value={profile.goal}
                onChange={(e) => onChange({ ...profile, goal: e.target.value })}
                placeholder="e.g. Retirement in 10 years"
                className="h-8 text-xs"
              />
            </div>
            <div>
              <label className="mb-1 block text-[11px] font-medium text-muted-foreground">
                Horizon
              </label>
              <Input
                value={profile.horizon}
                onChange={(e) => onChange({ ...profile, horizon: e.target.value })}
                placeholder="e.g. 10 years"
                className="h-8 text-xs"
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Streaming hook (Analyse tab) ─────────────────────────────────────────────

function useStream() {
  const [state, setState] = useState<StreamState>('idle');
  const [content, setContent] = useState('');
  const [error, setError] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const stream = useCallback(async (url: string, body: unknown) => {
    if (abortRef.current) abortRef.current.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setState('streaming');
    setContent('');
    setError(null);

    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(body),
        signal: ctrl.signal,
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({ detail: res.statusText }));
        throw new Error(err.detail ?? 'Request failed');
      }
      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split('\n');
        buf = lines.pop() ?? '';
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue;
          const data = line.slice(6);
          if (data === '[DONE]') {
            setState('done');
            return;
          }
          try {
            const { text } = JSON.parse(data) as { text: string };
            setContent((p) => p + text);
          } catch {
            /* skip */
          }
        }
      }
      setState('done');
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') {
        setState('idle');
        return;
      }
      setError(err instanceof Error ? err.message : 'Unknown error');
      setState('error');
    }
  }, []);

  const reset = useCallback(() => {
    abortRef.current?.abort();
    setState('idle');
    setContent('');
    setError(null);
  }, []);
  return { state, content, error, stream, reset };
}

// ─── Simple markdown renderer (Analyse tab) ───────────────────────────────────

function renderInline(text: string): React.ReactNode[] {
  return text
    .split(/(\*\*[^*]+\*\*)/g)
    .map((p, i) =>
      p.startsWith('**') && p.endsWith('**') ? <strong key={i}>{p.slice(2, -2)}</strong> : p,
    );
}

function MarkdownBlock({ content }: { content: string }) {
  const lines = content.split('\n');
  const nodes: React.ReactNode[] = [];
  let listBuf: string[] = [];
  const flush = (k: string) => {
    if (!listBuf.length) return;
    nodes.push(
      <ul key={k} className="my-2 space-y-1 pl-4">
        {listBuf.map((it, i) => (
          <li key={i} className="flex items-start gap-2 text-sm text-muted-foreground">
            <span className="mt-2 size-1.5 shrink-0 rounded-full bg-muted-foreground/50" />
            <span>{renderInline(it)}</span>
          </li>
        ))}
      </ul>,
    );
    listBuf = [];
  };
  lines.forEach((raw, idx) => {
    const line = raw.trimEnd();
    if (line.startsWith('### ')) {
      flush(`l${idx}`);
      nodes.push(
        <h3
          key={idx}
          className="mb-2 mt-6 flex items-center gap-2 text-sm font-semibold uppercase tracking-wider text-muted-foreground first:mt-0"
        >
          <span className="h-px flex-1 bg-border" />
          {line.slice(4)}
          <span className="h-px flex-1 bg-border" />
        </h3>,
      );
      return;
    }
    if (line.startsWith('## ')) {
      flush(`l${idx}`);
      nodes.push(
        <h2 key={idx} className="mb-3 mt-8 text-base font-bold text-foreground first:mt-0">
          {line.slice(3)}
        </h2>,
      );
      return;
    }
    if (line.startsWith('- ') || line.startsWith('* ')) {
      listBuf.push(line.slice(2));
      return;
    }
    flush(`l${idx}`);
    if (line === '' || line === '---') {
      nodes.push(<div key={idx} className="my-3" />);
      return;
    }
    if (line.startsWith('**[') || line.match(/^\*\*[A-Z\s]+\*\*/)) {
      nodes.push(
        <p key={idx} className="mb-1 mt-4 text-sm font-semibold text-foreground">
          {renderInline(line)}
        </p>,
      );
      return;
    }
    nodes.push(
      <p key={idx} className="text-sm leading-relaxed text-muted-foreground">
        {renderInline(line)}
      </p>,
    );
  });
  flush('final');
  return <div className="space-y-0.5">{nodes}</div>;
}

// ─── Holdings signals table ───────────────────────────────────────────────────

function HoldingsTable({
  signals: rawSignals,
  profile,
}: {
  signals: HoldingSignal[] | null;
  profile: InvestorProfile;
}) {
  const [openId, setOpenId] = useState<number | null>(null);
  const [analysisTarget, setAnalysisTarget] = useState<{ id: number; name: string } | null>(null);

  const { data: holdings } = useQuery({
    queryKey: ['holdings'],
    queryFn: () => portfolioApi.holdings(),
    staleTime: 60_000,
  });

  // Build a map from instrument_id → financial data
  const holdingMap = new Map((holdings ?? []).map((h) => [h.instrument_id, h]));

  // Only show signals for actively-held positions.
  // If holdings data is still loading (map empty), show all temporarily.
  // Once loaded: hide if not in the map (portfolio treats it as closed)
  // OR if units are a rounding artifact (< 0.5 units remaining).
  const holdingsLoaded = holdings !== undefined;
  const signals = (rawSignals ?? []).filter(sig => {
    if (!holdingsLoaded) return true; // show all while loading
    const h = holdingMap.get(sig.instrument_id);
    if (!h) return false;             // portfolio doesn't consider this an active holding
    return h.total_units >= 0.5;      // filter rounding artifacts
  });

  const profileCfg = PROFILES[profile.riskProfile];

  return (
    <>
      {analysisTarget && (
        <InstrumentAnalysisPanel
          instrumentId={analysisTarget.id}
          instrumentName={analysisTarget.name}
          onClose={() => setAnalysisTarget(null)}
        />
      )}
      <div className="overflow-hidden rounded-xl border border-border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/30">
              <th className="px-4 py-3 text-left text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                Instrument
              </th>
              <th className="px-4 py-3 text-right text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                Invested
              </th>
              <th className="px-4 py-3 text-right text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                Value
              </th>
              <th className="px-4 py-3 text-right text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                Return
              </th>
              <th className="px-4 py-3 text-center text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                Signal
              </th>
            </tr>
          </thead>
          <tbody>
            {signals.map((sig) => {
              const h = holdingMap.get(sig.instrument_id);
              const action = ACTION_CFG[sig.action as SignalAction] ?? ACTION_CFG.HOLD;
              const ActionIcon = action.icon;
              const isOpen = openId === sig.instrument_id;
              const pct = h?.profit_loss_percent;

              return (
                <>
                  <tr
                    key={sig.instrument_id}
                    className={cn(
                      'border-b border-border transition-colors hover:bg-muted/20',
                      isOpen && 'bg-muted/10',
                    )}
                  >
                    {/* Instrument */}
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <span
                          className={cn(
                            'rounded border px-1.5 py-0.5 text-[10px] font-medium',
                            TYPE_COLORS[h?.asset_type ?? ''] ??
                              'border-gray-500/30 bg-gray-500/5 text-gray-400',
                          )}
                        >
                          {h?.asset_type ?? '—'}
                        </span>
                        {['MF', 'ETF', 'STOCK', 'US_FUND', 'METAL', 'GOLD'].includes(h?.asset_type ?? '') ? (
                          <div className="flex flex-col gap-0.5">
                            <button
                              onClick={() =>
                                setAnalysisTarget({
                                  id: sig.instrument_id,
                                  name: sig.instrument_name,
                                })
                              }
                              className="group flex items-center gap-1.5 text-left font-medium leading-tight transition-colors hover:text-primary"
                              title="Click to view full scorecard"
                            >
                              <span className="underline-offset-2 group-hover:underline">
                                {sig.instrument_name}
                              </span>
                              <LineChart className="size-3 shrink-0 text-muted-foreground/40 transition-colors group-hover:text-primary" />
                            </button>
                            {sig.reason && (
                              <p className="line-clamp-1 text-[10px] leading-relaxed text-muted-foreground/70">
                                {sig.reason}
                              </p>
                            )}
                          </div>
                        ) : (
                          <span className="font-medium leading-tight">{sig.instrument_name}</span>
                        )}
                      </div>
                    </td>

                    {/* Invested */}
                    <td className="px-4 py-3 text-right tabular-nums text-muted-foreground">
                      {h ? <MaskedAmount value={h.invested_amount} format={formatINR} /> : '—'}
                    </td>

                    {/* Current value */}
                    <td className="px-4 py-3 text-right font-medium tabular-nums">
                      {h?.current_value != null ? <MaskedAmount value={h.current_value} format={formatINR} /> : '—'}
                    </td>

                    {/* Return % */}
                    <td className="px-4 py-3 text-right tabular-nums">
                      {pct != null ? (
                        <span
                          className={pct >= 0 ? 'text-[hsl(var(--success))]' : 'text-destructive'}
                        >
                          {pct >= 0 ? '+' : ''}
                          {pct.toFixed(2)}%
                        </span>
                      ) : (
                        '—'
                      )}
                    </td>

                    {/* Signal — click to expand AI analysis */}
                    <td className="px-4 py-3">
                      <button
                        onClick={() => setOpenId(isOpen ? null : sig.instrument_id)}
                        title={isOpen ? 'Hide analysis' : 'View AI analysis'}
                        className={cn(
                          'group flex w-full flex-col items-center gap-1 rounded-lg border px-2.5 py-2 text-left transition-all',
                          isOpen
                            ? cn(action.border, action.bg, 'ring-1 ring-primary/20')
                            : cn('border-transparent hover:border-border', action.bg),
                        )}
                      >
                        <span className={cn('flex items-center gap-1.5 text-xs font-semibold', action.color)}>
                          <ActionIcon className="size-3" />
                          {action.label}
                        </span>
                        {sig.reason && (
                          <span className="line-clamp-1 w-full text-center text-[10px] text-muted-foreground group-hover:text-foreground/70">
                            {isOpen ? 'Click to collapse ↑' : 'View analysis ↓'}
                          </span>
                        )}
                      </button>
                    </td>
                  </tr>

                  {/* Expandable AI analysis row */}
                  {isOpen && (
                    <tr
                      key={`${sig.instrument_id}-detail`}
                      className="border-b border-border bg-muted/5"
                    >
                      <td colSpan={5} className="px-5 py-5">
                        <div className="grid gap-5 sm:grid-cols-[auto_1fr_auto]">

                          {/* Left — score + confidence */}
                          <div className="flex flex-col gap-4">
                            <div>
                              <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Confidence</p>
                              <ConfidenceBar value={sig.confidence} />
                            </div>
                            <ScoreDetailInRow instrumentId={sig.instrument_id} storedScore={sig.zero1_score} />
                          </div>

                          {/* Centre — reason + key points + qualitative */}
                          <div className="min-w-0 space-y-3">
                            {/* Summary reason */}
                            <p className="text-sm leading-relaxed text-foreground font-medium">{sig.reason}</p>

                            {/* Key data points */}
                            {sig.key_points?.length > 0 && (
                              <ul className="space-y-1.5">
                                {sig.key_points.map((pt, i) => (
                                  <li key={i} className="flex items-start gap-2 text-xs text-muted-foreground">
                                    <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-primary/40" />
                                    {pt}
                                  </li>
                                ))}
                              </ul>
                            )}

                            {/* Qualitative AI note */}
                            {sig.qualitative_note && (
                              <div className="rounded-md border border-border bg-muted/20 px-3 py-2">
                                <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-1">AI Context</p>
                                <p className="text-xs leading-relaxed text-muted-foreground italic">{sig.qualitative_note}</p>
                              </div>
                            )}

                            {/* Tax note */}
                            {sig.tax_note && (
                              <div className="flex items-start gap-2">
                                <span className="shrink-0 rounded border border-amber-500/30 bg-amber-500/8 px-1.5 py-0.5 text-[9px] font-semibold uppercase text-amber-400">TAX</span>
                                <p className="text-xs leading-relaxed text-amber-400/80">{sig.tax_note}</p>
                              </div>
                            )}
                          </div>

                          {/* Right — risk profile badge */}
                          <div className="shrink-0">
                            <div className={cn('flex items-center gap-1.5 rounded-full border px-2.5 py-1', profileCfg.border, profileCfg.bg)}>
                              <profileCfg.icon className={cn('size-3', profileCfg.color)} />
                              <span className={cn('text-[11px] font-medium', profileCfg.color)}>{profileCfg.label}</span>
                            </div>
                          </div>
                        </div>
                      </td>
                    </tr>
                  )}
                </>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

// ─── Streaming output panel (Analyse tab) ─────────────────────────────────────

function StreamOutput({
  state,
  content,
  error,
  emptyTitle,
  emptySubtitle,
  emptyAction,
  loadingTitle,
  loadingSubtitle,
}: {
  state: StreamState;
  content: string;
  error: string | null;
  emptyTitle: string;
  emptySubtitle: string;
  emptyAction: React.ReactNode;
  loadingTitle: string;
  loadingSubtitle: string;
}) {
  if (state === 'idle')
    return (
      <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border py-24">
        <div className="mb-5 grid h-16 w-16 place-items-center rounded-2xl border border-border bg-muted/30">
          <Sparkles className="size-7 text-muted-foreground" />
        </div>
        <p className="text-lg font-semibold">{emptyTitle}</p>
        <p className="mt-1.5 max-w-sm text-center text-sm text-muted-foreground">{emptySubtitle}</p>
        <div className="mt-6">{emptyAction}</div>
      </div>
    );
  if (state === 'error')
    return (
      <div className="bg-destructive/8 flex items-center gap-3 rounded-xl border border-destructive/30 p-4">
        <XCircle className="size-5 shrink-0 text-destructive" />
        <div>
          <p className="text-sm font-medium text-destructive">Analysis failed</p>
          <p className="text-xs text-muted-foreground">{error}</p>
        </div>
      </div>
    );
  return (
    <Card>
      <CardContent className="p-6">
        {state === 'streaming' && content === '' && (
          <div className="flex flex-col items-center justify-center py-16">
            <div className="relative mb-5">
              <div className="size-14 animate-ping rounded-full bg-primary/10" />
              <div className="absolute inset-0 grid place-items-center">
                <Sparkles className="size-6 text-primary" />
              </div>
            </div>
            <p className="font-semibold">{loadingTitle}</p>
            <p className="mt-1 text-sm text-muted-foreground">{loadingSubtitle}</p>
          </div>
        )}
        {content && (
          <div>
            <MarkdownBlock content={content} />
            {state === 'streaming' && (
              <span className="inline-block h-4 w-0.5 animate-pulse bg-primary/60 align-middle" />
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

function SignalPage() {
  const [tab, setTab] = useState('holdings');
  const [query, setQuery] = useState('');
  const [profile, setProfile] = useState<InvestorProfile>(() => {
    const saved = localStorage.getItem('wealthfolio_investor_profile');
    if (saved) {
      try {
        const parsed = JSON.parse(saved);
        return {
          riskProfile: parsed.riskProfile || 'moderate',
          goal: parsed.goal || '',
          horizon: parsed.horizon || '',
        };
      } catch (e) {
        // Ignore error
      }
    }
    return { riskProfile: 'moderate', goal: '', horizon: '' };
  });

  useEffect(() => {
    localStorage.setItem('wealthfolio_investor_profile', JSON.stringify(profile));
  }, [profile]);

  const qc = useQueryClient();
  const { data: aiSettings } = useQuery({
    queryKey: ['ai-settings'],
    queryFn: () => aiSettingsApi.get(),
    staleTime: 30_000,
  });
  const analyse = useStream();

  // Auto-load stored signals whenever the risk profile changes.
  const { data: storedSignals, isLoading: loadingStored } = useQuery({
    queryKey: ['signal-holdings', profile.riskProfile],
    queryFn: () => signalApi.loadHoldings(profile.riskProfile),
    staleTime: Infinity, // don't auto-refetch — user controls refresh
  });

  const signalsMut = useMutation({
    mutationFn: () =>
      signalApi.generateHoldings({
        risk_profile: profile.riskProfile,
        goal: profile.goal || 'Long-term wealth creation',
        horizon: profile.horizon || '5+ years',
      }),
    onSuccess: (data) => {
      qc.setQueryData(['signal-holdings', profile.riskProfile], data);
    },
  });

  const refreshScoresMut = useMutation({
    mutationFn: () => signalApi.refreshScores(),
    onSuccess: () => {
      // Invalidate all cached instrument-metrics so the table reloads fresh scores.
      qc.invalidateQueries({ queryKey: ['instrument-metrics'] });
    },
  });

  // The displayed signals: mutation result if freshly generated, otherwise stored.
  const signals = signalsMut.data ?? storedSignals;
  const hasSignals = (signals?.signals?.length ?? 0) > 0;

  const profileCfg = PROFILES[profile.riskProfile];

  const handleAnalyse = (q?: string) => {
    const target = (q ?? query).trim();
    if (!target) return;
    if (q) setQuery(q);
    analyse.stream('/api/ai/signal/analyse', { query: target, risk_profile: profile.riskProfile });
  };

  return (
    <div className="space-y-5">
      {/* Header */}
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <p className="text-sm uppercase tracking-wider text-muted-foreground">AI</p>
          <h1 className="flex items-center gap-2.5 text-3xl font-semibold tracking-tight">
            Signal <Sparkles className="size-6 text-primary/50" />
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            AI-powered buy · sell · hold intelligence for your portfolio
          </p>
        </div>
        <div className="flex flex-col items-end gap-1.5">
          <div className="bg-amber-500/8 flex items-center gap-2 rounded-full border border-amber-500/30 px-3.5 py-1.5">
            <AlertTriangle className="size-3.5 text-amber-400" />
            <span className="text-xs text-amber-400/90">
              Informational only — not financial advice
            </span>
          </div>
          <p className="text-[11px] text-muted-foreground/60">
            Holdings data (instruments, amounts, dates) is sent to your AI provider. Name is never
            sent.
          </p>
        </div>
      </header>

      {/* Not configured banner */}
      {aiSettings && !aiSettings.configured && (
        <div className="bg-amber-500/8 flex items-center justify-between gap-4 rounded-lg border border-amber-500/30 px-4 py-3">
          <div className="flex items-center gap-2.5">
            <AlertTriangle className="size-4 shrink-0 text-amber-400" />
            <p className="text-sm text-amber-300">
              No AI provider configured — Signal needs an API key to generate analysis.
            </p>
          </div>
          <Link
            to="/settings"
            search={{ tab: 'ai' } as any}
            className="shrink-0 rounded-md border border-amber-500/40 bg-amber-500/15 px-3 py-1.5 text-xs font-medium text-amber-300 transition-colors hover:bg-amber-500/25"
          >
            Settings → AI
          </Link>
        </div>
      )}

      {/* Profile */}
      <ProfileSelector profile={profile} onChange={setProfile} />

      {/* Tabs */}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="grid w-64 grid-cols-2">
          <TabsTrigger value="holdings" className="flex items-center gap-1.5 text-xs">
            <BarChart3 className="size-3.5" /> Holdings
          </TabsTrigger>
          <TabsTrigger value="analyse" className="flex items-center gap-1.5 text-xs">
            <Search className="size-3.5" /> Analyse
          </TabsTrigger>
        </TabsList>

        {/* ── Holdings tab ── */}
        <TabsContent value="holdings" className="mt-5">
          <div className="space-y-4">
            {/* Toolbar */}
            {hasSignals && (
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2.5">
                  <div
                    className={cn(
                      'flex items-center gap-2 rounded-full border px-3 py-1',
                      profileCfg.border,
                      profileCfg.bg,
                    )}
                  >
                    <profileCfg.icon className={cn('size-3.5', profileCfg.color)} />
                    <span className={cn('text-xs font-medium', profileCfg.color)}>
                      {profileCfg.label} · {signals?.signals.length ?? 0} holdings
                    </span>
                  </div>
                  {signals?.generated_at && (
                    <span className="flex items-center gap-1 text-[11px] text-muted-foreground">
                      <Clock className="size-3" />
                      {formatRelative(signals.generated_at)}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  {refreshScoresMut.isSuccess && (
                    <span className="text-[11px] text-emerald-400">
                      ✓ {refreshScoresMut.data?.success}/{refreshScoresMut.data?.total} scores updated
                    </span>
                  )}
                  {refreshScoresMut.isError && (
                    <span className="text-[11px] text-destructive">Refresh failed</span>
                  )}
                  <button
                    onClick={() => refreshScoresMut.mutate()}
                    disabled={refreshScoresMut.isPending}
                    title="Re-compute and cache scores for all holdings"
                    className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground disabled:opacity-50"
                  >
                    <RefreshCw className={cn('size-3.5', refreshScoresMut.isPending && 'animate-spin')} />
                    {refreshScoresMut.isPending ? 'Refreshing…' : 'Refresh Scores'}
                  </button>
                </div>
              </div>
            )}

            {/* Loading stored signals */}
            {loadingStored && (
              <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> Loading saved signals…
              </div>
            )}

            {/* Generating state */}
            {signalsMut.isPending && (
              <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border py-20">
                <div className="relative mb-5">
                  <div className="size-14 animate-ping rounded-full bg-primary/10" />
                  <div className="absolute inset-0 grid place-items-center">
                    <Sparkles className="size-6 text-primary" />
                  </div>
                </div>
                <p className="font-semibold">Analysing your holdings…</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  Reading holdings, computing LTCG/STCG status, generating signals
                </p>
              </div>
            )}

            {/* Error */}
            {signalsMut.isError && (
              <div className="bg-destructive/8 flex items-center gap-3 rounded-xl border border-destructive/30 p-4">
                <XCircle className="size-5 shrink-0 text-destructive" />
                <div>
                  <p className="text-sm font-medium text-destructive">Analysis failed</p>
                  <p className="text-xs text-muted-foreground">
                    {(signalsMut.error as Error).message}
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  className="ml-auto"
                  onClick={() => signalsMut.mutate()}
                >
                  Retry
                </Button>
              </div>
            )}

            {/* Empty state */}
            {!loadingStored && !signalsMut.isPending && !hasSignals && (
              <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border py-24">
                <div className="mb-5 grid h-16 w-16 place-items-center rounded-2xl border border-border bg-muted/30">
                  <Activity className="size-7 text-muted-foreground" />
                </div>
                <p className="text-lg font-semibold">Zero1 Analysis Ready</p>
                <p className="mt-1.5 max-w-sm text-center text-sm text-muted-foreground">
                  AI signal generation is paused. Generate signals once to populate the table, then
                  click any MF fund name for the full Zero1 scorecard.
                </p>
                <Button className="mt-7" onClick={() => signalsMut.mutate()} variant="outline">
                  <Sparkles className="size-4" /> Generate Signals Once
                </Button>
              </div>
            )}

            {/* Table */}
            {!signalsMut.isPending && hasSignals && signals && (
              <HoldingsTable signals={signals.signals} profile={profile} />
            )}
          </div>
        </TabsContent>

        {/* ── Analyse tab ── */}
        <TabsContent value="analyse" className="mt-5">
          <div className="space-y-5">
            <div className="flex gap-3">
              <Input
                placeholder="Stock name, ISIN or symbol — e.g. Tata Motors, RELIANCE"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleAnalyse()}
                className="max-w-lg"
              />
              <Button
                onClick={() => handleAnalyse()}
                disabled={analyse.state === 'streaming' || !query.trim()}
              >
                {analyse.state === 'streaming' ? (
                  <>
                    <Loader2 className="size-4 animate-spin" /> Analysing…
                  </>
                ) : (
                  <>
                    <Sparkles className="size-4" /> Analyse
                  </>
                )}
              </Button>
              {analyse.state !== 'idle' && (
                <Button variant="ghost" size="icon" onClick={analyse.reset}>
                  <XCircle className="size-4 text-muted-foreground" />
                </Button>
              )}
            </div>

            {analyse.state !== 'idle' && (
              <div
                className={cn(
                  'flex w-fit items-center gap-2 rounded-full border px-3 py-1',
                  profileCfg.border,
                  profileCfg.bg,
                )}
              >
                <profileCfg.icon className={cn('size-3.5', profileCfg.color)} />
                <span className={cn('text-xs font-medium', profileCfg.color)}>
                  {profileCfg.label} lens · {query}
                </span>
              </div>
            )}

            <StreamOutput
              state={analyse.state}
              content={analyse.content}
              error={analyse.error}
              emptyTitle="Analyse any stock or fund"
              emptySubtitle="Get short-term, swing, and long-term outlook with entry/exit price levels, tax implications, and risk assessment."
              emptyAction={
                <div className="flex flex-col items-center gap-3">
                  <p className="text-xs text-muted-foreground">Try one of these:</p>
                  <div className="flex flex-wrap justify-center gap-2">
                    {SUGGESTIONS.map((s) => (
                      <button
                        key={s}
                        onClick={() => handleAnalyse(s)}
                        className="flex items-center gap-1 rounded-full border border-border px-3 py-1 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground"
                      >
                        {s} <ChevronRight className="size-3" />
                      </button>
                    ))}
                  </div>
                </div>
              }
              loadingTitle={`Analysing ${query}…`}
              loadingSubtitle="Processing fundamentals, technicals, valuation, and tax implications"
            />
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
