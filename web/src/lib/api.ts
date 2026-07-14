import { getConsecutiveFailures, onRetry, recordFailure, recordSuccess } from "./freshness";

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

// The daemon requires this custom header on every call: its presence is what
// defeats CSRF (a cross-origin attacker page can't send it without a preflight
// that only an allowlisted origin passes). See daemon/internal/api/security.go.
// Bearer tokens (remote deployments) are injected by the server-side proxy —
// never by client code.
function authHeaders(json: boolean): Record<string, string> {
  const h: Record<string, string> = { "X-Signaldeck": "1" };
  if (json) h["Content-Type"] = "application/json";
  return h;
}

async function get<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      cache: "no-store",
      credentials: "include",
      headers: authHeaders(false),
    });
  } catch (e) {
    recordFailure(); // network error — never reached the daemon
    throw e;
  }
  if (!res.ok) {
    // Only 5xx counts as a connectivity failure — a 4xx (401/403/…) means
    // the daemon answered, just not with data.
    if (res.status >= 500) recordFailure();
    const body = await res.text().catch(() => "");
    throw new Error(`API ${res.status}: ${body || path}`);
  }
  recordSuccess();
  return res.json() as Promise<T>;
}

async function post<T>(path: string, body: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method: "POST",
      credentials: "include",
      headers: authHeaders(true),
      body: JSON.stringify(body),
    });
  } catch (e) {
    recordFailure(); // network error — never reached the daemon
    throw e;
  }
  if (!res.ok) {
    if (res.status >= 500) recordFailure();
    const err = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new Error(err?.error ?? `API ${res.status}`);
  }
  recordSuccess();
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

  // ── composite verdict + alert outcomes (types + fns in the appended block
  // at the END of this file; hoisted function declarations, so the shorthand
  // references are safe here) ──
  composite,
  compositeTop,
  alertOutcomes,
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

// ── polling tiers (ms) — pages pick a tier; POLL_DEFAULT is the fallback ──
export const POLL_LIVE = 5000; // tape/order-book style views
export const POLL_FAST = 15000; // actively-watched dashboards
export const POLL_DEFAULT = 30000; // everything else
export const POLL_SLOW = 120000; // slow-moving reference data
const POLL_BACKOFF_CAP = 300000; // 5 min ceiling on failure backoff

// usePoll-style helper for client components.
//
// Legacy form — `setInterval(load, pollMs())` — keeps compiling: with no
// arguments it just returns POLL_DEFAULT.
//
// Managed form — `useEffect(() => pollMs(load, POLL_FAST), [load])` — owns the
// whole loop and returns its cleanup function: pauses while the tab is hidden
// and refetches immediately on visibilitychange, backs off ×2 per consecutive
// API failure (freshness store streak, cap 5 min, reset on any success), and
// re-fires immediately when the freshness store requests a retry.
export function pollMs(): number;
export function pollMs(run: () => void | Promise<void>, baseMs?: number): () => void;
export function pollMs(
  run?: () => void | Promise<void>,
  baseMs: number = POLL_DEFAULT,
): number | (() => void) {
  if (!run) return POLL_DEFAULT;

  let stopped = false;
  let inFlight = false;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const nextDelay = () => Math.min(baseMs * 2 ** getConsecutiveFailures(), POLL_BACKOFF_CAP);

  const schedule = () => {
    if (stopped) return;
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(tick, nextDelay());
  };

  const tick = async () => {
    if (stopped || inFlight) return;
    // Hidden tab: stop the loop here — the visibilitychange handler restarts
    // it (with an immediate refresh) when the tab comes back.
    if (typeof document !== "undefined" && document.hidden) return;
    inFlight = true;
    try {
      await run();
    } catch {
      // The fetch wrappers already recorded the failure; keep looping.
    } finally {
      inFlight = false;
    }
    schedule();
  };

  const onVisibility = () => {
    if (!document.hidden) void tick();
  };

  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onVisibility);
  }
  const offRetry = onRetry(() => void tick());
  schedule();

  return () => {
    stopped = true;
    if (timer !== null) clearTimeout(timer);
    if (typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", onVisibility);
    }
    offRetry();
  };
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
  active: number; // streamed hot-set size (live ws tick)
  cap: number; // stream cap (kept for back-compat; === streamCap)
  streamCap: number; // SIGNALDECK_STREAM_CAP — the small free-ws limit
  monitored: number; // streamed + broad polled universe (the real monitor count)
  monitorCap: number; // SIGNALDECK_UNIVERSE_CAP — the large monitoring ceiling
  autoAddsToday: number;
  autoAddDailyLimit: number;
}

/** Result of adding one candidate: streaming = it got a live-ws slot; monitored
 *  = it is tracked (always true on success). Older daemons omit both fields. */
export interface AddCandidateResult {
  symbol: string;
  market: Market;
  streaming?: boolean;
  monitored?: boolean;
}

/** Result of monitor-all: how many candidates were promoted to monitored. */
export interface MonitorAllResult {
  added: number;
  skipped: number;
  candidates: number;
  monitorCap: number;
  note?: string;
}

/** Discovered candidates + symbol-budget numbers (status defaults to "new"). */
export function candidates(status: "new" | "added" | "dismissed" | "all" = "new") {
  return get<CandidatesResponse>(`/api/candidates?status=${status}`);
}

/** Promote a candidate onto the watchlist: streamed when a live-ws slot is free,
 *  otherwise MONITORED via the broad polled universe (never fails on the stream
 *  cap). CSRF header via post(). */
export function addCandidate(symbol: string, market: Market) {
  return post<AddCandidateResult>("/api/candidates/add", { symbol, market });
}

/** Monitor EVERY new candidate in one action (all promoted to polled stream=0,
 *  bounded by the universe cap). CSRF header via post(). */
