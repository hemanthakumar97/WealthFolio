import { useState } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { UploadCloud, Loader2, FileWarning, CheckCircle2, FileText } from 'lucide-react';

import { ApiError, transactionsApi } from '@/lib/api';
import type {
  ManualTransactionRequest,
  UploadHistoryItem,
  UploadResult,
  UploadStatus,
} from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
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

export const Route = createFileRoute('/_app/transactions')({
  component: TransactionsPage,
});

function TransactionsPage() {
  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-1.5 md:max-w-3xl">
        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Activity
        </p>
        <h1 className="bg-gradient-to-r from-foreground to-foreground/75 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
          Transactions
        </h1>
        <p className="text-xs font-medium leading-relaxed text-muted-foreground/80">
          Upload Zerodha tradebooks or Groww MF statements (CSV / XLSX), or record one-off
          transactions manually. Duplicates are skipped automatically.
        </p>
      </header>

      <Tabs defaultValue="upload" className="space-y-6">
        <TabsList className="inline-flex rounded-xl border border-border/40 bg-muted/60 p-1">
          <TabsTrigger
            value="upload"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Import Statement
          </TabsTrigger>
          <TabsTrigger
            value="manual"
            className="rounded-lg px-4 py-1.5 text-xs font-semibold transition-all duration-200 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            Manual Entry
          </TabsTrigger>
        </TabsList>
        <TabsContent value="upload" className="mt-0 outline-none">
          <UploadPanel />
        </TabsContent>
        <TabsContent value="manual" className="mt-0 outline-none">
          <ManualPanel />
        </TabsContent>
      </Tabs>

      <UploadHistorySection />
    </div>
  );
}

// --- Upload tab --------------------------------------------------------------

