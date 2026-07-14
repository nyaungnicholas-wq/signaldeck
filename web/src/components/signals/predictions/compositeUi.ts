// Shared UI helpers for the PREDICT tab's composite (SignalScore) views.
// Pure functions and constants only — no fetching, no state.

import type { ViewMode } from "@/components/Plain";

/** Decile color for the forced-curve 1–10 score: green top, dim middle, red
 *  bottom. The score is a RANK on today's cross-section (see curveNote), so
 *  the color grades position on the curve, never conviction. */
export function scoreColor(score: number): string {
  if (score >= 9) return "var(--ok)";
  if (score >= 7) return "color-mix(in srgb, var(--ok) 70%, var(--dim))";
  if (score >= 6) return "color-mix(in srgb, var(--ok) 40%, var(--dim))";
  if (score >= 5) return "var(--dim)";
  if (score >= 4) return "color-mix(in srgb, var(--bad) 40%, var(--dim))";
  if (score >= 2) return "color-mix(in srgb, var(--bad) 70%, var(--dim))";
  return "var(--bad)";
}

/** Signed percentage-point formatter: 6.34 → "+6.3pp". */
export function fmtPp(pp: number): string {
  return `${pp >= 0 ? "+" : ""}${pp.toFixed(1)}pp`;
}

/** The API's `edge` is a FRACTION (calProb − 0.5, per edgeNote) — pp text. */
export function edgePp(edge: number): string {
  return fmtPp(edge * 100);
}

/** The 11 fixed-order factor legs: plain-English tile names for SIMPLE mode
 *  (PRO shows the raw key), plus which legs are CONTEXT (always-neutral —
 *  regime + shortvol inform the read but never vote a direction). */
export const FACTOR_META: Record<string, { plain: string; context?: boolean }> = {
  technical: { plain: "chart signals" },
  expectancy: { plain: "similar-setup payoff" },
  forecast: { plain: "ensemble forecast" },
  sentiment: { plain: "news mood" },
  gbm: { plain: "trend model" },
  meanrev: { plain: "snap-back model" },
  ranking: { plain: "fleet ranking" },
  regime: { plain: "market regime", context: true },
  insiders: { plain: "insider trades" },
  shortvol: { plain: "short-sale volume", context: true },
  breakout: { plain: "breakout watch" },
};

/** Factor tile title: plain-English in simple mode, the raw key in pro. */
export function factorName(key: string, mode: ViewMode): string {
  return mode === "simple" ? (FACTOR_META[key]?.plain ?? key) : key;
}

/** Ledger leg labels for SIMPLE mode (ensemble legs + the calibration line). */
export const LEG_PLAIN: Record<string, string> = {
  pressure: "order-flow pressure",
  expectancy: "similar-setup payoff",
  forecast: "ensemble forecast",
  sentiment: "news mood",
  gbm: "trend model",
  meanrev: "snap-back model",
  calibration: "calibration adjustment",
};

export function legName(leg: string, mode: ViewMode): string {
  return mode === "simple" ? (LEG_PLAIN[leg] ?? leg) : leg;
}
