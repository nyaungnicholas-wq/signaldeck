// Shared regime helpers — keep label→color mapping consistent across panels.
// Labels are free-form strings from the daemon; we normalize the four canonical
// regimes and fall back gracefully for anything unrecognized.

export type RegimeKind = "uptrend" | "downtrend" | "range" | "squeeze" | "other";

/** Canonicalize a raw regime label (case/spacing tolerant). */
export function regimeKind(label: string): RegimeKind {
  const s = (label ?? "").trim().toLowerCase();
  if (s === "uptrend" || s === "up" || s === "bull") return "uptrend";
  if (s === "downtrend" || s === "down" || s === "bear") return "downtrend";
  if (s === "range" || s === "ranging" || s === "sideways" || s === "chop") return "range";
  if (s === "squeeze" || s === "compression" || s === "coil") return "squeeze";
  return "other";
}

/** Color for a regime — green up, red down, dim range, purple squeeze. */
export function regimeColor(label: string): string {
  switch (regimeKind(label)) {
    case "uptrend":
      return "var(--bid)";
    case "downtrend":
      return "var(--ask)";
    case "squeeze":
      return "var(--crossed)";
    case "range":
      return "var(--dim)";
    default:
      return "var(--dim)";
  }
}

// Sort order used to group the regime map (strongest signals first).
const KIND_ORDER: Record<RegimeKind, number> = {
  uptrend: 0,
  squeeze: 1,
  downtrend: 2,
  range: 3,
  other: 4,
};

export function regimeSortRank(label: string): number {
  return KIND_ORDER[regimeKind(label)];
}

/** Clamp a 0..1 strength to a 0..100 bar width; tolerate NaN / out-of-range. */
export function strengthPct(strength: number): number {
  if (!Number.isFinite(strength)) return 0;
  return Math.max(0, Math.min(100, strength * 100));
}

// Breakout kind → color. donchian_up green, donchian_down red, volume_spike
// amber, squeeze_release purple, correlation_break warn (amber-ish). Anything
// else falls back to dim.
export function breakoutColor(kind: string): string {
  switch ((kind ?? "").trim().toLowerCase()) {
    case "donchian_up":
      return "var(--bid)";
    case "donchian_down":
      return "var(--ask)";
    case "volume_spike":
      return "var(--accent)";
    case "squeeze_release":
      return "var(--crossed)";
    case "correlation_break":
      return "var(--warn)";
    default:
      return "var(--dim)";
  }
}

/** Humanize a breakout/regime machine label ("donchian_up" → "donchian up"). */
export function humanizeKind(kind: string): string {
  return (kind ?? "").replace(/_/g, " ");
}
