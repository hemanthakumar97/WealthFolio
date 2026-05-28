export class ApiError extends Error {
  status: number;
  detail: string;
  constructor(status: number, detail: string) {
    super(detail);
    this.status = status;
    this.detail = detail;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { ...(init?.headers as Record<string, string>) };
  if (init?.body && !(init.body instanceof FormData)) {
    headers['Content-Type'] ??= 'application/json';
  }
  const res = await fetch(path, { ...init, credentials: 'include', headers });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.detail ?? res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export interface User {
  id: number;
  email: string;
  username?: string | null;
  profile_picture?: string | null;
}

export const authApi = {
  status: () => request<{ needs_setup: boolean; message: string }>('/api/auth/status'),
  setup: (body: { email: string; password: string; username?: string }) =>
    request<User>('/api/auth/setup', { method: 'POST', body: JSON.stringify(body) }),
  login: (body: { email: string; password: string }) =>
    request<User>('/api/auth/login', { method: 'POST', body: JSON.stringify(body) }),
  logout: () => request<{ message: string }>('/api/auth/logout', { method: 'POST' }),
  me: () => request<User>('/api/auth/me'),
  updateMe: (body: { email?: string; username?: string }) =>
    request<User>('/api/auth/me', { method: 'PUT', body: JSON.stringify(body) }),
  changePassword: (body: { current_password: string; new_password: string }) =>
    request<{ message: string }>('/api/auth/change-password', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  uploadProfilePicture: (file: File) => {
    const form = new FormData();
    form.append('file', file);
    return request<User>('/api/auth/profile-picture', { method: 'POST', body: form });
  },
};

export const healthApi = {
  check: () => request<{ status: string }>('/api/health'),
};

// --- Transactions ---

export type UploadStatus = 'PENDING' | 'PROCESSING' | 'COMPLETED' | 'FAILED' | 'PARTIAL';

export interface UploadResult {
  upload_id: number;
  filename: string;
  platform: string;
  records_total: number;
  records_imported: number;
  records_duplicates: number;
  records_errors: number;
  errors: string[];
}

export interface UploadHistoryItem {
  id: number;
  filename: string;
  platform: string;
  status: UploadStatus;
  records_total: number;
  records_imported: number;
  records_duplicates: number;
  records_errors: number;
  uploaded_at: string;
  processed_at?: string | null;
}

export interface UploadDetail extends UploadHistoryItem {
  errors: string[];
}

export interface ImportLog {
  id: number;
  row_number: number | null;
  status: 'IMPORTED' | 'DUPLICATE' | 'ERROR';
  message: string;
}

export interface SupportedFormat {
  name: string;
  platform: string;
  markers: string[];
  notes: string;
}

export interface ManualTransactionRequest {
  symbol: string;
  segment?: string;
  transaction_date: string;
  transaction_type: string;
  order_id?: string;
  amount: string;
  quantity: string;
  platform: string;
}

export const transactionsApi = {
  upload: (file: File, platform?: string) => {
    const form = new FormData();
    form.append('file', file);
    if (platform) form.append('platform', platform);
    return request<UploadResult>('/api/transactions/', { method: 'POST', body: form });
  },
  manual: (body: ManualTransactionRequest) =>
    request<{ id: number; message: string }>('/api/transactions/manual', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  remove: (id: number) => request<void>(`/api/transactions/${id}`, { method: 'DELETE' }),
  history: () => request<UploadHistoryItem[]>('/api/transactions/history'),
  historyItem: (id: number) => request<UploadDetail>(`/api/transactions/history/${id}`),
  logs: (uploadId: number) => request<ImportLog[]>(`/api/transactions/${uploadId}/logs`),
  formats: () => request<SupportedFormat[]>('/api/transactions/formats'),
};

// --- Instruments ---

export interface Instrument {
  id: number;
  name: string;
  isin: string | null;
  amfi_code: string | null;
  gfinance_symbol: string | null;
  yahoo_symbol: string | null;
  asset_type: 'MF' | 'ETF' | 'STOCK' | 'BOND' | 'GOLD' | 'OTHER';
  currency: 'INR' | 'USD';
  exchange: string | null;
  created_at: string;
  updated_at: string;
}

// --- Portfolio ---

export interface HoldingInfo {
  instrument_id: number;
  instrument_name: string;
  isin: string | null;
  gfinance_symbol: string | null;
  asset_type: string;
  currency: 'INR' | 'USD';
  total_units: number;
  avg_buy_price: number;
  invested_amount: number;
  current_price: number | null;
  last_price_date: string | null;
  last_fetched_at: string | null;
  current_value: number | null;
  profit_loss: number | null;
  profit_loss_percent: number | null;
  platform: string;
  categories: string[];
}

export interface ClosedPositionInfo {
  instrument_id: number;
  instrument_name: string;
  asset_type: string;
  status: 'CLOSED' | 'PARTIAL';
  total_units_bought: number;
  total_units_sold: number;
  units_held: number;
  avg_buy_price: number;
  avg_sell_price: number;
  total_invested: number;
  total_sold_value: number;
  realized_profit_loss: number;
  roi_percent: number;
  exit_date: string | null;
  platform: string;
  categories: string[];
}

export interface AllocationItem {
  name: string;
  invested: number;
  current_value: number;
  count: number;
  percentage: number;
}

export interface PortfolioSummary {
  total_invested: number;
  total_current_value: number;
  total_profit_loss: number;
  profit_loss_percent: number;
  xirr: number | null;
  holdings_count: number;
  last_updated: string | null;
  by_asset_type: AllocationItem[];
  by_platform: AllocationItem[];
}

export interface SnapshotRow {
  date: string;
  invested: number;
  value: number;
  profit: number;
  profit_percent: number;
}

export interface MonthlyReturn {
  month: string;
  invested: number;
  value: number;
  profit: number;
  profit_percent: number;
}

export const trendsApi = {
  portfolio: (params?: { days?: number; asset_type?: string; instrument_id?: number }) => {
    const qs = new URLSearchParams();
    if (params?.days) qs.set('days', String(params.days));
    if (params?.asset_type) qs.set('asset_type', params.asset_type);
    if (params?.instrument_id) qs.set('instrument_id', String(params.instrument_id));
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    return request<SnapshotRow[]>(`/api/trends/portfolio${suffix}`);
  },
  monthlyReturns: (months?: number) => {
    const qs = months !== undefined ? `?months=${months}` : '';
    return request<MonthlyReturn[]>(`/api/trends/monthly-returns${qs}`);
  },
  allocationHistory: (days?: number) => {
    const qs = days !== undefined ? `?days=${days}` : '';
    return request<any[]>(`/api/trends/allocation-history${qs}`);
  },
  benchmark: (symbol: string) =>
    request<any[]>(`/api/trends/benchmark?symbol=${encodeURIComponent(symbol)}`),
  backfill: (opts?: { rewrite?: boolean; instrument_id?: number }) =>
    request<{ status: string }>('/api/trends/backfill', {
      method: 'POST',
      body: JSON.stringify({ rewrite: opts?.rewrite ?? true, instrument_id: opts?.instrument_id ?? 0 }),
      headers: { 'Content-Type': 'application/json' },
    }),
  backfillStatus: () =>
    request<{ running: boolean; snapshots_created: number; error: string; completed_at: string }>(
      '/api/trends/backfill/status',
    ),
};

export const portfolioApi = {
  summary: () => request<PortfolioSummary>('/api/portfolio/summary'),
  holdings: (params?: { platform?: string; asset_type?: string; sort_by?: string }) => {
    const qs = new URLSearchParams();
    if (params?.platform) qs.set('platform', params.platform);
    if (params?.asset_type) qs.set('asset_type', params.asset_type);
    if (params?.sort_by) qs.set('sort_by', params.sort_by);
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    return request<HoldingInfo[]>(`/api/portfolio/holdings${suffix}`);
  },
  closedPositions: (params?: { platform?: string; asset_type?: string }) => {
    const qs = new URLSearchParams();
    if (params?.platform) qs.set('platform', params.platform);
    if (params?.asset_type) qs.set('asset_type', params.asset_type);
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    return request<ClosedPositionInfo[]>(`/api/portfolio/closed-positions${suffix}`);
  },
};

export const ASSET_TYPES = [
  { value: 'MF', label: 'MF' },
  { value: 'ETF', label: 'ETF' },
  { value: 'STOCK', label: 'Stock' },
  { value: 'US_FUND', label: 'US Fund' },
  { value: 'METAL', label: 'Metal' },
  { value: 'BOND', label: 'Bond' },
  { value: 'OTHER', label: 'Other' },
] as const;

// --- AI Settings ---

// --- AI Signal ---

export type SignalAction = 'BUY_MORE' | 'HOLD' | 'SWITCH' | 'PARTIAL_SELL' | 'BOOK_PROFIT';

export interface HoldingSignal {
  instrument_id: number;
  instrument_name: string;
  action: SignalAction;
  confidence: number;
  reason: string;
  key_points: string[];         // 3–5 data-backed bullet points from AI
  qualitative_note: string;     // AI qualitative context about the fund/stock
  tax_note: string;
  zero1_score?: number;
}

export interface HoldingsSignalsResult {
  signals: HoldingSignal[];
  risk_profile: string;
  generated_at?: string;
}

type ChartPoint = { d: string; v: number };

export interface MFMetrics {
  category: string;
  rolling_1y_avg_pct: number;
  rolling_3y_avg_pct: number;
  rolling_3y_min_pct: number;
  consistency_1y_pct: number;
  std_dev_1y_pct: number;
  std_dev_5y_median: number;
  sharpe_1y: number;
  max_drawdown_3y_pct: number;
  beta: number;
  aum_cr: number;
  ter_pct: number;
  zero1_score: number;
  available_max: number;
  data_gaps?: string[];
  nav_history: ChartPoint[];
  roll_1y_series: ChartPoint[];
  drawdown_series: ChartPoint[];
}

// ETFMetrics has the same shape as MFMetrics (same 6-metric Zero1 framework).
export type ETFMetrics = MFMetrics;

export interface StockMetrics {
  symbol: string;
  company_name: string;
  sector: string;
  industry: string;
  zero1_score: number;
  available_max: number;
  data_gaps?: string[];
  // Valuation
  trailing_pe: number;
  forward_pe: number;
  price_to_book: number;
  peg_ratio: number;
  // Profitability
  roe: number;
  profit_margin: number;
  operating_margin: number;
  // Growth
  revenue_growth: number;
  earnings_growth: number;
  // Financial health
  debt_to_equity: number;
  current_ratio: number;
  // Size & income
  market_cap_cr: number;
  dividend_yield: number;
  beta: number;
  week_52_high: number;
  week_52_low: number;
  // Price-based
  return_1y_pct: number;
  max_drawdown_3y_pct: number;
  price_history: ChartPoint[];
  drawdown_series: ChartPoint[];
}

export type InstrumentMetricsResponse =
  | { kind: 'mf'; metrics: MFMetrics }
  | { kind: 'etf'; metrics: ETFMetrics }
  | { kind: 'stock'; metrics: StockMetrics };

export const signalApi = {
  loadHoldings: (riskProfile: string) =>
    request<HoldingsSignalsResult>(`/api/ai/signal/holdings?risk_profile=${riskProfile}`),
  generateHoldings: (body: { risk_profile: string; goal: string; horizon: string }) =>
    request<HoldingsSignalsResult>('/api/ai/signal/holdings', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  getInstrumentMetrics: (instrumentId: number) =>
    request<InstrumentMetricsResponse>(`/api/ai/signal/instrument-metrics/${instrumentId}`),
  refreshScores: () =>
    request<{ total: number; success: number; errors?: string[] }>('/api/ai/signal/refresh-scores', { method: 'POST' }),
  getMFMetrics: (instrumentId: number) =>
    request<MFMetrics>(`/api/ai/signal/mf-metrics/${instrumentId}`),
};

export interface AISettings {
  provider: 'anthropic' | 'openai' | 'antigravity' | '';
  model: string;
  masked_key: string;
  configured: boolean;
}

export const aiSettingsApi = {
  get: () => request<AISettings>('/api/settings/ai'),
  put: (body: { provider: string; api_key: string; model: string }) =>
    request<AISettings>('/api/settings/ai', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
};

export const instrumentsApi = {
  list: (params?: { asset_type?: string; search?: string }) => {
    const qs = new URLSearchParams();
    if (params?.asset_type) qs.set('asset_type', params.asset_type);
    if (params?.search) qs.set('search', params.search);
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    return request<Instrument[]>(`/api/instruments${suffix}`);
  },
  updateAssetType: (id: number, asset_type: string) =>
    request<Instrument>(`/api/instruments/${id}`, {
      method: 'PUT',
      body: JSON.stringify({ asset_type }),
    }),
};

// --- Categories ---

export interface Category {
  id: number;
  name: string;
  description: string | null;
  color: string | null;
  instrument_count: number;
  created_at: string;
  updated_at: string;
}

export interface CategoryInstrument {
  instrument_id: number;
  instrument_name: string;
  isin: string | null;
  asset_type: string;
  weight: number;
}

export const categoriesApi = {
  list: () => request<Category[]>('/api/categories'),
  create: (body: { name: string; description?: string; color?: string }) =>
    request<Category>('/api/categories', { method: 'POST', body: JSON.stringify(body) }),
  get: (id: number) => request<Category>(`/api/categories/${id}`),
  update: (id: number, body: { name?: string; description?: string; color?: string }) =>
    request<Category>(`/api/categories/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  remove: (id: number) => request<void>(`/api/categories/${id}`, { method: 'DELETE' }),
  listInstruments: (id: number) =>
    request<CategoryInstrument[]>(`/api/categories/${id}/instruments`),
  addInstrument: (id: number, instrument_id: number, weight?: number) =>
    request<{ instrument_id: number; category_id: number; weight: number }>(
      `/api/categories/${id}/instruments`,
      { method: 'POST', body: JSON.stringify({ instrument_id, weight: weight ?? 1 }) },
    ),
  removeInstrument: (id: number, instr_id: number) =>
    request<void>(`/api/categories/${id}/instruments/${instr_id}`, { method: 'DELETE' }),
};

// --- Allocations ---

export type AllocCategory = 'EQUITY' | 'GOLD' | 'DEBT' | 'US_EQUITY' | 'OTHERS';

export interface InstrumentAllocation {
  instrument_id: number;
  instrument_name: string;
  alloc_category: AllocCategory;
  current_value: number;
  current_percent: number;
  target_percent: number;
  deviation: number;
  sip_amount: number;
  current_sip_percent: number;
  sip_target_percent: number;
  sip_deviation: number;
  updated_at: string;
}

export interface CategoryAllocation {
  alloc_category: AllocCategory;
  target_percent: number;
  sip_target_percent: number;
  sip_amount: number | null;
  current_value: number;
  current_percent: number;
  deviation: number;
  updated_at: string;
}

export interface AllocationOverview {
  total_value: number;
  total_sip: number;
  instrument_allocations: InstrumentAllocation[];
  category_allocations: CategoryAllocation[];
}

export interface DistributionItem {
  instrument_id: number;
  instrument_name: string;
  alloc_category: AllocCategory;
  target_percent: number;
  amount: number;
}

export const allocationsApi = {
  overview: () => request<AllocationOverview>('/api/allocations'),
  updateInstrument: (
    id: number,
    body: {
      target_percent?: number;
      sip_amount?: number;
      sip_target_percent?: number;
      alloc_category?: AllocCategory;
    },
  ) =>
    request<InstrumentAllocation>(`/api/allocations/instruments/${id}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  updateCategory: (
    name: AllocCategory,
    body: { target_percent?: number; sip_target_percent?: number; sip_amount?: number },
  ) =>
    request<CategoryAllocation>(`/api/allocations/categories/${name}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  calculateDistribution: (amount: number) =>
    request<{ amount: number; items: DistributionItem[]; total_target: number }>(
      '/api/allocations/calculate-distribution',
      { method: 'POST', body: JSON.stringify({ amount }) },
    ),
};

// --- Market Mood ---

export interface IndexConfigItem {
  index_name: string;
  display_name: string;
  category: string;
  is_active: boolean;
}

export interface MarketMoodResponse {
  index_name: string;
  display_name: string;
  current_pe: number;
  median_pe_3yr: number;
  pct_25: number;
  pct_75: number;
  mood: 'Green' | 'Yellow' | 'Red';
  recommendation: string;
  data_points: number;
  as_of: string;
  is_active: boolean;
}

export interface PEDataPoint {
  date: string;
  pe_ratio: number;
}

export interface IndexDetailsResponse extends MarketMoodResponse {
  history: PEDataPoint[];
}

export interface MMIData {
  value: number;
  mood: string;
}

export interface CachedMMI {
  data: MMIData | null;
  updated_at: string | null;
}

export interface MetalPrice {
  usd: number;
  inr: number;
  valuation: 'OVERVALUED' | 'FAIR DEAL' | 'UNDERVALUED';
  is_extended?: boolean;
}

export interface MetalsAlert {
  active: boolean;
  status: 'NEUTRAL' | 'CAUTION' | 'EUPHORIA_WATCH' | 'CRITICAL_EUPHORIA';
  message: string;
}

export interface MetalsDriver {
  title: string;
  desc: string;
}

export interface MetalsData {
  gold: MetalPrice;
  silver: MetalPrice;
  usd_inr: number;
  gsr: number;
  dma_50_dist: number;
  alert: MetalsAlert;
  market_drivers: MetalsDriver[];
  updated_at: string;
}

export interface CachedMetals {
  data: MetalsData | null;
  updated_at: string | null;
}

export interface WatchlistItem {
  id: number;
  symbol: string;
  created_at: string;
}

export const marketApi = {
  config: () => request<IndexConfigItem[]>('/api/portfolio/market/config'),
  toggleConfig: (index_name: string) =>
    request<IndexConfigItem>(
      `/api/portfolio/market/config/${encodeURIComponent(index_name)}/toggle`,
      {
        method: 'POST',
      },
    ),
  moods: () => request<MarketMoodResponse[]>('/api/portfolio/market/moods'),
  indexHistory: (index_name: string, period?: string) => {
    const qs = period ? `?period=${period}` : '';
    return request<IndexDetailsResponse>(
      `/api/portfolio/market/history/${encodeURIComponent(index_name)}${qs}`,
    );
  },
  mmi: () => request<CachedMMI>('/api/portfolio/market/mmi'),
  metals: () => request<CachedMetals>('/api/portfolio/market/metals'),
  refresh: () =>
    request<{ mmi_updated: boolean; metals_updated: boolean; refreshed_at: string }>(
      '/api/portfolio/market/refresh',
      { method: 'POST' },
    ),
  sync: () => request<{ message: string }>('/api/portfolio/market/sync', { method: 'POST' }),
  ingestPE: (body: {
    index_name: string;
    date: string;
    pe_ratio?: number;
    pb_ratio?: number;
    div_yield?: number;
  }) =>
    request<{ message: string }>('/api/portfolio/market/ingest-pe', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  watchlist: () => request<WatchlistItem[]>('/api/portfolio/market/watchlist'),
  addWatchlist: (symbol: string) =>
    request<WatchlistItem>('/api/portfolio/market/watchlist', {
      method: 'POST',
      body: JSON.stringify({ symbol }),
    }),
  removeWatchlist: (symbol: string) =>
    request<void>(`/api/portfolio/market/watchlist/${encodeURIComponent(symbol)}`, {
      method: 'DELETE',
    }),
};

// --- Backfill ---

export interface BackfillStatus {
  is_running: boolean;
  total: number;
  processed: number;
  current: string;
  logs: string[];
  warnings: string[];
  errors: string[];
  started_at: string | null;
  finished_at: string | null;
}

export interface MFAPIMatch {
  schemeCode: number;
  schemeName: string;
}

export interface AutoSearchResult {
  source: 'mfapi' | 'yahoo';
  code: string;
  name: string;
  exchange?: string;
  exch_disp?: string;
}

export const backfillApi = {
  status: () => request<BackfillStatus>('/api/backfill/status'),
  startFromSheet: (instrument_id: number, sheet_url: string) => {
    const form = new FormData();
    form.append('instrument_id', String(instrument_id));
    form.append('sheet_url', sheet_url);
    return request<{ status: string }>('/api/backfill/start', { method: 'POST', body: form });
  },
  startFromFile: (instrument_id: number, file: File) => {
    const form = new FormData();
    form.append('instrument_id', String(instrument_id));
    form.append('file', file);
    return request<{ status: string }>('/api/backfill/start', { method: 'POST', body: form });
  },
  autoSearch: (instrument_id: number) =>
    request<AutoSearchResult[]>(`/api/backfill/auto-search?instrument_id=${instrument_id}`),
  startAuto: (instrument_id: number, source: 'mfapi' | 'yahoo', code: string) =>
    request<{ status: string }>('/api/backfill/start-auto', {
      method: 'POST',
      body: JSON.stringify({ instrument_id, source, code }),
    }),
  searchMFAPI: (q: string) =>
    request<MFAPIMatch[]>(`/api/backfill/mfapi/search?q=${encodeURIComponent(q)}`),
  startFromMFAPI: (instrument_id: number, amfi_code: string) =>
    request<{ status: string }>('/api/backfill/mfapi/start', {
      method: 'POST',
      body: JSON.stringify({ instrument_id, amfi_code }),
    }),
  updateAMFICode: (id: number, amfi_code: string) =>
    request<{ amfi_code: string }>(`/api/backfill/instruments/${id}/amfi-code`, {
      method: 'PUT',
      body: JSON.stringify({ amfi_code }),
    }),
  updateGFinanceSymbol: (id: number, gfinance_symbol: string) =>
    request<{ success: string; gfinance_symbol: string }>(
      `/api/backfill/instruments/${id}/gfinance-symbol`,
      { method: 'PUT', body: JSON.stringify({ gfinance_symbol }) },
    ),
};

export interface GmailStatus {
  configured: boolean;
  connected: boolean;
  email: string;
  client_id: string;
  masked_secret: string;
  lookback_days: number;
}

export interface EmailWatchRule {
  id: number;
  name: string;
  platform: string;
  from_email: string;
  subject_query: string;
  parser_type: string;
  enabled: boolean;
  created_at: string;
}

export interface DryRunResult {
  rule_id: number;
  rule_name: string;
  query: string;
  matched_count: number;
  messages: Array<{
    msg_id: string;
    subject: string;
    date: string;
    status: 'IMPORTED' | 'DUPLICATE' | 'ERROR' | 'DRY_RUN_MATCH';
    message: string;
  }>;
}

export const settingsApi = {
  gmailStatus: () => request<GmailStatus>('/api/settings/gmail'),
  gmailDisconnect: () =>
    request<{ disconnected: boolean }>('/api/settings/gmail', { method: 'DELETE' }),
  saveGmailConfig: (body: { client_id: string; client_secret: string; lookback_days: number }) =>
    request<GmailStatus>('/api/settings/gmail', { method: 'PUT', body: JSON.stringify(body) }),
  listRules: () => request<EmailWatchRule[]>('/api/settings/email-rules'),
  testRun: () => request<DryRunResult[]>('/api/gmail/test', { method: 'POST' }),
  createRule: (body: Omit<EmailWatchRule, 'id' | 'enabled' | 'created_at'>) =>
    request<EmailWatchRule>('/api/settings/email-rules', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  toggleRule: (id: number, enabled: boolean) =>
    request<EmailWatchRule>(`/api/settings/email-rules/${id}`, {
      method: 'PUT',
      body: JSON.stringify({ enabled }),
    }),
  deleteRule: (id: number) =>
    request<void>(`/api/settings/email-rules/${id}`, { method: 'DELETE' }),
};
