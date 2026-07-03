// Typed client for the SignalDeck daemon API (:8322). Every page goes
// through this module — it is the single source of truth for shapes.

export const API_BASE =
  process.env.NEXT_PUBLIC_SIGNALDECK_API ?? "http://127.0.0.1:8322";

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
  ic: number;
  buckets: HonestyBucket[];
  points: { score: number; fwd: number; ts: number }[];
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
  insights: (limit = 50) => get<Insight[]>(`/api/insights?limit=${limit}`),
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
};

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

// usePoll-style helper for client components (simple interval fetcher).
export function pollMs(): number {
  return 5000;
}
