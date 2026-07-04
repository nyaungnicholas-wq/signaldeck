// Typed client for the SignalDeck API. Every page goes through this module.
// Default is same-origin ("") — the Next.js app proxies /api/* to the daemon
// (see next.config.ts), so the browser only ever talks to one host (:8323)
// and the daemon port stays internal. Override with NEXT_PUBLIC_SIGNALDECK_API
// only if you want the browser to hit the daemon directly.
export const API_BASE = process.env.NEXT_PUBLIC_SIGNALDECK_API ?? "";

export type Market = "crypto" | "stocks";
export type Horizon = "1h" | "1d" | "1w";
export const HORIZONS: Horizon[] = ["1h", "1d", "1w"];

export interface SymbolInfo {
  id: number;
  symbol: string;
  market: Market;
  name: string;
  active: boolean;
  addedAt: number;
}

export interface ScoreComponent {
  name: string;
  value: number;
  norm: number;
  weight: number;
  contrib: number;
  note: string;
}

export interface Score {
  symbol?: string;
  ts: number;
  horizon: Horizon;
  score: number;
  components: ScoreComponent[];
}

export interface WatchRow extends SymbolInfo {
  lastClose: number;
  dayChangePct: number;
  spark: number[];
  scores: Partial<Record<Horizon, Score>>;
  latestBarTs: number;
}

export interface Bar {
  ts: number;
  o: number;
  h: number;
  l: number;
  c: number;
  v: number;
}

export interface Expectancy {
  horizon: Horizon;
  stateKey: string;
  n: number;
  meanFwd: number;
  medianFwd: number;
  hitRate: number;
  stdev: number;
  updatedAt: number;
}

export interface Insight {
  id: number;
  scope: "symbol" | "market";
  symbol?: string;
  ts: number;
  headline: string;
  body: string;
  data: string;
}

export interface Snap {
  ts: number;
  bid: number;
  ask: number;
  mid: number;
  wmid: number;
  imb: number;
  spread: number;
  applyLatNs: number;
}

export interface SymbolDetail {
  symbol: SymbolInfo;
  coverage: Record<string, { bars: number; from: number; to: number }>;
  scores: Partial<Record<Horizon, Score>>;
  stateKeys: Partial<Record<Horizon, string>>;
  expectancy: Partial<Record<Horizon, Expectancy[]>>;
  insights?: Insight[];
  latestSnap?: Snap;
}

export interface HonestyBucket {
  label: string;
  n: number;
  meanFwd: number;
  hitRate: number;
}

export interface Honesty {
  horizon: Horizon;
  n: number;
  // ic is null when withheld below the independent-N gate (see icGated/icNote).
  ic: number | null;
  buckets: HonestyBucket[];
  points: { score: number; fwd: number; ts: number }[];
  // Phase 0 honesty fields (present since the IC pseudo-replication fix).
  rawN?: number; // raw resolved score_outcomes rows (minute-cadence, inflated)
  independentN?: number; // distinct (symbol, UTC-day) observations — the real N
  minIndependentN?: number; // gate floor for reporting an IC
  icGated?: boolean; // true when the IC is withheld for too few independent obs
  icNote?: string; // "insufficient independent resolutions (k/min)" when gated
  live?: boolean; // false until a live track record clears the gate
  trackLabel?: string; // human label, e.g. "backtested / in-sample …"
}

export interface TrendsMover {
  symbol: string;
  market: Market;
  score: number;
  dayChangePct: number;
}

export interface Trends {
  tracked: number;
  scored: number;
  positive1d: number;
  movers: TrendsMover[];
  marketInsight?: Insight;
}

export interface WorkerRun {
  id: number;
  worker: string;
  startedAt: number;
  finishedAt?: number;
  status: "running" | "ok" | "error";
  detail: string;
}

export interface DQEvent {
  id: number;
  symbol?: string;
  ts: number;
  kind: string;
  detail: string;
}

export interface QualityCoverage {
  symbol: string;
  market: Market;
  active: boolean;
  coverage: Record<string, { bars: number; from: number; to: number }>;
}

export interface Quality {
  symbols: QualityCoverage[];
  events: DQEvent[];
}

export interface Hud {
  available: boolean;
  fetchedAt?: number;
  // trader-hud /api/summary payload, passed through verbatim.
  summary?: Record<string, unknown>;
}

// Optional bearer token for remote-exposed deployments (unset for localhost).
const API_TOKEN = process.env.NEXT_PUBLIC_SIGNALDECK_TOKEN;

// The daemon requires this custom header on every call: its presence is what
// defeats CSRF (a cross-origin attacker page can't send it without a preflight
// that only an allowlisted origin passes). See daemon/internal/api/security.go.
function authHeaders(json: boolean): Record<string, string> {
  const h: Record<string, string> = { "X-Signaldeck": "1" };
  if (json) h["Content-Type"] = "application/json";
  if (API_TOKEN) h["Authorization"] = `Bearer ${API_TOKEN}`;
  return h;
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    cache: "no-store",
    credentials: "include",
    headers: authHeaders(false),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`API ${res.status}: ${body || path}`);
  }
  return res.json() as Promise<T>;
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: "POST",
    credentials: "include",
    headers: authHeaders(true),
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const err = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new Error(err?.error ?? `API ${res.status}`);
  }
  return res.json() as Promise<T>;
}

const q = (symbol: string, market: Market) =>
  `symbol=${encodeURIComponent(symbol)}&market=${market}`;

