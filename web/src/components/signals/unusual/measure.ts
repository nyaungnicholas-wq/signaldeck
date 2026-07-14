// UNUSUAL tab — pure helpers for the rebuilt feed (no React, no DOM).
//
// The daemon now stamps every /api/anomalies row with structured
// {measure, value, proxy} fields (api/anomalies.go, "STRUCTURED ANOMALY
// FIELDS" block). These accessors prefer the structured fields and fall back
// to the exact detail-sniffing the old UnusualActivityPanel used, so an older
// daemon still renders honestly instead of misstating a ratio as a z-score.

import type { AnomalyRow, Market } from "@/lib/api";

export type Measure = "z" | "ratio";

/** Which statistic the row's value is — structured field first, else the
 *  verbatim detail marker ("value here is the TR/ATR ratio, not a z-score"). */
export function rowMeasure(r: AnomalyRow): Measure {
  if (r.measure === "z" || r.measure === "ratio") return r.measure;
  return r.detail.includes("TR/ATR ratio") ? "ratio" : "z";
}

/** The measured statistic (z or ratio — rowMeasure says which). */
export function rowValue(r: AnomalyRow): number {
  return typeof r.value === "number" && Number.isFinite(r.value) ? r.value : r.z;
}

/** Stock volume-side imbalance proxy (no order book on free stock data). */
export function rowProxy(r: AnomalyRow): boolean {
  return typeof r.proxy === "boolean" ? r.proxy : r.detail.includes("volume-side proxy");
}

// ── severity bands ────────────────────────────────────────────────────────
// UW-style lanes on honest statistics: |z| 2–3 notable (gray), 3–4 elevated
// (amber), 4+ extreme (red). Ratio rows band by the ratio VALUE on the same
// cuts (TR spikes only fire at ≥3× ATR) and are always labeled as a ratio,
// never as a z-score.

export type SeverityBand = "notable" | "elevated" | "extreme";

export const SEVERITY_COLOR: Record<SeverityBand, string> = {
  notable: "var(--dim)",
  elevated: "var(--warn)",
  extreme: "var(--bad)",
};

export const SEVERITY_ORDER: SeverityBand[] = ["notable", "elevated", "extreme"];

export function severityBand(r: AnomalyRow): SeverityBand {
  const a = Math.abs(rowValue(r));
  if (a >= 4) return "extreme";
  if (a >= 3) return "elevated";
  return "notable";
}

const BAND_RANK: Record<SeverityBand, number> = { notable: 0, elevated: 1, extreme: 2 };

/** Rank a band for comparisons (worst-of aggregation in the summary strip). */
export function bandRank(b: SeverityBand): number {
  return BAND_RANK[b];
}

// ── magnitude bars ────────────────────────────────────────────────────────
// Each row's statistic as a 0..1 fill fraction for the per-row bar. Z rows
// scale on min(|z|,6)/6; ratio rows are a DIFFERENT statistic and get their
// OWN scale, min(ratio,10)/10 — the bar is always labeled by rowMeasure so
// the two are never conflated.

export const Z_BAR_MAX = 6;
export const RATIO_BAR_MAX = 10;

export function magnitudeFrac(r: AnomalyRow): number {
  const a = Math.abs(rowValue(r));
  if (!Number.isFinite(a)) return 0;
  const max = rowMeasure(r) === "ratio" ? RATIO_BAR_MAX : Z_BAR_MAX;
  return Math.min(a, max) / max;
}

// ── kind labels (simple/pro wording — jargon demoted, never deleted) ─────

export const KIND_ORDER: AnomalyRow["kind"][] = [
  "anomaly_imbalance",
  "anomaly_vol",
  "anomaly_volume",
];

const KIND_LABELS: Record<AnomalyRow["kind"], { simple: string; pro: string }> = {
  anomaly_imbalance: { simple: "ONE-SIDED FLOW", pro: "IMBALANCE" },
  anomaly_vol: { simple: "PRICE SWINGS", pro: "VOLATILITY" },
  anomaly_volume: { simple: "VOLUME SPIKE", pro: "VOLUME" },
};

export function kindLabel(kind: AnomalyRow["kind"], mode: "simple" | "pro"): string {
  return KIND_LABELS[kind][mode];
}

/** Kind-chip color: imbalance is directional (buy=bid green / sell=ask red,
 *  by the sign of its statistic); vol/volume fire high-side only (warn). */
export function kindColor(r: AnomalyRow): string {
  if (r.kind === "anomaly_imbalance") {
    return rowValue(r) >= 0 ? "var(--bid)" : "var(--ask)";
  }
  return "var(--warn)";
}

// ── compound signals (client-side co-occurrence, right lane) ─────────────
// A symbol with ≥2 DISTINCT anomaly kinds inside a rolling 24h window ending
// at its own latest event. Computed from the same fetched rows the tape
// shows — CO-OCCURRENCE, NOT CAUSATION, and the lane says so.

export interface CompoundKind {
  kind: AnomalyRow["kind"];
  /** The strongest row of this kind inside the window (severity, then |value|). */
  strongest: AnomalyRow;
  count: number;
}

export interface CompoundGroup {
  symbol: string;
  market: Market;
  kinds: CompoundKind[]; // canonical order (imbalance, vol, volume)
  events: number; // rows inside the window
  latestTs: number;
  earliestTs: number;
}

export function compoundGroups(rows: AnomalyRow[], windowSec = 86_400): CompoundGroup[] {
  const bySym = new Map<string, AnomalyRow[]>();
  for (const r of rows) {
    const key = `${r.market}:${r.symbol}`;
    const list = bySym.get(key);
    if (list) list.push(r);
    else bySym.set(key, [r]);
  }

  const groups: CompoundGroup[] = [];
  for (const list of bySym.values()) {
    const latestTs = Math.max(...list.map((r) => r.ts));
    const recent = list.filter((r) => r.ts >= latestTs - windowSec);
    const byKind = new Map<AnomalyRow["kind"], { strongest: AnomalyRow; count: number }>();
    for (const r of recent) {
      const cur = byKind.get(r.kind);
      if (!cur) {
        byKind.set(r.kind, { strongest: r, count: 1 });
        continue;
      }
      cur.count++;
      const rb = BAND_RANK[severityBand(r)];
      const cb = BAND_RANK[severityBand(cur.strongest)];
      if (rb > cb || (rb === cb && Math.abs(rowValue(r)) > Math.abs(rowValue(cur.strongest)))) {
        cur.strongest = r;
      }
    }
    if (byKind.size < 2) continue;
    groups.push({
      symbol: recent[0].symbol,
      market: recent[0].market,
      kinds: KIND_ORDER.filter((k) => byKind.has(k)).map((k) => ({
        kind: k,
        strongest: byKind.get(k)!.strongest,
        count: byKind.get(k)!.count,
      })),
      events: recent.length,
      latestTs,
      earliestTs: Math.min(...recent.map((r) => r.ts)),
    });
  }

  groups.sort((a, b) => b.kinds.length - a.kinds.length || b.latestTs - a.latestTs);
  return groups;
}
