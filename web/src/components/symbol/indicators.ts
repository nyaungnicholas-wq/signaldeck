// Client-side technical indicators for the candlestick chart.
//
// Pure functions over the OHLCV bars the chart already holds — no API round
// trip. Everything here is framework-free: inputs are `Bar[]` (unix-second
// `ts`), outputs are plain {time,value} points that CandleChart feeds straight
// into lightweight-charts v5 series. Nothing in this file touches React or the
// DOM, so it stays trivially testable and cheap to recompute.
//
// The chart consumes an `IndicatorRender` generically (plots + horizontal
// levels + which pane), so adding an indicator never means touching the chart's
// series bookkeeping — only this registry.

import type { Bar } from "@/lib/api";
import type { Tf } from "@/components/symbol/CandleChart";

// ── palette (canvas colors must be literal — the chart is not CSS-var aware) ──
const C = {
  amber: "#fbbf24", // --accent
  blue: "#60a5fa",
  purple: "#a78bfa", // --crossed
  teal: "#2dd4bf",
  green: "#34d399", // --bid
  red: "#f87171", // --ask
  dim: "#7a8595", // --faint
  orange: "#fb923c",
  text: "#e8eef6",
} as const;

const UP_FILL = "rgba(52,211,153,0.5)";
const DOWN_FILL = "rgba(248,113,113,0.5)";

// `color` is optional per-point — set only by color-flipping plots (Supertrend
// line, PSAR dots). Plain lines omit it and inherit PlotSpec.color.
export type LinePoint = { time: number; value: number; color?: string };
export type HistPoint = { time: number; value: number; color: string };

export type PlotKind = "line" | "histogram" | "dots";
export type DashStyle = "solid" | "dashed" | "dotted";

/** One drawable series inside an indicator. */
export interface PlotSpec {
  id: string; // unique within the indicator (e.g. "macd_signal")
  kind: PlotKind;
  data: LinePoint[] | HistPoint[];
  color: string; // fallback color (per-point color on HistPoint/dots wins)
  lineWidth?: 1 | 2 | 3 | 4;
  style?: DashStyle;
  title?: string; // price-axis label; omit to hide
}

/** One horizontal reference line (threshold / level). */
export interface LevelSpec {
  price: number;
  color: string;
  style?: DashStyle;
  title?: string;
}

/** Everything the chart needs to render one enabled indicator. */
export interface IndicatorRender {
  pane: "price" | "own"; // overlay on the price pane, or its own pane below
  plots: PlotSpec[]; // may be empty for level-only overlays (fib/pivots/sr)
  levels: LevelSpec[]; // drawn on plots[0] (own pane) or the candle series (price)
}

export type IndicatorId =
  | "ma"
  | "bbands"
  | "rsi"
  | "macd"
  | "volume"
  | "stoch"
  | "adx"
  | "atr"
  | "cci"
  | "willr"
  | "vwap"
  | "fib"
  | "pivots"
  | "sr"
  | "ichimoku"
  | "supertrend"
  | "psar"
  | "keltner";

export type IndicatorGroup = "essential" | "momentum" | "price" | "advanced";

export interface IndicatorMeta {
  id: IndicatorId;
  group: IndicatorGroup;
  name: string; // plain-English label (simple mode)
  params: string; // parameter suffix shown in pro mode ("14", "12,26,9")
  pane: "price" | "own";
}

