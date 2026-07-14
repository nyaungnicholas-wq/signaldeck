// ALERTS tab — the published rulebook (pure module, no React).
//
// Every alert kind maps to its NAMED rule with the exact thresholds the
// daemon fires on, so a chip is never just a colored word — it cites the
// spec. Sources of truth (mirrored here verbatim, keep in sync):
//   daemon/internal/alerts/alerts.go    — prediction thresholds + dedup
//   daemon/internal/breakout/breakout.go — Donchian/volume/squeeze constants
//   daemon/internal/anomaly/anomaly.go   — z threshold + proxy label
//
// HONESTY: an alert kind this file doesn't know is NOT hidden or guessed at —
// ruleFor() returns a generic entry that says out loud the rule is
// unpublished, so open-ended kinds from newer daemons always render.

export interface AlertRule {
  /** Chip label ("BREAKOUT"). */
  label: string;
  /** Chip color (CSS var). */
  color: string;
  /** One-line named rule — what fires it, with the headline threshold. */
  rule: string;
  /** Exact threshold lines for the methodology panel. */
  thresholds: string[];
  /** Published reference page for the underlying detector, when one exists. */
  href?: string;
  hrefLabel?: string;
  /** false = kind not in this rulebook (rendered generically, never hidden). */
  known: boolean;
}

/** Display order for kind chips (unknown kinds append after, alphabetical). */
export const KIND_ORDER = [
  "breakout",
  "regime_change",
  "prediction_high",
  "prediction_low",
  "anomaly_imbalance",
  "anomaly_vol",
  "anomaly_volume",
];

const ANOMALY_BASE = [
  "fires when the reading is ≥ 2.5 standard deviations from this symbol's OWN trailing baseline (SIGNALDECK_ANOM_Z, default 2.5)",
  "descriptive unusualness — it predicts nothing; the alert text always states the window and baseline it was measured against",
];

export const ALERT_RULES: Record<string, AlertRule> = {
  breakout: {
    label: "BREAKOUT",
    color: "var(--accent)",
    rule: "donchian / vol-spike / squeeze — exact thresholds in METHODOLOGY",
    thresholds: [
      "donchian up/down: close beyond the highest high / lowest low of the PRIOR 20 bars (channel excludes the current bar — no lookahead)",
      "volume spike: latest volume > 2.5× its own 20-bar average volume",
      "squeeze release: Bollinger(20, 2σ) band width sat in the bottom 20% of the last 90 bars, then expanded above its 20-bar average width",
    ],
    href: "/markets/regimes",
    hrefLabel: "see /markets/regimes",
    known: true,
  },
  regime_change: {
    label: "REGIME",
    color: "var(--dim)",
    rule: "detected regime label changed (e.g. uptrend → range) — descriptive, not a forecast",
    thresholds: [
      "fires when the stored regime label for a watched symbol flips — a descriptive classification of price history, never a prediction",
    ],
    href: "/markets/regimes",
    hrefLabel: "see /markets/regimes",
    known: true,
  },
  prediction_high: {
    label: "P(UP) HIGH",
    color: "var(--bid)",
    rule: "calibrated P(up) ≥ 0.65",
    thresholds: [
      "fires when the calibrated probability of an up move ≥ 0.65 (SIGNALDECK_ALERT_HI, default 0.65)",
      "deduped: at most one alert per symbol + horizon + side per 24h",
    ],
    known: true,
  },
  prediction_low: {
    label: "P(UP) LOW",
    color: "var(--ask)",
    rule: "calibrated P(up) ≤ 0.35",
    thresholds: [
      "fires when the calibrated probability of an up move ≤ 0.35 (SIGNALDECK_ALERT_LO, default 0.35)",
      "deduped: at most one alert per symbol + horizon + side per 24h",
    ],
    known: true,
  },
  anomaly_imbalance: {
    label: "IMBALANCE",
    color: "var(--warn)",
    rule: "z ≥ 2.5 vs its own baseline — trade imbalance",
    thresholds: [
      ...ANOMALY_BASE,
      "crypto = real order-book imbalance; stocks = volume-side PROXY (no order book on free stock data) — the alert says so verbatim",
    ],
    known: true,
  },
  anomaly_vol: {
    label: "VOLATILITY",
    color: "var(--warn)",
    rule: "z ≥ 2.5 vs its own baseline — volatility / true-range spike",
    thresholds: [
      ...ANOMALY_BASE,
      "the true-range spike form states a TR/ATR ratio instead of a z — the alert text always says which",
    ],
    known: true,
  },
  anomaly_volume: {
    label: "VOLUME",
    color: "var(--warn)",
    rule: "z ≥ 2.5 vs its own baseline — volume spike",
    thresholds: [...ANOMALY_BASE],
    known: true,
  },
};

/** Rule for a kind — unknown kinds get an honest generic entry, never a guess. */
export function ruleFor(kind: string): AlertRule {
  return (
    ALERT_RULES[kind] ?? {
      label: kind.replace(/_/g, " ").toUpperCase(),
      color: "var(--dim)",
      rule: `kind "${kind}" is not in the published rulebook yet — rendered generically`,
      thresholds: [
        "no published thresholds for this kind yet — the alert's own text is the only spec; nothing here is inferred",
      ],
      known: false,
    }
  );
}

/** Sort kinds: published order first, then unknown kinds alphabetically. */
export function sortKinds(kinds: string[]): string[] {
  return [...kinds].sort((a, b) => {
    const ia = KIND_ORDER.indexOf(a);
    const ib = KIND_ORDER.indexOf(b);
    if (ia !== -1 && ib !== -1) return ia - ib;
    if (ia !== -1) return -1;
    if (ib !== -1) return 1;
    return a.localeCompare(b);
  });
}