function UploadPanel() {
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [platform, setPlatform] = useState<string>('auto');
  const [dragging, setDragging] = useState(false);
  const [result, setResult] = useState<UploadResult | null>(null);

  const upload = useMutation({
    mutationFn: () => {
      if (!file) return Promise.reject(new Error('Pick a file first'));
      return transactionsApi.upload(file, platform === 'auto' ? undefined : platform);
    },
    onSuccess: (data) => {
      setResult(data);
      toast.success(`Imported ${data.records_imported} of ${data.records_total} rows`);
      queryClient.invalidateQueries({ queryKey: ['upload-history'] });
      setFile(null);
    },
    onError: (err) => {
      toast.error(err instanceof ApiError ? err.detail : (err as Error).message);
    },
  });

  return (
    <div className="space-y-6">
      <Card className="rounded-2xl border border-border/80 bg-card/45 p-6 shadow-sm backdrop-blur-sm">
        <div className="space-y-5">
          <div className="space-y-1">
            <h3 className="text-lg font-semibold text-foreground">Import Statement</h3>
            <p className="text-xs text-muted-foreground">
              Drop a CSV or XLSX statement exported from your broker. The system automatically
              detects the format.
            </p>
          </div>

          <label
            htmlFor="upload-file"
            onDragOver={(e) => {
              e.preventDefault();
              setDragging(true);
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDragging(false);
              const dropped = e.dataTransfer.files?.[0];
              if (dropped) setFile(dropped);
            }}
            className={cn(
              'relative grid cursor-pointer place-items-center overflow-hidden rounded-2xl border-2 border-dashed border-border/80 px-6 py-10 text-center transition-all duration-300',
              dragging && 'scale-[1.01] border-primary bg-primary/5 shadow-inner',
              file && 'border-indigo-500/40 bg-indigo-500/5 shadow-inner',
              !file && !dragging && 'hover:border-muted-foreground/45 hover:bg-muted/30',
            )}
          >
            <div className="flex flex-col items-center gap-3">
              <div
                className={cn(
                  'rounded-full p-4 transition-all duration-300',
                  file ? 'bg-indigo-500/10 text-indigo-500' : 'bg-muted text-muted-foreground',
                )}
              >
                <UploadCloud className={cn('size-8 stroke-[1.5]', file && 'animate-pulse')} />
              </div>
              {file ? (
                <div className="space-y-1">
                  <p className="max-w-sm truncate text-sm font-semibold text-foreground">
                    {file.name}
                  </p>
                  <p className="text-[11px] font-medium text-muted-foreground/90">
                    {(file.size / 1024).toFixed(1)} KB · Click or drag to change statement
                  </p>
                </div>
              ) : (
                <div className="space-y-1">
                  <p className="text-sm font-semibold text-foreground">
                    Drop file here, or click to browse
                  </p>
                  <p className="text-[11px] text-muted-foreground/80">
                    Supports CSV, XLS or XLSX statements up to 10 MB
                  </p>
                </div>
              )}
            </div>
            <input
              id="upload-file"
              type="file"
              accept=".csv,.xlsx,.xls"
              className="hidden"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
          </label>

          <div className="grid items-end gap-4 border-t border-border/40 pt-5 sm:grid-cols-[1fr_auto]">
            <div className="space-y-2">
              <Label
                htmlFor="platform"
                className="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                Platform Override
              </Label>
              <Select value={platform} onValueChange={setPlatform}>
                <SelectTrigger
                  id="platform"
                  className="h-9 rounded-xl border-border/80 bg-card/60 text-xs"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className="rounded-xl border-border/80">
                  <SelectItem value="auto">Auto-detect (Recommended)</SelectItem>
                  <SelectItem value="ZERODHA">Zerodha (Kite)</SelectItem>
                  <SelectItem value="GROWW">Groww</SelectItem>
                  <SelectItem value="INDMONEY">INDMoney</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <Button
              onClick={() => upload.mutate()}
              disabled={!file || upload.isPending}
              className="hover:scale-101 h-9 cursor-pointer rounded-xl border border-transparent px-6 text-xs font-semibold shadow-sm shadow-primary/20 transition-all duration-300"
            >
              {upload.isPending ? (
                <span className="flex items-center gap-2">
                  <Loader2 className="size-3.5 animate-spin" />
                  Importing statement…
                </span>
              ) : (
                'Import Statement'
              )}
            </Button>
          </div>
        </div>
      </Card>

      {result && <UploadResultCard result={result} />}
    </div>
  );
}

function UploadResultCard({ result }: { result: UploadResult }) {
  const ok = result.records_imported > 0;
  return (
    <Card
      className={cn(
        'overflow-hidden rounded-2xl border bg-card/45 p-5 shadow-sm backdrop-blur-sm transition-all duration-300',
        ok ? 'border-emerald-500/35' : 'border-amber-500/35',
      )}
    >
      <div className="space-y-4">
        <div className="flex items-start gap-4">
          <div
            className={cn(
              'rounded-xl p-3 shadow-inner',
              ok
                ? 'border border-emerald-500/15 bg-emerald-500/10 text-emerald-500'
                : 'border border-amber-500/15 bg-amber-500/10 text-amber-500',
            )}
          >
            {ok ? (
              <CheckCircle2 className="size-5 stroke-[1.5]" />
            ) : (
              <FileWarning className="size-5 stroke-[1.5]" />
            )}
          </div>
          <div className="flex-1 space-y-1">
            <h3 className="truncate text-sm font-semibold tracking-tight text-foreground">
              {result.filename}
            </h3>
            <div className="flex flex-wrap items-center gap-2">
              <span className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                {result.platform}
              </span>
              <span className="text-xs text-muted-foreground">•</span>
              <p className="text-xs font-semibold text-muted-foreground/80">
                <span className="text-emerald-500">{result.records_imported}</span> imported ·{' '}
                <span className="text-muted-foreground">{result.records_duplicates}</span>{' '}
                duplicates · <span className="text-rose-500">{result.records_errors}</span> errors
              </p>
            </div>
          </div>
        </div>

        {result.errors?.length > 0 && (
          <div className="space-y-2 border-t border-border/40 pt-4">
            <p className="text-[10px] font-bold uppercase tracking-widest text-rose-500">
              First Error Logs ({result.records_errors} total)
            </p>
            <ul className="scrollbar-thin max-h-[220px] space-y-1.5 overflow-y-auto rounded-xl border border-rose-500/15 bg-rose-500/5 p-3">
              {result.errors.slice(0, 10).map((e, i) => (
                <li
                  key={i}
                  className="flex items-start gap-2 font-mono text-[10.5px] leading-normal text-rose-600 dark:text-rose-400"
                >
                  <span className="select-none text-rose-400">•</span>
                  <span className="break-all">{e}</span>
                </li>
              ))}
              {result.errors.length > 10 && (
                <li className="pl-4 pt-1 text-[10px] font-semibold text-muted-foreground">
                  …and {result.errors.length - 10} additional errors encountered
                </li>
              )}
            </ul>
          </div>
        )}
      </div>
    </Card>
  );
}