// Registry — drives the toggle menu AND the render dispatch. Order within a
// group is the display order.
export const INDICATOR_META: IndicatorMeta[] = [
  { id: "ma", group: "essential", name: "Moving averages", params: "EMA 20/50 · SMA 200", pane: "price" },
  { id: "bbands", group: "essential", name: "Bollinger Bands", params: "20, 2", pane: "price" },
  { id: "rsi", group: "essential", name: "RSI", params: "14", pane: "own" },
  { id: "macd", group: "essential", name: "MACD", params: "12, 26, 9", pane: "own" },
  { id: "volume", group: "essential", name: "Volume", params: "up/down", pane: "own" },
  { id: "stoch", group: "momentum", name: "Stochastic", params: "14, 3", pane: "own" },
  { id: "adx", group: "momentum", name: "ADX / DMI", params: "14", pane: "own" },
  { id: "atr", group: "momentum", name: "ATR", params: "14", pane: "own" },
  { id: "cci", group: "momentum", name: "CCI", params: "20", pane: "own" },
  { id: "willr", group: "momentum", name: "Williams %R", params: "14", pane: "own" },
  { id: "vwap", group: "price", name: "VWAP", params: "session", pane: "price" },
  { id: "fib", group: "price", name: "Fibonacci retracement", params: "swing", pane: "price" },
  { id: "pivots", group: "price", name: "Pivot points", params: "classic", pane: "price" },
  { id: "sr", group: "price", name: "Support / resistance", params: "auto", pane: "price" },
  { id: "ichimoku", group: "advanced", name: "Ichimoku Cloud", params: "9, 26, 52", pane: "price" },
  { id: "supertrend", group: "advanced", name: "Supertrend", params: "10, 3", pane: "price" },
  { id: "psar", group: "advanced", name: "Parabolic SAR", params: "0.02, 0.2", pane: "price" },
  { id: "keltner", group: "advanced", name: "Keltner Channels", params: "20, 2", pane: "price" },
];

export const GROUP_LABEL: Record<IndicatorGroup, string> = {
  essential: "Essential",
  momentum: "Momentum",
  price: "Price levels",
  advanced: "Advanced",
};

const META_BY_ID: Record<IndicatorId, IndicatorMeta> = Object.fromEntries(
  INDICATOR_META.map((m) => [m.id, m]),
) as Record<IndicatorId, IndicatorMeta>;

export function indicatorMeta(id: IndicatorId): IndicatorMeta {
  return META_BY_ID[id];
}

// ─────────────────────────────────────────────────────────────────────────
// MATH HELPERS — array-in, array-out. `null` marks a bar with no value yet
// (warm-up), so downstream `toLine` drops it and the series simply starts late.

function sma(vals: number[], p: number): (number | null)[] {
  const out: (number | null)[] = new Array(vals.length).fill(null);
  if (p <= 0) return out;
  let sum = 0;
  for (let i = 0; i < vals.length; i++) {
    sum += vals[i];
    if (i >= p) sum -= vals[i - p];
    if (i >= p - 1) out[i] = sum / p;
  }
  return out;
}

function ema(vals: number[], p: number): (number | null)[] {
  const out: (number | null)[] = new Array(vals.length).fill(null);
  if (p <= 0) return out;
  const k = 2 / (p + 1);
  let prev: number | null = null;
  for (let i = 0; i < vals.length; i++) {
    if (prev === null) {
      if (i >= p - 1) {
        let s = 0;
        for (let j = i - p + 1; j <= i; j++) s += vals[j];
        prev = s / p;
        out[i] = prev;
      }
    } else {
      prev = vals[i] * k + prev * (1 - k);
      out[i] = prev;
    }
  }
  return out;
}

// Wilder's smoothing (RMA) — RSI / ATR / ADX all use it.
function rma(vals: number[], p: number): (number | null)[] {
  const out: (number | null)[] = new Array(vals.length).fill(null);
  if (p <= 0) return out;
  let prev: number | null = null;
  for (let i = 0; i < vals.length; i++) {
    if (prev === null) {
      if (i >= p - 1) {
        let s = 0;
        for (let j = i - p + 1; j <= i; j++) s += vals[j];
        prev = s / p;
        out[i] = prev;
      }
    } else {
      prev = (prev * (p - 1) + vals[i]) / p;
      out[i] = prev;
    }
  }
  return out;
}

function stdev(vals: number[], p: number): (number | null)[] {
  const out: (number | null)[] = new Array(vals.length).fill(null);
  const means = sma(vals, p);
  for (let i = p - 1; i < vals.length; i++) {
    const m = means[i];
    if (m === null) continue;
    let s = 0;
    for (let j = i - p + 1; j <= i; j++) {
      const d = vals[j] - m;
      s += d * d;
    }
    out[i] = Math.sqrt(s / p);
  }
  return out;
}

