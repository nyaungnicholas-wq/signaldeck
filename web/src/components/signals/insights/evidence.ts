// Evidence-blob helpers for SIGNALS → INSIGHTS.
//
// The daemon stores each insight's receipts as a JSON `data` blob; writers
// that tag their blobs set data.kind (daily_briefing, weekly_report,
// signalbt_weekly, …) — /api/insights?kind= filters on that server-side
// (store.InsightsByKind). Everything here parses DEFENSIVELY: malformed or
// empty blobs yield null / empty fields, never a throw — the Risk watcher
// and older writers persist '' (the Go zero value) as data.

import { fmtPct, fmtPrice, fmtScore, humanizeState } from "@/lib/format";

export type Evidence = Record<string, unknown>;

/** Parse an insight's raw data blob. Returns null for anything that is not
 *  a non-empty JSON object (empty string, '{}', arrays, bare scalars,
 *  malformed JSON) — callers then simply render no evidence expander. */
export function parseEvidence(data: string | null | undefined): Evidence | null {
  if (typeof data !== "string") return null;
  const t = data.trim();
  if (!t || t === "{}" || t === "null") return null;
  try {
    const obj: unknown = JSON.parse(t);
    if (!obj || typeof obj !== "object" || Array.isArray(obj)) return null;
    return obj as Evidence;
  } catch {
    return null;
  }
}

/** The writer tag (data.kind) if present — e.g. "daily_briefing". */
export function evidenceKind(ev: Evidence | null): string | null {
  if (!ev) return null;
  const k = ev["kind"];
  return typeof k === "string" && k.trim() !== "" ? k : null;
}

// Human labels for the kinds the daemon writes today; unknown kinds fall
// back to underscore→space so new writers appear without a UI change.
const KIND_LABELS: Record<string, string> = {
  daily_briefing: "daily briefing",
  weekly_report: "weekly report",
  signalbt_weekly: "signal backtest",
  market_brief: "market brief",
  symbol_agent_graduated: "agent graduation",
  adaptive_weights_shift: "weights shift",
  universe_discovery_autoadd: "auto-added symbol",
};

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind.replace(/_/g, " ");
}

export interface EvidenceField {
  key: string;
  label: string;
  value: string;
}

// ── formatting ──────────────────────────────────────────────────────────

function humanizeKey(key: string): string {
  return key
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/_/g, " ")
    .toLowerCase();
}