// --- Manual tab --------------------------------------------------------------

const manualSchema = z.object({
  symbol: z.string().min(1, 'Required'),
  segment: z.string().optional(),
  transaction_date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, 'Use YYYY-MM-DD'),
  transaction_type: z.string().min(1, 'Required'),
  quantity: z.string().refine((v) => Number(v) > 0, 'Quantity > 0'),
  amount: z.string().refine((v) => Number(v) > 0, 'Amount > 0'),
  platform: z.string().min(1, 'Required'),
  order_id: z.string().optional(),
});

function ManualPanel() {
  const queryClient = useQueryClient();
  const form = useForm<ManualTransactionRequest>({
    resolver: zodResolver(manualSchema),
    defaultValues: {
      symbol: '',
      segment: 'Equity',
      transaction_date: new Date().toISOString().slice(0, 10),
      transaction_type: 'BUY',
      quantity: '',
      amount: '',
      platform: 'MANUAL',
      order_id: '',
    },
  });

  const create = useMutation({
    mutationFn: transactionsApi.manual,
    onSuccess: () => {
      toast.success('Transaction recorded');
      queryClient.invalidateQueries({ queryKey: ['upload-history'] });
      form.reset({ ...form.getValues(), symbol: '', quantity: '', amount: '', order_id: '' });
    },
    onError: (err) => {
      toast.error(err instanceof ApiError ? err.detail : (err as Error).message);
    },
  });

  return (
    <Card className="rounded-2xl border border-border/80 bg-card/45 p-6 shadow-sm backdrop-blur-sm">
      <div className="space-y-5">
        <div className="space-y-1 border-b border-border/40 pb-4">
          <h3 className="text-lg font-semibold text-foreground">Manual Entry</h3>
          <p className="text-xs text-muted-foreground">
            Useful for recording transaction history not supported by automated CSV imports (e.g.
            physical gold, real estate, customized bonds).
          </p>
        </div>
        <form
          className="grid gap-5 sm:grid-cols-2"
          onSubmit={form.handleSubmit((v) => create.mutate(v))}
        >
          <Field label="Symbol / Name" error={form.formState.errors.symbol?.message}>
            <Input
              placeholder="e.g. RELIANCE or HDFC Top 100"
              {...form.register('symbol')}
              className="h-9 rounded-xl border-border/80 bg-card/50 text-sm transition-colors hover:bg-card focus-visible:bg-card"
            />
          </Field>
          <Field label="Segment" error={form.formState.errors.segment?.message}>
            <Controlled
              value={form.watch('segment') ?? 'Equity'}
              onChange={(v) => form.setValue('segment', v)}
              options={[
                { value: 'Equity', label: 'Equity' },
                { value: 'MF', label: 'Mutual Fund' },
                { value: 'ETF', label: 'ETF' },
                { value: 'US Equity', label: 'US Equity' },
                { value: 'Gold', label: 'Gold' },
                { value: 'Bond', label: 'Bond' },
                { value: 'Other', label: 'Other' },
              ]}
            />
          </Field>

          <Field label="Date" error={form.formState.errors.transaction_date?.message}>
            <Input
              type="date"
              {...form.register('transaction_date')}
              className="h-9 rounded-xl border-border/80 bg-card/50 text-sm text-foreground transition-colors hover:bg-card focus-visible:bg-card"
            />
          </Field>
          <Field label="Type" error={form.formState.errors.transaction_type?.message}>
            <Controlled
              value={form.watch('transaction_type')}
              onChange={(v) => form.setValue('transaction_type', v)}
              options={[
                { value: 'BUY', label: 'Buy' },
                { value: 'SELL', label: 'Sell' },
                { value: 'DIVIDEND', label: 'Dividend' },
                { value: 'BONUS', label: 'Bonus' },
                { value: 'SPLIT', label: 'Split' },
              ]}
            />
          </Field>

          <Field label="Quantity" error={form.formState.errors.quantity?.message}>
            <Input
              type="number"
              step="0.000001"
              min="0"
              placeholder="0.00"
              {...form.register('quantity')}
              className="h-9 rounded-xl border-border/80 bg-card/50 text-sm transition-colors hover:bg-card focus-visible:bg-card"
            />
          </Field>
          <Field label="Amount (₹)" error={form.formState.errors.amount?.message}>
            <Input
              type="number"
              step="0.01"
              min="0"
              placeholder="0.00"
              {...form.register('amount')}
              className="h-9 rounded-xl border-border/80 bg-card/50 text-sm transition-colors hover:bg-card focus-visible:bg-card"
            />
          </Field>

          <Field label="Platform" error={form.formState.errors.platform?.message}>
            <Controlled
              value={form.watch('platform')}
              onChange={(v) => form.setValue('platform', v)}
              options={[
                { value: 'MANUAL', label: 'Manual' },
                { value: 'ZERODHA', label: 'Zerodha' },
                { value: 'GROWW', label: 'Groww' },
                { value: 'INDMONEY', label: 'INDMoney' },
              ]}
            />
          </Field>
          <Field label="Order ID (optional)" error={form.formState.errors.order_id?.message}>
            <Input
              placeholder="For unique deduplication checks"
              {...form.register('order_id')}
              className="h-9 rounded-xl border-border/80 bg-card/50 text-sm transition-colors hover:bg-card focus-visible:bg-card"
            />
          </Field>

          <div className="flex justify-end border-t border-border/40 pt-4 sm:col-span-2">
            <Button
              type="submit"
              disabled={create.isPending}
              className="hover:scale-101 h-9 cursor-pointer rounded-xl border border-transparent px-6 text-xs font-semibold shadow-sm shadow-primary/20 transition-all duration-300"
            >
              {create.isPending ? (
                <span className="flex items-center gap-2">
                  <Loader2 className="size-3.5 animate-spin" /> Recording…
                </span>
              ) : (
                'Record Transaction'
              )}
            </Button>
          </div>
        </form>
      </div>
    </Card>
  );
}