function trueRange(bars: Bar[]): number[] {
  return bars.map((b, i) => {
    if (i === 0) return b.h - b.l;
    const pc = bars[i - 1].c;
    return Math.max(b.h - b.l, Math.abs(b.h - pc), Math.abs(b.l - pc));
  });
}

function atr(bars: Bar[], p: number): (number | null)[] {
  return rma(trueRange(bars), p);
}

function toLine(bars: Bar[], vals: (number | null)[]): LinePoint[] {
  const out: LinePoint[] = [];
  for (let i = 0; i < bars.length; i++) {
    const v = vals[i];
    if (v !== null && Number.isFinite(v)) out.push({ time: bars[i].ts, value: v });
  }
  return out;
}

// ─────────────────────────────────────────────────────────────────────────
// PER-INDICATOR COMPUTE. Each returns an IndicatorRender or null (too few bars
// to be meaningful). The dispatcher `computeIndicator` maps id -> builder.

function closes(bars: Bar[]) {
  return bars.map((b) => b.c);
}

function line(id: string, data: LinePoint[], color: string, opts?: Partial<PlotSpec>): PlotSpec {
  return { id, kind: "line", data, color, lineWidth: 2, ...opts };
}

function computeMA(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 2) return null;
  const c = closes(bars);
  const plots: PlotSpec[] = [];
  const e20 = toLine(bars, ema(c, 20));
  const e50 = toLine(bars, ema(c, 50));
  const s200 = toLine(bars, sma(c, 200));
  if (e20.length) plots.push(line("ema20", e20, C.amber, { title: "EMA20" }));
  if (e50.length) plots.push(line("ema50", e50, C.blue, { title: "EMA50" }));
  if (s200.length) plots.push(line("sma200", s200, C.purple, { title: "SMA200" }));
  if (!plots.length) return null;
  return { pane: "price", plots, levels: [] };
}

function computeBBands(bars: Bar[]): IndicatorRender | null {
  const p = 20;
  if (bars.length < p) return null;
  const c = closes(bars);
  const mid = sma(c, p);
  const sd = stdev(c, p);
  const up: (number | null)[] = mid.map((m, i) => (m === null || sd[i] === null ? null : m + 2 * sd[i]!));
  const lo: (number | null)[] = mid.map((m, i) => (m === null || sd[i] === null ? null : m - 2 * sd[i]!));
  return {
    pane: "price",
    plots: [
      line("bb_up", toLine(bars, up), C.dim, { lineWidth: 1, title: "BB↑" }),
      line("bb_mid", toLine(bars, mid), C.dim, { lineWidth: 1, style: "dashed", title: "BB" }),
      line("bb_lo", toLine(bars, lo), C.dim, { lineWidth: 1, title: "BB↓" }),
    ],
    levels: [],
  };
}

function rsiValues(c: number[], p = 14): (number | null)[] {
  const gains: number[] = [];
  const losses: number[] = [];
  for (let i = 0; i < c.length; i++) {
    if (i === 0) {
      gains.push(0);
      losses.push(0);
      continue;
    }
    const ch = c[i] - c[i - 1];
    gains.push(Math.max(0, ch));
    losses.push(Math.max(0, -ch));
  }
  const ag = rma(gains, p);
  const al = rma(losses, p);
  return c.map((_, i) => {
    const g = ag[i];
    const l = al[i];
    if (g === null || l === null) return null;
    if (l === 0) return 100;
    return 100 - 100 / (1 + g / l);
  });
}

function computeRSI(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 15) return null;
  const data = toLine(bars, rsiValues(closes(bars), 14));
  if (!data.length) return null;
  return {
    pane: "own",
    plots: [line("rsi", data, C.blue, { title: "RSI" })],
    levels: [
      { price: 70, color: C.red, style: "dashed", title: "70" },
      { price: 30, color: C.green, style: "dashed", title: "30" },
    ],
  };
}