export function monitorAllCandidates() {
  return post<MonitorAllResult>("/api/candidates/monitor-all", {});
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
  skill: SymbolAgentSkill[] | null; // per-component measured edge (older daemons serialize null when no model row exists)
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

// ─────────────────────────────────────────────────────────────────────────
// VISUAL KIT — STAGE 3: ONE-CALL DASHBOARD (appended block; keep at END).
// GET /api/dashboard returns every home-screen section in one payload so the
// page stops issuing ~10 fetches: tape + heatmap + gauges + movers + merged
// feed (all cached server-side up to 60s — the payload says so) and, ONLY
// when a session cookie is present, the caller's watchlist sparklines
// (batched server-side, one SQL). HONESTY (render every caption/note):
// every gauge carries its gate caption verbatim (breadth n, VIX FRED lag,
// anomalies are descriptive, confidence is GATED below 30 resolved outcomes
// — show "not significant", never a verdict); heatmap cells with mcap=null
// are UNIFORM size and labeled "sized: mcap unavailable"; all prices are
// stored daily closes on worker cadence, never live quotes.

/** One heatmap tile. mcap null = EDGAR hasn't covered it (uniform size). */
export interface DashHeatCell {
  symbol: string;
  name: string;
  changePct: number;
  mcap: number | null;
  ts: number;
}

/** One kind-tagged row of the merged home feed. */
export interface DashFeedItem {
  kind: "news" | "filing" | "anomaly" | "briefing";
  ts: number;
  symbol?: string;
  market?: Market;
  title: string;
  detail?: string;
  url?: string;
  sentiment?: string; // news only
  sub?: string; // anomaly kind / filing form
  z?: number; // anomaly z-score
}

/** One watchlist row with sparkline closes (oldest→newest, ≤60 points). */
export interface DashWatchSpark {
  symbol: string;
  market: Market;
  name: string;
  closes: number[] | null; // null/empty = no daily bars yet (honest absence)
  lastClose: number;
  dayChangePct: number;
}

/** Breadth gauge: % advancers over symbols with fresh daily bars. */
export interface DashBreadthGauge {
  pct: number;
  advancers: number;
  decliners: number;
  n: number;
  hasData: boolean;
  caption: string; // "breadth over N symbols — …" — ALWAYS render it
}

/** VIX gauge: FRED daily close + conventional regime band. */
export interface DashVixGauge {
  level?: number;
  regime?: "calm" | "normal" | "elevated" | "stressed";
  regimeOrdinal?: number; // 0..3
  ts?: number;
  dayChangePct?: number;
  hasData: boolean;
  caption: string; // FRED ~1-day-lag note — ALWAYS render it
}

/** Anomaly gauge: count in the last 24h (hour-deduped, DESCRIPTIVE). */
export interface DashAnomalyGauge {
  count: number;
  windowH: number;
  caption: string;
}

/** Confidence gauge — GATED: below minResolvedN the caption says n=X/30. */
export interface DashConfidenceGauge {
  avg: number; // mean |cal_prob − 0.5| × 2 over latest 1d predictions
  n: number; // symbols with a prediction
  resolvedN: number;
  minResolvedN: number;
  gated: boolean;
  hasData: boolean;
  caption: string; // the gate text — ALWAYS render it
}

/** GET /api/dashboard payload. watchlist null ⇒ no session (UI: "log in"). */
export interface DashboardResponse {
  asOf: number;
  cacheTtlS: number;
  note: string;
  tape: { items: TapeItem[] | null; note: string };
  heatmap: {
    items: DashHeatCell[] | null;
    n: number;
    mcapCovered: number;
    note: string;
    mcapNote: string;
    sizeNote: string;
  };
  movers: {
    gainers: DashHeatCell[] | null;
    losers: DashHeatCell[] | null;
    note: string;
    mcapNote: string;
  };
  gauges: {
    breadth: DashBreadthGauge;
    vix: DashVixGauge;
    anomalies: DashAnomalyGauge;
    confidence: DashConfidenceGauge;
  };
  feed: { items: DashFeedItem[] | null; count: number; note: string };
  watchlist: {
    sparks: DashWatchSpark[] | null;
    unseenAlerts: number;
    sparkPoints: number;
    note: string;
  } | null;
}

/** Fetch the whole dashboard in ONE call (server caches shared sections 60s). */
export function dashboard() {
  return get<DashboardResponse>("/api/dashboard");
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 4 — DASHBOARD REBUILD (appended block; keep at END).
// The daemon's /api/dashboard watchlist sparks now carry the latest 1d
// ensemble score for the sidebar score chip. Declared as an interface MERGE
// with DashWatchSpark above (same-module declaration merging) so this file
// stays append-only. null/absent = not scored yet — the UI shows "—".
export interface DashWatchSpark {
  /** Latest 1d ensemble score in [-1,+1]; null/absent = pending (honest). */
  score1d?: number | null;
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 5 — VISUAL HUBS (appended block; keep at END).
// (1) screenerRows(): the MARKETS screener reads the PUBLIC whole-universe
//     GET /api/screener (same WatchRow shape, incl. `spark` daily closes)
//     instead of the session-scoped /api/watchlist — the hub renders logged
//     out. (2) latestPredictions(): the SIGNALS predictions table in one
//     batched call. HONESTY: the payload's gate fields (gated/resolvedN/
//     minResolvedN/caption) and the backtested-not-live trackLabel MUST be
//     rendered next to any confidence badge — a badge without its gate
//     caption is a lie of omission. A 404 from an older daemon binary means
//     the endpoint isn't deployed yet — say that, never fake rows.

/** Whole-universe screener rows (public read; spark = last ~30 daily closes). */
export function screenerRows() {
  return get<WatchRow[]>("/api/screener");
}

/** One symbol's newest calibrated prediction (SIGNALS hub table row). */
export interface LatestPredictionRow {
  symbol: string;
  market: Market;
  name: string;
  ts: number;
  rawProb: number;
  calProb: number;
  nUsed: number;
}

/** GET /api/predictions/latest payload — rows + the honesty gate, together. */
export interface LatestPredictionsResponse {
  horizon: "1d" | "1w";
  rows: LatestPredictionRow[] | null;
  n: number;
  resolvedN: number;
  minResolvedN: number;
  gated: boolean;
  caption: string; // gate text ("n=X/30 resolved — not significant yet …") — ALWAYS render
  trackLabel: string; // "backtested / in-sample — not a live track record" — ALWAYS render
  note: string;
}

/** Latest calibrated prediction per active symbol, strongest conviction first. */
export function latestPredictions(horizon: "1d" | "1w") {
  return get<LatestPredictionsResponse>(`/api/predictions/latest?horizon=${horizon}`);
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNAL8 WAVE — STAGE 5: COMPANIES DIRECTORY (appended block; keep at END).
// (1) companiesList(): the full SEC-registered company table (free EDGAR
//     company_tickers_exchange.json, synced daily by companies-sync) joined
//     server-side to OUR tracked data where present. HONESTY: every market
//     column (price/chg/volume/mcap/shares/float) is null for rows we do not
//     track or EDGAR hasn't covered — render "—", NEVER a fabricated number.
//     Prices are stored daily closes on worker cadence, not live quotes;
//     sector = the SEC's own SIC industry description ('' = not classified
//     by a sweep yet). Render note/mcapNote verbatim.
// (2) earningsEst(): the filing-cadence earnings-ESTIMATE calendar — next
//     10-Q/10-K ≈ last periodic filing + ~91d. Every row is an ESTIMATE and
//     the payload's note ("not a confirmed date") MUST be rendered.
// A 404 from either means the running daemon predates this wave — say that,
// never fake rows.

/** One directory row. Null market fields = no real data (render "—"). */
export interface CompanyDirRow {
  ticker: string;
  name: string;
  exchange: string; // "" = SEC lists no exchange (render "—")
  cik: number;
  sic: string;
  sicDesc: string; // "" = not yet classified by a filings sweep
  tracked: boolean;
  price: number | null;
  dayChangePct: number | null;
  volume: number | null;
  mcap: number | null;
  sharesOutstanding: number | null;
  float: number | null;
  barTs?: number;
}

/** One filter facet option (sector / exchange) with its directory count. */
export interface CompanyFacet {
  value: string;
  n: number;
}

/** GET /api/companies payload. */
export interface CompaniesResponse {
  companies: CompanyDirRow[] | null;
  total: number; // filtered count (pagination denominator)
  limit: number;
  offset: number;
  trackedCount: number;
  unknownMcapExcluded: number; // >0 only with a mcap filter active
  directoryCount: number; // 0 = companies-sync hasn't completed a run yet
  lastSyncTs: number;
  sectors: CompanyFacet[] | null;
  exchanges: CompanyFacet[] | null;
  note: string; // provenance + honesty — render verbatim
  mcapNote: string; // mcap/float provenance — render verbatim
  asOf: number;
}

/** Filters for the companies directory (all optional). */
export interface CompaniesQuery {
  q?: string;
  sector?: string;
  exchange?: string;
  mcapMin?: number;
  mcapMax?: number;
  tracked?: boolean;
  limit?: number;
  offset?: number;
}

/** The SEC company directory joined to tracked market data (paginated). */
export function companiesList(f: CompaniesQuery = {}) {
  const p = new URLSearchParams();
  if (f.q) p.set("q", f.q);
  if (f.sector) p.set("sector", f.sector);
  if (f.exchange) p.set("exchange", f.exchange);
  if (f.mcapMin && f.mcapMin > 0) p.set("mcapMin", String(f.mcapMin));
  if (f.mcapMax && f.mcapMax > 0) p.set("mcapMax", String(f.mcapMax));
  if (f.tracked) p.set("tracked", "true");
  if (f.limit) p.set("limit", String(f.limit));
  if (f.offset) p.set("offset", String(f.offset));
  const qs = p.toString();
  return get<CompaniesResponse>(`/api/companies${qs ? `?${qs}` : ""}`);
}

/** One estimated next-report row (10-Q/10-K cadence heuristic — labeled). */
export interface EarningsEstRow {
  symbol: string;
  name: string;
  lastForm: string; // 10-Q | 10-K (the cadence anchor)
  lastFiledTs: number;
  estTs: number; // ESTIMATED next report date (epoch)
  estimate: true;
  overdue: boolean; // estTs already passed (cadence slipped)
}

/** GET /api/earnings-est payload — rows + the mandatory estimate label. */
export interface EarningsEstResponse {
  rows: EarningsEstRow[] | null;
  count: number;
  total: number;
  note: string; // "estimated from filing cadence — not a confirmed date" — ALWAYS render
  asOf: number;
}

/** Filing-cadence earnings estimates, soonest first (labeled, never confirmed). */
export function earningsEst(limit = 100) {
  return get<EarningsEstResponse>(`/api/earnings-est?limit=${limit}`);
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 2 — MAKE THE PROOF VISIBLE (appended block; keep at END).
// (a) GATE COUNTDOWN: /api/track-record now carries a `gate` block — the
// threshold, how many independent (symbol, UTC-day) resolutions remain, and a
// LABELED ESTIMATE of when the gate clears, derived only from the measured
// accrual of the last 7 days. estDaysToUngate is null when nothing accrued
// recently (an honest unknown). firstResolveEta offers, per horizon with ZERO
// resolved outcomes, the estimated timestamp of the first resolution
// (earliest open prediction + its forward window) — also labeled an estimate.
// (b) WEEKLY SIGNAL-BACKTEST PIN: /api/signal-backtest?pinned=1 serves the
// snapshot the signalbt-weekly worker stored on Sunday evening ET, so the page
// can show "as of Sunday" without recomputing; when no pin exists the daemon
// falls back to a live compute and says so via pinnedNote.

/** Measured accrual of independent resolutions over the last 7 days. */
export interface TrackGateAccrual {
  independentNew: number; // distinct new (symbol, UTC-day) resolutions
  tradingDays: number; // NYSE trading days inside the window
  perTradingDay: number; // the measured accrual rate
}

/** The gate-countdown block on /api/track-record. All estimates are labeled. */
export interface TrackGate {
  threshold: number; // independent-N floor (30)
  remaining: number; // max(0, threshold - independentN)
  estDaysToUngate: number | null; // null = unknown (no recent accrual); 0 = clear
  estimate: true; // this block is an estimate, render it as one
  estBasis: string; // plain-English basis of the estimate — render verbatim
  accrual7d: TrackGateAccrual;
  firstResolveEta: Record<string, number | null>; // horizon -> est. first-resolution ts (only when 0 resolved)
  firstResolveEtaNote: string; // estimate label — render verbatim
}

/** TrackRecord + the Stage-2 gate countdown. */
export interface TrackRecordWithGate extends TrackRecord {
  gate?: TrackGate;
}

/** Fetch the track record including the gate-countdown block. */
export function trackRecordWithGate(horizon: Horizon = "1d") {
  return get<TrackRecordWithGate>(`/api/track-record?horizon=${horizon}`);
}

/** /api/signal-backtest payload + the Stage-2 pin envelope. */
export interface SignalBacktestPinnedResponse extends SignalBacktestResponse {
  pinned: boolean; // true = the stored Sunday snapshot, not a fresh compute
  pinnedDay?: string; // NY Sunday the pin covers (YYYY-MM-DD)
  pinnedTs?: number; // when the pin was computed (unix seconds)
  equityDownsampled?: boolean; // stored curve was decimated (stats unaffected)
  pinnedNote?: string; // present when pinned was requested but none exists
}

/** The weekly pinned own-signal backtest (falls back to live + note when absent). */
export function signalBacktestPinned(horizon: "1d" | "1w") {
  return get<SignalBacktestPinnedResponse>(
    `/api/signal-backtest?horizon=${horizon}&pinned=1`,
  );
}

/** A live own-signal backtest compute, typed with the pin envelope (pinned=false). */
export function signalBacktestLive(horizon: "1d" | "1w") {
  return get<SignalBacktestPinnedResponse>(
    `/api/signal-backtest?horizon=${horizon}`,
  );
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 3 — ALERT DELIVERY BEYOND THE MAC (appended block; keep at END).
// GET /api/notify-status reports which outbound delivery transports are
// configured — macOS (always attempted, honestly labeled untracked) plus the
// three optional env-configured remotes (Discord webhook, Telegram bot,
// generic webhook) — with last delivery ts and last SECRET-REDACTED error per
// transport. The payload never contains webhook URLs or tokens. Email is
// deliberately absent: it needs SMTP creds/provider (noted as future).

/** One delivery transport's honest status + the env var(s) that enable it. */
export interface NotifyTransport {
  name: string; // "macos" | "discord" | "telegram" | "webhook"
  configured: boolean;
  env: string; // env var(s) to set in daemon/.env ("(built-in)" for macos)
  note?: string; // honest caveat (e.g. macOS delivery untracked)
  lastOk?: number; // unix seconds of last successful delivery
  lastError?: string; // redacted — never contains secrets
  lastErrorTs?: number;
}

/** GET /api/notify-status payload. */
export interface NotifyStatusResponse {
  transports: NotifyTransport[];
  email: string; // honest "not supported — needs SMTP/provider (future)" note
  note: string; // how remote transports are configured + degrade
}

/** Fetch delivery-transport status (secrets never included). */
export function notifyStatus() {
  return get<NotifyStatusResponse>(`/api/notify-status`);
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 5 — FINRA REG SHO DAILY SHORT SALE VOLUME (appended block; keep at
// END). GET /api/shorts serves FINRA's free, registration-less Consolidated
// NMS daily short-sale volume (universe-scoped to tracked symbols).
// HONESTY — render `caveat` VERBATIM wherever a ratio is shown: this is the
// daily short sale VOLUME ratio, NOT short interest; it includes market-maker
// activity; a high ratio is NOT directly bearish. `latestZ` is descriptive
// (latest ratio vs the symbol's own trailing window), never a signal.

/** One symbol-day of Reg SHO daily short-sale volume. */
export interface ShortVolumePoint {
  symbolId: number;
  symbol?: string;
  day: string; // YYYY-MM-DD trade date
  shortVol: number; // may be fractional (fractional-share trades)
  shortExempt: number;
  totalVol: number;
  shortPct: number; // shortVol/totalVol, 0 when totalVol=0
}

/** One fleet-wide extremes row (+ last ≤30 daily ratios, ASC). */
export interface ShortsExtreme extends ShortVolumePoint {
  symbol: string;
  spark: number[];
}

/** GET /api/shorts?symbol= — one stock's series + labeled descriptive z. */
export interface ShortsSymbolResponse {
  caveat: string; // render verbatim
  note: string;
  symbol: string;
  days: number;
  series: ShortVolumePoint[] | null; // ASC; null/empty = nothing stored yet
  latestZ: number | null; // null = <10 prior days or flat baseline
  zNote: string; // render verbatim next to the z
}

/** GET /api/shorts — fleet-wide latest-day top ratios (floor stated). */
export interface ShortsExtremesResponse {
  caveat: string; // render verbatim
  note: string;
  day: string; // "" = nothing stored yet
  minTotalVol: number;
  floorNote: string;
  extremes: ShortsExtreme[] | null;
  emptyNote?: string; // present when nothing is stored yet
}

/** Fetch one tracked stock's Reg SHO daily short-volume series. */
export function shortsSymbol(symbol: string, days = 30) {
  const p = new URLSearchParams({ symbol, days: String(days) });
  return get<ShortsSymbolResponse>(`/api/shorts?${p.toString()}`);
}

/** Fetch the fleet-wide latest-day short-volume-ratio extremes. */
export function shortsExtremes(limit = 20) {
  return get<ShortsExtremesResponse>(`/api/shorts?limit=${limit}`);
}

// ─────────────────────────────────────────────────────────────────────────
// STAGE 2 — VERDICT CARDS (appended block; keep at END).
// The daemon now attaches each symbol's newest calibrated 1d prediction AND
// its symbol-agent evidence tier to three existing payloads, all batched
// server-side (one VerdictStats read per response, never per-row):
//   (1) /api/dashboard watchlist sparks   → DashWatchSpark merge below
//   (2) /api/screener + /api/watchlist    → WatchRow merge below
//   (3) /api/predictions/latest rows      → LatestPredictionRow merge below
//       (+ top-level tierThreshold on the payload)
// All three are declaration MERGES with the interfaces above (same-module
// declaration merging) so this file stays append-only.
// HONESTY (render rules for <VerdictCard/>):
//   · calProb1d/absent-tier fields are OPTIONAL — an older daemon binary
//     simply omits them and the card shows the honest "NO READ YET", never
//     a fabricated lean;
//   · calProb1d null = no prediction stored yet → "NO READ YET — still
//     collecting evidence";
//   · tier1d "" = no symbol-agent row yet → reads as the static tier
//     ("no learned model yet"), NEVER "own model";
//   · nSamples1d/tierThreshold feed the always-visible tier badge
//     ("still learning 12/40 — using global model");
//   · every verdict is backtested calibration, not a live track record —
//     the card carries that caveat itself.

/** /api/dashboard watchlist sparks now carry the 1d verdict inputs. */
export interface DashWatchSpark {
  /** Newest calibrated 1d P(up); null/absent = no prediction stored (honest). */
  calProb1d?: number | null;
  /** Ensemble legs behind that prediction (0 when calProb1d is null). */
  nUsed1d?: number;
  /** Symbol-agent evidence tier: personal|regime|global|static; "" = no row yet. */
  tier1d?: string;
  /** This symbol's OWN resolved outcomes behind that tier. */
  nSamples1d?: number;
  /** Personal-model graduation gate (nSamples1d/tierThreshold = "12/40"). */
  tierThreshold?: number;
}

/** /api/screener and /api/watchlist rows carry the same 1d verdict inputs. */
export interface WatchRow {
  calProb1d?: number | null;
  nUsed1d?: number;
  tier1d?: string;
  nSamples1d?: number;
  tierThreshold?: number;
}

/** /api/predictions/latest rows now say WHICH model produced each number. */
export interface LatestPredictionRow {
  /** Evidence tier behind the prediction; ""/absent = no agent row yet. */
  tier?: string;
  /** This symbol's own resolved outcomes behind that tier. */
  nSamples?: number;
}

/** /api/predictions/latest payload gains the personal-model gate constant. */
export interface LatestPredictionsResponse {
  /** Samples a symbol needs before its OWN model is trusted (e.g. 40). */
  tierThreshold?: number;
}

// ─────────────────────────────────────────────────────────────────────────
// COMPOSITE VERDICT + ALERT OUTCOMES (appended block; keep at END).
// (a) /api/composite — one symbol's 1–10 composite score built from EXACTLY
//     11 fixed-order factor legs (technical, expectancy, forecast, sentiment,
//     gbm, meanrev, ranking, regime, insiders, shortvol, breakout) plus the
//     additive percentage-point ledger reconciling the legs to the calibrated
//     edge. available:false is an honest miss (`reason` says why) — render
//     `curveNote` either way.
// (b) /api/composite/top — fleet ranking by composite score with prev-rank
//     deltas (null = no previous ranking to compare against).
// (c) /api/alert-outcomes — MEASURED forward returns after past alerts, by
//     kind; every mean/median/hitRate is null when gated below minN (an
//     honest unknown, never a fabricated stat) — render `note` + `method`.
// (d) /api/anomalies rows now also carry {measure, value, proxy} — additive
//     OPTIONAL fields (older daemons omit them); declaration-merged into
//     AnomalyRow below, so this file stays append-only.

/** One composite factor leg (exactly 11 per verdict, fixed order). */
export interface CompositeFactor {
  /** technical|expectancy|forecast|sentiment|gbm|meanrev|ranking|regime|insiders|shortvol|breakout */
  key: string;
  verdict: -1 | 0 | 1; // bearish | neutral/no-read | bullish
  evidence: string; // plain-English basis — render verbatim
  skillHitRate?: number | null; // measured per-leg hit rate; absent/null = unmeasured
  skillIC?: number | null; // measured per-leg IC; absent/null = unmeasured
  skillN?: number | null; // samples behind the skill stats
  gated: boolean; // true = leg excluded from the score
  gateReason?: string; // why (present when gated)
}

/** One additive ledger entry (leg → contribution in percentage points). */
export interface CompositeLedgerEntry {
  leg: string;
  prob?: number | null; // the leg's probability input, when it has one
  contribPp: number;
}

/** The percentage-point ledger reconciling factor legs to the calibrated edge. */
export interface CompositeLedger {
  method: string; // how contributions were attributed — render verbatim
  exact: boolean; // true = entries sum exactly to targetPp
  entries: CompositeLedgerEntry[] | null;
  sumPp: number;
  targetPp: number;
}

/** GET /api/composite — no verdict yet (honest miss; `reason` says why). */
export interface CompositeUnavailable {
  available: false;
  symbol: string;
  market: Market;
  reason: string;
  curveNote: string; // render verbatim
}

/** Conviction — the SECOND axis, orthogonal to the 1–10 rank. A top rank on a
 *  coin-flip-sized, unproven, stale, or self-contradictory edge is LOW
 *  conviction. Older daemons omit it (optional on the payloads below). */
export interface Conviction {
  band: "low" | "moderate" | "high";
  label: string; // "LOW conviction" etc. — render verbatim
  drivers: string[]; // plain-English reasons — render verbatim
  skillNote: string; // the fleet live-edge status — render verbatim
  riskNote: string; // persistent "not a certainty" caveat — render verbatim
}

/** GET /api/composite — the full verdict. */
export interface CompositeAvailable {
  available: true;
  symbol: string;
  market: Market;
  horizon: Horizon;
  ts: number;
  score: number; // 1–10 composite
  curvePct: number;
  edge: number;
  edgeLine: string; // render verbatim
  rawProb: number;
  calProb: number;
  nUsed: number; // ensemble legs behind calProb
  predTs: number; // ts of the prediction the verdict is built on
  factors: CompositeFactor[]; // exactly 11, fixed order (see CompositeFactor.key)
  ledger: CompositeLedger;
  conviction?: Conviction; // second axis (optional — older daemons omit)
  curveNote: string; // render verbatim
  edgeNote: string; // render verbatim
  trackLabel: string; // render verbatim
}

/** GET /api/composite payload — discriminate on `available`. */
export type Composite = CompositeUnavailable | CompositeAvailable;

/** One /api/composite/top ranking row. */
export interface CompositeTopRow {
  symbol: string;
  market: Market;
  horizon: Horizon;
  ts: number;
  score: number;
  curvePct: number;
  edge: number;
  rank: number; // 1 = best
  prevRank: number | null; // null = not present in the previous ranking
  rankChange: number | null; // null = no previous rank to compare
  conviction?: "low" | "moderate" | "high"; // coarse band (optional — older daemons omit)
  convictionLabel?: string;
}

/** GET /api/composite/top payload. */
export interface CompositeTop {
  horizon: Horizon;
  rows: CompositeTopRow[] | null;
  n: number; // rows returned
  total: number; // symbols eligible before the limit
  minCurveN: number;
  curveNote: string; // render verbatim
  edgeNote: string; // render verbatim
  rankNote: string; // render verbatim
  trackLabel: string; // render verbatim
  convictionNote?: string; // render verbatim (optional — older daemons omit)
  skillNote?: string; // fleet live-edge status — render verbatim
}

/** Measured forward returns after one alert kind (nulls = gated below minN). */
export interface AlertKindOutcome {
  kind: string;
  n: number; // alerts of this kind in the window
  n1d: number; // alerts with a resolved 1d forward return
  n5d: number;
  mean1d: number | null; // null = gated (n1d < minN) — an honest unknown
  median1d: number | null;
  hitRate1d: number | null;
  gated1d: boolean;
  mean5d: number | null;
  median5d: number | null;
  gated5d: boolean;
}

/** GET /api/alert-outcomes payload. */
export interface AlertOutcomes {
  kinds: AlertKindOutcome[] | null;
  days: number; // lookback actually used
  sinceTs: number;
  minN: number; // gate floor per kind/window
  note: string; // render verbatim
  method: string; // how forward returns were measured — render verbatim
}

/** /api/anomalies rows also state their measurement basis (additive; older daemons omit). */
export interface AnomalyRow {
  measure?: "z" | "ratio"; // which statistic `value` is
  value?: number; // the measured statistic (the same number `detail` states)
  proxy?: boolean; // true = stock volume-side proxy — render proxyNote
}

/** Fetch one symbol's composite verdict (union — check `available`). */
export function composite(symbol: string, market: Market) {
  return get<Composite>(`/api/composite?${q(symbol, market)}`);
}

/** Fetch the fleet composite ranking (no market = both markets). */
export function compositeTop(limit = 20, market?: Market) {
  const p = new URLSearchParams({ limit: String(limit) });
  if (market) p.set("market", market);
  return get<CompositeTop>(`/api/composite/top?${p.toString()}`);
}

/** Fetch measured post-alert forward returns by alert kind. */
export function alertOutcomes(days = 30) {
  return get<AlertOutcomes>(`/api/alert-outcomes?days=${days}`);
}

// ── live-feed wave (SSE push) ────────────────────────────────────────────
// The daemon streams the newest 1s microstructure snapshot over Server-Sent
// Events so the UI reflects the live book at 1 Hz+ without REST polling. The
// same-origin URL is proxied to the daemon by src/app/api/[...path]/route.ts,
// which passes the streaming body through untouched. `ms` is the server tick
// (clamped daemon-side to [250, 10000]); the browser EventSource sends the
// session cookie automatically same-origin.
export function snapStreamUrl(symbol: string, market: Market, ms = 1000): string {
  const p = new URLSearchParams({ symbol, market, ms: String(ms) });
  return `${API_BASE}/api/stream/snaps?${p.toString()}`;
}

// ── LIVE tab wave (TradingView webhook pipeline) — appended block; keep new
// client functions at the END of this file so parallel edits by other agents
// never collide. GET /api/tv-status answers "is the TradingView webhook + its
// public tunnel + the streamed-symbol firing pipeline live?"; GET
// /api/tv-signals is the recent raw-alert feed.

/** One received TradingView webhook alert (raw; feed is newest-first). */
export interface TVSignal {
  id: number;
  symbol?: string; // resolved SignalDeck symbol (absent = unmatched ticker)
  market?: string;
  ticker: string; // raw ticker as the alert arrived
  action: string;
  price?: number;
  message: string;
  ts: number;
  seen: boolean;
}

/** One streamed symbol's firing tally in the /api/tv-status grid. */
export interface TvStatusSymbol {
  symbol: string;
  market: string;
  count: number;
  lastFiredAt: number | null; // null = no alert has resolved to this symbol yet
}

/** GET /api/tv-status — the TradingView webhook pipeline's health at a glance.
 *  publicHosts is the ngrok tunnel host(s) (never localhost); tunnelReachable
 *  is a best-effort self-probe, null when no public host is configured. */
export interface TvStatus {
  secretConfigured: boolean;
  webhookPath: string;
  publicHosts: string[];
  tunnelReachable: boolean | null;
  tunnelCheckedAt: number | null;
  total: number;
  last24h: number;
  lastAt: number | null;
  lastTicker: string;
  symbols: TvStatusSymbol[];
}

/** Webhook + tunnel + per-symbol firing health for the LIVE tab. */
export function tvStatus() {
  return get<TvStatus>("/api/tv-status");
}

/** The recent raw TradingView webhook alerts (newest first). */
export function tvSignals(limit = 30) {
  return get<TVSignal[]>(`/api/tv-signals?limit=${limit}`);
}

// ── TradingView external rating (tv-rating worker, 15m) — appended ────────
/** GET /api/tv-rating — TradingView's OWN technical-analysis rating for one
 *  symbol (public scanner, delayed). External context, NOT our model. */
export interface TVRating {
  available: boolean;
  symbol: string;
  market: string;
  reason?: string;
  ts?: number;
  recoAll?: number;
  recoMA?: number;
  recoOther?: number;
  rsi?: number;
  close?: number;
  label?: string;
  note: string;
}

export const tvRating = (symbol: string, market: Market) =>
  get<TVRating>(`/api/tv-rating?symbol=${encodeURIComponent(symbol)}&market=${market}`);

// ── MODEL EVOLUTION wave (self-audit + learned-model history) — appended ──
// GET /api/self-audit is the once-per-day deterministic auditor's latest
// findings (calibration drift, prediction bias, factor-IC sign flips);
// GET /api/model-evolution is the learned model's history over a trailing
// window — adaptive blend weights per (regime, leg) and per-leg factor-IC
// trend. Points ascend by ts; gaps are honest gaps — never interpolate.

/** Status vocabulary shared by self-audit findings and factor-skill points. */
export type AuditStatus =
  | "ok"
  | "degrading"
  | "over_confident"
  | "under_confident"
  | "sign_flip"
  | "insufficient";

/** One deterministic self-audit finding. metric ∈ "calibration:1d|1w",
 *  "prediction_bias:1d|1w", "factor_ic:<leg>". */
export interface SelfAuditFinding {
  ts: number;
  metric: string;
  value: number;
  status: AuditStatus;
  detail: string; // carries thresholds + n — render verbatim
  symbolId?: number;
}

/** GET /api/self-audit payload. */
export interface SelfAudit {
  note: string; // render verbatim
  generatedTs: number;
  empty: boolean; // true = the once-per-day auditor hasn't produced findings yet
  findings: SelfAuditFinding[];
}

/** One learned-blend-weight snapshot. */
export interface EvolutionWeightPoint {
  ts: number;
  weight: number;
}

/** Weight history for one (regime cell, ensemble leg). */
export interface EvolutionWeightSeries {
  regime: string;
  leg: string;
  points: EvolutionWeightPoint[]; // ascending ts; gaps are honest gaps
}

/** One measured factor-IC point ("insufficient" = withheld below the gate). */
export interface FactorSkillPoint {
  ts: number;
  ic: number;
  status: AuditStatus;
}

/** Factor-IC trend for one ensemble leg. */
export interface FactorSkillSeries {
  leg: string;
  points: FactorSkillPoint[]; // ascending ts; gaps are honest gaps
}

/** GET /api/model-evolution payload. Arrays are never null. */
export interface ModelEvolution {
  note: string; // render verbatim
  days: number; // lookback actually used
  weights: EvolutionWeightSeries[];
  factorSkill: FactorSkillSeries[];
}

/** The latest deterministic self-audit findings. */
export function selfAudit() {
  return get<SelfAudit>("/api/self-audit");
}

/** Learned-model history over the trailing window (default 30 days). */
export function modelEvolution(days = 30) {
  return get<ModelEvolution>(`/api/model-evolution?days=${days}`);
}

// ── CROSS-SECTIONAL ALPHA wave (/api/alphax) — appended ───────────────────
// The pooled cross-sectional model's per-horizon status: purged walk-forward
// OOS grade, gate state with a stated reason, and — ONLY while ungated — the
// top-20 current symbol scores (RELATIVE to the same-day universe, never
// absolute direction). Go nil slices arrive as JSON null — `?? []` topScores.

/** Purged walk-forward out-of-sample grade for one alpha-model horizon. */
export interface AlphaXGrade {
  lift: number; // accuracy minus base rate, as a fraction (render as pp)
  auc: number;
  accuracy: number;
  baseRate: number;
  brier?: number;
  nTrain: number;
  nTest: number;
}

/** One symbol's current alpha score — P(top half of the universe). */
export interface AlphaXTopScore {
  symbol: string;
  prob: number;
  ts: number;
}

/** One horizon's model status. available:false = no graded model yet. */
export interface AlphaXHorizon {
  available: boolean;
  ts?: number;
  gated: boolean;
  gateReason?: string; // stated reason — render verbatim
  grade?: AlphaXGrade;
  topScores?: AlphaXTopScore[] | null; // only while ungated; null when absent
  scoresNote?: string; // "relative rank, not direction" — render verbatim
}

/** GET /api/alphax payload. Horizons keyed "1d" / "1w". */
export interface AlphaX {
  note: string; // render verbatim
  horizons: Partial<Record<"1d" | "1w", AlphaXHorizon>>;
}

/** The pooled cross-sectional alpha model's per-horizon status. */
export function alphaX() {
  return get<AlphaX>("/api/alphax");
}

// ── NEWS-TRENDS wave (/api/news-trends) — appended ────────────────────────
// One symbol's 30d headline-volume series + live news-volume z (null with a
// stated gate reason when the baseline can't support one) + the fleet's
// top-10 trending headline tokens over the last 24h. Descriptive attention,
// not a forecast. Go nil slices arrive as JSON null — `?? []` every array.

/** One day's headline count (zero-news days are simply absent). */
export interface NewsVolumeDay {
  day: string; // YYYY-MM-DD (UTC)
  n: number;
}

/** One fleet-wide trending headline token. */
export interface FleetToken {
  token: string;
  count: number; // headline occurrences (once per headline)
  symbols: number; // distinct symbols whose headlines used it
}

/** GET /api/news-trends payload. */
export interface NewsTrends {
  symbol: string;
  volumeSeries: NewsVolumeDay[] | null; // oldest first; null when empty
  day: string;
  todayCount: number;
  latestZ: number | null; // null = honest absence, never a fabricated 0
  zGateReason?: string; // stated reason when latestZ is withheld
  fleetTokens: FleetToken[] | null;
  note: string; // render verbatim
}

/** One symbol's headline-volume trend + the fleet token board. */
export const newsTrends = (symbol: string, market: Market) =>
  get<NewsTrends>(`/api/news-trends?symbol=${encodeURIComponent(symbol)}&market=${market}`);

// ── STRATEGY-LAB wave (/api/strategy-lab) — appended ──────────────────────
// Classic published strategies backtested walk-forward on our own bars with
// costs. Fleet aggregates always; per-symbol rows when ?symbol= is passed.
// cagr/winRate are only honest when their flags say so — gate on them.

/** One (symbol, strategy) backtest row. */
export interface StrategyResultRow {
  strategy: string;
  ts: number;
  totalReturn: number;
  cagr: number; // only show when cagrReported
  sharpe: number;
  maxDrawdown: number;
  winRate: number; // only show when winRateMeaningful
  nTrades: number;
  cagrReported: boolean;
  winRateMeaningful: boolean;
  nBars: number;
}

/** Per-strategy fleet aggregate (sorted by median Sharpe by the daemon). */
export interface StrategyFleetAgg {
  strategy: string;
  nSymbols: number;
  medianSharpe: number;
  pctProfitable: number; // fraction of symbols with total return > 0
  medianTotalRet: number;
}

/** GET /api/strategy-lab payload. symbol/strategies only with ?symbol=. */
export interface StrategyLab {
  symbol?: string;
  strategies?: StrategyResultRow[] | null; // null when the daemon has none
  emptyNote?: string; // stated reason for an empty symbol — render verbatim
  fleet: StrategyFleetAgg[] | null;
  note: string; // render verbatim
}

/** Fleet table alone (no args) or fleet + one symbol's per-strategy rows. */
export function strategyLab(symbol?: string, market?: Market) {
  return get<StrategyLab>(
    symbol && market ? `/api/strategy-lab?${q(symbol, market)}` : "/api/strategy-lab",
  );
}

// ─────────────────────────────────────────────────────────────────────────
// CHART ANALYTICS WAVE — CANDLE PATTERNS + TRENDLINES (appended block; keep at
// END). Display-layer inputs for the candlestick chart. Both degrade honestly:
// a 404 / empty payload throws in get(), the caller swallows it, and the
// feature is silently absent — the chart never crashes. Go serializes nil
// slices as JSON null, so every array must be read through `?? []` at the call
// site (bars, patterns, trendlines).

/** One measured forward-return edge for a pattern on THIS symbol. null when the
 *  symbol lacks enough history (n < 15) to measure the edge honestly. */
export interface PatternMeasured {
  hitRate: number; // fraction of past occurrences that closed up over `horizon` bars
  meanFwd: number; // mean forward return over `horizon` bars (fraction, e.g. 0.012)
  n: number; // sample size on THIS symbol
  horizon: number; // forward horizon in bars the edge was measured over
}

/** One recognized candlestick pattern on a bar. bias: -1 bear, 0 neutral, +1 bull. */
export interface CandlePattern {
  name: string; // plain-English name ("Hammer", "Bullish Engulfing")
  bias: -1 | 0 | 1;
  desc: string; // one-line plain-English description
  measured: PatternMeasured | null; // null => not enough history to measure
}

/** All patterns detected on one bar. */
export interface PatternBar {
  ts: number; // bar timestamp (unix seconds)
  patterns: CandlePattern[];
}

/** GET /api/candle-patterns payload. `note` is the honest weak/context-only
 *  framing — render it verbatim. */
export interface CandlePatterns {
  symbol: string;
  market: Market;
  tf: string;
  bars: PatternBar[] | null; // nil slice => null
  note: string;
}

/** Detected candlestick patterns for one symbol+timeframe (best-effort). */
export function candlePatterns(symbol: string, market: Market, tf: "1m" | "1h" | "1d") {
  return get<CandlePatterns>(`/api/candle-patterns?${q(symbol, market)}&tf=${tf}`);
}

/** One drawn trendline (support/resistance) between two anchor points. */
export interface Trendline {
  fromTs: number;
  fromPrice: number;
  toTs: number;
  toPrice: number;
  kind: "support" | "resistance";
  touchCount: number; // how many swing points the line was fit through
}

/** GET /api/trend payload. `note` renders verbatim. */
export interface TrendAnalysis {
  symbol: string;
  market: Market;
  tf: string;
  classification: "uptrend" | "downtrend" | "range";
  slopePctPerBar: number; // signed % change per bar of the fitted trend
  trendlines: Trendline[] | null; // nil slice => null
  channel: boolean; // the two lines read as a channel
  note: string;
}

/** Trend classification + trendlines for one symbol+timeframe (best-effort). */
export function trend(symbol: string, market: Market, tf: "1m" | "1h" | "1d") {
  return get<TrendAnalysis>(`/api/trend?${q(symbol, market)}&tf=${tf}`);
}