export const api = {
  health: () => get<{ version: string; uptimeS: number; alpaca: boolean }>("/api/health"),

  // ── auth (session cookie; all calls send credentials: "include") ──
  register: (username: string, password: string) =>
    post<Me>("/api/auth/register", { username, password }),
  login: (username: string, password: string) =>
    post<Me>("/api/auth/login", { username, password }),
  logout: () => post<{ ok: boolean }>("/api/auth/logout", {}),
  me: () => get<Me>("/api/auth/me"),

  watchlist: () => get<WatchRow[]>("/api/watchlist"),
  symbol: (symbol: string, market: Market) =>
    get<SymbolDetail>(`/api/symbol?${q(symbol, market)}`),
  bars: (symbol: string, market: Market, tf: "1m" | "1h" | "1d", limit = 500) =>
    get<Bar[]>(`/api/bars?${q(symbol, market)}&tf=${tf}&limit=${limit}`),
  scoreHistory: (symbol: string, market: Market, horizon: Horizon, days = 30) =>
    get<Score[]>(`/api/scores/history?${q(symbol, market)}&horizon=${horizon}&days=${days}`),
  snaps: (symbol: string, market: Market, seconds = 300) =>
    get<Snap[]>(`/api/snaps?${q(symbol, market)}&seconds=${seconds}`),
  trends: () => get<Trends>("/api/trends"),
  honesty: (horizon: Horizon) => get<Honesty>(`/api/honesty?horizon=${horizon}`),
  quality: () => get<Quality>("/api/quality"),
  agents: () => get<WorkerRun[]>("/api/agents"),
  hud: () => get<Hud>("/api/hud"),
  insights: (limit = 50, kind?: string) =>
    get<Insight[]>(`/api/insights?limit=${limit}${kind ? `&kind=${encodeURIComponent(kind)}` : ""}`),
  subscribe: (symbol: string, market: Market) =>
    post<SymbolInfo>("/api/subscribe", { symbol, market }),
  unsubscribe: (symbol: string, market: Market) =>
    post<SymbolInfo>("/api/unsubscribe", { symbol, market }),
  exportUrl: (kind: "bars" | "scores" | "outcomes", params: string) =>
    `${API_BASE}/api/export/${kind}.csv?${params}`,

  // ── Wave 2: quant capabilities ──
  forecast: (symbol: string, market: Market) =>
    get<Forecast[]>(`/api/forecast?${q(symbol, market)}`),
  backtest: (symbol: string, market: Market, text: string) =>
    post<BacktestResponse>("/api/backtest", { symbol, market, text }),
  risk: (holdings: RiskHolding[], notionalUSD = 100000) =>
    post<RiskResponse>("/api/risk", { holdings, notionalUSD }),
  correlation: () => get<CorrelationResponse>("/api/correlation"),
  portfolio: () => get<PortfolioResponse>("/api/portfolio"),
  portfolioAdd: (symbol: string, market: Market, qty: number, note = "") =>
    post<{ id: number; entryPrice: number; scoreAtEntry: number }>("/api/portfolio/add", {
      symbol, market, qty, note,
    }),
  portfolioClose: (id: number, symbol: string, market: Market) =>
    post<{ closed: number; exitPrice: number }>("/api/portfolio/close", { id, symbol, market }),

  // ── AI agents ──
  aiStatus: () => get<AIStatus>("/api/ai/status"),
  aiAnalyst: () => get<AnalystBrief>("/api/ai/analyst"),
  aiChat: (question: string) => post<ChatAnswer>("/api/ai/chat", { question }),
  aiFiling: (text: string) => post<FilingResult>("/api/ai/filing", { text }),

  // ── prediction + trends ──
  predictions: (symbol: string, market: Market) =>
    get<Record<string, Prediction>>(`/api/predictions?${q(symbol, market)}`),
  calibration: (horizon: "1d" | "1w") => get<Calibration>(`/api/calibration?horizon=${horizon}`),
  regime: () => get<RegimeResponse>("/api/regime"),
  ranking: () => get<RankedRow[]>("/api/ranking"),
  breakouts: () => get<BreakoutRow[]>("/api/breakouts"),

  // ── news / sectors / macro ──
  news: (symbol?: string, market?: Market) =>
    get<NewsItem[]>(symbol ? `/api/news?${q(symbol, market!)}` : "/api/news"),
  sectors: () => get<SectorAgg[]>("/api/sectors"),
  macro: () => get<Macro>("/api/macro"),
  regimeConditioned: (symbol: string, market: Market) =>
    get<RegimeConditioned>(`/api/regime-conditioned?${q(symbol, market)}`),

  // ── storage-permanence wave: dataset accounting (/quality panel) ──
  dataStats: () => get<DataStats>("/api/datastats"),

  // ── alerts wave (session-scoped; the API returns 401 when logged out) ──
  alerts: (unseenOnly = false, limit = 100) =>
    get<AlertRow[]>(`/api/alerts?limit=${limit}${unseenOnly ? "&unseen=1" : ""}`),
  markAlertsSeen: () => post<{ ok: boolean; marked: number }>("/api/alerts/seen", {}),

  // ── Stage 5: own-signal backtester (replay the feature store through the
  // ensemble blend; OOS IC/quintiles/turnover/costed equity vs SPY, gated on
  // independent-N). horizon 1d|1w. ──
  signalBacktest: (horizon: "1d" | "1w") =>
    get<SignalBacktestResponse>(`/api/signal-backtest?horizon=${horizon}`),

  // ── Stage 6: gated model legs (GBM + mean-reversion) with OOS grade. Each
  // leg is used by the ensemble ONLY when its lift > 0. ──
  modelForecasts: (symbol: string, market: Market) =>
    get<ModelForecast[]>(`/api/model-forecasts?${q(symbol, market)}`),
};

// One per-user alert row (alerts wave).
export interface AlertRow {
  id: number;
  symbol?: string;
  market?: Market;
  horizon?: string;
  kind: "breakout" | "regime_change" | "prediction_high" | "prediction_low" | string;
  detail: string;
  ts: number;
  seen: boolean;
}

export interface Me {
  id: number;
  username: string;
  isAdmin: boolean;
}

export interface NewsItem {
  id: string;
  symbol?: string;
  ts: number;
  headline: string;
  url: string;
  source: string;
  sentiment: string;
  score: number;
  rationale: string;
}
export interface SectorAgg {
  Sector: string;
  MeanScore: number;
  MeanRet1M: number;
  N: number;
  Symbols: string[];
}
export interface Macro {
  breadthPct: number;
  positive: number;
  scored: number;
  volPct: number;
  volLabel: string;
  push20Macro?: unknown;
  note: string;
  asOf: number;
}
export interface RegimeCond {
  regime: string;
  n: number;
  meanFwd: number;
  medianFwd: number;
  hitRate: number;
}
export interface RegimeConditioned {
  current: string;
  byHorizon: Record<string, Record<string, RegimeCond>>;
}