function computeMACD(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 26) return null;
  const c = closes(bars);
  const e12 = ema(c, 12);
  const e26 = ema(c, 26);
  const macd: (number | null)[] = e12.map((v, i) => (v === null || e26[i] === null ? null : v - e26[i]!));
  // signal = EMA9 of the (defined) macd line
  const macdDefined: number[] = [];
  const idxMap: number[] = [];
  macd.forEach((v, i) => {
    if (v !== null) {
      macdDefined.push(v);
      idxMap.push(i);
    }
  });
  const sig9 = ema(macdDefined, 9);
  const signal: (number | null)[] = new Array(bars.length).fill(null);
  sig9.forEach((v, k) => {
    if (v !== null) signal[idxMap[k]] = v;
  });
  const hist: HistPoint[] = [];
  for (let i = 0; i < bars.length; i++) {
    if (macd[i] === null || signal[i] === null) continue;
    const h = macd[i]! - signal[i]!;
    hist.push({ time: bars[i].ts, value: h, color: h >= 0 ? UP_FILL : DOWN_FILL });
  }
  return {
    pane: "own",
    plots: [
      { id: "macd_hist", kind: "histogram", data: hist, color: C.dim, title: "hist" },
      line("macd", toLine(bars, macd), C.blue, { title: "MACD" }),
      line("macd_signal", toLine(bars, signal), C.amber, { title: "signal" }),
    ],
    levels: [{ price: 0, color: C.dim, style: "dotted" }],
  };
}

function computeVolume(bars: Bar[]): IndicatorRender | null {
  if (!bars.length) return null;
  const data: HistPoint[] = bars.map((b) => ({
    time: b.ts,
    value: b.v,
    color: b.c >= b.o ? UP_FILL : DOWN_FILL,
  }));
  return {
    pane: "own",
    plots: [{ id: "vol", kind: "histogram", data, color: C.dim, title: "Vol" }],
    levels: [],
  };
}

function computeStoch(bars: Bar[]): IndicatorRender | null {
  const p = 14;
  if (bars.length < p + 3) return null;
  const k: (number | null)[] = new Array(bars.length).fill(null);
  for (let i = p - 1; i < bars.length; i++) {
    let hh = -Infinity;
    let ll = Infinity;
    for (let j = i - p + 1; j <= i; j++) {
      hh = Math.max(hh, bars[j].h);
      ll = Math.min(ll, bars[j].l);
    }
    k[i] = hh === ll ? 50 : (100 * (bars[i].c - ll)) / (hh - ll);
  }
  const kDef: number[] = [];
  const kIdx: number[] = [];
  k.forEach((v, i) => {
    if (v !== null) {
      kDef.push(v);
      kIdx.push(i);
    }
  });
  const d3 = sma(kDef, 3);
  const d: (number | null)[] = new Array(bars.length).fill(null);
  d3.forEach((v, m) => {
    if (v !== null) d[kIdx[m]] = v;
  });
  return {
    pane: "own",
    plots: [line("stoch_k", toLine(bars, k), C.blue, { title: "%K" }), line("stoch_d", toLine(bars, d), C.amber, { lineWidth: 1, title: "%D" })],
    levels: [
      { price: 80, color: C.red, style: "dashed", title: "80" },
      { price: 20, color: C.green, style: "dashed", title: "20" },
    ],
  };
}

