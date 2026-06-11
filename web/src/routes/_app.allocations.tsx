import { useState, useEffect } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer } from 'recharts';
import {
  Pencil,
  Check,
  X,
  Calculator,
  Copy,
  CheckCheck,
  Loader2,
  Wallet,
  TrendingUp,
  Sliders,
  ArrowUpRight,
  ArrowDownRight,
  Filter,
  Sparkles,
  Scale,
  Minus,
  Brain,
} from 'lucide-react';

import {
  allocationsApi,
  type InstrumentAllocation,
  type CategoryAllocation,
  type AllocCategory,
  type RebalanceAction,
  type AllocSuggestResult,
  type TrendTag,
} from '@/lib/api';
import { formatINR as formatCurrency } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { MaskedAmount } from '@/lib/privacy';

export const Route = createFileRoute('/_app/allocations')({
  component: AllocationsPage,
});

const ALLOC_COLORS: Record<AllocCategory, string> = {
  EQUITY: '#6366f1',
  METALS: '#f59e0b',
  DEBT: '#14b8a6',
  US_EQUITY: '#3b82f6',
  OTHERS: '#94a3b8',
};

const ALLOC_LABELS: Record<AllocCategory, string> = {
  EQUITY: 'Equity',
  METALS: 'Metals',
  DEBT: 'Debt',
  US_EQUITY: 'US Equity',
  OTHERS: 'Others',
};

// --- Deviation Badge ---
function DeviationBadge({ value }: { value: number }) {
  const pos = value >= 0;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-bold tabular-nums transition-all duration-200',
        pos
          ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600 shadow-[0_0_10px_rgba(16,185,129,0.05)] dark:text-emerald-400'
          : 'border-rose-500/20 bg-rose-500/10 text-rose-600 shadow-[0_0_10px_rgba(244,63,94,0.05)] dark:text-rose-400',
      )}
    >
      {pos ? (
        <ArrowUpRight className="size-3 animate-pulse text-emerald-500" />
      ) : (
        <span className="mr-0.5 size-1 animate-pulse rounded-full bg-rose-500" />
      )}
      {pos ? '+' : ''}
      {value.toFixed(2)}%
    </span>
  );
}

// --- Rebalance suggestion cells ---
function NoTargetHint({ label = 'set target' }: { label?: string }) {
  return (
    <span className="text-[11px] font-medium italic text-muted-foreground/45">— {label} —</span>
  );
}

function OnTargetBadge() {
  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-border/40 bg-muted/30 px-2.5 py-0.5 text-[11px] font-semibold text-muted-foreground/80">
      <Check className="size-3 text-emerald-500" />
      On target
    </span>
  );
}

function RebalancePill({
  action,
  label,
  sub,
}: {
  action: Extract<RebalanceAction, 'SELL' | 'BUY'>;
  label: React.ReactNode;
  sub?: React.ReactNode;
}) {
  const sell = action === 'SELL';
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span
        className={cn(
          'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-[11px] font-bold tabular-nums',
          sell
            ? 'border-rose-500/25 bg-rose-500/10 text-rose-600 dark:text-rose-400'
            : 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
        )}
      >
        {sell ? <ArrowDownRight className="size-3" /> : <ArrowUpRight className="size-3" />}
        {label}
      </span>
      {sub && <span className="text-[10px] tabular-nums text-muted-foreground/70">{sub}</span>}
    </div>
  );
}

function InstrumentRebalanceCell({ ia }: { ia: InstrumentAllocation }) {
  if (!ia.has_target) return <NoTargetHint />;
  if (ia.rebalance_action === 'HOLD' || ia.rebalance_action === '') return <OnTargetBadge />;

  const sell = ia.rebalance_action === 'SELL';
  const amt = Math.abs(ia.rebalance_amount);

  // Unit-traded assets (ETF / Stock / Gold ETF): suggest whole units.
  // Floor when selling (don't oversell), ceil when buying (cover the gap).
  if (ia.unitized) {
    const rawUnits = Math.abs(ia.rebalance_units);
    const units = sell ? Math.floor(rawUnits) : Math.ceil(rawUnits);
    if (units < 1) {
      return <span className="text-[11px] text-muted-foreground/70">within 1 unit</span>;
    }
    return (
      <RebalancePill
        action={ia.rebalance_action}
        label={`${sell ? 'Sell' : 'Buy'} ${units} unit${units === 1 ? '' : 's'}`}
        sub={
          <>
            ≈ <MaskedAmount value={amt} format={formatCurrency} />
          </>
        }
      />
    );
  }

  // Amount-based assets (MF / US funds): suggest a rupee amount.
  return (
    <RebalancePill
      action={ia.rebalance_action}
      label={
        <>
          {sell ? 'Redeem ' : 'Invest '}
          <MaskedAmount value={amt} format={formatCurrency} />
        </>
      }
    />
  );
}

function CategoryRebalanceCell({ ca }: { ca: CategoryAllocation }) {
  if (ca.rebalance_action === '') return <NoTargetHint />;
  if (ca.rebalance_action === 'HOLD') return <OnTargetBadge />;
  const sell = ca.rebalance_action === 'SELL';
  const amt = Math.abs(ca.rebalance_amount);
  return (
    <RebalancePill
      action={ca.rebalance_action}
      label={
        <>
          {sell ? 'Trim ' : 'Add '}
          <MaskedAmount value={amt} format={formatCurrency} />
        </>
      }
    />
  );
}

const ALLOC_CATEGORIES: AllocCategory[] = ['EQUITY', 'METALS', 'DEBT', 'US_EQUITY', 'OTHERS'];