export interface Prediction {
  horizon: Horizon;
  ts: number;
  rawProb: number;
  calProb: number;
  nUsed: number;
  components: string;
}
export interface CalBin {
  Lo: number;
  Hi: number;
  MeanPred: number;
  MeanActual: number;
  N: number;
}
export interface Calibration {
  horizon: Horizon;
  n: number;
  bins: CalBin[];
  brier: number;
  reliability: number;
  // Phase 0 labeling: calibration is backtested / in-sample until live.
  live?: boolean;
  trackLabel?: string;
}
export interface RegimeState {
  symbol: string;
  market: Market;
  ts: number;
  label: string;
  strength: number;
  note: string;
}
export interface RegimeChange {
  symbol: string;
  ts: number;
  from: string;
  to: string;
}
export interface RegimeResponse {
  states: RegimeState[] | null;
  changes: RegimeChange[] | null;
}
export interface RankedRow {
  symbol: string;
  market: Market;
  score: number;
  rank: number;
  ret1m: number;
  ret3m: number;
}
export interface BreakoutRow {
  symbol: string;
  ts: number;
  kind: string;
  detail: string;
  strength: number;
}

export interface AIStats {
  day: string;
  calls: number;
  dailyCap: number;
  promptTokens: number;
  outputTokens: number;
  lastCallTs: number;
  lastError: string;
}
export interface AIStatus {
  enabled: boolean;
  model?: string;
  stats?: AIStats;
  charters?: Record<string, string>;
}
export interface AnalystBrief {
  market?: string;
  perSymbol?: Record<string, string>;
  model?: string;
  disabled?: boolean;
  error?: string;
}
export interface ChatAnswer {
  text?: string;
  model?: string;
  disabled?: boolean;
  error?: string;
}
export interface FilingResult {
  Bull?: string;
  Bear?: string;
  RedFlags?: string;
  Raw?: string;
  Model?: string;
  Disabled?: boolean;
  Truncated?: boolean;
  error?: string;
}

export interface Forecast {
  horizon: Horizon;
  ts: number;
  prob: number;
  accuracy: number;
  brier: number;
  auc: number;
  baseRate: number;
  lift: number;
  nTrain: number;
  nEval: number;
}

export interface BacktestResult {
  TotalReturn: number;
  CAGR: number;
  MaxDrawdown: number;
  Sharpe: number;
  NumTrades: number;
  WinRate: number;
  ExposurePct: number;
  Equity: number[];
  VsBuyHold: number;
  // Phase 0 annualization guards. CAGR is only honest to show when CAGRReported;
  // WinRate only when WinRateMeaningful. BarsPerYear/SpanYears drive the copy.
  BarsPerYear?: number;
  SpanYears?: number;
  ClosedTrades?: number;
  CAGRReported?: boolean;
  WinRateMeaningful?: boolean;
}
export interface BacktestResponse {
  strategy: { Name: string };
  result: BacktestResult;
  explain: string;
  ts: number[];
}

export interface RiskHolding {
  symbol: string;
  market: Market;
  weight: number;
}
export interface RiskContribution {
  Symbol: string;
  PctOfRisk: number;
  Weight: number;
  Vol: number;
}
export interface RiskScenario {
  Name: string;
  PnLPct: number;
  Detail: string;
}
export interface RiskReport {
  Confidence: number;
  NotionalUSD: number;
  HistVaRPct: number;
  HistCVaRPct: number;
  ParamVaRPct: number;
  Contributions: RiskContribution[];
  Scenarios: RiskScenario[];
}
export interface RiskResponse {
  report: RiskReport;
  summary: string;
}

export interface CorrelationResponse {
  symbols: string[];
  matrix: number[][];
  mostPair: [string, string];
  mostR: number;
  leastPair: [string, string];
  leastR: number;
  diversification: number;
}

export interface PositionRow {
  id: number;
  symbol: string;
  market: Market;
  qty: number;
  entryPrice: number;
  entryTs: number;
  note: string;
  scoreAtEntry: number;
  open: boolean;
  exitPrice: number | null;
  exitTs: number | null;
  lastPrice: number;
  pnlAbs: number;
  pnlPct: number;
}
export interface PortfolioResponse {
  positions: PositionRow[] | null;
  stat: {
    GrossValue: number;
    TotalPnLAbs: number;
    TotalPnLPct: number;
    Winners: number;
    Losers: number;
    Best: string;
    Worst: string;
  };
}

// ── storage-permanence wave: dataset accounting ──
export interface TableStat {
  table: string;
  rows: number;
  minTs?: number;
  maxTs?: number;
}
export interface RetentionWindows {
  snapshotsHours: number;
  bars1mDays: number;
  bars1hDays: number;
  dailyForever: boolean;
}
export interface DataStats {
  tables: TableStat[];
  dbBytes: number;
  walBytes: number;
  generatedAt: number;
  // tiered-storage wave: cold-archive size on disk + active retention windows.
  archiveBytes?: number;
  retention?: RetentionWindows;
}

// usePoll-style helper for client components (simple interval fetcher).
export function pollMs(): number {
  return 5000;
}

// ── universe-discovery wave (appended block — keep new client functions at
// the END of this file so parallel edits by other agents never collide) ──

// One discovered candidate symbol (universe-discovery worker).
export interface Candidate {
  symbol: string;
  market: Market;
  firstSeenTs: number;
  lastSeenTs: number;
  seenCount: number;
  dollarVol: number;
  pctChange: number;
  status: "new" | "added" | "dismissed" | string;
}

export interface CandidatesResponse {
  candidates: Candidate[];
  active: number; // currently active symbols
  cap: number; // SIGNALDECK_SYMBOL_CAP budget
  autoAddsToday: number;
  autoAddDailyLimit: number;
}