function computeADX(bars: Bar[]): IndicatorRender | null {
  const p = 14;
  if (bars.length < p * 2) return null;
  const plusDM: number[] = [];
  const minusDM: number[] = [];
  const tr: number[] = [];
  for (let i = 0; i < bars.length; i++) {
    if (i === 0) {
      plusDM.push(0);
      minusDM.push(0);
      tr.push(bars[i].h - bars[i].l);
      continue;
    }
    const up = bars[i].h - bars[i - 1].h;
    const dn = bars[i - 1].l - bars[i].l;
    plusDM.push(up > dn && up > 0 ? up : 0);
    minusDM.push(dn > up && dn > 0 ? dn : 0);
    const pc = bars[i - 1].c;
    tr.push(Math.max(bars[i].h - bars[i].l, Math.abs(bars[i].h - pc), Math.abs(bars[i].l - pc)));
  }
  const atrS = rma(tr, p);
  const pdmS = rma(plusDM, p);
  const mdmS = rma(minusDM, p);
  const pdi: (number | null)[] = new Array(bars.length).fill(null);
  const mdi: (number | null)[] = new Array(bars.length).fill(null);
  const dx: number[] = [];
  const dxIdx: number[] = [];
  for (let i = 0; i < bars.length; i++) {
    if (atrS[i] === null || pdmS[i] === null || mdmS[i] === null || atrS[i] === 0) continue;
    const p1 = (100 * pdmS[i]!) / atrS[i]!;
    const m1 = (100 * mdmS[i]!) / atrS[i]!;
    pdi[i] = p1;
    mdi[i] = m1;
    const sum = p1 + m1;
    if (sum > 0) {
      dx.push((100 * Math.abs(p1 - m1)) / sum);
      dxIdx.push(i);
    }
  }
  const adxS = rma(dx, p);
  const adx: (number | null)[] = new Array(bars.length).fill(null);
  adxS.forEach((v, m) => {
    if (v !== null) adx[dxIdx[m]] = v;
  });
  return {
    pane: "own",
    plots: [
      line("adx", toLine(bars, adx), C.text, { title: "ADX" }),
      line("pdi", toLine(bars, pdi), C.green, { lineWidth: 1, title: "+DI" }),
      line("mdi", toLine(bars, mdi), C.red, { lineWidth: 1, title: "−DI" }),
    ],
    levels: [{ price: 25, color: C.dim, style: "dotted", title: "25" }],
  };
}

function computeATR(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 15) return null;
  const data = toLine(bars, atr(bars, 14));
  if (!data.length) return null;
  return { pane: "own", plots: [line("atr", data, C.purple, { title: "ATR" })], levels: [] };
}

function computeCCI(bars: Bar[]): IndicatorRender | null {
  const p = 20;
  if (bars.length < p) return null;
  const tp = bars.map((b) => (b.h + b.l + b.c) / 3);
  const ma = sma(tp, p);
  const cci: (number | null)[] = new Array(bars.length).fill(null);
  for (let i = p - 1; i < bars.length; i++) {
    const m = ma[i];
    if (m === null) continue;
    let md = 0;
    for (let j = i - p + 1; j <= i; j++) md += Math.abs(tp[j] - m);
    md /= p;
    cci[i] = md === 0 ? 0 : (tp[i] - m) / (0.015 * md);
  }
  return {
    pane: "own",
    plots: [line("cci", toLine(bars, cci), C.blue, { title: "CCI" })],
    levels: [
      { price: 100, color: C.red, style: "dashed", title: "100" },
      { price: -100, color: C.green, style: "dashed", title: "-100" },
    ],
  };
}

function computeWillR(bars: Bar[]): IndicatorRender | null {
  const p = 14;
  if (bars.length < p) return null;
  const wr: (number | null)[] = new Array(bars.length).fill(null);
  for (let i = p - 1; i < bars.length; i++) {
    let hh = -Infinity;
    let ll = Infinity;
    for (let j = i - p + 1; j <= i; j++) {
      hh = Math.max(hh, bars[j].h);
      ll = Math.min(ll, bars[j].l);
    }
    wr[i] = hh === ll ? -50 : (-100 * (hh - bars[i].c)) / (hh - ll);
  }
  return {
    pane: "own",
    plots: [line("willr", toLine(bars, wr), C.amber, { title: "%R" })],
    levels: [
      { price: -20, color: C.red, style: "dashed", title: "-20" },
      { price: -80, color: C.green, style: "dashed", title: "-80" },
    ],
  };
}