function fmtDur(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.round((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.round((s % 86400) / 3600)}h`;
}

function fmtNum(v: number, key: string): string {
  const k = key.toLowerCase();
  if (/pct$/.test(k) || /pctchange|changepct/.test(k)) return fmtPct(v);
  if (/(price|close|open|high|low)$/.test(k)) return fmtPrice(v);
  if (/score/.test(k)) return fmtScore(v);
  if (Number.isInteger(v)) return v.toLocaleString("en-US");
  return Math.abs(v) < 1 ? v.toFixed(3) : v.toFixed(2);
}

function fmtScalar(v: unknown, key: string): string {
  if (typeof v === "number") return isFinite(v) ? fmtNum(v, key) : "—";
  if (typeof v === "boolean") return v ? "yes" : "no";
  if (typeof v === "string") return v.length > 160 ? `${v.slice(0, 157)}…` : v;
  return String(v);
}

function isPlainScalar(v: unknown): boolean {
  return v == null || typeof v !== "object";
}

/** One level of nesting rendered inline; anything deeper collapses to a
 *  field count — the expander shows receipts, not a JSON tree. */
function fmtValue(v: unknown, key: string): string | null {
  if (v == null) return null;
  if (typeof v === "string" && v.trim() === "") return null;
  if (Array.isArray(v)) {
    if (v.length === 0) return null;
    if (v.every(isPlainScalar)) {
      const head = v.slice(0, 8).map((x) => fmtScalar(x, key)).join(", ");
      return v.length > 8 ? `${head} … +${v.length - 8} more` : head;
    }
    return `${v.length} entries`;
  }
  if (typeof v === "object") {
    const es = Object.entries(v as Evidence).filter(([, x]) => x != null);
    if (es.length === 0) return null;
    if (es.length <= 6 && es.every(([, x]) => isPlainScalar(x))) {
      return es.map(([k, x]) => `${humanizeKey(k)} ${fmtScalar(x, k)}`).join(" · ");
    }
    return `${es.length} fields`;
  }
  return fmtScalar(v, key);
}

// ── known evidence keys: plain-English label (simple mode) + custom format.
// Pro mode always shows the raw key name. Covers symbolEvidence /
// marketEvidence (internal/insights), briefing Facts, and agent blobs.
const sign = (n: number, dp: number) => {
  const r = parseFloat(n.toFixed(dp));
  return `${r > 0 ? "+" : ""}${r}`;
};

const DEFS: Record<string, { simple: string; fmt?: (v: number) => string }> = {
  rsi: { simple: "momentum (RSI)", fmt: (n) => n.toFixed(1) },
  rvol: { simple: "volume vs normal", fmt: (n) => `${n.toFixed(1)}×` },
  sentiment: { simple: "news sentiment", fmt: (n) => sign(n, 2) },
  n: { simple: "sample size" },
  hitRate: {
    simple: "share that rose next",
    fmt: (n) => (n >= 0 && n <= 1 ? `${(n * 100).toFixed(0)}%` : fmtPct(n, false)),
  },
  // THE UNITS IN THIS TABLE ARE MIXED, and fmtPct does no scaling -- it takes a
  // value already in percent and appends the sign and the %. dayChangePct below
  // arrives in percent, so it is passed straight through; medianFwd/meanFwd
  // arrive as FRACTIONS and must be scaled, exactly as
  // components/symbol/ExpectancyPanel.tsx:107 already does with
  // `fmtPct(r.medianFwd * 100)` for the same field.
  //
  // Unscaled, a real +1.23% forward move rendered as "+0.01%". Measured
  // 2026-09-13: 29 of 34 live insights carried medianFwd as a fraction, so
  // nearly every evidence expander understated the move by 100x -- in the
  // direction that makes the record look quieter than it was.
  medianFwd: { simple: "typical next move", fmt: (n) => fmtPct(n * 100) },
  meanFwd: { simple: "average next move", fmt: (n) => fmtPct(n * 100) },
  lastClose: { simple: "last price", fmt: fmtPrice },
  dayChangePct: { simple: "day change", fmt: (n) => fmtPct(n) },
  driver1d: { simple: "main 1-day driver" },
  stateKey: { simple: "market state" },
  stale: { simple: "data behind" },
  staleForSec: { simple: "behind for", fmt: fmtDur },
  scores: { simple: "pressure scores" },
  total: { simple: "symbols scored" },
  positive: { simple: "scored positive" },
  negative: { simple: "scored negative" },
  regime: { simple: "detected regime" },
  best: { simple: "strongest symbol" },
  bestScore: { simple: "strongest score", fmt: fmtScore },
  bestDayPct: { simple: "strongest day move", fmt: (n) => fmtPct(n) },
  worst: { simple: "weakest symbol" },
  worstScore: { simple: "weakest score", fmt: fmtScore },
  worstDayPct: { simple: "weakest day move", fmt: (n) => fmtPct(n) },
  model: { simple: "AI model used" },
  horizon: { simple: "horizon" },
  maxShift: { simple: "largest weight shift" },
  weights: { simple: "signal weights" },
};

// Never rendered as bullets: kind is the chip, symbol is the card header.
const SKIP_KEYS = new Set(["kind", "symbol"]);

const MAX_FIELDS = 24;

/** Flatten an evidence blob into labeled, formatted key-value bullets.
 *  Simple mode gets plain-English labels; pro mode keeps raw key names.
 *  `omitted` counts fields beyond the render cap. */
export function evidenceFields(
  ev: Evidence,
  mode: "simple" | "pro",
): { fields: EvidenceField[]; omitted: number } {
  const fields: EvidenceField[] = [];
  for (const [key, raw] of Object.entries(ev)) {
    if (SKIP_KEYS.has(key)) continue;
    const def = DEFS[key];
    let value: string | null;
    if (def?.fmt && typeof raw === "number" && isFinite(raw)) {
      value = def.fmt(raw);
    } else if (key === "scores" && raw && typeof raw === "object" && !Array.isArray(raw)) {
      // horizon→score map: keep the house signed-score format (+0.32)
      const es = Object.entries(raw as Evidence).filter(
        ([, x]) => typeof x === "number" && isFinite(x),
      );
      value = es.length
        ? es.map(([h, s]) => `${h} ${fmtScore(s as number)}`).join(" · ")
        : null;
    } else if (key === "stateKey" && typeof raw === "string" && mode === "simple") {
      value = humanizeState(raw);
    } else {
      value = fmtValue(raw, key);
    }
    if (value == null) continue;
    fields.push({
      key,
      label: mode === "simple" ? (def?.simple ?? humanizeKey(key)) : key,
      value,
    });
  }
  return {
    fields: fields.slice(0, MAX_FIELDS),
    omitted: Math.max(0, fields.length - MAX_FIELDS),
  };
}