/** Discovered candidates + symbol-budget numbers (status defaults to "new"). */
export function candidates(status: "new" | "added" | "dismissed" | "all" = "new") {
  return get<CandidatesResponse>(`/api/candidates?status=${status}`);
}

/** Promote a candidate: subscribe + this user's watchlist (409 at cap). CSRF header via post(). */
export function addCandidate(symbol: string, market: Market) {
  return post<SymbolInfo>("/api/candidates/add", { symbol, market });
}

/** Dismiss a candidate (sticks across future discovery sweeps). CSRF header via post(). */
export function dismissCandidate(symbol: string, market: Market) {
  return post<{ ok: boolean }>("/api/candidates/dismiss", { symbol, market });
}

// ── learning-flywheel wave (appended block — keep new client functions at
// the END of this file so parallel edits by other agents never collide) ──

// One regime cell of the model's learned self-knowledge: sample counts,
// per-leg evidence (hit-rate + IC), and the derived weights — absent when the
// honesty gate withheld them (n < minSamples, or no leg beat the coin flip).
export interface AdaptiveCell {
  n: number;
  weights?: Record<string, number>;
  hitRates?: Record<string, number>;
  ic?: Record<string, number>;
  legN?: Record<string, number>;
  gated: boolean;
}
export interface AdaptiveWeightsPayload {
  computedTs: number;
  cells: Record<string, AdaptiveCell>;
}
export interface AdaptiveResponse {
  available: boolean;
  minSamples: number;
  fallback: string;
  weights?: AdaptiveWeightsPayload;
}

/** Learned per-regime ensemble weights (the MODEL SELF-KNOWLEDGE panel). */
export function adaptive() {
  return get<AdaptiveResponse>("/api/adaptive");
}

// ── broad-universe wave (appended block — keep new client functions at the
// END of this file so parallel edits by other agents never collide) ──

/**
 * The HOT/BROAD split: how many symbols are in the live STREAMED hot set
 * (real-time ws + full 1m pipeline, bounded by the free ws cap) vs the broad
 * DAILY-ONLY universe (REST daily bars only, hundreds of names), plus the caps
 * governing each. Makes the "wide coverage, still free" story visible.
 */
export interface UniverseSplit {
  streamed: number; // live ws + full 1m pipeline
  streamCap: number; // SIGNALDECK_STREAM_CAP
  dailyOnly: number; // REST daily bars only (broad universe)
  universeCap: number; // SIGNALDECK_UNIVERSE_CAP
  universeSeed: number; // curated seed-list size
}

/** Streamed-count vs daily-universe-count + their caps. */
export function universe() {
  return get<UniverseSplit>("/api/universe");
}

// ── per-symbol agents wave (appended block — keep new client functions at the
// END of this file so parallel edits by other agents never collide) ──

/** One component's measured predictive edge for a symbol (a skill bar). */
export interface SymbolAgentSkill {
  component: string; // pressure | expectancy | forecast | sentiment
  hitRate: number; // directional hit-rate, [0,1]
  hasHR: boolean; // hit-rate was measured (component made directional calls)
  ic: number; // Pearson corr(leg prob, realized fwd return), [-1,1]
  hasIC: boolean; // IC was measured
  n: number; // examples where this component was present
}

/**
 * THIS SYMBOL'S AGENT: the model learned from THIS symbol's own resolved
 * outcomes. tier reports which evidence tier is actually driving the blend
 * (personal | regime | global | static); when it isn't "personal" the symbol
 * is still learning (nSamples/threshold) and the GLOBAL model is in force —
 * activeWeights is then empty, and the UI must say so honestly.
 */
export interface SymbolAgent {
  symbol: string;
  market: Market;
  horizon: Horizon;
  available: boolean; // a model row exists yet
  tier: "personal" | "regime" | "global" | "static";
  personal: boolean; // tier === personal (own model in use)
  nSamples: number; // this symbol's own resolved outcomes for the horizon
  threshold: number; // MinPersonal — samples needed to graduate to personal
  personality: string; // deterministic plain-English read of the skill
  skill: SymbolAgentSkill[]; // per-component measured edge
  activeWeights: Record<string, number>; // blend weights in force (empty unless personal)
  updatedTs: number;
}

/** One symbol+horizon's own agent (tier + personality + skill + weights). */
export function symbolAgent(symbol: string, market: Market, horizon: Horizon) {
  return get<SymbolAgent>(`/api/symbol-agent?${q(symbol, market)}&horizon=${horizon}`);
}

// ── free-data wave / Stage 2 (appended block — keep new client functions at the
// END of this file so parallel edits by other agents never collide) ──

/** One FRED observation (day epoch + level). */
export interface MacroPoint {
  series: string;
  ts: number; // observation day, UTC-midnight epoch seconds
  value: number;
}

/** Overview response (no ?series=): latest of every tracked series. */
export interface MacroOverview {
  latest: Record<string, MacroPoint>;
  tracked: string[]; // series ids we poll (VIXCLS/DGS10/T10Y2Y/DFF)
  note: string;
}

/** One series' chart-ready history (oldest-first) + its latest point. */
export interface MacroSeriesResponse {
  series: string;
  points: MacroPoint[];
  latest: MacroPoint | null;
  count: number;
}

/**
 * FREE MACRO from FRED (St. Louis Fed, keyless CSV endpoint): VIXCLS=VIX,
 * DGS10=10y yield, T10Y2Y=10y-2y spread, DFF=fed funds. Call with no series for
 * an overview (latest of each); with a series for its history.
 */
export function macroSeries(): Promise<MacroOverview>;
export function macroSeries(series: string, limit?: number): Promise<MacroSeriesResponse>;
export function macroSeries(series?: string, limit?: number) {
  if (!series) return get<MacroOverview>("/api/macro-series");
  const l = limit ? `&limit=${limit}` : "";
  return get<MacroSeriesResponse>(`/api/macro-series?series=${encodeURIComponent(series)}${l}`);
}

/** One SEC EDGAR company-fact for a symbol (latest of a metric, or a history point). */
export interface FundamentalRow {
  symbolId: number;
  symbol?: string;
  metric: string; // Revenues | EPS | SharesOutstanding | LatestFilingDate | CIK
  value: number; // dates/CIK are epoch/int values
  asOf: number; // period-end / effective date, epoch seconds
  fetchedAt: number;
}