// VWAP — session-anchored on intraday (reset each UTC day); a single cumulative
// anchor over the loaded window on daily bars (per-day reset is meaningless when
// each bar IS a day).
function computeVWAP(bars: Bar[], tf: Tf): IndicatorRender | null {
  if (!bars.length) return null;
  const intraday = tf !== "1d";
  let cumPV = 0;
  let cumV = 0;
  let curDay = -1;
  const out: LinePoint[] = [];
  for (const b of bars) {
    if (intraday) {
      const day = Math.floor(b.ts / 86400);
      if (day !== curDay) {
        curDay = day;
        cumPV = 0;
        cumV = 0;
      }
    }
    const tp = (b.h + b.l + b.c) / 3;
    const vol = b.v > 0 ? b.v : 1;
    cumPV += tp * vol;
    cumV += vol;
    if (cumV > 0) out.push({ time: b.ts, value: cumPV / cumV });
  }
  return { pane: "price", plots: [line("vwap", out, C.purple, { lineWidth: 1, title: "VWAP" })], levels: [] };
}

// Fibonacci retracement from the swing high/low of the loaded window. (The
// window is what the chart fits on load; scrolling further back does not
// re-anchor — a deliberate simplification, see the panel note.)
function computeFib(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 5) return null;
  let hi = -Infinity;
  let lo = Infinity;
  for (const b of bars) {
    hi = Math.max(hi, b.h);
    lo = Math.min(lo, b.l);
  }
  if (!Number.isFinite(hi) || !Number.isFinite(lo) || hi === lo) return null;
  const span = hi - lo;
  const ratios = [0, 0.236, 0.382, 0.5, 0.618, 0.786, 1];
  const levels: LevelSpec[] = ratios.map((r) => ({
    price: hi - span * r,
    color: r === 0 || r === 1 ? C.dim : C.amber,
    style: "dotted",
    title: `${(r * 100).toFixed(1)}%`,
  }));
  return { pane: "price", plots: [], levels };
}

// Classic pivot points from the last completed bar's H/L/C (the "prior period").
function computePivots(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 2) return null;
  const prev = bars[bars.length - 2];
  const P = (prev.h + prev.l + prev.c) / 3;
  const r1 = 2 * P - prev.l;
  const s1 = 2 * P - prev.h;
  const r2 = P + (prev.h - prev.l);
  const s2 = P - (prev.h - prev.l);
  return {
    pane: "price",
    plots: [],
    levels: [
      { price: r2, color: C.red, style: "dotted", title: "R2" },
      { price: r1, color: C.red, style: "dashed", title: "R1" },
      { price: P, color: C.amber, style: "solid", title: "P" },
      { price: s1, color: C.green, style: "dashed", title: "S1" },
      { price: s2, color: C.green, style: "dotted", title: "S2" },
    ],
  };
}

// Auto support/resistance — local swing highs/lows, clustered by proximity,
// ranked by touch count. Emits the strongest handful as horizontal lines.
function computeSR(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 20) return null;
  const k = 3; // fractal half-window
  const pivots: { price: number; kind: "support" | "resistance" }[] = [];
  for (let i = k; i < bars.length - k; i++) {
    let isHigh = true;
    let isLow = true;
    for (let j = i - k; j <= i + k; j++) {
      if (bars[j].h > bars[i].h) isHigh = false;
      if (bars[j].l < bars[i].l) isLow = false;
    }
    if (isHigh) pivots.push({ price: bars[i].h, kind: "resistance" });
    if (isLow) pivots.push({ price: bars[i].l, kind: "support" });
  }
  if (!pivots.length) return null;
  const ref = bars[bars.length - 1].c || 1;
  const tol = Math.abs(ref) * 0.01; // cluster within 1%
  const clusters: { sum: number; n: number; support: number; resistance: number }[] = [];
  for (const pv of pivots.sort((a, b) => a.price - b.price)) {
    const last = clusters[clusters.length - 1];
    if (last && Math.abs(pv.price - last.sum / last.n) <= tol) {
      last.sum += pv.price;
      last.n += 1;
      if (pv.kind === "support") last.support += 1;
      else last.resistance += 1;
    } else {
      clusters.push({ sum: pv.price, n: 1, support: pv.kind === "support" ? 1 : 0, resistance: pv.kind === "resistance" ? 1 : 0 });
    }
  }
  const top = clusters
    .filter((c2) => c2.n >= 2)
    .sort((a, b) => b.n - a.n)
    .slice(0, 6);
  if (!top.length) return null;
  const levels: LevelSpec[] = top.map((c2) => {
    const price = c2.sum / c2.n;
    const isRes = c2.resistance >= c2.support;
    return { price, color: isRes ? C.red : C.green, style: "solid", title: `${isRes ? "R" : "S"}·${c2.n}` };
  });
  return { pane: "price", plots: [], levels };
}

