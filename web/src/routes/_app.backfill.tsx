import { useState, useEffect, useRef } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation } from '@tanstack/react-query';
import { Play, RefreshCw, AlertCircle, ChevronRight, Database, Sliders } from 'lucide-react';

import {
  backfillApi,
  instrumentsApi,
  type Instrument,
  type BackfillStatus,
  type AutoSearchResult,
} from '@/lib/api';
import { useSSE } from '@/lib/sse';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_app/backfill')({
  component: BackfillPage,
});

// --- Auto picker — works for MF (MFAPI) and ETF/STOCK (Yahoo Finance) ---
function AutoPicker({
  instrument,
  onSelect,
}: {
  instrument: Instrument;
  onSelect: (result: AutoSearchResult) => void;
}) {
  const savedCode = instrument.amfi_code || instrument.yahoo_symbol;
  const savedSource: 'mfapi' | 'yahoo' | null = instrument.amfi_code
    ? 'mfapi'
    : instrument.yahoo_symbol
      ? 'yahoo'
      : null;

  const [selected, setSelected] = useState<AutoSearchResult | null>(
    savedCode && savedSource
      ? { source: savedSource, code: savedCode, name: instrument.name }
      : null,
  );
  const [searching, setSearching] = useState(false);
  const [results, setResults] = useState<AutoSearchResult[]>([]);
  const [error, setError] = useState('');
  const [showResults, setShowResults] = useState(false);

  useEffect(() => {
    if (savedCode && savedSource) {
      const pre: AutoSearchResult = { source: savedSource, code: savedCode, name: instrument.name };
      setSelected(pre);
      onSelect(pre);
      setShowResults(false);
    } else {
      doSearch();
    }
  }, [instrument.id]);

  const doSearch = async () => {
    setSearching(true);
    setError('');
    setShowResults(true);
    setSelected(null);
    try {
      const res = await backfillApi.autoSearch(instrument.id);
      const safe = res ?? [];
      setResults(safe);
      if (safe.length === 0) setError('No matches found on MFAPI or Yahoo Finance.');
    } catch {
      setError('Search failed. Check your connection.');
    } finally {
      setSearching(false);
    }
  };

  const pick = (r: AutoSearchResult) => {
    setSelected(r);
    setShowResults(false);
    onSelect(r);
  };

  const sourceLabel = (src: string) => (src === 'mfapi' ? 'MFAPI' : 'Yahoo Finance');

  const sourceBadgeClass = (src: string) =>
    src === 'mfapi'
      ? 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20'
      : 'bg-orange-500/10 text-orange-600 dark:text-orange-400 border-orange-500/20';

  return (
    <div className="space-y-4">
      {selected && !showResults ? (
        <div className="flex items-start justify-between gap-4 rounded-xl border border-emerald-500/20 bg-emerald-500/5 p-4 shadow-lg shadow-emerald-500/5 backdrop-blur-md transition-all duration-300">
          <div className="min-w-0 space-y-1">
            <div className="flex items-center gap-2">
              <span
                className={`rounded-full border px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${sourceBadgeClass(selected.source)}`}
              >
                {sourceLabel(selected.source)}
              </span>
            </div>
            <p className="truncate text-base font-semibold text-foreground">{selected.name}</p>
            {selected.exchange && (
              <p className="flex items-center gap-1 text-xs text-muted-foreground">
                <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-500" />
                {selected.exch_disp || selected.exchange}
              </p>
            )}
          </div>
          <Button
            size="sm"
            variant="outline"
            className="rounded-lg border-emerald-500/20 font-medium text-emerald-600 hover:bg-emerald-500/10 hover:text-emerald-700"
            onClick={doSearch}
          >
            Change Match
          </Button>
        </div>
      ) : (
        <div className="space-y-3">
          <div className="flex items-center gap-2">
            <p className="text-xs font-semibold text-muted-foreground">
              {searching
                ? 'Searching MFAPI and Yahoo Finance databases…'
                : `${results.length} match${results.length !== 1 ? 'es' : ''} identified`}
            </p>
            {!searching && (
              <Button
                size="sm"
                variant="ghost"
                className="h-6 gap-1 px-2 text-[11px] text-primary/80 hover:text-primary"
                onClick={doSearch}
              >
                <RefreshCw className="size-3" /> Re-trigger search
              </Button>
            )}
          </div>

          {error && (
            <div className="flex items-center gap-2 rounded-lg border border-destructive/10 bg-destructive/5 p-3 text-sm text-destructive">
              <AlertCircle className="size-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {searching ? (
            <div className="space-y-2">
              {[1, 2, 3].map((i) => (
                <div
                  key={i}
                  className="h-12 animate-pulse rounded-xl border border-border/40 bg-muted/60"
                />
              ))}
            </div>
          ) : (
            <div className="scrollbar-thin max-h-64 space-y-2 overflow-y-auto pr-1.5">
              {results.map((r) => (
                <button
                  key={`${r.source}-${r.code}`}
                  onClick={() => pick(r)}
                  className="group flex w-full items-center justify-between gap-3 rounded-xl border border-border/60 bg-card/40 px-4 py-3.5 text-left text-sm backdrop-blur-sm transition-all duration-300 hover:translate-x-1 hover:border-primary/40 hover:bg-accent/40"
                >
                  <div className="min-w-0 space-y-1.5">
                    <div className="flex items-center gap-2">
                      <span
                        className={`rounded-full border px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider ${sourceBadgeClass(r.source)}`}
                      >
                        {sourceLabel(r.source)}
                      </span>
                      {r.exchange && (
                        <span className="rounded-full bg-muted/40 px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                          {r.exch_disp || r.exchange}
                        </span>
                      )}
                    </div>
                    <p className="truncate font-semibold text-foreground transition-colors group-hover:text-primary">
                      {r.name}
                    </p>
                  </div>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-all duration-300 group-hover:translate-x-0.5 group-hover:text-primary" />
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// --- Run tab ---
function RunTab() {
  const [instrumentId, setInstrumentId] = useState<string>('');
  const [selectedAuto, setSelectedAuto] = useState<AutoSearchResult | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [running, setRunning] = useState(false);
  const logRef = useRef<HTMLDivElement>(null);
  const { on } = useSSE();

  const { data: instruments = [] } = useQuery({
    queryKey: ['instruments'],
    queryFn: () => instrumentsApi.list(),
  });

  const { data: statusData } = useQuery({
    queryKey: ['backfill', 'status'],
    queryFn: backfillApi.status,
    refetchInterval: running ? 1000 : false,
  });

  const selectedInstrument = (instruments as Instrument[]).find(
    (i) => String(i.id) === instrumentId,
  );

  useEffect(() => {
    const unsub = on('backfill', (data) => {
      const d = data as { log?: string };
      if (d?.log) setLogs((prev) => [...prev, d.log!]);
    });
    return () => {
      unsub();
    };
  }, [on]);

  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [logs]);

  useEffect(() => {
    if (statusData) {
      setRunning(statusData.is_running);
      if (statusData.logs.length > logs.length) setLogs(statusData.logs);
    }
  }, [statusData]);

  // Reset auto selection when instrument changes.
  useEffect(() => {
    setSelectedAuto(null);
  }, [instrumentId]);

  const startMut = useMutation({
    mutationFn: () => {
      const id = Number(instrumentId);
      return backfillApi.startAuto(id, selectedAuto!.source, selectedAuto!.code);
    },
    onMutate: () => {
      setLogs([]);
      setRunning(true);
    },
    onError: (err: Error) => {
      setLogs((prev) => [...prev, `❌ Error: ${err.message}`]);
      setRunning(false);
    },
  });

  const canStart = !!instrumentId && !running && !!selectedAuto;

  const status = statusData as BackfillStatus | undefined;

  return (
    <div className="space-y-6">
      <div className="space-y-6 rounded-2xl border border-border/80 bg-card/40 p-6 shadow-xl shadow-black/5 backdrop-blur-md">
        <div className="flex items-center gap-3">
          <div className="rounded-xl bg-primary/10 p-2.5 text-primary">
            <Sliders className="size-5" />
          </div>
          <div>
            <h3 className="text-lg font-bold">Import Historical Prices</h3>
            <p className="text-xs text-muted-foreground">
              Select an asset instrument to sync its history.
            </p>
          </div>
        </div>

        {/* Instrument selector */}
        <div className="space-y-2">
          <label className="text-sm font-semibold text-foreground/90">Instrument</label>
          <Select value={instrumentId} onValueChange={setInstrumentId}>
            <SelectTrigger className="w-full rounded-xl border-border/80 bg-card/30 backdrop-blur-sm focus:ring-primary sm:w-96">
              <SelectValue placeholder="Select instrument to backfill…" />
            </SelectTrigger>
            <SelectContent className="backdrop-blur-md">
              {(instruments as Instrument[]).map((i) => (
                <SelectItem key={i.id} value={String(i.id)}>
                  <div className="flex w-full items-center justify-between">
                    <span className="font-semibold text-foreground/90">{i.name}</span>
                    {i.amfi_code && (
                      <span className="ml-3 rounded-full bg-blue-500/10 px-2.5 py-0.5 font-mono text-[10px] font-bold text-blue-600 dark:text-blue-400">
                        AMFI: {i.amfi_code}
                      </span>
                    )}
                    {i.yahoo_symbol && !i.amfi_code && (
                      <span className="ml-3 rounded-full bg-orange-500/10 px-2.5 py-0.5 font-mono text-[10px] font-bold text-orange-600 dark:text-orange-400">
                        YAHOO: {i.yahoo_symbol}
                      </span>
                    )}
                  </div>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {/* Auto-match picker */}
        {selectedInstrument && (
          <div className="mt-4 space-y-3 border-t border-border/50 pt-5">
            <div className="flex flex-col gap-1">
              <label className="text-sm font-semibold text-foreground/90">Auto-match Engine</label>
              <p className="text-xs text-muted-foreground">
                Mutual Funds are matched to{' '}
                <span className="font-mono font-medium text-blue-500">api.mfapi.in</span>. Stocks &
                ETFs are matched to{' '}
                <span className="font-mono font-medium text-orange-500">Yahoo Finance</span>. Fully
                automatic mapping.
              </p>
            </div>
            <AutoPicker instrument={selectedInstrument} onSelect={setSelectedAuto} />
          </div>
        )}

        <div className="flex items-center gap-4 border-t border-border/50 pt-4">
          <Button
            onClick={() => startMut.mutate()}
            disabled={!canStart}
            className={cn(
              'h-11 gap-2 rounded-xl px-6 font-bold shadow-md transition-all duration-300',
              running
                ? 'cursor-not-allowed border border-primary/20 bg-primary/20 text-primary shadow-none'
                : 'bg-primary text-primary-foreground shadow-primary/20 hover:scale-[1.02] hover:bg-primary/90 active:scale-[0.98]',
            )}
          >
            {running ? (
              <>
                <RefreshCw className="size-4 animate-spin" /> Ingestion Running…
              </>
            ) : (
              <>
                <Play className="size-4 fill-current" /> Start Historical Sync
              </>
            )}
          </Button>

          {running && (
            <span className="relative flex h-3.5 w-3.5">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-75"></span>
              <span className="relative inline-flex h-3.5 w-3.5 rounded-full bg-primary"></span>
            </span>
          )}
        </div>
      </div>

      {/* Progress / logs */}
      {(logs.length > 0 || status?.is_running) && (
        <div className="space-y-4">
          {status && (
            <div className="flex flex-wrap items-center gap-3 rounded-xl border border-border/50 bg-muted/30 p-3.5 px-4 text-sm shadow-sm backdrop-blur-sm">
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    'h-2.5 w-2.5 rounded-full',
                    status.is_running
                      ? 'animate-pulse bg-primary'
                      : status.errors.length > 0
                        ? 'animate-ping bg-rose-500'
                        : 'bg-emerald-500',
                  )}
                />
                <span
                  className={cn(
                    'font-bold tracking-tight',
                    status.is_running
                      ? 'text-primary'
                      : status.errors.length > 0
                        ? 'text-rose-500 dark:text-rose-400'
                        : 'text-emerald-500 dark:text-emerald-400',
                  )}
                >
                  {status.is_running
                    ? 'Syncing historical prices...'
                    : status.errors.length > 0
                      ? 'Sync finished with errors'
                      : 'Sync complete'}
                </span>
              </div>

              <div className="ml-auto flex items-center gap-2">
                {status.errors.length > 0 && (
                  <Badge
                    variant="destructive"
                    className="rounded-full border border-rose-500/20 bg-rose-500/10 px-2.5 py-0.5 font-semibold text-rose-500 hover:bg-rose-500/10 dark:text-rose-400"
                  >
                    {status.errors.length} {status.errors.length === 1 ? 'Error' : 'Errors'}
                  </Badge>
                )}
                {status.warnings.length > 0 && (
                  <Badge
                    variant="outline"
                    className="rounded-full border border-amber-500/20 bg-amber-500/10 px-2.5 py-0.5 font-semibold text-amber-600 hover:bg-amber-500/10 dark:text-amber-400"
                  >
                    {status.warnings.length} {status.warnings.length === 1 ? 'Warning' : 'Warnings'}
                  </Badge>
                )}
                {!status.is_running && status.errors.length === 0 && (
                  <Badge
                    variant="outline"
                    className="rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-0.5 font-semibold text-emerald-600 hover:bg-emerald-500/10 dark:text-emerald-400"
                  >
                    Success
                  </Badge>
                )}
              </div>
            </div>
          )}

          {/* Retro Developer Dark Terminal */}
          <div className="relative rounded-2xl border border-zinc-800 bg-zinc-950 p-5 shadow-2xl">
            {/* Terminal Header */}
            <div className="mb-4 flex items-center justify-between border-b border-zinc-800/80 pb-3">
              <div className="flex items-center gap-1.5">
                <span className="h-3 w-3 rounded-full bg-rose-500/80" />
                <span className="h-3 w-3 rounded-full bg-amber-500/80" />
                <span className="h-3 w-3 rounded-full bg-emerald-500/80" />
                <span className="ml-2 font-mono text-[10px] text-zinc-500">
                  TimescaleDB Data Stream
                </span>
              </div>
              <div className="flex items-center gap-2">
                <span className="h-2 w-2 animate-pulse rounded-full bg-primary" />
                <span className="font-mono text-[10px] text-zinc-400">backfill_job.log</span>
              </div>
            </div>

            <div
              ref={logRef}
              className="scrollbar-thin scrollbar-thumb-zinc-800 scrollbar-track-transparent h-80 space-y-1.5 overflow-y-auto pr-2 font-mono text-[11px] leading-relaxed"
            >
              {logs.length === 0 ? (
                <span className="animate-pulse text-zinc-500">Initializing log stream...</span>
              ) : (
                logs.map((line, i) => (
                  <div
                    key={i}
                    className={cn(
                      'rounded-md px-2 py-1 transition-colors hover:bg-zinc-900/50',
                      line.includes('❌') || line.toLowerCase().includes('error')
                        ? 'border-l-2 border-rose-500/60 bg-rose-950/20 pl-1.5 text-rose-400'
                        : line.includes('⚠️') || line.toLowerCase().includes('warn')
                          ? 'border-l-2 border-amber-500/60 bg-amber-950/20 pl-1.5 text-amber-400'
                          : line.includes('✅') || line.toLowerCase().includes('success')
                            ? 'border-l-2 border-emerald-500/60 bg-emerald-950/10 pl-1.5 text-emerald-400'
                            : 'text-zinc-300',
                    )}
                  >
                    <span className="mr-2 select-none text-zinc-600">
                      {(i + 1).toString().padStart(3, '0')}
                    </span>
                    {line}
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// --- Page ---
function BackfillPage() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 border-b border-border/40 pb-5 md:flex-row md:items-center md:justify-between">
        <div className="flex items-center gap-4">
          <div className="grid h-12 w-12 place-items-center rounded-2xl border border-primary/20 bg-gradient-to-tr from-primary/20 to-primary/5 text-primary shadow-lg shadow-primary/5">
            <Database className="size-6 animate-pulse" />
          </div>
          <div>
            <h1 className="bg-gradient-to-r from-foreground via-foreground/95 to-foreground/80 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
              Backfill Engine
            </h1>
            <p className="mt-0.5 text-sm text-muted-foreground">
              Sync historical asset prices via MFAPI or Yahoo Finance into TimescaleDB.
            </p>
          </div>
        </div>
      </div>

      <div className="mt-4">
        <RunTab />
      </div>
    </div>
  );
}