function Field({
  label,
  error,
  children,
}: {
  label: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </Label>
      {children}
      {error && (
        <p className="animate-bounce text-[10px] font-semibold tracking-tight text-rose-500">
          {error}
        </p>
      )}
    </div>
  );
}

function Controlled({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="h-9 rounded-xl border-border/80 bg-card/50 text-xs transition-colors hover:bg-card focus:bg-card">
        <SelectValue />
      </SelectTrigger>
      <SelectContent className="rounded-xl border-border/80 shadow-md">
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value} className="rounded-lg text-xs font-medium">
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// --- History -----------------------------------------------------------------

function UploadHistorySection() {
  const { data, isLoading } = useQuery({
    queryKey: ['upload-history'],
    queryFn: transactionsApi.history,
  });

  return (
    <Card className="overflow-hidden rounded-2xl border border-border/80 bg-card/45 shadow-sm backdrop-blur-sm">
      <CardHeader className="border-b border-border/40 bg-muted/10 px-6 py-4">
        <div className="space-y-1">
          <CardTitle className="text-base font-bold text-foreground">Upload History</CardTitle>
          <CardDescription className="text-xs text-muted-foreground">
            Recent broker statement imports — most recent first.
          </CardDescription>
        </div>
      </CardHeader>
      <CardContent className="p-0">
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
            <Loader2 className="size-5 animate-spin text-primary" /> Loading upload logs…
          </div>
        ) : !data || data.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-12 text-center text-muted-foreground/80">
            <FileText className="mb-2 size-8 stroke-[1.5] text-muted-foreground" />
            <p className="text-xs font-semibold">No uploads recorded yet</p>
            <p className="mt-0.5 text-[10px] text-muted-foreground">
              Your statement imports will appear here.
            </p>
          </div>
        ) : (
          <div className="scrollbar-thin overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border/85 bg-muted/20">
                  <th className="px-4 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    File Name
                  </th>
                  <th className="px-4 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Platform
                  </th>
                  <th className="px-4 py-3.5 text-left text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Status
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Total
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Imported
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Duplicates
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Errors
                  </th>
                  <th className="px-4 py-3.5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground/90">
                    Uploaded At
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/60">
                {data.map((row) => (
                  <UploadRow key={row.id} row={row} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function UploadRow({ row }: { row: UploadHistoryItem }) {
  return (
    <tr className="animate-fade-in transition-colors duration-150 hover:bg-muted/40">
      <td className="px-4 py-3">
        <div className="flex items-center gap-3">
          <div className="rounded-xl border border-indigo-500/10 bg-indigo-500/10 p-2 text-indigo-500">
            <FileText className="size-4 stroke-[1.5]" />
          </div>
          <span
            className="max-w-[180px] truncate font-semibold text-foreground xl:max-w-[280px]"
            title={row.filename}
          >
            {row.filename}
          </span>
        </div>
      </td>
      <td className="px-4 py-3">
        <span className="inline-flex items-center rounded-md border border-border/50 bg-muted px-2 py-0.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {row.platform}
        </span>
      </td>
      <td className="px-4 py-3">
        <StatusBadge status={row.status} />
      </td>
      <td className="px-4 py-3 text-right font-semibold tabular-nums text-foreground/80">
        {row.records_total}
      </td>
      <td className="px-4 py-3 text-right font-semibold tabular-nums text-emerald-500">
        {row.records_imported}
      </td>
      <td className="px-4 py-3 text-right font-semibold tabular-nums text-muted-foreground">
        {row.records_duplicates}
      </td>
      <td className="px-4 py-3 text-right font-semibold tabular-nums text-rose-500">
        {row.records_errors}
      </td>
      <td className="px-4 py-3 text-right text-xs font-medium tabular-nums text-muted-foreground/80">
        {formatDateTime(row.uploaded_at)}
      </td>
    </tr>
  );
}

function StatusBadge({ status }: { status: UploadStatus }) {
  const variant = (
    {
      COMPLETED: 'success',
      PARTIAL: 'warning',
      FAILED: 'destructive',
      PROCESSING: 'secondary',
      PENDING: 'secondary',
    } as const
  )[status];
  return (
    <Badge
      variant={variant}
      className={cn(
        'rounded-full border border-transparent px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider shadow-sm',
        status === 'COMPLETED' &&
          'border-emerald-500/15 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
        status === 'PARTIAL' &&
          'border-amber-500/15 bg-amber-500/10 text-amber-600 dark:text-amber-400',
        status === 'FAILED' && 'border-rose-500/15 bg-rose-500/10 text-rose-600 dark:text-rose-400',
        (status === 'PROCESSING' || status === 'PENDING') &&
          'animate-pulse border-blue-500/15 bg-blue-500/10 text-blue-600 dark:text-blue-400',
      )}
    >
      {status}
    </Badge>
  );
}