// Ichimoku Cloud. SIMPLIFICATION: senkou A/B are NOT displaced forward 26
// periods (lightweight-charts can't plot into the future without synthesizing
// timestamps), and there is no shaded fill between them (v5 has no
// fill-between-series primitive) — all 5 lines are drawn, with the two span
// lines in cloud colors so their cross still reads bullish/bearish.
function computeIchimoku(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 52) return null;
  const midOver = (period: number): (number | null)[] => {
    const out: (number | null)[] = new Array(bars.length).fill(null);
    for (let i = period - 1; i < bars.length; i++) {
      let hh = -Infinity;
      let ll = Infinity;
      for (let j = i - period + 1; j <= i; j++) {
        hh = Math.max(hh, bars[j].h);
        ll = Math.min(ll, bars[j].l);
      }
      out[i] = (hh + ll) / 2;
    }
    return out;
  };
  const tenkan = midOver(9);
  const kijun = midOver(26);
  const senkouA: (number | null)[] = tenkan.map((t, i) => (t === null || kijun[i] === null ? null : (t + kijun[i]!) / 2));
  const senkouB = midOver(52);
  // chikou (lagging span): close shifted BACK 26 — real earlier timestamps.
  const chikou: LinePoint[] = [];
  for (let i = 26; i < bars.length; i++) chikou.push({ time: bars[i - 26].ts, value: bars[i].c });
  return {
    pane: "price",
    plots: [
      line("ichi_tenkan", toLine(bars, tenkan), C.blue, { lineWidth: 1, title: "Tenkan" }),
      line("ichi_kijun", toLine(bars, kijun), C.red, { lineWidth: 1, title: "Kijun" }),
      line("ichi_spanA", toLine(bars, senkouA), C.green, { lineWidth: 1, title: "Span A" }),
      line("ichi_spanB", toLine(bars, senkouB), C.orange, { lineWidth: 1, title: "Span B" }),
      line("ichi_chikou", chikou, C.dim, { lineWidth: 1, style: "dashed", title: "Chikou" }),
    ],
    levels: [],
  };
}

// Supertrend(10,3) — one line, color flips with trend (per-point color).
function computeSupertrend(bars: Bar[]): IndicatorRender | null {
  const p = 10;
  const mult = 3;
  if (bars.length < p + 1) return null;
  const a = atr(bars, p);
  const data: LinePoint[] = [];
  let prevUpper: number | null = null;
  let prevLower: number | null = null;
  let prevST: number | null = null;
  let uptrend = true;
  for (let i = 0; i < bars.length; i++) {
    if (a[i] === null) continue;
    const hl2 = (bars[i].h + bars[i].l) / 2;
    let upper = hl2 + mult * a[i]!;
    let lower = hl2 - mult * a[i]!;
    if (prevUpper !== null && prevLower !== null && prevST !== null) {
      upper = bars[i - 1].c > prevUpper ? upper : Math.min(upper, prevUpper);
      lower = bars[i - 1].c < prevLower ? lower : Math.max(lower, prevLower);
      if (prevST === prevUpper) uptrend = bars[i].c > upper ? true : false;
      else uptrend = bars[i].c < lower ? false : true;
    } else {
      uptrend = bars[i].c >= hl2;
    }
    const st = uptrend ? lower : upper;
    data.push({ time: bars[i].ts, value: st, color: uptrend ? C.green : C.red });
    prevUpper = upper;
    prevLower = lower;
    prevST = st;
  }
  if (!data.length) return null;
  return { pane: "price", plots: [{ id: "supertrend", kind: "line", data, color: C.green, lineWidth: 2, title: "ST" }], levels: [] };
}

