import { useState, useRef } from 'react';
import { createFileRoute, useSearch } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Camera,
  Save,
  KeyRound,
  User,
  Mail,
  CheckCircle2,
  XCircle,
  Plug,
  Plus,
  Trash2,
  Power,
  PowerOff,
  ChevronUp,
  PlayCircle,
  Loader2,
  Sparkles,
  Eye,
  EyeOff,
  Settings,
  AlertCircle,
} from 'lucide-react';

import { authApi, aiSettingsApi, settingsApi, type AISettings } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import { usePrivacy } from '@/lib/privacy';

// ---- API helpers ------------------------------------------------------------
const api = settingsApi;

const PARSER_LABELS: Record<string, string> = {
  groww_mf: 'Groww MF',
  zerodha_contract_note: 'Zerodha Equity',
  indmoney_us: 'IndMoney US',
};

const PLATFORM_COLORS: Record<string, string> = {
  GROWW: 'border-blue-500/40 text-blue-600 bg-blue-500/5 dark:text-blue-400',
  ZERODHA: 'border-violet-500/40 text-violet-600 bg-violet-500/5 dark:text-violet-400',
  INDMONEY: 'border-emerald-500/40 text-emerald-600 bg-emerald-500/5 dark:text-emerald-400',
};

const PARSER_TYPES = [
  { value: 'groww_mf', label: 'Groww MF allotment email' },
  { value: 'zerodha_contract_note', label: 'Zerodha equity contract note (PDF)' },
  { value: 'indmoney_us', label: 'IndMoney US stock SIP email' },
];

export const Route = createFileRoute('/_app/settings')({
  component: SettingsPage,
});

// Helper for dynamic string masking
function maskString(val: string, masked: boolean) {
  if (!masked) return val;
  if (!val) return '';
  if (val.includes('@')) {
    const [left, right] = val.split('@');
    if (left.length <= 2) return `*@${right}`;
    return `${left[0]}•••@${right}`;
  }
  if (val.length > 8) {
    return `${val.slice(0, 4)}••••••••${val.slice(-4)}`;
  }
  return '••••••••';
}

// --- Profile tab ---
function ProfileTab() {
  const qc = useQueryClient();
  const { data: user } = useQuery({ queryKey: ['me'], queryFn: authApi.me });
  const { masked } = usePrivacy();
  const [email, setEmail] = useState('');
  const [username, setUsername] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const updateMut = useMutation({
    mutationFn: () =>
      authApi.updateMe({ email: email || undefined, username: username || undefined }),
    onSuccess: (u) => {
      qc.setQueryData(['me'], u);
      setEmail('');
      setUsername('');
    },
  });

  const picMut = useMutation({
    mutationFn: (file: File) => authApi.uploadProfilePicture(file),
    onSuccess: (u) => qc.setQueryData(['me'], u),
  });

  const initials = (user?.username ?? user?.email ?? '?').slice(0, 2).toUpperCase();
  const displayUser = user?.username ? maskString(user.username, masked) : 'User Profile';
  const displayEmail = user?.email ? maskString(user.email, masked) : '—';

  return (
    <div className="max-w-xl space-y-6 rounded-2xl border border-border/85 bg-card/45 p-6 shadow-xl shadow-black/5 backdrop-blur-md">
      <div className="flex items-center gap-3">
        <div className="rounded-lg bg-primary/10 p-2 text-primary">
          <User className="size-5" />
        </div>
        <div>
          <h3 className="text-lg font-bold">Profile Identity</h3>
          <p className="text-xs text-muted-foreground">
            Manage your identity and profile metadata.
          </p>
        </div>
      </div>

      {/* Avatar */}
      <div className="flex items-center gap-6 rounded-xl border border-border/50 bg-muted/20 p-4">
        <div className="group relative cursor-pointer" onClick={() => fileRef.current?.click()}>
          {user?.profile_picture ? (
            <img
              src={user.profile_picture}
              alt="Profile"
              className="h-20 w-20 rounded-full object-cover shadow-md ring-4 ring-background transition-all duration-300 group-hover:opacity-85"
            />
          ) : (
            <div className="grid h-20 w-20 place-items-center rounded-full bg-gradient-to-tr from-primary to-primary/80 text-2xl font-black text-primary-foreground shadow-md ring-4 ring-background transition-all duration-300 group-hover:scale-105">
              {initials}
            </div>
          )}
          <div className="absolute inset-0 flex items-center justify-center rounded-full bg-black/40 opacity-0 transition-all duration-300 group-hover:opacity-100">
            <Camera className="size-5 animate-pulse text-white" />
          </div>
          <input
            ref={fileRef}
            type="file"
            accept=".jpg,.jpeg,.png,.gif,.webp"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) picMut.mutate(f);
            }}
          />
        </div>
        <div className="space-y-1">
          <p className="text-lg font-bold text-foreground">{displayUser}</p>
          <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <Mail className="size-3 text-muted-foreground" />
            {displayEmail}
          </p>
          {picMut.isPending && (
            <p className="animate-pulse text-xs font-medium text-primary">Uploading fresh photo…</p>
          )}
          {picMut.isSuccess && (
            <p className="flex items-center gap-1 text-xs font-semibold text-emerald-500">
              ✓ Photo updated successfully.
            </p>
          )}
          {picMut.error && (
            <p className="text-xs font-semibold text-destructive">Error: {picMut.error.message}</p>
          )}
        </div>
      </div>

      {/* Profile form */}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate();
        }}
        className="space-y-4"
      >
        <div className="space-y-1.5">
          <Label htmlFor="s-email" className="text-xs font-bold text-foreground/80">
            Email address
          </Label>
          <Input
            id="s-email"
            type="email"
            placeholder={user?.email ? maskString(user.email, masked) : 'email@example.com'}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="rounded-xl border-border/80 bg-card/30"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="s-username" className="text-xs font-bold text-foreground/80">
            Username
          </Label>
          <Input
            id="s-username"
            placeholder={user?.username ? maskString(user.username, masked) : 'Your display name'}
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            className="rounded-xl border-border/80 bg-card/30"
          />
        </div>
        {updateMut.error && (
          <div className="flex items-center gap-2 rounded-lg border border-rose-500/10 bg-rose-500/5 p-3 text-sm text-rose-500">
            <AlertCircle className="size-4 shrink-0" />
            <span>{updateMut.error.message}</span>
          </div>
        )}
        {updateMut.isSuccess && (
          <div className="flex items-center gap-2 rounded-lg border border-emerald-500/10 bg-emerald-500/5 p-3 text-sm text-emerald-500">
            <CheckCircle2 className="size-4 shrink-0" />
            <span>Profile metadata updated successfully.</span>
          </div>
        )}
        <Button
          type="submit"
          disabled={updateMut.isPending || (!email && !username)}
          className="gap-2 rounded-xl font-bold shadow-md shadow-primary/10 transition-all hover:scale-[1.02] active:scale-[0.98]"
        >
          <Save className="size-4" />
          {updateMut.isPending ? 'Saving…' : 'Save Changes'}
        </Button>
      </form>
    </div>
  );
}

