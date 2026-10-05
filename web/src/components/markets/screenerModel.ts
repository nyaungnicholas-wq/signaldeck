// Shared model for the SCREENER page — pure types + helpers (no React, no
// DOM). Extracted in the page split: src/app/markets/screener/page.tsx now
// composes hooks/useScreenerData + hooks/useScreenerFilters with the
// components in this directory; behavior is identical to the old single file.

import type { Horizon, Market, RankedRow, RegimeState, ScoreComponent, WatchRow } from "@/lib/api";

export type Direction = "all" | "buy" | "sell";
export type MarketFilter = "all" | Market;
/** Stage 5: TABLE stays the workhorse; HEATMAP is the same filtered set as
 *  color tiles (day % change) — a view toggle, not different data.
 *  Stage 4 (tables→charts): CARDS is the same filtered set as a grid of
 *  verdict cards (sparkline + honest tier badge). Default view follows the
 *  SIMPLE/PRO toggle — cards in simple, table in pro — until the user picks
 *  one explicitly. No view ever loses data: all three render the SAME rows. */
export type View = "cards" | "table" | "heatmap";
export type SortKey =
  | "symbol"
  | "market"
  | "spark"
  | "lastClose"
  | "dayChangePct"
  | "score"
  | "verdict"
  | "rank"
  | "regime"
  | "driver"
  | "latestBarTs";
export type SortDir = "asc" | "desc";

export const COLUMNS: { key: SortKey; label: string; numeric: boolean; title?: string; sortable?: boolean }[] = [
  { key: "symbol", label: "SYMBOL", numeric: false },
  { key: "market", label: "MARKET", numeric: false },
  // Stage 5: inline 30-day trend from the same stored daily closes as LAST —
  // no extra fetch, and <5 closes renders the honest dashed placeholder.
  { key: "spark", label: "TREND 30D", numeric: false, sortable: false, title: "Last ~30 stored daily closes (worker cadence, not live)" },
  { key: "lastClose", label: "LAST", numeric: true, title: "Last close price" },
  { key: "dayChangePct", label: "DAY %", numeric: true, title: "Change since previous close" },
  { key: "score", label: "SCORE", numeric: true, title: "Pressure score, −1 (sell) to +1 (buy)" },
  // Stage 2 (verdict cards): the REAL calibrated 1d P(up) as an honest
  // verdict card — sortable by that probability; "NO READ YET" rows sink.
  { key: "verdict", label: "VERDICT (1d)", numeric: false, title: "Calibrated 1d P(up) as a verdict card — near-50% reads NO CLEAR LEAN; no stored prediction reads NO READ YET (never a fabricated lean). Backtested calibration, not a live track record." },
  // Stage 5: cross-sectional relative-strength rank + current regime label.
  { key: "rank", label: "RANK", numeric: true, title: "Cross-sectional relative-strength rank (1 = strongest); — = not in the latest ranking pass" },
  { key: "regime", label: "REGIME", numeric: false, title: "Current detected regime (described from stored bars, no lookahead); — = not classified yet" },
  { key: "driver", label: "TOP DRIVER", numeric: false, title: "Component contributing most to the score" },
  { key: "latestBarTs", label: "LAST BAR", numeric: true, title: "Time of the most recent price bar" },
];

export interface Derived {
  row: WatchRow;
  /** score for the selected horizon; null when the daemon hasn't scored it yet */
  score: number | null;
  driver: ScoreComponent | null;
  /** relative-strength rank; null = not in the latest ranking pass (honest) */
  rank: number | null;
  /** current regime state; null = not classified yet (honest) */
  regime: RegimeState | null;
}

export function topDriver(components: ScoreComponent[] | undefined): ScoreComponent | null {
  if (!components || components.length === 0) return null;
  let best: ScoreComponent | null = null;
  for (const c of components) {
    if (!isFinite(c.contrib)) continue;
    if (best === null || Math.abs(c.contrib) > Math.abs(best.contrib)) best = c;
  }
  return best;
}

export function sortValue(d: Derived, key: SortKey): string | number | null {
  switch (key) {
    case "symbol":
      return d.row.symbol;
    case "market":
      return d.row.market;
    case "spark":
      return null; // not sortable (visual column)
    case "lastClose":
      return isFinite(d.row.lastClose) ? d.row.lastClose : null;
    case "dayChangePct":
      return isFinite(d.row.dayChangePct) ? d.row.dayChangePct : null;
    case "score":
      return d.score;
    case "verdict": {
      // Stage 2: sort by the REAL calibrated 1d P(up); rows without a stored
      // prediction return null and sink to the bottom (honest absence).
      const p = d.row.calProb1d;
      return typeof p === "number" && isFinite(p) ? p : null;
    }
    case "rank":
      // rank 1 is best — negate so "desc" (the numeric default) puts #1 on top.
      return d.rank === null ? null : -d.rank;
    case "regime":
      return d.regime ? d.regime.label : null;
    case "driver":
      return d.driver ? d.driver.name : null;
    case "latestBarTs":
      return d.row.latestBarTs || null;
  }
}

/** Build the derived row set for a horizon, joining best-effort rank/regime. */
export function deriveRows(
  rows: WatchRow[],
  horizon: Horizon,
  ranking: RankedRow[] | null,
  regimes: RegimeState[] | null,
): Derived[] {
  const rankBy = new Map<string, number>();
  for (const r of ranking ?? []) rankBy.set(`${r.market}:${r.symbol}`, r.rank);
  const regimeBy = new Map<string, RegimeState>();
  for (const s of regimes ?? []) regimeBy.set(`${s.market}:${s.symbol}`, s);
  return rows.map((row) => {
    const s = row.scores?.[horizon];
    const score = s && isFinite(s.score) ? s.score : null;
    const key = `${row.market}:${row.symbol}`;
    return {
      row,
      score,
      driver: topDriver(s?.components),
      rank: rankBy.get(key) ?? null,
      regime: regimeBy.get(key) ?? null,
    };
  });
}