// Parabolic SAR(0.02, 0.2) — dots, colored by trend side.
function computePSAR(bars: Bar[]): IndicatorRender | null {
  if (bars.length < 5) return null;
  const step = 0.02;
  const maxAf = 0.2;
  const data: LinePoint[] = [];
  let uptrend = bars[1].c >= bars[0].c;
  let af = step;
  let ep = uptrend ? bars[0].h : bars[0].l;
  let sar = uptrend ? bars[0].l : bars[0].h;
  for (let i = 1; i < bars.length; i++) {
    sar = sar + af * (ep - sar);
    const b = bars[i];
    if (uptrend) {
      sar = Math.min(sar, bars[i - 1].l, i >= 2 ? bars[i - 2].l : bars[i - 1].l);
      if (b.h > ep) {
        ep = b.h;
        af = Math.min(af + step, maxAf);
      }
      if (b.l < sar) {
        uptrend = false;
        sar = ep;
        ep = b.l;
        af = step;
      }
    } else {
      sar = Math.max(sar, bars[i - 1].h, i >= 2 ? bars[i - 2].h : bars[i - 1].h);
      if (b.l < ep) {
        ep = b.l;
        af = Math.min(af + step, maxAf);
      }
      if (b.h > sar) {
        uptrend = true;
        sar = ep;
        ep = b.h;
        af = step;
      }
    }
    data.push({ time: b.ts, value: sar, color: uptrend ? C.green : C.red });
  }
  return {
    pane: "price",
    plots: [{ id: "psar", kind: "dots", data, color: C.amber, lineWidth: 1, title: "SAR" }],
    levels: [],
  };
}

// Keltner Channels(20, 2×ATR): mid = EMA20 of close, bands = mid ± 2·ATR20.
function computeKeltner(bars: Bar[]): IndicatorRender | null {
  const p = 20;
  if (bars.length < p) return null;
  const mid = ema(closes(bars), p);
  const a = atr(bars, p);
  const up: (number | null)[] = mid.map((m, i) => (m === null || a[i] === null ? null : m + 2 * a[i]!));
  const lo: (number | null)[] = mid.map((m, i) => (m === null || a[i] === null ? null : m - 2 * a[i]!));
  return {
    pane: "price",
    plots: [
      line("kc_up", toLine(bars, up), C.teal, { lineWidth: 1, title: "KC↑" }),
      line("kc_mid", toLine(bars, mid), C.teal, { lineWidth: 1, style: "dashed", title: "KC" }),
      line("kc_lo", toLine(bars, lo), C.teal, { lineWidth: 1, title: "KC↓" }),
    ],
    levels: [],
  };
}

const BUILDERS: Record<IndicatorId, (bars: Bar[], tf: Tf) => IndicatorRender | null> = {
  ma: computeMA,
  bbands: computeBBands,
  rsi: computeRSI,
  macd: computeMACD,
  volume: computeVolume,
  stoch: computeStoch,
  adx: computeADX,
  atr: computeATR,
  cci: computeCCI,
  willr: computeWillR,
  vwap: computeVWAP,
  fib: computeFib,
  pivots: computePivots,
  sr: computeSR,
  ichimoku: computeIchimoku,
  supertrend: computeSupertrend,
  psar: computePSAR,
  keltner: computeKeltner,
};

/** Compute one indicator's render, or null if there are too few bars. Only
 *  enabled indicators should be passed — computation is on-demand. */
export function computeIndicator(id: IndicatorId, bars: Bar[], tf: Tf): IndicatorRender | null {
  const build = BUILDERS[id];
  if (!build) return null;
  try {
    return build(bars, tf);
  } catch {
    return null; // never let a math edge-case crash the chart
  }
}