/**
 * FREE FUNDAMENTALS from SEC EDGAR (companyfacts XBRL). metrics is the latest
 * value of each metric; pass history=<metric> to also get that metric's series.
 * A symbol with no rows hasn't been swept yet or doesn't file with the SEC.
 */
export interface FundamentalsResponse {
  symbol: string;
  metrics: FundamentalRow[];
  note: string;
  history?: FundamentalRow[];
  historyMetric?: string;
}

/** SEC EDGAR fundamentals for one US stock (optionally one metric's history). */
export function fundamentals(symbol: string, historyMetric?: string) {
  const h = historyMetric ? `&history=${encodeURIComponent(historyMetric)}` : "";
  return get<FundamentalsResponse>(`/api/fundamentals?symbol=${encodeURIComponent(symbol)}${h}`);
}

// ─────────────────────────────────────────────────────────────────────────
// PREDICTION LEDGER (Stage 3) (appended block — do not merge into the sections
// above). The append-only, hash-chained audit log of every flagship
// prediction. entry_hash = sha256(prev_hash ‖ canonical-json(entry fields)),
// so recomputing the chain reproduces every head and any silent edit/delete of
// a historical row breaks it — the tamper-evidence buyers/allocators require.

/** One committed ledger entry (chain fields are server-assigned). */
export interface LedgerEntry {
  seq: number; // monotonic append order (chain index)
  predictedAt: number; // wall-clock unix seconds when appended
  symbolId: number;
  horizon: Horizon; // 1d | 1w
  barTs: number; // the prediction's bar timestamp
  rawProb: number; // uncalibrated ensemble probability
  calProb: number; // calibrated probability
  featureHash: string; // sha256 of the persisted feature-vector JSON
  modelVersion: number;
  prevHash: string; // entry_hash of seq-1 ("" for genesis)
  entryHash: string; // the chain link
}

/** Chain-integrity result: intact iff every recomputed hash matches. */
export interface LedgerVerifyResponse {
  intact: boolean;
  count: number; // rows examined
  head: string; // entry_hash of the last row ("" if empty)
  brokenAtSeq?: number; // first seq whose hash/linkage disagrees (tamper)
}

/** The committed ledger entries for one symbol+horizon (newest first). */
export interface LedgerResponse {
  symbol: string;
  horizon: Horizon;
  count: number;
  entries: LedgerEntry[];
}

/** Recompute + verify the whole prediction-ledger hash chain. */
export function ledgerVerify() {
  return get<LedgerVerifyResponse>("/api/ledger/verify");
}