// --- Security tab ---
function SecurityTab() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [matchErr, setMatchErr] = useState('');

  const changeMut = useMutation({
    mutationFn: () => authApi.changePassword({ current_password: current, new_password: next }),
    onSuccess: () => {
      setCurrent('');
      setNext('');
      setConfirm('');
      setMatchErr('');
    },
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      setMatchErr('Passwords do not match.');
      return;
    }
    setMatchErr('');
    changeMut.mutate();
  };

  return (
    <div className="max-w-xl space-y-6 rounded-2xl border border-border/85 bg-card/45 p-6 shadow-xl shadow-black/5 backdrop-blur-md">
      <div className="flex items-center gap-3">
        <div className="rounded-lg bg-primary/10 p-2 text-primary">
          <KeyRound className="size-5" />
        </div>
        <div>
          <h3 className="text-lg font-bold">Change Password</h3>
          <p className="text-xs text-muted-foreground">
            Keep your login credentials secure. Requires minimum 6 characters.
          </p>
        </div>
      </div>

      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="s-cur" className="text-xs font-bold text-foreground/80">
            Current Password
          </Label>
          <Input
            id="s-cur"
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            className="rounded-xl border-border/80 bg-card/30"
            required
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="s-new" className="text-xs font-bold text-foreground/80">
            New Password
          </Label>
          <Input
            id="s-new"
            type="password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            className="rounded-xl border-border/80 bg-card/30"
            minLength={6}
            required
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="s-conf" className="text-xs font-bold text-foreground/80">
            Confirm New Password
          </Label>
          <Input
            id="s-conf"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            className="rounded-xl border-border/80 bg-card/30"
            required
          />
        </div>

        {matchErr && (
          <div className="flex items-center gap-2 rounded-lg border border-rose-500/10 bg-rose-500/5 p-3 text-sm text-rose-500">
            <AlertCircle className="size-4 shrink-0" />
            <span>{matchErr}</span>
          </div>
        )}
        {changeMut.error && (
          <div className="flex items-center gap-2 rounded-lg border border-rose-500/10 bg-rose-500/5 p-3 text-sm text-rose-500">
            <AlertCircle className="size-4 shrink-0" />
            <span>{changeMut.error.message}</span>
          </div>
        )}
        {changeMut.isSuccess && (
          <div className="flex items-center gap-2 rounded-lg border border-emerald-500/10 bg-emerald-500/5 p-3 text-sm text-emerald-500">
            <CheckCircle2 className="size-4 shrink-0" />
            <span>Password updated successfully.</span>
          </div>
        )}

        <Button
          type="submit"
          disabled={changeMut.isPending || !current || !next || !confirm}
          className="gap-2 rounded-xl font-bold shadow-md shadow-primary/10 transition-all hover:scale-[1.02] active:scale-[0.98]"
        >
          <KeyRound className="size-4" />
          {changeMut.isPending ? 'Changing…' : 'Change Password'}
        </Button>
      </form>
    </div>
  );
}

// --- Types ---
interface DryRunTransaction {
  instrument: string;
  type: string;
  quantity: string;
  price: string;
  amount: string;
  date: string;
  order_id: string;
}
interface DryRunEmail {
  message_id: string;
  subject: string;
  received_at: string;
  already_imported: boolean;
  transactions: DryRunTransaction[];
  parse_errors?: string[];
}
interface LocalDryRunResult {
  rule_id: number;
  rule_name: string;
  parser_type: string;
  emails: DryRunEmail[];
}