// --- Editable allocation-category chip ---
function CategorySelect({
  value,
  onChange,
}: {
  value: AllocCategory;
  onChange: (v: AllocCategory) => void;
}) {
  const color = ALLOC_COLORS[value];
  return (
    <Select value={value} onValueChange={(v) => onChange(v as AllocCategory)}>
      <SelectTrigger
        className="h-7 w-[124px] gap-1 rounded-full border px-2.5 py-0.5 text-[10px] font-bold tracking-wide shadow-none focus:ring-1 focus:ring-violet-500/20"
        style={{ borderColor: color + '40', color, backgroundColor: color + '0d' }}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent className="rounded-xl border-border/60 bg-card/95 backdrop-blur-md">
        {ALLOC_CATEGORIES.map((c) => (
          <SelectItem key={c} value={c} className="text-xs font-medium">
            <span className="flex items-center gap-2">
              <span className="size-2 rounded-full" style={{ backgroundColor: ALLOC_COLORS[c] }} />
              {ALLOC_LABELS[c]}
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// --- Inline editable cell ---
interface EditableCellProps {
  value: number;
  onSave: (v: number) => Promise<unknown>;
  format?: 'percent' | 'currency';
}

function EditableCell({ value, onSave, format = 'percent' }: EditableCellProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);

  const start = () => {
    setDraft(String(value));
    setEditing(true);
  };
  const cancel = () => setEditing(false);

  const save = async () => {
    const n = parseFloat(draft);
    if (isNaN(n) || n < 0) {
      cancel();
      return;
    }
    setSaving(true);
    try {
      await onSave(n);
    } catch (err) {
      console.error(err);
    } finally {
      setSaving(false);
      setEditing(false);
    }
  };

  if (!editing) {
    return (
      <span
        className="group inline-flex cursor-pointer items-center gap-1.5 rounded-lg border border-transparent px-2.5 py-1 font-semibold transition-all hover:border-border/40 hover:bg-muted/40"
        onClick={start}
      >
        <span className="tabular-nums text-foreground">
          {format === 'percent' ? `${value.toFixed(2)}%` : formatCurrency(value)}
        </span>
        <Pencil className="size-3 text-muted-foreground/80 opacity-0 transition-opacity group-hover:opacity-100" />
      </span>
    );
  }

  return (
    <span className="relative z-10 inline-flex items-center gap-1.5 rounded-xl border border-border/80 bg-muted/65 p-1 shadow-inner">
      <Input
        className="h-7 w-20 border-0 bg-transparent px-2 py-0 text-xs font-bold tabular-nums text-foreground focus:bg-transparent focus-visible:ring-0 focus-visible:ring-offset-0"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save();
          if (e.key === 'Escape') cancel();
        }}
        autoFocus
      />
      <Button
        size="icon"
        variant="ghost"
        className="size-6 rounded-lg text-muted-foreground/90 transition-colors hover:bg-emerald-500/10 hover:text-emerald-600"
        onClick={save}
        disabled={saving}
      >
        {saving ? <Loader2 className="size-3 animate-spin" /> : <Check className="size-3.5" />}
      </Button>
      <Button
        size="icon"
        variant="ghost"
        className="size-6 rounded-lg text-muted-foreground/90 transition-colors hover:bg-rose-500/10 hover:text-rose-600"
        onClick={cancel}
        disabled={saving}
      >
        <X className="size-3.5" />
      </Button>
    </span>
  );
}

// --- Custom Pie Tooltip ---
function CustomPieTooltip({ active, payload }: any) {
  if (active && payload && payload.length) {
    const data = payload[0].payload;
    return (
      <div className="rounded-2xl border border-border/80 bg-card/90 p-4 shadow-xl backdrop-blur-md">
        <div className="mb-1.5 flex items-center gap-2">
          <span className="size-2.5 rounded-full" style={{ backgroundColor: data.color }} />
          <span className="text-xs font-bold text-foreground">{data.name}</span>
        </div>
        <div className="space-y-1">
          <div className="flex justify-between gap-6 text-[11px] text-muted-foreground">
            <span>Current Value:</span>
            <span className="font-semibold tabular-nums text-foreground">
              <MaskedAmount value={data.value} format={formatCurrency} />
            </span>
          </div>
          <div className="flex justify-between gap-6 text-[11px] text-muted-foreground">
            <span>Allocation Share:</span>
            <span className="font-bold tabular-nums text-foreground">
              {data.percent.toFixed(2)}%
            </span>
          </div>
        </div>
      </div>
    );
  }
  return null;
}

// --- Category View Tab ---
function CategoryView({ data }: { data: CategoryAllocation[] }) {
  const qc = useQueryClient();

  const updateMut = useMutation({
    mutationFn: ({
      name,
      body,
    }: {
      name: AllocCategory;
      body: Parameters<typeof allocationsApi.updateCategory>[1];
    }) => allocationsApi.updateCategory(name, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['allocations'] }),
  });

  const pieData = data
    .map((ca) => ({
      name: ALLOC_LABELS[ca.alloc_category],
      value: ca.current_value,
      color: ALLOC_COLORS[ca.alloc_category],
      percent: ca.current_percent,
    }))
    .filter((d) => d.value > 0);

  const totalValuation = pieData.reduce((sum, item) => sum + item.value, 0);

  return (
    <div className="space-y-6">
      <div className="grid gap-6 lg:grid-cols-12">
        {/* Chart Card */}
        {pieData.length > 0 && (
          <Card className="flex flex-col justify-between rounded-2xl border border-border/60 bg-card/45 p-6 shadow-sm backdrop-blur-sm lg:col-span-5">
            <div>
              <h3 className="mb-1 text-sm font-bold text-foreground">Asset Allocation Chart</h3>
              <p className="text-xs text-muted-foreground">
                Visual distribution of current assets.
              </p>
            </div>

            <div className="relative my-auto flex flex-col items-center justify-center py-6">
              {/* Center label */}
              <div className="pointer-events-none absolute inset-0 z-0 flex flex-col items-center justify-center">
                <span className="text-[10px] font-bold uppercase tracking-widest text-muted-foreground/80">
                  Total Assets
                </span>
                <span className="mt-0.5 text-lg font-extrabold tabular-nums text-foreground">
                  <MaskedAmount value={totalValuation} format={formatCurrency} />
                </span>
              </div>

              <ResponsiveContainer width="100%" height={230}>
                <PieChart key={pieData.length}>
                  <Pie
                    data={pieData}
                    cx="50%"
                    cy="50%"
                    innerRadius={68}
                    outerRadius={88}
                    paddingAngle={4}
                    dataKey="value"
                    isAnimationActive={true}
                    animationDuration={1200}
                    animationEasing="ease-in-out"
                  >
                    {pieData.map((entry, i) => (
                      <Cell key={i} fill={entry.color} stroke="hsl(var(--card))" strokeWidth={3} />
                    ))}
                  </Pie>
                  <Tooltip content={<CustomPieTooltip />} />
                </PieChart>
              </ResponsiveContainer>
            </div>

            {/* Custom styled visual legends list */}
            <div className="flex flex-wrap items-center justify-center gap-2.5 border-t border-border/40 pt-4">
              {pieData.map((item, idx) => (
                <div
                  key={idx}
                  className="flex cursor-default items-center gap-1.5 rounded-lg border border-border/30 bg-muted/30 px-2 py-1 text-xs font-semibold text-muted-foreground/80 transition-colors hover:text-foreground"
                >
                  <span
                    className="size-2 animate-pulse rounded-full shadow-inner"
                    style={{ backgroundColor: item.color }}
                  />
                  <span>{item.name}</span>
                  <span className="ml-0.5 text-[10px] font-bold tabular-nums text-foreground">
                    {item.percent.toFixed(1)}%
                  </span>
                </div>
              ))}
            </div>
          </Card>
        )}

        {/* Category Table Card */}
        <Card
          className={cn(
            pieData.length > 0 ? 'lg:col-span-7' : 'lg:col-span-12',
            'flex flex-col justify-between overflow-hidden rounded-2xl border border-border/60 bg-card/45 shadow-sm backdrop-blur-sm',
          )}
        >
          <div>
            <div className="border-b border-border/40 bg-muted/10 p-6 pb-4">
              <h3 className="text-sm font-bold text-foreground">Category Target Allocations</h3>
              <p className="text-xs text-muted-foreground">
                Adjust target percentages and SIP allocation targets below.
              </p>
            </div>
            <div className="scrollbar-thin overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border/40 bg-muted/20">
                    <th className="px-5 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      Category
                    </th>
                    <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      Value
                    </th>
                    <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      Current%
                    </th>
                    <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      Target%
                    </th>
                    <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      Deviation
                    </th>
                    <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      Rebalance
                    </th>
                    <th className="px-5 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                      SIP Target%
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/40">
                  {data.map((ca) => (
                    <tr
                      key={ca.alloc_category}
                      className="group transition-colors duration-150 hover:bg-muted/30"
                    >
                      <td className="px-5 py-3.5">
                        <div className="flex items-center gap-2.5">
                          <div
                            className="h-3 w-3 rounded-full border border-white/10 shadow-inner"
                            style={{ backgroundColor: ALLOC_COLORS[ca.alloc_category] }}
                          />
                          <span className="font-semibold text-foreground">
                            {ALLOC_LABELS[ca.alloc_category]}
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-3.5 text-right font-semibold tabular-nums text-foreground/80">
                        <MaskedAmount value={ca.current_value} format={formatCurrency} />
                      </td>
                      <td className="px-4 py-3.5 text-right font-semibold tabular-nums text-muted-foreground/90">
                        {ca.current_percent.toFixed(2)}%
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <EditableCell
                          value={ca.target_percent}
                          onSave={(v) =>
                            updateMut.mutateAsync({
                              name: ca.alloc_category,
                              body: { target_percent: v },
                            })
                          }
                        />
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <DeviationBadge value={ca.deviation} />
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <CategoryRebalanceCell ca={ca} />
                      </td>
                      <td className="px-5 py-3.5 text-right">
                        <EditableCell
                          value={ca.sip_target_percent}
                          onSave={(v) =>
                            updateMut.mutateAsync({
                              name: ca.alloc_category,
                              body: { sip_target_percent: v },
                            })
                          }
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          <div className="flex items-center gap-1.5 border-t border-border/40 bg-muted/10 p-4 text-[11px] text-muted-foreground">
            <span className="size-1.5 animate-pulse rounded-full bg-violet-500" />
            Click on any Target% or SIP Target% to edit and update your portfolio plan.
          </div>
        </Card>
      </div>
    </div>
  );
}

// --- Instrument View Tab ---
function InstrumentView({
  data,
  totalValue: _totalValue,
}: {
  data: InstrumentAllocation[];
  totalValue: number;
}) {
  const qc = useQueryClient();
  const [filterCat, setFilterCat] = useState<string>('ALL');

  const updateMut = useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: number;
      body: Parameters<typeof allocationsApi.updateInstrument>[1];
    }) => allocationsApi.updateInstrument(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['allocations'] }),
  });

  const filtered = filterCat === 'ALL' ? data : data.filter((d) => d.alloc_category === filterCat);

  return (
    <div className="space-y-4">
      <Card className="rounded-2xl border border-border/60 bg-card/45 p-4 shadow-sm backdrop-blur-sm">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <div className="relative">
              <Select value={filterCat} onValueChange={setFilterCat}>
                <SelectTrigger className="h-10 w-48 rounded-xl border-border/60 bg-card/60 text-xs font-semibold shadow-inner focus:ring-1 focus:ring-violet-500/20">
                  <span className="flex items-center gap-2 text-foreground">
                    <Filter className="size-3.5 text-muted-foreground/80" />
                    <SelectValue />
                  </span>
                </SelectTrigger>
                <SelectContent className="rounded-xl border-border/60 bg-card/95 backdrop-blur-md">
                  <SelectItem value="ALL" className="text-xs font-medium">
                    All Categories
                  </SelectItem>
                  {(['EQUITY', 'METALS', 'DEBT', 'US_EQUITY', 'OTHERS'] as AllocCategory[]).map(
                    (c) => (
                      <SelectItem key={c} value={c} className="text-xs font-medium">
                        {ALLOC_LABELS[c]}
                      </SelectItem>
                    ),
                  )}
                </SelectContent>
              </Select>
            </div>
            <span className="rounded-xl border border-border/30 bg-muted/30 px-3 py-1.5 text-xs font-semibold text-muted-foreground/80">
              {filtered.length} instruments matching
            </span>
          </div>
        </div>
      </Card>

      <Card className="overflow-hidden rounded-2xl border border-border/60 bg-card/45 shadow-sm backdrop-blur-sm">
        <CardContent className="p-0">
          <div className="scrollbar-thin overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border/40 bg-muted/20">
                  <th className="px-5 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Instrument
                  </th>
                  <th className="px-4 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Category
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Value
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Current%
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Target%
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Deviation
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Rebalance
                  </th>
                  <th className="px-5 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    SIP Allocation
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/40">
                {filtered.length === 0 ? (
                  <tr>
                    <td colSpan={8} className="px-4 py-16 text-center text-muted-foreground">
                      <div className="mx-auto flex max-w-sm flex-col items-center justify-center text-muted-foreground/80">
                        <div className="mb-3 rounded-full border border-border/40 bg-muted/30 p-4">
                          <Wallet className="size-8 stroke-[1.5] text-muted-foreground/60" />
                        </div>
                        <p className="text-sm font-bold text-foreground">
                          No instrument allocations found
                        </p>
                        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                          Add instruments via the Holdings page, then configure their targets here.
                        </p>
                      </div>
                    </td>
                  </tr>
                ) : (
                  filtered.map((ia) => (
                    <tr
                      key={ia.instrument_id}
                      className="group transition-colors duration-150 hover:bg-muted/30"
                    >
                      <td className="px-5 py-3.5">
                        <div className="font-semibold text-foreground">{ia.instrument_name}</div>
                        <div className="mt-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground/55">
                          {ia.asset_type.replace('_', ' ')} ·{' '}
                          {ia.unitized ? 'units' : 'amount'}
                        </div>
                      </td>
                      <td className="px-4 py-3.5">
                        <CategorySelect
                          value={ia.alloc_category}
                          onChange={(v) =>
                            updateMut.mutate({
                              id: ia.instrument_id,
                              body: { alloc_category: v },
                            })
                          }
                        />
                      </td>
                      <td className="px-4 py-3.5 text-right font-semibold tabular-nums text-foreground/80">
                        <MaskedAmount value={ia.current_value} format={formatCurrency} />
                      </td>
                      <td className="px-4 py-3.5 text-right font-semibold tabular-nums text-muted-foreground/90">
                        {ia.current_percent.toFixed(2)}%
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <EditableCell
                          value={ia.target_percent}
                          onSave={(v) =>
                            updateMut.mutateAsync({
                              id: ia.instrument_id,
                              body: { target_percent: v },
                            })
                          }
                        />
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <DeviationBadge value={ia.deviation} />
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <InstrumentRebalanceCell ia={ia} />
                      </td>
                      <td className="px-5 py-3.5 text-right">
                        <EditableCell
                          value={ia.sip_amount}
                          format="currency"
                          onSave={(v) =>
                            updateMut.mutateAsync({ id: ia.instrument_id, body: { sip_amount: v } })
                          }
                        />
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-border/40 bg-muted/10 p-4 text-[11px] text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <ArrowDownRight className="size-3 text-rose-500" /> Sell / Redeem when overweight
            </span>
            <span className="inline-flex items-center gap-1.5">
              <ArrowUpRight className="size-3 text-emerald-500" /> Buy / Invest when underweight
            </span>
            <span className="text-muted-foreground/70">
              ETF, stock &amp; gold suggestions are in units; mutual funds &amp; US funds in ₹.
            </span>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// --- Distribution Calculator ---
function DistributionCalculator() {
  const [amount, setAmount] = useState('');
  const [copied, setCopied] = useState(false);
  const calcMut = useMutation({
    mutationFn: () => allocationsApi.calculateDistribution(Number(amount)),
  });

  const copyText = () => {
    if (!calcMut.data) return;
    const lines = calcMut.data.items.map(
      (item) =>
        `${item.instrument_name}: ${formatCurrency(item.amount)} (${item.target_percent.toFixed(1)}%)`,
    );
    navigator.clipboard.writeText(lines.join('\n'));
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const categorySummary = calcMut.data
    ? calcMut.data.items.reduce(
        (acc, item) => {
          const cat = item.alloc_category;
          acc[cat] = (acc[cat] || 0) + item.amount;
          return acc;
        },
        {} as Record<AllocCategory, number>,
      )
    : null;

  return (
    <Card className="space-y-6 rounded-2xl border border-border/60 bg-card/45 p-6 shadow-sm backdrop-blur-sm">
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <div className="rounded-2xl border border-violet-500/15 bg-violet-500/10 p-3 text-violet-500 shadow-inner">
            <Calculator className="size-6 stroke-[1.5]" />
          </div>
          <div className="space-y-0.5">
            <h3 className="text-lg font-bold text-foreground">
              Investment Distribution Calculator
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Enter a lump-sum amount to instantly split it by your target allocations. Use{' '}
              <span className="font-semibold text-violet-500">AI Suggest targets</span> first to make
              this distribution trend-aware.
            </p>
          </div>
        </div>

        <div className="flex max-w-lg flex-wrap items-center gap-3 border-t border-border/40 pt-5">
          <div className="relative min-w-[200px] flex-1">
            <span className="absolute left-3.5 top-1/2 -translate-y-1/2 select-none text-xs font-bold text-muted-foreground/85">
              ₹
            </span>
            <Input
              type="number"
              placeholder="Investment Amount (₹)"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              className="h-10 w-full rounded-xl border-border/60 bg-card/50 pl-7 text-xs font-semibold tabular-nums text-foreground transition-all hover:bg-card focus:ring-1 focus:ring-violet-500/20 focus-visible:bg-card"
              onKeyDown={(e) => {
                if (e.key === 'Enter' && amount && Number(amount) > 0) {
                  calcMut.mutate();
                }
              }}
            />
          </div>
          <Button
            onClick={() => calcMut.mutate()}
            disabled={!amount || Number(amount) <= 0 || calcMut.isPending}
            className="h-10 cursor-pointer rounded-xl border-0 bg-gradient-to-r from-violet-600 to-indigo-600 px-6 text-xs font-bold text-white shadow-sm shadow-primary/20 transition-all duration-300 hover:scale-[1.01] hover:from-violet-500 hover:to-indigo-500"
          >
            {calcMut.isPending ? (
              <span className="flex items-center gap-2">
                <Loader2 className="size-3.5 animate-spin" /> Calculating…
              </span>
            ) : (
              'Calculate Distribution'
            )}
          </Button>
        </div>
      </div>

      {calcMut.data && (
        <div className="space-y-6 border-t border-border/40 pt-6">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <p className="text-xs font-bold uppercase tracking-wider text-muted-foreground">
                Optimal Distribution Breakdown
              </p>
              <h4 className="mt-0.5 text-lg font-black tabular-nums text-foreground">
                Target Allocation for{' '}
                <MaskedAmount value={calcMut.data.amount} format={formatCurrency} />
              </h4>
            </div>
            <Button
              size="sm"
              variant="outline"
              onClick={copyText}
              className={cn(
                'flex h-9 items-center gap-2 rounded-xl border-border/60 px-4 text-xs font-bold shadow-sm transition-all duration-300 hover:bg-muted/40',
                copied &&
                  'border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400',
              )}
            >
              {copied ? (
                <CheckCheck className="size-4 animate-bounce text-emerald-500" />
              ) : (
                <Copy className="size-3.5 text-muted-foreground/80" />
              )}
              {copied ? 'Copied Details' : 'Copy Breakdown'}
            </Button>
          </div>

          <div className="grid gap-6 md:grid-cols-12">
            {/* Category breakdown visual representation */}
            {categorySummary && (
              <div className="space-y-4 rounded-2xl border border-border/50 bg-muted/10 p-5 md:col-span-4">
                <div>
                  <h4 className="text-xs font-extrabold uppercase tracking-wider text-foreground">
                    Category Allocation
                  </h4>
                  <p className="text-[10px] text-muted-foreground">
                    Distribution summary by category classes.
                  </p>
                </div>
                <div className="space-y-3">
                  {(Object.keys(categorySummary) as AllocCategory[]).map((cat) => {
                    const value = categorySummary[cat] || 0;
                    const percent = (value / calcMut.data.amount) * 100;
                    return (
                      <div key={cat} className="space-y-1">
                        <div className="flex justify-between text-xs font-semibold">
                          <span className="text-muted-foreground">{ALLOC_LABELS[cat]}</span>
                          <span className="tabular-nums text-foreground">
                            {percent.toFixed(1)}%
                          </span>
                        </div>
                        <div className="h-2 w-full overflow-hidden rounded-full border border-border/30 bg-muted/60">
                          <div
                            className="h-full rounded-full shadow-inner transition-all duration-500"
                            style={{
                              backgroundColor: ALLOC_COLORS[cat],
                              width: `${percent}%`,
                            }}
                          />
                        </div>
                        <div className="flex justify-between text-[10px] tabular-nums text-muted-foreground/80">
                          <span>Amount:</span>
                          <span className="font-bold text-foreground/80">
                            <MaskedAmount value={value} format={formatCurrency} />
                          </span>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {/* Distribution details table */}
            <div
              className={cn(
                categorySummary ? 'md:col-span-8' : 'md:col-span-12',
                'overflow-hidden rounded-2xl border border-border/60 bg-card/45 shadow-sm',
              )}
            >
              <div className="scrollbar-thin overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border/40 bg-muted/20">
                      <th className="px-4 py-3 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                        Instrument
                      </th>
                      <th className="px-4 py-3 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                        Category
                      </th>
                      <th className="px-4 py-3 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                        Target Allocation
                      </th>
                      <th className="px-4 py-3 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                        Suggested Amount
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border/40">
                    {calcMut.data.items.map((item) => (
                      <tr
                        key={item.instrument_id}
                        className="transition-colors duration-150 hover:bg-muted/30"
                      >
                        <td className="px-4 py-3 font-semibold text-foreground">
                          {item.instrument_name}
                        </td>
                        <td className="px-4 py-3">
                          <Badge
                            variant="outline"
                            className="rounded-full border bg-transparent px-2 py-0.5 text-[9px] font-bold tracking-wide"
                            style={{
                              borderColor: ALLOC_COLORS[item.alloc_category] + '30',
                              color: ALLOC_COLORS[item.alloc_category],
                              backgroundColor: ALLOC_COLORS[item.alloc_category] + '0d',
                            }}
                          >
                            {ALLOC_LABELS[item.alloc_category]}
                          </Badge>
                        </td>
                        <td className="px-4 py-3 text-right font-semibold tabular-nums text-muted-foreground/90">
                          {item.target_percent.toFixed(1)}%
                        </td>
                        <td className="px-4 py-3 text-right font-extrabold tabular-nums text-foreground">
                          <MaskedAmount value={item.amount} format={formatCurrency} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      )}
    </Card>
  );
}

// --- AI Trend Suggestions ---
const RISK_PROFILES = [
  { id: 'conservative', label: 'Conservative' },
  { id: 'moderate', label: 'Moderate' },
  { id: 'aggressive', label: 'Aggressive' },
] as const;

function TrendChip({ trend }: { trend: TrendTag }) {
  if (trend === 'UPTREND')
    return (
      <span className="inline-flex items-center gap-1 rounded-full border border-emerald-500/25 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-bold text-emerald-600 dark:text-emerald-400">
        <ArrowUpRight className="size-3" /> Uptrend
      </span>
    );
  if (trend === 'DOWNTREND')
    return (
      <span className="inline-flex items-center gap-1 rounded-full border border-rose-500/25 bg-rose-500/10 px-2 py-0.5 text-[10px] font-bold text-rose-600 dark:text-rose-400">
        <ArrowDownRight className="size-3" /> Downtrend
      </span>
    );
  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-border/40 bg-muted/30 px-2 py-0.5 text-[10px] font-bold text-muted-foreground/80">
      <Minus className="size-3" /> Neutral
    </span>
  );
}

// current → suggested with delta coloring
function TargetShift({ current, suggested }: { current: number; suggested: number }) {
  const delta = suggested - current;
  const color =
    Math.abs(delta) < 0.05
      ? 'text-muted-foreground'
      : delta > 0
        ? 'text-emerald-600 dark:text-emerald-400'
        : 'text-rose-600 dark:text-rose-400';
  return (
    <span className="inline-flex items-center gap-1 tabular-nums">
      <span className="text-muted-foreground/70">{current.toFixed(1)}%</span>
      <ArrowUpRight className="size-3 rotate-45 text-muted-foreground/40" />
      <span className={cn('font-bold', color)}>{suggested.toFixed(1)}%</span>
      {Math.abs(delta) >= 0.05 && (
        <span className={cn('text-[10px] font-semibold', color)}>
          ({delta > 0 ? '+' : ''}
          {delta.toFixed(1)})
        </span>
      )}
    </span>
  );
}

function PctInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-lg border border-border/60 bg-muted/40 px-1.5 py-0.5">
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-6 w-14 border-0 bg-transparent px-1 py-0 text-right text-xs font-bold tabular-nums focus-visible:ring-0 focus-visible:ring-offset-0"
      />
      <span className="text-[10px] font-semibold text-muted-foreground">%</span>
    </span>
  );
}

function AISuggestPanel({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [risk, setRisk] = useState<string>('moderate');
  const [catEdit, setCatEdit] = useState<Record<string, string>>({});
  const [instrEdit, setInstrEdit] = useState<Record<number, string>>({});

  const suggestMut = useMutation({
    mutationFn: () => allocationsApi.aiSuggest({ risk_profile: risk }),
    onMutate: () => {
      setCatEdit({});
      setInstrEdit({});
    },
  });

  const applyMut = useMutation({
    mutationFn: (data: AllocSuggestResult) =>
      allocationsApi.applyTargets({
        category_targets: data.category_suggestions.map((c) => ({
          alloc_category: c.alloc_category,
          target_percent: Number(catEdit[c.alloc_category] ?? c.suggested_target_percent),
        })),
        instrument_targets: data.instrument_suggestions.map((i) => ({
          instrument_id: i.instrument_id,
          target_percent: Number(instrEdit[i.instrument_id] ?? i.suggested_target_percent),
          alloc_category: i.alloc_category,
        })),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['allocations'] });
      onClose();
    },
  });

  const data = suggestMut.data;
  const instrumentsByCat = data
    ? data.instrument_suggestions.reduce(
        (acc, it) => {
          (acc[it.alloc_category] ||= []).push(it);
          return acc;
        },
        {} as Record<AllocCategory, AllocSuggestResult['instrument_suggestions']>,
      )
    : null;

  return (
    <Card className="overflow-hidden rounded-2xl border border-violet-500/25 bg-card/55 shadow-sm backdrop-blur-sm">
      {/* Panel header */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border/40 bg-gradient-to-r from-violet-500/10 to-indigo-500/5 p-5">
        <div className="flex items-center gap-3">
          <div className="rounded-xl border border-violet-500/20 bg-violet-500/10 p-2.5 text-violet-500 shadow-inner">
            <Brain className="size-5 stroke-[1.5]" />
          </div>
          <div>
            <h3 className="text-sm font-bold text-foreground">AI Trend-Based Targets</h3>
            <p className="text-xs text-muted-foreground">
              Tilts your targets toward what's trending up, within your risk bands. Review & edit
              before applying.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Select value={risk} onValueChange={setRisk}>
            <SelectTrigger className="h-9 w-36 rounded-xl border-border/60 bg-card/60 text-xs font-semibold">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="rounded-xl">
              {RISK_PROFILES.map((p) => (
                <SelectItem key={p.id} value={p.id} className="text-xs font-medium">
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            onClick={() => suggestMut.mutate()}
            disabled={suggestMut.isPending}
            className="h-9 gap-2 rounded-xl border-0 bg-gradient-to-r from-violet-600 to-indigo-600 px-4 text-xs font-bold text-white shadow-sm hover:from-violet-500 hover:to-indigo-500"
          >
            {suggestMut.isPending ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Sparkles className="size-3.5" />
            )}
            {data ? 'Regenerate' : 'Generate'}
          </Button>
          <Button
            size="icon"
            variant="ghost"
            onClick={onClose}
            className="size-9 rounded-xl text-muted-foreground hover:bg-muted/40"
          >
            <X className="size-4" />
          </Button>
        </div>
      </div>

      {/* Panel body */}
      <div className="p-5">
        {suggestMut.isPending && (
          <div className="flex flex-col items-center justify-center gap-3 py-12 text-muted-foreground">
            <Loader2 className="size-7 animate-spin text-violet-500" />
            <p className="text-sm font-semibold text-foreground">Analysing your portfolio trends…</p>
            <p className="text-xs">Reading momentum, returns and market mood — this can take a few seconds.</p>
          </div>
        )}

        {suggestMut.isError && (
          <div className="rounded-xl border border-rose-500/25 bg-rose-500/5 p-4 text-sm font-semibold text-rose-500">
            {(suggestMut.error as Error).message}
          </div>
        )}

        {!suggestMut.isPending && !data && !suggestMut.isError && (
          <div className="flex flex-col items-center justify-center gap-2 py-10 text-center text-muted-foreground">
            <Sparkles className="size-7 text-violet-500/70" />
            <p className="text-sm font-semibold text-foreground">Pick a risk profile and hit Generate</p>
            <p className="max-w-md text-xs leading-relaxed">
              The AI reads each holding's trend, quality score and recent returns, then proposes
              category &amp; fund target %s you can review and apply.
            </p>
          </div>
        )}

        {data && (
          <div className="space-y-5">
            {/* Narrative */}
            <div className="grid gap-3 md:grid-cols-2">
              <div className="rounded-xl border border-border/50 bg-muted/10 p-4">
                <p className="mb-1 text-[10px] font-bold uppercase tracking-wider text-muted-foreground/80">
                  Market Read
                </p>
                <p className="text-xs leading-relaxed text-foreground/90">{data.market_context}</p>
              </div>
              <div className="rounded-xl border border-border/50 bg-muted/10 p-4">
                <p className="mb-1 text-[10px] font-bold uppercase tracking-wider text-muted-foreground/80">
                  Strategy
                </p>
                <p className="text-xs leading-relaxed text-foreground/90">{data.rationale}</p>
              </div>
            </div>

            {/* Category suggestions */}
            <div className="overflow-hidden rounded-xl border border-border/50">
              <div className="border-b border-border/40 bg-muted/20 px-4 py-2.5 text-[11px] font-bold uppercase tracking-wider text-muted-foreground/90">
                Category Targets
              </div>
              <table className="w-full text-sm">
                <tbody className="divide-y divide-border/40">
                  {data.category_suggestions.map((c) => (
                    <tr key={c.alloc_category} className="hover:bg-muted/20">
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2">
                          <span
                            className="size-2.5 rounded-full"
                            style={{ backgroundColor: ALLOC_COLORS[c.alloc_category] }}
                          />
                          <span className="font-semibold text-foreground">
                            {ALLOC_LABELS[c.alloc_category]}
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-2.5">
                        <TargetShift
                          current={c.current_percent}
                          suggested={Number(catEdit[c.alloc_category] ?? c.suggested_target_percent)}
                        />
                      </td>
                      <td className="px-4 py-2.5">
                        <TrendChip trend={c.trend} />
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        <PctInput
                          value={String(catEdit[c.alloc_category] ?? c.suggested_target_percent)}
                          onChange={(v) =>
                            setCatEdit((m) => ({ ...m, [c.alloc_category]: v }))
                          }
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Instrument suggestions grouped by category */}
            {instrumentsByCat &&
              (Object.keys(instrumentsByCat) as AllocCategory[]).map((cat) => (
                <div key={cat} className="overflow-hidden rounded-xl border border-border/50">
                  <div className="flex items-center gap-2 border-b border-border/40 bg-muted/20 px-4 py-2.5">
                    <span
                      className="size-2.5 rounded-full"
                      style={{ backgroundColor: ALLOC_COLORS[cat] }}
                    />
                    <span className="text-[11px] font-bold uppercase tracking-wider text-muted-foreground/90">
                      {ALLOC_LABELS[cat]} funds
                    </span>
                  </div>
                  <table className="w-full text-sm">
                    <tbody className="divide-y divide-border/40">
                      {instrumentsByCat[cat].map((it) => (
                        <tr key={it.instrument_id} className="align-top hover:bg-muted/20">
                          <td className="px-4 py-2.5">
                            <div className="font-semibold text-foreground">
                              {it.instrument_name}
                            </div>
                            {it.reason && (
                              <div className="mt-0.5 max-w-md text-[11px] leading-relaxed text-muted-foreground">
                                {it.reason}
                              </div>
                            )}
                          </td>
                          <td className="whitespace-nowrap px-4 py-2.5">
                            <TargetShift
                              current={it.current_percent}
                              suggested={Number(
                                instrEdit[it.instrument_id] ?? it.suggested_target_percent,
                              )}
                            />
                          </td>
                          <td className="px-4 py-2.5">
                            <TrendChip trend={it.trend} />
                          </td>
                          <td className="px-4 py-2.5 text-right">
                            <PctInput
                              value={String(
                                instrEdit[it.instrument_id] ?? it.suggested_target_percent,
                              )}
                              onChange={(v) =>
                                setInstrEdit((m) => ({ ...m, [it.instrument_id]: v }))
                              }
                            />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ))}

            {/* Apply bar */}
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/50 bg-muted/10 p-4">
              <p className="text-[11px] text-muted-foreground">
                Applying overwrites your category &amp; instrument <strong>Target%</strong> with the
                values above. Rebalance suggestions and the Distribution Calculator update instantly.
              </p>
              <Button
                onClick={() => applyMut.mutate(data)}
                disabled={applyMut.isPending}
                className="h-9 gap-2 rounded-xl border-0 bg-gradient-to-r from-emerald-600 to-teal-600 px-5 text-xs font-bold text-white shadow-sm hover:from-emerald-500 hover:to-teal-500"
              >
                {applyMut.isPending ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Check className="size-3.5" />
                )}
                Apply all targets
              </Button>
            </div>
          </div>
        )}
      </div>
    </Card>
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
  sub: React.ReactNode;
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
    blue: 'bg-blue-500/5 border-blue-500/10',
    emerald: 'bg-emerald-500/5 border-emerald-500/10',
    rose: 'bg-rose-500/5 border-rose-500/10',
    violet: 'bg-violet-500/5 border-violet-500/10',
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
        'overflow-hidden border border-border/60 bg-card/45 shadow-sm backdrop-blur-sm transition-all duration-300 hover:-translate-y-1 hover:shadow-md',
        glowShadow,
      )}
    >
      <CardContent className="flex items-start justify-between p-5">
        <div className="space-y-1">
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground/80">
            {label}
          </p>
          {customContent ? (
            customContent
          ) : value !== undefined ? (
            <div className={cn('text-2xl font-extrabold tabular-nums tracking-tight', accent)}>
              <MaskedAmount value={value} format={formatCurrency} />
            </div>
          ) : (
            <span className="font-semibold text-muted-foreground">—</span>
          )}
          <div className="mt-1 text-[11px] font-medium text-muted-foreground/75">{sub}</div>
        </div>
        {Icon && (
          <div className={cn('rounded-xl border p-2.5 shadow-inner', bgLight)}>
            <Icon className="size-4.5" />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// --- Page ---
function AllocationsPage() {
  const [isMounted, setIsMounted] = useState(false);
  const qc = useQueryClient();

  useEffect(() => {
    const timer = setTimeout(() => setIsMounted(true), 150);
    return () => clearTimeout(timer);
  }, []);

  const { data, isLoading, error } = useQuery({
    queryKey: ['allocations'],
    queryFn: allocationsApi.overview,
  });

  const [showAI, setShowAI] = useState(false);

  const seedMut = useMutation({
    mutationFn: allocationsApi.seedFromHoldings,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['allocations'] }),
  });

  if (isLoading || !isMounted) {
    return (
      <div className="space-y-6">
        <div className="space-y-2">
          <div className="h-4 w-20 animate-pulse rounded bg-muted/65" />
          <div className="h-8 w-48 animate-pulse rounded bg-muted/65" />
          <div className="h-4 w-72 animate-pulse rounded bg-muted/50" />
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="h-24 animate-pulse rounded-2xl bg-muted/60" />
          <div className="h-24 animate-pulse rounded-2xl bg-muted/60" />
          <div className="h-24 animate-pulse rounded-2xl bg-muted/60" />
        </div>
        <div className="h-96 animate-pulse rounded-2xl bg-muted/40" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-2xl border border-rose-500/25 bg-rose-500/5 p-6 text-sm font-semibold text-rose-500">
        Failed to load allocations: {error.message}
      </div>
    );
  }

  const overview = data!;

  // Rebalance roll-up across instruments that have a target.
  const planned = overview.instrument_allocations.some((i) => i.has_target);
  const toSell = overview.instrument_allocations
    .filter((i) => i.rebalance_action === 'SELL')
    .reduce((s, i) => s + Math.abs(i.rebalance_amount), 0);
  const toBuy = overview.instrument_allocations
    .filter((i) => i.rebalance_action === 'BUY')
    .reduce((s, i) => s + Math.abs(i.rebalance_amount), 0);

  return (
    <div className="space-y-6">
      {/* Header */}
      <header className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
        <div className="flex flex-col gap-1 md:max-w-3xl">
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Asset Management
          </p>
          <h1 className="bg-gradient-to-r from-foreground to-foreground/75 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
            Allocations
          </h1>
          <p className="text-xs font-medium leading-relaxed text-muted-foreground/80">
            Track every holding against a target, and get exact sell/buy suggestions to rebalance.
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button
            onClick={() => setShowAI((v) => !v)}
            className="h-10 gap-2 rounded-xl border-0 bg-gradient-to-r from-violet-600 to-indigo-600 px-4 text-xs font-bold text-white shadow-sm shadow-primary/20 transition-all hover:from-violet-500 hover:to-indigo-500"
          >
            <Brain className="size-3.5" />
            AI Suggest targets
          </Button>
          <Button
            onClick={() => seedMut.mutate()}
            disabled={seedMut.isPending}
            variant="outline"
            className="h-10 gap-2 rounded-xl border-violet-500/30 bg-violet-500/5 px-4 text-xs font-bold text-violet-600 shadow-sm transition-all hover:bg-violet-500/10 dark:text-violet-400"
          >
            {seedMut.isPending ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Sparkles className="size-3.5" />
            )}
            {planned ? 'Fill missing targets' : 'Populate from holdings'}
          </Button>
        </div>
      </header>

      {showAI && <AISuggestPanel onClose={() => setShowAI(false)} />}

      {!planned && (
        <div className="flex items-start gap-3 rounded-2xl border border-violet-500/20 bg-violet-500/5 p-4 text-xs text-muted-foreground">
          <Scale className="mt-0.5 size-4 shrink-0 text-violet-500" />
          <p className="leading-relaxed">
            No targets set yet. Click{' '}
            <span className="font-semibold text-foreground">Populate from holdings</span> to seed each
            instrument and category target from your current mix, then tweak any{' '}
            <span className="font-semibold text-foreground">Target%</span> to see live rebalance
            suggestions.
          </p>
        </div>
      )}

      {/* KPI strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard
          label="Total Portfolio Value"
          value={overview.total_value}
          sub="Current net worth allocation"
          color="blue"
          icon={Wallet}
        />
        <KpiCard
          label="Total Monthly SIP"
          value={overview.total_sip}
          sub="Configured monthly savings"
          color="emerald"
          icon={TrendingUp}
        />
        <KpiCard
          label="Instruments Tracked"
          customContent={
            <div className="text-2xl font-bold tracking-tight text-violet-500 dark:text-violet-400">
              {overview.instrument_allocations.length}
            </div>
          }
          sub="Unique asset holdings"
          color="violet"
          icon={Sliders}
        />
        <KpiCard
          label="To Rebalance"
          customContent={
            <div className="flex items-baseline gap-3">
              <div className="text-lg font-extrabold tabular-nums text-rose-500 dark:text-rose-400">
                <MaskedAmount value={toSell} format={formatCurrency} />
              </div>
              <div className="text-lg font-extrabold tabular-nums text-emerald-500 dark:text-emerald-400">
                <MaskedAmount value={toBuy} format={formatCurrency} />
              </div>
            </div>
          }
          sub={
            planned ? (
              <span>
                <span className="text-rose-500">sell</span> ·{' '}
                <span className="text-emerald-500">buy</span> to hit targets
              </span>
            ) : (
              'Set targets to see suggestions'
            )
          }
          color="rose"
          icon={Scale}
        />
      </div>

      {/* Tabs */}
      <Tabs defaultValue="categories" className="space-y-6">
        <TabsList className="inline-flex rounded-xl border border-border/45 bg-muted/60 p-1">
          <TabsTrigger
            value="categories"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Category View
          </TabsTrigger>
          <TabsTrigger
            value="instruments"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Instrument View
          </TabsTrigger>
          <TabsTrigger
            value="calculator"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Distribution Calculator
          </TabsTrigger>
        </TabsList>

        <TabsContent value="categories" className="mt-0 outline-none">
          <CategoryView data={overview.category_allocations} />
        </TabsContent>

        <TabsContent value="instruments" className="mt-0 outline-none">
          <InstrumentView
            data={overview.instrument_allocations}
            totalValue={overview.total_value}
          />
        </TabsContent>

        <TabsContent value="calculator" className="mt-0 outline-none">
          <DistributionCalculator />
        </TabsContent>
      </Tabs>
    </div>
  );
}