/** The committed ledger entries for one symbol+horizon. */
export function ledger(symbol: string, market: Market, horizon: Horizon = "1d", limit = 100) {
  return get<LedgerResponse>(`/api/ledger?${q(symbol, market)}&horizon=${horizon}&limit=${limit}`);
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 4 — INTERNAL SIMULATED PAPER TRADING (appended block; keep at END).
// GET /api/paper returns a self-contained SIMULATED book driven by the
// platform's own calibrated predictions. It is NOT a live account: no broker,
// no real money. The payload carries live:false + a label so the UI can never
// misrepresent it. Long/flat only; entries/exits fill at the NEXT bar's open
// with realistic per-side costs.

/** One mark on the simulated equity curve. */
export interface PaperEquityPoint {
  ts: number;
  cash: number;
  positionsValue: number;
  equity: number;
}

/** An open simulated position (present => currently long). */
export interface PaperPosition {
  strategy: string;
  symbol?: string;
  qty: number;
  avgPx: number;
  openedTs: number;
}

/** One simulated fill in the append-only trade log. */
export interface PaperTrade {
  id: number;
  strategy: string;
  symbol?: string;
  side: "buy" | "sell";
  qty: number;
  px: number; // fill price = next bar OPEN after the signal
  cost: number; // dollar cost charged on this fill
  ts: number;
  reason: string;
}

/** Costed track-record summary. Stats the sample can't support are gated off. */
export interface PaperSummary {
  startEquity: number;
  lastEquity: number;
  totalReturn: number;
  maxDrawdown: number;
  sharpe: number;
  sharpeValid: boolean;
  winRate: number;
  winRateValid: boolean;
  closedTrades: number;
  turnover: number;
  numFills: number;
  spanYears: number;
}

/** The full /api/paper payload for one simulated strategy. */
export interface PaperResponse {
  strategy: string;
  strategies: string[];
  live: false; // ALWAYS false — this is a simulation
  label: string; // "simulated paper trading — not live money, not advice"
  startCash: number;
  longThresh: number;
  flatThresh: number;
  equity: PaperEquityPoint[];
  positions: PaperPosition[];
  trades: PaperTrade[];
  summary: PaperSummary;
}

/** Fetch the simulated paper-trading book for a strategy (default flagship-1d). */
export function paper(strategy = "flagship-1d", trades = 100) {
  return get<PaperResponse>(
    `/api/paper?strategy=${encodeURIComponent(strategy)}&trades=${trades}`,
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// STAGE 5 — OWN-SIGNAL BACKTESTER (appended block). Types for
// /api/signal-backtest: the OUT-OF-SAMPLE grade of the platform's OWN calibrated
// ensemble signal, replayed from the feature store. Every headline number is
// GATED behind `gated` (min independent-N); with ~0 resolved live outcomes today
// the honest state is gated=true + a note, which the panel renders as
// "insufficient data". Never presented as live (`live` is always false).
// ─────────────────────────────────────────────────────────────────────────────

/** IC (rank correlation of signal vs forward return) at one forward lag. */
export interface SignalICPoint {
  lagDays: number;
  ic: number;
  n: number;
}

/** One signal-quintile's realized forward-return profile (Q1 low .. Q5 high). */
export interface SignalQuintile {
  quintile: number;
  n: number;
  meanSignal: number;
  meanFwd: number;
  hitRate: number;
}

/** One mark of the costed equity curve + SPY buy-and-hold benchmark. */
export interface SignalEquityPoint {
  ts: number;
  strategy: number; // net-of-cost signal-driven equity
  benchmark: number; // SPY buy-and-hold equity (0/flat when SPY untracked)
}

/** The graded result of replaying the platform's own signal out of sample. */
export interface SignalBacktestResult {
  horizon: string;
  rawN: number;
  independentN: number;
  minIndependentN: number;
  gated: boolean; // true => insufficient independent-N; hide headline numbers

  ic: number;
  icDecay: SignalICPoint[];
  quintiles: SignalQuintile[];
  quintileSpread: number; // Q5.meanFwd - Q1.meanFwd
  hitRate: number;
  meanFwd: number;

  turnover: number;
  costBps: number;
  equity: SignalEquityPoint[];
  strategyReturn: number;
  benchmarkReturn: number;
  excessReturn: number;

  live: false; // ALWAYS false — this is a replay, not a live track record
  trackLabel: string;
  note: string;
}

/** Full /api/signal-backtest payload. */
export interface SignalBacktestResponse {
  result: SignalBacktestResult;
  horizons: ("1d" | "1w")[];
  benchmarkSymbol: string; // "SPY"
  hasBenchmark: boolean;
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 6 — GATED MODEL LEGS (appended block; keep at END). One row per model
// leg (GBM + mean-reversion) per horizon, with its LATEST P(up) and its strictly
// out-of-sample grade. The ensemble folds a leg into the blend ONLY when its
// `lift` > 0 — the same honesty gate the linear forecast passes. Empty array =
// the trainer hasn't produced a leg yet (too little resolved history) — an
// honest "no model yet", not a fabricated number.
export interface ModelForecast {
  horizon: Horizon;
  model: "gbm" | "meanrev";
  ts: number; // when trained/graded, unix seconds
  prob: number; // latest P(up) for the most recent bar
  accuracy: number;
  brier: number;
  auc: number;
  baseRate: number;
  lift: number; // OOS lift; leg is USED by the ensemble iff > 0
  nTrain: number;
  nEval: number;
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 7 — LIVE OUT-OF-SAMPLE TRACK RECORD (appended block; keep at END).
// GET /api/track-record grades the platform's OWN calibrated predictions
// (prob frozen at prediction time, graded against realized bars) — winrate,
// Brier, reliability curve, IC — each with a confidence interval and an EXPLICIT
// independent-N gate. Below the gate every headline number is null and `gated`
// is true with a `note`, so the page renders honest and mostly-empty (which is
// the truth today). The payload also embeds the ledger integrity result
// (self-verifying, Stage 3) and a costed paper-equity summary w/ turnover +
// capacity inputs (Stage 4) so the record links to its own evidence.

/** One bin of the reliability (calibration) curve. */
export interface ReliabilityBin {
  lo: number;
  hi: number;
  meanPred: number;
  meanActual: number;
  n: number;
}

/** Descriptive per-market breakdown of the independent record (not a skill claim). */
export interface TrackByMarket {
  market: Market;
  n: number;
  upRate: number; // realized fraction of up moves in this market
  meanFwd: number; // mean realized forward return
  dirHitRate: number; // fraction of directional bets (prob>0.5 == up) that were right
}

/** Compact costed summary of the linked simulated paper book (turnover/capacity). */
export interface TrackPaperSummary {
  available: boolean;
  strategy?: string;
  totalReturn?: number;
  maxDrawdown?: number;
  turnover?: number; // total traded notional / starting equity
  numFills?: number;
  spanYears?: number;
}

/** Linked prediction-ledger integrity (Stage 3) shown on the track record. */
export interface TrackLedger {
  intact: boolean;
  count: number;
  head: string;
}

/** The /api/track-record payload. Skill numbers are null when `gated`. */
export interface TrackRecord {
  horizon: Horizon;
  rawN: number; // raw resolved prediction_outcomes rows (minute-cadence inflated)
  independentN: number; // distinct (symbol, UTC-day) resolutions — the real N
  minIndependentN: number; // gate floor
  gated: boolean; // true => headline numbers withheld (too few independent obs)
  live: true; // this IS a live forward record (prob frozen at prediction time)
  trackLabel: string;
  note?: string; // "not yet significant — k/threshold" when gated

  winRate: number | null;
  winRateCI?: [number, number];
  baseRate?: number;
  brier: number | null;
  brierSkill?: number; // 1 - Brier/Brier_baserate; >0 beats the base-rate constant
  ic: number | null;
  icCI?: [number, number];
  reliabilityScore?: number;

  reliability: ReliabilityBin[];
  byMarket: TrackByMarket[] | null;
  byRegime: unknown[] | null; // null: regime-at-prediction-time not persisted (honest)

  // per-horizon resolved/total counts so the page shows how thin the record is
  coverage?: Record<string, { resolved: number; total: number }>;
  ledger?: TrackLedger;
  paper: TrackPaperSummary;
}

/** Fetch the live out-of-sample track record for a horizon (default 1d). */
export function trackRecord(horizon: Horizon = "1d") {
  return get<TrackRecord>(`/api/track-record?horizon=${horizon}`);
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 7 — CHART OVERLAY MARKERS (appended block; keep at END).
// GET /api/chart-overlays returns markers to overlay on the candlestick chart:
// score extremes (pressure score crossing into strong-buy/sell), regime changes
// (the transition is the signal), and breakouts. Score markers are emitted only
// on the crossing bar (not every extreme bar) to stay legible. Data already
// exists in the DB from the scoring/regime/breakout workers.

/** One annotation placed on the price chart. */
export interface ChartOverlayMarker {
  ts: number; // bar timestamp (unix seconds)
  type: "score" | "regime" | "breakout";
  label: string; // short badge text
  text: string; // longer tooltip
  value: number; // score / breakout strength (0 when N/A)
  up: boolean; // bullish (green, below bar) vs bearish (red, above bar)
}

/** The /api/chart-overlays payload for one symbol. */
export interface ChartOverlays {
  symbol: string;
  market: Market;
  count: number;
  markers: ChartOverlayMarker[];
}

/** Fetch the score/regime/breakout overlay markers for one symbol's chart. */
export function chartOverlays(symbol: string, market: Market, days = 365) {
  return get<ChartOverlays>(`/api/chart-overlays?${q(symbol, market)}&days=${days}`);
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 1: SEC FILINGS INTELLIGENCE (appended block; keep at
// END). Public-domain SEC EDGAR data rendered plain-English. Every payload
// carries an honest `note` about the LEGAL lag of the data (Form 4 ~2
// business days after the trade; 13F quarterly + up to 45 days) — surface it.

/** One SEC filing in the plain-English feed. */
export interface Filing {
  id: string; // SEC accession number
  symbolId: number;
  symbol?: string;
  form: string; // raw form type ("4", "8-K", "S-3", …)
  filedTs: number;
  title: string;
  url: string; // EDGAR archive link
  label: string; // plain-English reading ("8-K — earnings release (Item 2.02)")
}

/** GET /api/filings payload. */
export interface FilingsResponse {
  filings: Filing[] | null;
  count: number;
  form: string;
  symbol?: string;
  note: string;
}

/** Fetch the filings feed (fleet-wide without symbol; form is a prefix filter). */
export function filings(symbol?: string, form?: string, limit = 100) {
  const p = new URLSearchParams();
  if (symbol) p.set("symbol", symbol);
  if (form) p.set("form", form);
  p.set("limit", String(limit));
  return get<FilingsResponse>(`/api/filings?${p.toString()}`);
}

/** One parsed Form 4 insider transaction (aggregated per filing). */
export interface InsiderTrade {
  accession: string;
  symbolId: number;
  symbol?: string;
  insider: string;
  title: string; // officer title / Director / 10% owner
  code: string; // dominant transaction code (P/S/A/M/…)
  codeLabel: string; // honest reading; only P/S are open-market trades
  openMarket: boolean; // true ONLY for P and S
  shares: number;
  price: number;
  value: number;
  txTs: number; // transaction date
  filedTs: number; // filing time (~2 business days AFTER the trade, by law)
}

/** GET /api/insiders payload. */
export interface InsidersResponse {
  trades: InsiderTrade[] | null;
  count: number;
  symbol?: string;
  note: string;
}

/** Fetch insider trades (fleet-wide recent without symbol; code filters P/S/…). */
export function insiders(symbol?: string, code?: string, limit = 100) {
  const p = new URLSearchParams();
  if (symbol) p.set("symbol", symbol);
  if (code) p.set("code", code);
  p.set("limit", String(limit));
  return get<InsidersResponse>(`/api/insiders?${p.toString()}`);
}

/** One 13F position (latest stored period). symbolId null = unmatched issuer. */
export interface InstHolding {
  cik: string;
  manager: string;
  period: string; // report quarter end (YYYY-MM-DD) — lags up to 45 days
  symbolId: number | null;
  symbol?: string;
  cusip: string;
  name: string; // issuer name as filed
  value: number; // as reported (USD)
  shares: number;
}

/** GET /api/institutions payloads (three shapes by query). */
export interface InstitutionsBySymbol {
  holdings: InstHolding[] | null;
  count: number;
  symbol: string;
  note: string;
}
export interface InstitutionsByManager {
  holdings: InstHolding[] | null;
  count: number;
  cik: string;
  note: string;
}
export interface InstitutionsOverview {
  managers: { cik: string; manager: string; period: string; positions: number; totalValue: number }[] | null;
  curated: { cik: number; name: string }[];
  note: string;
}

export function institutionsBySymbol(symbol: string, limit = 50) {
  return get<InstitutionsBySymbol>(`/api/institutions?symbol=${encodeURIComponent(symbol)}&limit=${limit}`);
}
export function institutionsByManager(manager: string, limit = 100) {
  return get<InstitutionsByManager>(`/api/institutions?manager=${encodeURIComponent(manager)}&limit=${limit}`);
}
export function institutionsOverview() {
  return get<InstitutionsOverview>(`/api/institutions`);
}

/** GET /api/dilution?symbol= payload. level unknown = never derived (honest). */
export interface DilutionFlag {
  symbol: string;
  derived: boolean;
  level: "low" | "elevated" | "high" | "unknown";
  reasons: string[];
  updatedTs?: number;
  note: string;
}

/** Fleet-wide flagged list (level elevated/high only). */
export interface DilutionFlagged {
  flagged: { symbol: string; level: string; reasons: string[]; updatedTs: number }[] | null;
  count: number;
  note: string;
}

export function dilution(symbol: string) {
  return get<DilutionFlag>(`/api/dilution?symbol=${encodeURIComponent(symbol)}`);
}
export function dilutionFlagged(limit = 100) {
  return get<DilutionFlagged>(`/api/dilution?limit=${limit}`);
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 2: CONGRESSIONAL TRADES (appended block; keep at END).
// Public-domain STOCK Act disclosures (Senate eFD / House Clerk) via the free
// Stock Watcher community mirrors. HONESTY (surface all of it): disclosures
// LAG 30-45 DAYS BY LAW (lagNote) — never real-time; amounts are the RANGES
// on the disclosure, not exact values; `source` reports mirror health — both
// mirrors are DEAD as of 2026-07-04 (DNS gone / S3 403), so stored history is
// served and the UI must say the source is down, not pretend freshness.

/** One disclosed congressional stock transaction. */
export interface CongressTrade {
  id: string; // deterministic content hash (dedup key)
  chamber: "senate" | "house";
  member: string; // senator / representative as disclosed
  symbol: string; // disclosed ticker (uppercased)
  symbolId: number | null; // null = ticker not tracked by SignalDeck (honest)
  txType: string; // purchase | sale_full | sale_partial | sale | exchange | …
  amountRange: string; // disclosed RANGE ("$1,001 - $15,000"), never exact
  txTs: number; // transaction date (0 = unparseable on the disclosure)
  disclosedTs: number; // filing date — lags the trade 30-45 days by law
}

/** Per-chamber mirror health from the poller's last run. */
export interface CongressChamberStatus {
  ok: boolean;
  fetched?: number;
  new?: number;
  detail?: string; // error text when !ok (honest, not hidden)
}

/** The poller's last mirror-health snapshot (absent before the first run). */
export interface CongressMirrorStatus {
  checkedTs?: number;
  senate?: CongressChamberStatus;
  house?: CongressChamberStatus;
}

/** GET /api/congress payload. */
export interface CongressResponse {
  trades: CongressTrade[] | null;
  count: number;
  symbol?: string;
  member?: string;
  chamber?: string;
  recent90d?: number; // symbol queries only: trades in the last 90d
  lastTxTs?: number; // symbol queries only: latest transaction date
  lagNote: string; // the EXPLICIT legal-lag note — always render it
  note: string;
  source?: CongressMirrorStatus | null;
}

/** Fetch congressional trades (all filters optional). */
export function congress(symbol?: string, member?: string, chamber?: string, limit = 100) {
  const p = new URLSearchParams();
  if (symbol) p.set("symbol", symbol);
  if (member) p.set("member", member);
  if (chamber) p.set("chamber", chamber);
  p.set("limit", String(limit));
  return get<CongressResponse>(`/api/congress?${p.toString()}`);
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 3: ANOMALY LAYER (appended block; keep at END).
// Trade-imbalance + unusual-volatility/volume detections. HONESTY (surface
// all of it): every row is a DESCRIPTIVE z-score of recent activity vs the
// SAME symbol's own trailing baseline — each detail states the window and
// baseline it was measured over — NOT a prediction (`note`). Stock
// "imbalance" is a volume-side PROXY (up-volume vs down-volume on 1m bars;
// free stock data has no order book) and both its detail and `proxyNote`
// say so — always render the labels.

/** One detected anomaly (descriptive statistic, never a prediction). */
export interface AnomalyRow {
  id: number;
  symbol: string;
  market: Market;
  ts: number; // detection instant (last bar/snap ts)
  kind: "anomaly_imbalance" | "anomaly_vol" | "anomaly_volume";
  z: number; // signed z-score (or stated TR/ATR ratio — detail says which)
  detail: string; // states window + baseline (+ proxy label for stocks)
}

/** GET /api/anomalies payload. */
export interface AnomaliesResponse {
  anomalies: AnomalyRow[] | null;
  count: number;
  symbol?: string;
  kind?: string;
  note: string; // "descriptive, not predictions" — always render it
  proxyNote: string; // stock-imbalance proxy label — render near imbalance rows
}

/** Fetch anomalies (all filters optional; no symbol ⇒ fleet-wide feed). */
export function anomalies(symbol?: string, market?: Market, kind?: string, limit = 50) {
  const p = new URLSearchParams();
  if (symbol) p.set("symbol", symbol);
  if (market) p.set("market", market);
  if (kind) p.set("kind", kind);
  p.set("limit", String(limit));
  return get<AnomaliesResponse>(`/api/anomalies?${p.toString()}`);
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 4: HOME SURFACES (appended block; keep at END).
// Ticker tape (index/sector ETFs + BTC + VIX), movers with best-effort mcap,
// and the honest free-data calendar. HONESTY (render every note): tape prices
// are STORED DAILY CLOSES on worker cadence — not live quotes; VIX is the
// FRED VIXCLS daily close (~1 trading day lag); mcap = SEC EDGAR
// SharesOutstanding × last close and is "unavailable" (null) when EDGAR
// hasn't covered a symbol — never fabricated; earnings dates are ESTIMATES
// (last SEC filing date + ~91 days), never confirmed dates. There is NO IPO
// calendar: no free, redistributable source of IPO pricing/listing dates
// exists (EDGAR S-1s show intent, not dates), so per the honesty doctrine
// the surface is omitted rather than faked.

/** One ticker-tape strip entry. hasData=false = registered, no bars yet. */
export interface TapeItem {
  symbol: string;
  label: string;
  kind: "index" | "sector" | "crypto" | "vix";
  market?: Market;
  price: number;
  dayChangePct: number;
  ts: number;
  hasData: boolean;
  note?: string; // VIX carries its FRED-lag note
}

/** GET /api/tape payload. */
export interface TapeResponse {
  items: TapeItem[] | null;
  count: number;
  note: string;
}

/** Fetch the whole ticker-tape strip in one call. */
export function tape() {
  return get<TapeResponse>("/api/tape");
}

/** One gainers/losers row. mcap null = "mcap unavailable" (EDGAR gap). */
export interface MoverRow {
  symbol: string;
  name: string;
  price: number;
  dayChangePct: number;
  mcap: number | null;
  mcapAsOf?: number;
  ts: number;
}

/** GET /api/movers payload. */
export interface MoversResponse {
  gainers: MoverRow[] | null;
  losers: MoverRow[] | null;
  universeN: number;
  minMcap: number;
  unknownMcapExcluded: number; // symbols dropped by the mcap filter for UNKNOWN mcap
  note: string;
  mcapNote: string;
  asOf: number;
}

/** Fetch gainers/losers; minMcap in dollars (0 = no filter). */
export function movers(minMcap = 0, limit = 10) {
  const p = new URLSearchParams();
  if (minMcap > 0) p.set("minMcap", String(minMcap));
  p.set("limit", String(limit));
  return get<MoversResponse>(`/api/movers?${p.toString()}`);
}

/** One latest-observed FRED print (NOT a forward release calendar). */
export interface EconPrint {
  series: string;
  label: string;
  value: number;
  ts: number;
}

/** One "reports soon" row — ALWAYS an estimate; render the EST label. */
export interface EarningsEst {
  symbol: string;
  name: string;
  lastFilingTs: number;
  estTs: number;
  estimate: true;
}

/** GET /api/calendar payload (econ prints + earnings estimates; no IPO — see block comment). */
export interface CalendarResponse {
  econ: EconPrint[] | null;
  econNote: string;
  earningsEst: EarningsEst[] | null;
  earningsNote: string;
  asOf: number;
}

/** Fetch the honest free-data calendar card. */
export function calendar() {
  return get<CalendarResponse>("/api/calendar");
}