// --- Test run results panel ---
function TestResults({ results, onClose }: { results: LocalDryRunResult[]; onClose: () => void }) {
  const totalEmails = results.reduce((s, r) => s + (r.emails?.length ?? 0), 0);
  const totalTx = results.reduce(
    (s, r) => s + (r.emails ?? []).reduce((es, e) => es + (e.transactions?.length ?? 0), 0),
    0,
  );

  return (
    <div className="space-y-0 overflow-hidden rounded-2xl border border-border/80 bg-card/60 shadow-xl backdrop-blur-md">
      <div className="flex items-center justify-between border-b bg-muted/40 px-5 py-4">
        <div>
          <p className="text-sm font-bold text-foreground">Dry-run Results</p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Parsed <span className="font-semibold text-primary">{totalEmails}</span> emails ·
            Identified <span className="font-semibold text-emerald-500">{totalTx}</span>{' '}
            transactions to import (dry-run, nothing saved).
          </p>
        </div>
        <button
          onClick={onClose}
          className="rounded-lg p-1.5 text-xs font-semibold text-muted-foreground transition-all hover:bg-muted hover:text-foreground"
        >
          Dismiss Panel
        </button>
      </div>

      <div className="scrollbar-thin max-h-96 divide-y divide-border/40 overflow-y-auto">
        {results.map((rule) => (
          <div key={rule.rule_id} className="space-y-3 p-4">
            <div className="flex items-center gap-2">
              <span className="rounded-full border border-border bg-muted px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                {rule.rule_name}
              </span>
              <span className="text-xs text-muted-foreground">
                ({rule.emails?.length ?? 0} matching email{rule.emails?.length !== 1 ? 's' : ''})
              </span>
            </div>

            {!rule.emails?.length ? (
              <p className="pl-2 text-xs italic text-muted-foreground">
                No matching emails detected in Gmail watch rules.
              </p>
            ) : (
              rule.emails.map((email) => (
                <div
                  key={email.message_id}
                  className="space-y-3 rounded-xl border border-border/50 bg-muted/20 p-4 transition-all duration-300 hover:border-border"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-bold text-foreground">{email.subject}</p>
                      <p className="mt-0.5 text-[10px] font-medium text-muted-foreground/80">
                        {email.received_at}
                      </p>
                    </div>
                    {email.already_imported && (
                      <span className="shrink-0 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-0.5 text-[10px] font-bold text-emerald-600">
                        imported
                      </span>
                    )}
                  </div>

                  {email.parse_errors?.length ? (
                    <div className="space-y-1 rounded-lg border border-rose-500/20 bg-rose-500/10 px-3 py-2">
                      {email.parse_errors.map((e, i) => (
                        <p key={i} className="text-xs font-semibold text-rose-500">
                          ⚠️ Parse Error: {e}
                        </p>
                      ))}
                    </div>
                  ) : null}

                  {email.transactions?.length ? (
                    <div className="overflow-hidden rounded-xl border border-border/60 bg-card/30">
                      <div className="overflow-x-auto">
                        <table className="w-full text-left text-xs">
                          <thead>
                            <tr className="border-b border-border/40 bg-muted/40 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                              <th className="px-4 py-2">Instrument</th>
                              <th className="px-4 py-2 text-center">Type</th>
                              <th className="px-4 py-2 text-right">Qty</th>
                              <th className="px-4 py-2 text-right">Price</th>
                              <th className="px-4 py-2 text-right">Amount</th>
                              <th className="px-4 py-2 text-center">Date</th>
                            </tr>
                          </thead>
                          <tbody className="divide-y divide-border/30">
                            {email.transactions.map((tx, i) => (
                              <tr key={i} className="transition-colors hover:bg-muted/10">
                                <td className="max-w-[150px] truncate px-4 py-2 font-semibold text-foreground/90">
                                  {tx.instrument}
                                </td>
                                <td className="px-4 py-2 text-center">
                                  <span
                                    className={cn(
                                      'inline-flex rounded-full px-2 py-0.5 text-[9px] font-black tracking-wide',
                                      tx.type === 'BUY'
                                        ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                                        : 'bg-rose-500/10 text-rose-600 dark:text-rose-400',
                                    )}
                                  >
                                    {tx.type}
                                  </span>
                                </td>
                                <td className="px-4 py-2 text-right font-mono tabular-nums text-foreground/80">
                                  {tx.quantity}
                                </td>
                                <td className="px-4 py-2 text-right font-mono tabular-nums text-foreground/80">
                                  ₹{tx.price}
                                </td>
                                <td className="px-4 py-2 text-right font-mono tabular-nums text-foreground/80">
                                  ₹{tx.amount}
                                </td>
                                <td className="px-4 py-2 text-center text-muted-foreground">
                                  {tx.date}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  ) : !email.parse_errors?.length ? (
                    <p className="pl-2 text-xs italic text-muted-foreground">
                      No transactions parsed from this email pattern.
                    </p>
                  ) : null}
                </div>
              ))
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Add rule form ---
function AddRuleForm({ onSave, onCancel }: { onSave: () => void; onCancel: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState({
    name: '',
    platform: '',
    from_email: '',
    subject_query: '',
    parser_type: '',
  });

  const create = useMutation({
    mutationFn: () => api.createRule(form),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['settings', 'email-rules'] });
      onSave();
    },
  });

  const set = (k: string, v: string) => setForm((f) => ({ ...f, [k]: v }));
  const valid = form.name && form.from_email && form.parser_type;

  return (
    <div className="space-y-4 rounded-2xl border border-primary/20 bg-primary/5 p-5 shadow-inner shadow-primary/5 backdrop-blur-md">
      <div className="flex items-center justify-between">
        <p className="text-sm font-bold text-primary">New Monitoring Rule</p>
        <span className="h-2 w-2 animate-pulse rounded-full bg-primary" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label className="text-xs font-bold text-foreground/80">Rule Name</Label>
          <Input
            placeholder="e.g. Groww SIP Auto Import"
            value={form.name}
            onChange={(e) => set('name', e.target.value)}
            className="rounded-xl border-border/80 bg-card/40"
          />
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs font-bold text-foreground/80">Parser Type</Label>
          <select
            value={form.parser_type}
            onChange={(e) => set('parser_type', e.target.value)}
            className="h-9 w-full rounded-xl border border-border/80 bg-card/40 px-3 text-xs font-semibold text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
          >
            <option value="">Select platform parser…</option>
            {PARSER_TYPES.map((p) => (
              <option key={p.value} value={p.value} className="bg-card">
                {p.label}
              </option>
            ))}
          </select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs font-bold text-foreground/80">Sender Email</Label>
          <Input
            placeholder="e.g. noreply@groww.in"
            value={form.from_email}
            onChange={(e) => set('from_email', e.target.value)}
            className="rounded-xl border-border/80 bg-card/40"
          />
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs font-bold text-foreground/80">
            Subject Filter (Gmail Query)
          </Label>
          <Input
            placeholder='e.g. subject:"Groww Mutual Fund"'
            value={form.subject_query}
            onChange={(e) => set('subject_query', e.target.value)}
            className="rounded-xl border-border/80 bg-card/40"
          />
        </div>
      </div>
      {create.isError && (
        <p className="text-xs font-semibold text-rose-500">Error: {String(create.error)}</p>
      )}
      <div className="flex gap-2 pt-2">
        <Button
          size="sm"
          onClick={() => create.mutate()}
          disabled={!valid || create.isPending}
          className="rounded-xl px-4 font-bold"
        >
          {create.isPending ? 'Saving…' : 'Save Rule'}
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel} className="rounded-xl px-4 font-bold">
          Cancel
        </Button>
      </div>
    </div>
  );
}

// --- Integrations tab ---
function IntegrationsTab() {
  const qc = useQueryClient();
  const { masked } = usePrivacy();
  const [showAddForm, setShowAddForm] = useState(false);
  const [testResults, setTestResults] = useState<LocalDryRunResult[] | null>(null);

  const [clientId, setClientId] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [lookbackDays, setLookbackDays] = useState(7);
  const [showSecret, setShowSecret] = useState(false);
  const [saved, setSaved] = useState(false);
  const [synced, setSynced] = useState(false);

  const { data: gmailStatus, isLoading: gmailLoading } = useQuery({
    queryKey: ['settings', 'gmail'],
    queryFn: api.gmailStatus,
    staleTime: 10_000,
  });

  if (!synced && gmailStatus) {
    setClientId(gmailStatus.client_id ?? '');
    setLookbackDays(gmailStatus.lookback_days ?? 7);
    setSynced(true);
  }

  const { data: rules = [], isLoading: rulesLoading } = useQuery({
    queryKey: ['settings', 'email-rules'],
    queryFn: api.listRules,
    staleTime: 30_000,
  });

  const saveGmail = useMutation({
    mutationFn: () => api.saveGmailConfig({ client_id: clientId, client_secret: clientSecret, lookback_days: lookbackDays }),
    onSuccess: (data) => {
      qc.setQueryData(['settings', 'gmail'], data);
      setClientSecret('');
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    },
  });

  const disconnect = useMutation({
    mutationFn: api.gmailDisconnect,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings', 'gmail'] }),
  });

  const testRun = useMutation({
    mutationFn: api.testRun,
    onSuccess: (data) => setTestResults(data as unknown as LocalDryRunResult[]),
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => api.toggleRule(id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings', 'email-rules'] }),
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteRule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings', 'email-rules'] }),
  });

  const connected = gmailStatus?.connected ?? false;
  const configured = gmailStatus?.configured ?? false;
  const enabledCount = rules.filter((r) => r.enabled).length;

  const savedClientId = gmailStatus?.client_id ?? '';
  const savedLookbackDays = gmailStatus?.lookback_days ?? 7;
  const hasChanges = clientId !== savedClientId || clientSecret !== '' || lookbackDays !== savedLookbackDays;
  const canSave =
    hasChanges && clientId.trim() !== '' && (clientSecret.trim() !== '' || configured);

  const maskedClientId = maskString(clientId, masked);
  const maskedSecretValue = clientSecret
    ? maskString(clientSecret, masked)
    : configured
      ? maskString(gmailStatus?.masked_secret ?? '', masked)
      : 'Google Console Client Secret';

  const displayGmailAccount = gmailStatus?.email ? maskString(gmailStatus.email, masked) : '';

  return (
    <div className="max-w-2xl space-y-6">
      {/* Gmail auth card */}
      <div className="space-y-6 rounded-2xl border border-border/80 bg-card/45 p-6 shadow-xl shadow-black/5">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
          <div className="grid h-12 w-12 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
            <Mail className="size-6" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-lg font-bold">Gmail Integration</p>
            <p className="mt-0.5 text-xs text-muted-foreground">
              Sync transaction structures directly from automated broker emails.
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {configured ? (
              <Badge
                variant="outline"
                className="rounded-full border-emerald-500/30 bg-emerald-500/5 px-2.5 py-0.5 font-semibold text-emerald-600 dark:text-emerald-400"
              >
                Configured
              </Badge>
            ) : (
              <Badge
                variant="outline"
                className="rounded-full border-amber-500/30 bg-amber-500/5 px-2.5 py-0.5 font-semibold text-amber-600 dark:text-amber-400"
              >
                Needs Config
              </Badge>
            )}
            {connected && (
              <Badge
                variant="outline"
                className="rounded-full border-emerald-500/30 bg-emerald-500/5 px-2.5 py-0.5 font-semibold text-emerald-600 dark:text-emerald-400"
              >
                Connected
              </Badge>
            )}
          </div>
        </div>

        {/* Credentials Form */}
        <div className="space-y-4 rounded-xl border border-border/60 bg-muted/10 p-4">
          <p className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/95">
            Gmail OAuth 2.0 Credentials
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="gmail-client-id" className="text-xs font-bold text-foreground/80">
                Client ID
              </Label>
              <Input
                id="gmail-client-id"
                placeholder="Google Console Client ID"
                value={maskedClientId}
                disabled={masked}
                onChange={(e) => setClientId(e.target.value)}
                className="h-9 rounded-xl border-border/80 bg-card/30 font-mono text-xs"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gmail-client-secret" className="text-xs font-bold text-foreground/80">
                Client Secret
              </Label>
              <div className="relative">
                <Input
                  id="gmail-client-secret"
                  type={showSecret ? 'text' : 'password'}
                  placeholder={maskedSecretValue}
                  value={clientSecret}
                  disabled={masked}
                  onChange={(e) => setClientSecret(e.target.value)}
                  className="h-9 rounded-xl border-border/80 bg-card/30 pr-9 font-mono text-xs"
                />
                {!masked && (
                  <button
                    type="button"
                    onClick={() => setShowSecret((v) => !v)}
                    className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                  >
                    {showSecret ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                  </button>
                )}
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gmail-lookback-days" className="text-xs font-bold text-foreground/80">
                Lookback Days
              </Label>
              <Input
                id="gmail-lookback-days"
                type="number"
                min={1}
                max={90}
                value={lookbackDays}
                disabled={masked}
                onChange={(e) => setLookbackDays(Math.max(1, parseInt(e.target.value) || 7))}
                className="h-9 rounded-xl border-border/80 bg-card/30 font-mono text-xs"
              />
              <p className="text-[11px] text-muted-foreground">
                How many days back to scan Gmail for broker emails.
              </p>
            </div>
          </div>

          {saveGmail.isError && (
            <p className="text-xs font-semibold text-rose-500">
              Error: {(saveGmail.error as Error).message}
            </p>
          )}
          {saved && (
            <p className="flex items-center gap-1.5 text-xs font-semibold text-emerald-500">
              <CheckCircle2 className="inline size-3.5" /> OAuth credentials updated and validated.
            </p>
          )}

          <div className="flex justify-end pt-1">
            <Button
              size="sm"
              onClick={() => saveGmail.mutate()}
              disabled={saveGmail.isPending || !canSave || masked}
              className="h-8.5 gap-1.5 rounded-lg px-4 text-xs font-bold"
            >
              <Save className="size-3.5" />
              {saveGmail.isPending ? 'Saving…' : 'Save Credentials'}
            </Button>
          </div>
        </div>

        {/* Connection Action section */}
        {gmailLoading ? (
          <p className="animate-pulse text-sm font-medium text-muted-foreground">
            Consulting Gmail module state…
          </p>
        ) : !configured ? (
          <div className="flex items-center gap-2.5 rounded-xl border border-amber-500/20 bg-amber-500/5 px-4 py-3 text-xs font-medium text-amber-600 dark:text-amber-400">
            <Plug className="size-4 shrink-0 animate-pulse" />
            Establish your Google Client OAuth 2.0 Credentials above to connect rules listening.
          </div>
        ) : connected ? (
          <div className="flex flex-wrap items-center gap-3 border-t border-border/40 pt-4">
            <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <CheckCircle2 className="size-4 text-emerald-500" />
              <span>Connected email account: </span>
              <span className="font-bold text-foreground">{displayGmailAccount}</span>
            </div>
            <div className="mt-2 flex w-full gap-2 sm:ml-auto sm:mt-0 sm:w-auto">
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 rounded-lg text-xs font-semibold"
                asChild
              >
                <a href="/api/gmail/connect">
                  <Plug className="size-3.5" /> Reconnect
                </a>
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 rounded-lg border-destructive/30 text-xs font-semibold text-destructive hover:bg-destructive/10"
                onClick={() => disconnect.mutate()}
                disabled={disconnect.isPending}
              >
                <XCircle className="size-3.5" />
                {disconnect.isPending ? 'Disconnecting…' : 'Disconnect'}
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-3 border-t border-border/40 pt-4 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs font-medium text-muted-foreground">
              Credentials mapped. Connect local app workspace to your personal Gmail account.
            </p>
            <Button size="sm" className="h-8.5 gap-1.5 rounded-lg text-xs font-bold" asChild>
              <a href="/api/gmail/connect">
                <Mail className="size-3.5" /> Connect Gmail
              </a>
            </Button>
          </div>
        )}
      </div>

      {/* Monitoring rules */}
      <div className="space-y-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-base font-bold">Monitoring Rules</p>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {rulesLoading
                ? 'Fetching monitoring lists…'
                : `${enabledCount} of ${rules.length} parsing pipelines active — automated runs at 7 AM IST`}
            </p>
          </div>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => testRun.mutate()}
              disabled={testRun.isPending || !connected}
              className="gap-1.5 rounded-lg text-xs font-semibold"
              title={
                !connected
                  ? 'Connect Gmail first'
                  : 'Dry-run: fetch & parse emails without importing'
              }
            >
              {testRun.isPending ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <PlayCircle className="size-3.5" />
              )}
              {testRun.isPending ? 'Testing…' : 'Test Run Sync'}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="gap-1.5 rounded-lg text-xs font-semibold"
              onClick={() => setShowAddForm((v) => !v)}
            >
              {showAddForm ? <ChevronUp className="size-3.5" /> : <Plus className="size-3.5" />}
              {showAddForm ? 'Cancel' : 'Add Rule'}
            </Button>
          </div>
        </div>

        {testRun.isError && (
          <div className="flex items-center gap-2 rounded-lg border border-rose-500/10 bg-rose-500/5 p-3 text-sm text-rose-500">
            <AlertCircle className="size-4 shrink-0" />
            <span>Error during dry-run: {String(testRun.error)}</span>
          </div>
        )}

        {testResults && <TestResults results={testResults} onClose={() => setTestResults(null)} />}

        {showAddForm && (
          <AddRuleForm
            onSave={() => setShowAddForm(false)}
            onCancel={() => setShowAddForm(false)}
          />
        )}

        <div className="divide-y divide-border/40 overflow-hidden rounded-2xl border border-border/80 shadow-sm">
          {rulesLoading ? (
            <p className="p-5 text-center text-sm italic text-muted-foreground">
              Reading brokerage configuration pipelines…
            </p>
          ) : rules.length === 0 ? (
            <p className="p-5 text-center text-sm italic text-muted-foreground">
              No monitoring rules registered. Add one to start auto-ingestion.
            </p>
          ) : (
            rules.map((rule) => (
              <div
                key={rule.id}
                className={cn(
                  'flex items-start gap-4 bg-card/25 px-5 py-4 transition-colors hover:bg-card/45',
                  !rule.enabled && 'opacity-60',
                )}
              >
                {/* Enabled indicator */}
                <div
                  className={cn(
                    'mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full',
                    rule.enabled
                      ? 'bg-emerald-500 shadow-md shadow-emerald-500/30'
                      : 'bg-muted-foreground/30',
                  )}
                />

                {/* Rule info */}
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-bold text-foreground">{rule.name}</span>
                    <Badge
                      variant="outline"
                      className={cn(
                        'rounded-full border px-2 py-0.5 text-[9px] font-bold',
                        PLATFORM_COLORS[rule.platform] ??
                          'border-gray-500/30 bg-gray-500/5 text-gray-500',
                      )}
                    >
                      {PARSER_LABELS[rule.parser_type] ?? rule.parser_type}
                    </Badge>
                  </div>
                  <p className="truncate text-xs font-medium text-muted-foreground">
                    sender: {maskString(rule.from_email, masked)}
                  </p>
                  {rule.subject_query && (
                    <p className="mt-1 inline-block truncate rounded-md border border-primary/10 bg-primary/5 px-2 py-0.5 font-mono text-xs text-primary/80">
                      {rule.subject_query}
                    </p>
                  )}
                </div>

                {/* Actions */}
                <div className="flex shrink-0 items-center gap-1.5 self-center">
                  <button
                    onClick={() => toggle.mutate({ id: rule.id, enabled: !rule.enabled })}
                    title={rule.enabled ? 'Disable rule' : 'Enable rule'}
                    className="h-8.5 w-8.5 grid place-items-center rounded-lg text-muted-foreground transition-all hover:bg-muted hover:text-foreground"
                  >
                    {rule.enabled ? (
                      <PowerOff className="size-4 text-rose-500" />
                    ) : (
                      <Power className="size-4 text-emerald-500" />
                    )}
                  </button>
                  <button
                    onClick={() => {
                      if (confirm(`Delete "${rule.name}"?`)) remove.mutate(rule.id);
                    }}
                    title="Delete rule"
                    className="h-8.5 w-8.5 grid place-items-center rounded-lg text-muted-foreground transition-all hover:bg-destructive/10 hover:text-destructive"
                  >
                    <Trash2 className="size-4" />
                  </button>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

// --- AI Settings tab ---

const AI_PROVIDERS = [
  {
    id: 'anthropic' as const,
    name: 'Anthropic Claude',
    defaultModel: 'claude-sonnet-4-6',
    color: 'text-violet-500 dark:text-violet-400',
    bg: 'bg-violet-500/5',
    border: 'border-violet-500/20 hover:border-violet-500/40',
    placeholder: 'sk-ant-api03-…',
    docsUrl: 'https://console.anthropic.com/settings/keys',
    docsLabel: 'console.anthropic.com',
  },
  {
    id: 'openai' as const,
    name: 'OpenAI GPT',
    defaultModel: 'gpt-4o',
    color: 'text-emerald-500 dark:text-emerald-400',
    bg: 'bg-emerald-500/5',
    border: 'border-emerald-500/20 hover:border-emerald-500/40',
    placeholder: 'sk-proj-…',
    docsUrl: 'https://platform.openai.com/api-keys',
    docsLabel: 'platform.openai.com',
  },
  {
    id: 'antigravity' as const,
    name: 'Google Antigravity',
    defaultModel: 'gemini-3.1-pro-preview',
    color: 'text-blue-500 dark:text-blue-400',
    bg: 'bg-blue-500/5',
    border: 'border-blue-500/20 hover:border-blue-500/40',
    placeholder: 'AIza…',
    docsUrl: 'https://aistudio.google.com/app/apikey',
    docsLabel: 'aistudio.google.com',
  },
] as const;

function AITab() {
  const qc = useQueryClient();
  const { masked } = usePrivacy();
  const { data: settings, isLoading } = useQuery<AISettings>({
    queryKey: ['ai-settings'],
    queryFn: () => aiSettingsApi.get(),
  });

  const [selectedProvider, setSelectedProvider] = useState<'anthropic' | 'openai' | 'antigravity'>(
    'anthropic',
  );
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState('');
  const [showKey, setShowKey] = useState(false);
  const [saved, setSaved] = useState(false);

  // Sync state from loaded settings (runs once)
  const [synced, setSynced] = useState(false);
  const loadedProvider = settings?.provider;
  if (!synced && loadedProvider) {
    if (AI_PROVIDERS.some((p) => p.id === loadedProvider)) {
      setSelectedProvider(loadedProvider as any);
    }
    setModel(settings?.model ?? '');
    setSynced(true);
  }

  const currentProviderCfg = AI_PROVIDERS.find((p) => p.id === selectedProvider) ?? AI_PROVIDERS[0];

  const handleProviderChange = (p: typeof selectedProvider) => {
    setSelectedProvider(p);
    const newCfg = AI_PROVIDERS.find((x) => x.id === p);
    if (!newCfg) return;

    const defaultForNew = newCfg.defaultModel;
    setModel((prev) => {
      const currentDefault = currentProviderCfg.defaultModel;
      if (prev === '' || prev === currentDefault) return defaultForNew;
      return prev;
    });
  };

  const savedModel = settings?.model ?? '';
  const savedProvider = settings?.provider ?? '';
  const hasChanges =
    apiKey.trim() !== '' || model !== savedModel || selectedProvider !== (savedProvider as any);
  const canSave = hasChanges && (settings?.configured || apiKey.trim() !== '');

  const saveMut = useMutation({
    mutationFn: () =>
      aiSettingsApi.put({
        provider: selectedProvider,
        api_key: apiKey,
        model: model || currentProviderCfg.defaultModel,
      }),
    onSuccess: (data) => {
      qc.setQueryData(['ai-settings'], data);
      setApiKey('');
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    },
  });

  const removeMut = useMutation({
    mutationFn: () => aiSettingsApi.put({ provider: '', api_key: '', model: '' }),
    onSuccess: (data) => {
      qc.setQueryData(['ai-settings'], data);
      setApiKey('');
      setModel('');
      setSynced(false);
    },
  });

  const displayMaskedKey =
    settings?.configured && settings.provider === selectedProvider
      ? maskString(settings.masked_key, masked)
      : currentProviderCfg.placeholder;

  return (
    <div className="max-w-xl space-y-6">
      <div className="space-y-1">
        <h3 className="text-lg font-bold">AI Machine Intelligence</h3>
        <p className="text-xs leading-relaxed text-muted-foreground">
          Powers the smart <strong>Signal</strong> features: dynamic portfolio audits, dividend
          tracking, tax optimization, and entry signals. Keys are stored locally inside TimescaleDB.
        </p>
      </div>

      {/* Status badge */}
      {isLoading ? null : settings?.configured ? (
        <div className="flex flex-wrap items-center gap-3 rounded-xl border border-emerald-500/20 bg-emerald-500/5 px-4 py-3 text-sm">
          <div className="flex items-center gap-2 font-bold text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 className="size-4 shrink-0 text-emerald-500" />
            <span>
              {AI_PROVIDERS.find((p) => p.id === settings.provider)?.name ?? settings.provider}{' '}
              Active
            </span>
          </div>
          <span className="rounded border border-border/40 bg-muted/50 px-2 py-0.5 font-mono text-xs text-muted-foreground/80">
            {maskString(settings.masked_key, masked)}
          </span>
          <span className="font-mono text-[10px] text-muted-foreground/85">
            Model:{' '}
            {settings.model ||
              AI_PROVIDERS.find((p) => p.id === settings.provider)?.defaultModel ||
              'default'}
          </span>
          <button
            onClick={() => removeMut.mutate()}
            disabled={removeMut.isPending}
            className="ml-auto text-xs font-semibold text-rose-500 transition-colors hover:text-rose-600"
          >
            {removeMut.isPending ? 'Removing…' : 'Remove Provider'}
          </button>
        </div>
      ) : (
        <div className="flex items-center gap-2.5 rounded-xl border border-border/80 bg-muted/20 px-4 py-3 text-xs font-medium text-muted-foreground">
          <XCircle className="size-4 text-muted-foreground" />
          <span>No AI Model Provider currently connected.</span>
        </div>
      )}

      {/* Provider selector */}
      <div>
        <p className="mb-2 text-xs font-bold uppercase tracking-wide text-muted-foreground/90">
          Choose AI Engine
        </p>
        <div className="grid grid-cols-3 gap-3">
          {AI_PROVIDERS.map((p) => {
            const selected = selectedProvider === p.id;
            const displayModel = selected && model ? model : p.defaultModel;
            return (
              <button
                key={p.id}
                onClick={() => handleProviderChange(p.id)}
                className={cn(
                  'flex flex-col items-start gap-2 rounded-xl border p-4 text-left shadow-sm transition-all duration-300 hover:scale-[1.02] active:scale-[0.98]',
                  selected
                    ? cn(p.border, p.bg, 'border-primary shadow-md shadow-primary/5')
                    : 'border-border/70 bg-card/20 backdrop-blur-sm hover:border-border hover:bg-muted/40',
                )}
              >
                <span
                  className={cn(
                    'text-xs font-bold uppercase tracking-wide',
                    selected ? p.color : 'text-foreground/95',
                  )}
                >
                  {p.name}
                </span>
                <span className="font-mono text-[9px] leading-none text-muted-foreground/80">
                  {displayModel}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      {/* API Key input */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <label className="text-xs font-bold text-foreground/80">API Authorization Key</label>
          <a
            href={currentProviderCfg.docsUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="text-[11px] font-semibold text-primary hover:underline"
          >
            Get provider key → {currentProviderCfg.docsLabel}
          </a>
        </div>
        <div className="relative">
          <Input
            type={showKey ? 'text' : 'password'}
            placeholder={displayMaskedKey}
            value={apiKey}
            disabled={masked}
            onChange={(e) => setApiKey(e.target.value)}
            className="rounded-xl border-border/80 bg-card/30 pr-9 font-mono text-sm"
          />
          {!masked && (
            <button
              type="button"
              onClick={() => setShowKey((v) => !v)}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
            >
              {showKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </button>
          )}
        </div>
        <p className="text-[10px] font-medium text-muted-foreground">
          Stored secure locally — never transmitted to external cloud systems.
        </p>
      </div>

      {/* Model name */}
      <div className="space-y-1.5">
        <label className="text-xs font-bold text-foreground/80">Model Identifier Override</label>
        <Input
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder={currentProviderCfg.defaultModel}
          className="rounded-xl border-border/80 bg-card/30 font-mono text-sm"
        />
        <p className="text-[10px] font-medium text-muted-foreground">
          Leave blank to default to provider recommended preset:{' '}
          <span className="font-mono font-bold">{currentProviderCfg.defaultModel}</span>
        </p>
      </div>

      {saveMut.isError && (
        <p className="text-xs font-semibold text-rose-500">
          Error: {(saveMut.error as Error).message}
        </p>
      )}
      {saved && (
        <p className="flex items-center gap-1.5 text-xs font-semibold text-emerald-500">
          <CheckCircle2 className="inline size-4" /> API key authorization successfully stored.
        </p>
      )}

      <Button
        onClick={() => saveMut.mutate()}
        disabled={saveMut.isPending || !canSave || masked}
        className="gap-2 rounded-xl font-bold shadow-md shadow-primary/10 transition-all hover:scale-[1.02] active:scale-[0.98]"
      >
        <Save className="size-4" />
        {saveMut.isPending ? 'Saving API Key…' : 'Save Connection Key'}
      </Button>

      {/* Info box */}
      <div className="space-y-3 rounded-2xl border border-primary/10 bg-primary/5 p-5 shadow-inner">
        <div className="flex items-center gap-2">
          <Sparkles className="size-4 animate-pulse text-primary" />
          <p className="text-xs font-bold uppercase tracking-wider text-primary">
            Signal Capabilities Powered by AI
          </p>
        </div>
        <ul className="space-y-2 pl-1 text-xs font-medium text-muted-foreground">
          <li className="flex items-start gap-2">
            <span className="select-none font-black text-primary">•</span>
            <span>Comprehensive portfolio analysis with buy/hold/sell signals per holding.</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="select-none font-black text-primary">•</span>
            <span>Real-time LTCG/STCG calculations with exact net post-charges gain.</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="select-none font-black text-primary">•</span>
            <span>₹1.25L annual LTCG tax exemption harvesting & reinvestment optimizer.</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="select-none font-black text-primary">•</span>
            <span>Single stock/fund depth outlook with swing & multi-year trend predictions.</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="select-none font-black text-primary">•</span>
            <span>
              Precise buy zones, trailing stop losses, and target execution price targets.
            </span>
          </li>
        </ul>
      </div>
    </div>
  );
}

// --- Page ---
function SettingsPage() {
  const search = useSearch({ strict: false }) as { tab?: string };
  const defaultTab =
    search?.tab === 'integrations' ? 'integrations' : search?.tab === 'ai' ? 'ai' : 'profile';

  return (
    <div className="space-y-8">
      {/* Page Header */}
      <div className="flex items-center gap-4 border-b border-border/40 pb-5">
        <div className="grid h-12 w-12 place-items-center rounded-2xl border border-primary/20 bg-gradient-to-tr from-primary/20 to-primary/5 text-primary shadow-lg shadow-primary/5">
          <Settings className="animate-spin-slow size-6" />
        </div>
        <div>
          <h1 className="bg-gradient-to-r from-foreground via-foreground/95 to-foreground/80 bg-clip-text text-3xl font-extrabold tracking-tight text-transparent">
            System Preferences
          </h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Configure your personal profile, secure authorization keys, broker integrations, and AI
            modules.
          </p>
        </div>
      </div>

      <Tabs defaultValue={defaultTab} className="space-y-6">
        <TabsList className="rounded-xl border border-border/40 bg-muted/50 p-1 backdrop-blur-md">
          <TabsTrigger
            value="profile"
            className="gap-1.5 rounded-lg px-4 py-2 text-xs font-semibold transition-all data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            <User className="size-3.5" /> Profile
          </TabsTrigger>
          <TabsTrigger
            value="security"
            className="gap-1.5 rounded-lg px-4 py-2 text-xs font-semibold transition-all data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            <KeyRound className="size-3.5" /> Security
          </TabsTrigger>
          <TabsTrigger
            value="integrations"
            className="gap-1.5 rounded-lg px-4 py-2 text-xs font-semibold transition-all data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            <Plug className="size-3.5" /> Integrations
          </TabsTrigger>
          <TabsTrigger
            value="ai"
            className="gap-1.5 rounded-lg px-4 py-2 text-xs font-semibold transition-all data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            <Sparkles className="size-3.5" /> AI Intelligence
          </TabsTrigger>
        </TabsList>
        <TabsContent value="profile" className="mt-0 focus-visible:outline-none">
          <ProfileTab />
        </TabsContent>
        <TabsContent value="security" className="mt-0 focus-visible:outline-none">
          <SecurityTab />
        </TabsContent>
        <TabsContent value="integrations" className="mt-0 focus-visible:outline-none">
          <IntegrationsTab />
        </TabsContent>
        <TabsContent value="ai" className="mt-0 focus-visible:outline-none">
          <AITab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
