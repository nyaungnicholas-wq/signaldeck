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

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, { cache: "no-store" });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`API ${res.status}: ${body || path}`);
  }
  return res.json() as Promise<T>;
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
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
};

// usePoll-style helper for client components (simple interval fetcher).
export function pollMs(): number {
  return 5000;
}
