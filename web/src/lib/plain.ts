// ─────────────────────────────────────────────────────────────────────────
// PLAIN — the translation layer (Stage 1 of the "numbers with meaning" pass).
//
// One central dictionary that turns every metric the product shows into a
// plain-English sentence with an honest good/bad read. Components render it
// through <Plain> (components/Plain.tsx) which flips presentation with the
// SIMPLE/PRO view-mode toggle.
//
// HONESTY DOCTRINE (non-negotiable):
//   · plain language NEVER oversimplifies into a lie — wording maps 1:1 to
//     the real number and its published buckets;
//   · gated / null / non-finite inputs always read "no read yet" — a missing
//     number is NEVER dressed up as a verdict;
//   · sample-size caveats ride along in the sentence, they are not hidden by
//     simple mode.
//
// This file is PURE (no React, no DOM) so it can be unit-tested with
// `node --test src/lib/plain.test.mjs` (Node's native type-stripping).
// ─────────────────────────────────────────────────────────────────────────

/** -1 (bad) .. +1 (good); null = no read / not a good-vs-bad quantity. */
export type Goodness = number | null;

export interface PlainReading {
  /** One plain-English sentence — what SIMPLE mode leads with. */
  plain: string;
  /** The longer honest explanation (tooltips / PRO subtitles). */
  detail: string;
  /** Good/bad read for the colored dot. null = neutral/no read. */
  goodness: Goodness;
  /** The formatted raw value (what PRO mode leads with). */
  raw: string;
}

/** Optional context a metric may need to stay honest. */
export interface PlainCtx {
  /** Explicit gate: when true the reading is withheld → "no read yet". */
  gated?: boolean;
  /** Sample size behind the number (adds "small sample" caveats). */
  n?: number;
  /** The gate threshold n must reach (for sample_gate wording). */
  minN?: number;
  /** Benchmark for win-rate (realized base rate), 0..1. */
  baseRate?: number;
  /** Horizon tag for wording ("1d", "1w", …). */
  horizon?: string;
}

const NO_READ = "no read yet — not enough data";

function noRead(detail: string): PlainReading {
  return { plain: NO_READ, detail, goodness: null, raw: "—" };
}

function bad(v: unknown, ctx?: PlainCtx): v is null | undefined {
  return ctx?.gated === true || v == null || (typeof v === "number" && !Number.isFinite(v));
}

function clamp(v: number, lo = -1, hi = 1): number {
  return Math.min(hi, Math.max(lo, v));
}

function pct(v: number, digits = 0): string {
  return `${(v * 100).toFixed(digits)}%`;
}

function smallSample(ctx?: PlainCtx): string {
  if (ctx?.n != null && ctx.n > 0 && ctx.n < 100) return ` (small sample — ${ctx.n} obs)`;
  return "";
}

// ── calibrated probability → verdict ─────────────────────────────────────

export type VerdictLabel = "LEANS UP" | "LEANS DOWN" | "NO CLEAR LEAN" | "NO READ YET";

export interface Verdict {
  label: VerdictLabel;
  /** "▲" | "▼" | "→" | "·" — colored arrow for verdict cards. */
  arrow: string;
  /** CSS var for the arrow/label color. */
  color: string;
  /** 0..100 chance of an up move, or null when there is no read. */
  pct: number | null;
  /** 0..1 distance from coin-flip (|p−0.5|·2), null when no read. */
  confidence: number | null;
  /** Plain wording for that confidence. */
  confidenceWord: string;
}

/** Confidence wording buckets — |p−0.5|·2. */
export function confidenceWord(conf: number): string {
  if (!Number.isFinite(conf)) return "no read";
  if (conf < 0.1) return "basically a coin flip";
  if (conf < 0.25) return "slight lean";
  if (conf < 0.5) return "moderate lean";
  return "strong lean";
}

/**
 * The verdict everywhere else builds on: calibrated P(up) → LEANS UP/DOWN.
 * Only ever call this with the REAL calibrated probability. Gated or missing
 * input yields the honest NO READ YET verdict — never a fake lean.
 */
export function probVerdict(calProb: number | null | undefined, ctx?: PlainCtx): Verdict {
  if (bad(calProb, ctx)) {
    return {
      label: "NO READ YET",
      arrow: "·",
      color: "var(--faint)",
      pct: null,
      confidence: null,
      confidenceWord: "no read",
    };
  }
  const p = calProb as number;
  const conf = Math.abs(p - 0.5) * 2;
  const word = confidenceWord(conf);
  if (conf < 0.1) {
    return { label: "NO CLEAR LEAN", arrow: "→", color: "var(--dim)", pct: Math.round(p * 100), confidence: conf, confidenceWord: word };
  }
  const up = p >= 0.5;
  return {
    label: up ? "LEANS UP" : "LEANS DOWN",
    arrow: up ? "▲" : "▼",
    color: up ? "var(--bid)" : "var(--ask)",
    pct: Math.round(p * 100),
    confidence: conf,
    confidenceWord: word,
  };
}

/**
 * Honest tier badge for a symbol agent — which evidence tier actually drives
 * the blend. Personal = its own record; anything else says so out loud.
 */
export function tierBadge(
  tier: string | null | undefined,
  nSamples?: number | null,
  threshold?: number | null,
): { label: string; detail: string } {
  const k = nSamples ?? 0;
  const need = threshold ?? 0;
  const progress = need > 0 ? ` ${k}/${need}` : k > 0 ? ` ${k} samples` : "";
  switch (tier) {
    case "personal":
      return {
        label: `own model${need > 0 ? ` (${k}/${need})` : ""}`,
        detail: "This symbol has resolved enough of its own outcomes to use its personal model.",
      };
    case "regime":
      return {
        label: `still learning${progress} — using regime model`,
        detail: "Not enough of this symbol's own outcomes yet — falling back to the model learned for its market regime.",
      };
    case "global":
      return {
        label: `still learning${progress} — using global model`,
        detail: "Not enough of this symbol's own outcomes yet — falling back to the model learned across all symbols.",
      };
    case "static":
      return {
        label: `no learned model yet${progress ? ` (${k} samples)` : ""} — static prior`,
        detail: "Nothing learned yet for this symbol at any tier — predictions use the static equal-weight prior.",
      };
    default:
      return { label: "tier unknown", detail: "The agent did not report which evidence tier is in use." };
  }
}

// ── regime label words ────────────────────────────────────────────────────

const REGIME_WORDS: Record<string, string> = {
  uptrend: "steady uptrend — price has been climbing",
  downtrend: "downtrend — price has been falling",
  range: "moving sideways — no clear direction",
  chop: "choppy — swinging with no clear direction",
  squeeze: "coiled tight — unusually quiet, often before a bigger move",
  // VIX regimes (FRED close vs conventional bands)
  calm: "market calm",
  normal: "market normal",
  elevated: "market nervous",
  stressed: "market fearful",
};

const REGIME_GOODNESS: Record<string, number> = {
  uptrend: 0.7,
  downtrend: -0.7,
  range: 0,
  chop: 0,
  squeeze: 0,
  calm: 0.6,
  normal: 0.2,
  elevated: -0.5,
  stressed: -1,
};

/** "uptrend" → "steady uptrend — price has been climbing". Unknown labels
 *  are humanized, never invented. */
export function regimeSentence(label: string | null | undefined): string {
  if (!label) return NO_READ;
  const key = label.toLowerCase().trim();
  return REGIME_WORDS[key] ?? key.replace(/[_:|]+/g, " ");
}

// ── z-score → percentile wording ─────────────────────────────────────────

/** |z| → honest "top X%" bucket (one-sided normal tail, conventional cuts). */
export function zTail(absZ: number): string | null {
  if (absZ >= 3.1) return "top 0.1%";
  if (absZ >= 2.6) return "top 0.5%";
  if (absZ >= 2.3) return "top 1%";
  if (absZ >= 2.0) return "top 2%";
  if (absZ >= 1.65) return "top 5%";
  return null; // within normal range
}

// ── THE DICTIONARY ────────────────────────────────────────────────────────

export type MetricKey =
  | "cal_prob"
  | "ic"
  | "brier"
  | "win_rate"
  | "base_rate"
  | "lift"
  | "regime"
  | "pressure"
  | "zscore"
  | "vix"
  | "quintile_spread"
  | "reliability"
  | "sample_gate";

interface MetricDef {
  /** Column-header / label wording per mode. */
  label: { simple: string; pro: string };
  read(value: number | string | null | undefined, ctx?: PlainCtx): PlainReading;
}

export const PLAIN: Record<MetricKey, MetricDef> = {
  // Calibrated probability of an up move (THE product number).
  cal_prob: {
    label: { simple: "chance of rising", pro: "cal P(up)" },
    read(v, ctx) {
      const detail =
        "Calibrated probability the price is higher at the horizon — the raw ensemble output corrected against the platform's own resolved outcomes. 50% = coin flip.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const vd = probVerdict(v, ctx);
      const dir = vd.label === "LEANS UP" ? "leans up" : vd.label === "LEANS DOWN" ? "leans down" : "no clear lean";
      return {
        plain: `${dir} — ${vd.pct}% chance of rising (${vd.confidenceWord})${smallSample(ctx)}`,
        detail,
        goodness: clamp((v - 0.5) * 2 * 2), // ±0.25 from coin-flip saturates the dot
        raw: pct(v, 1),
      };
    },
  },

  // Information coefficient — signal quality.
  ic: {
    label: { simple: "signal quality", pro: "IC" },
    read(v, ctx) {
      const detail =
        "Information coefficient — correlation between the signal and the realized forward return over independent observations. Higher is better; 0 = no information, negative = inverted.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const a = Math.abs(v);
      let word: string;
      if (a < 0.02) word = "none";
      else if (a < 0.05) word = "weak";
      else if (a < 0.1) word = "decent";
      else word = "strong";
      let plain = `signal quality: ${word} (higher is better)`;
      if (v < 0 && a >= 0.02) plain = `signal quality: ${word} but INVERTED — it points the wrong way`;
      plain += smallSample(ctx);
      return { plain, detail, goodness: a < 0.02 ? 0 : clamp(v * 10), raw: v.toFixed(3) };
    },
  },

  // Brier score — prediction error.
  brier: {
    label: { simple: "prediction error", pro: "Brier" },
    read(v, ctx) {
      const detail =
        "Brier score — mean squared error of the calibrated probability vs the 0/1 outcome. Lower is better; 0.25 = what a coin flip scores.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      let word: string;
      if (v < 0.2) word = "clearly better than a coin flip";
      else if (v < 0.245) word = "a bit better than a coin flip";
      else if (v <= 0.255) word = "about a coin flip";
      else word = "WORSE than a coin flip";
      return {
        plain: `prediction error: ${v.toFixed(3)} — lower is better; 0.25 = coin flip (${word})${smallSample(ctx)}`,
        detail,
        goodness: clamp((0.25 - v) * 8),
        raw: v.toFixed(3),
      };
    },
  },

  // Win rate vs its honest benchmark.
  win_rate: {
    label: { simple: "how often right", pro: "win rate" },
    read(v, ctx) {
      const detail =
        "Fraction of resolved predictions where the market moved the predicted way. Only meaningful against the base rate — the market drifts up on its own.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const base = ctx?.baseRate;
      let vs = "";
      let good: Goodness = null;
      if (base != null && Number.isFinite(base)) {
        const d = v - base;
        vs =
          Math.abs(d) < 0.005
            ? ` — same as just guessing the base rate (${pct(base)})`
            : ` — ${d > 0 ? "beats" : "LOSES to"} the ${pct(base)} base rate by ${pct(Math.abs(d), 1)}`;
        good = clamp(d * 10);
      }
      return {
        plain: `right ${pct(v)} of the time${vs}${smallSample(ctx)}`,
        detail,
        goodness: good,
        raw: pct(v, 1),
      };
    },
  },

  // Base rate — the benchmark itself (neutral, never good/bad).
  base_rate: {
    label: { simple: "the bar to beat", pro: "base rate" },
    read(v, ctx) {
      const detail =
        "Realized up-rate of the sample — what a constant 'always up' forecast would score. Any claimed skill must beat this.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      return {
        plain: `the market rose ${pct(v)} of the time on its own — that's the bar to beat`,
        detail,
        goodness: null,
        raw: pct(v, 1),
      };
    },
  },

  // Lift / edge vs guessing (e.g. hit-rate lift or Brier skill), as a fraction.
  lift: {
    label: { simple: "edge vs guessing", pro: "lift" },
    read(v, ctx) {
      const detail =
        "Edge over the naive benchmark (always guessing the base rate). Positive = the signal adds something; 0 or negative = it doesn't.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const s = v > 0 ? "+" : "";
      const word =
        v <= 0 ? "no edge vs guessing" : v < 0.02 ? "barely any edge vs guessing" : v < 0.1 ? "a small real edge vs guessing" : "a solid edge vs guessing";
      return {
        plain: `${word} (${s}${pct(v, 1)})${smallSample(ctx)}`,
        detail,
        goodness: v <= 0 ? clamp(v * 10) : clamp(v * 8),
        raw: `${s}${pct(v, 1)}`,
      };
    },
  },

  // Regime label → sentence.
  regime: {
    label: { simple: "market mood", pro: "regime" },
    read(v, ctx) {
      const detail =
        "Detected market regime — a descriptive label from stored price history, not a prediction.";
      // A regime is only ever a non-empty string label; anything else
      // (null, "", stray numbers/NaN) is honestly "no read yet".
      if (ctx?.gated === true || typeof v !== "string" || v.trim() === "") return noRead(detail);
      const key = v.toLowerCase().trim();
      return {
        plain: regimeSentence(key),
        detail,
        goodness: REGIME_GOODNESS[key] ?? null,
        raw: String(v),
      };
    },
  },

  // Pressure score −1..+1 (matches the daemon's insight-writer buckets).
  pressure: {
    label: { simple: "buying/selling pressure", pro: "pressure score" },
    read(v, ctx) {
      const detail =
        "Composite pressure score, −1..+1 — how one-sided recent activity is. Matches the daemon's verdict buckets; descriptive, not advice.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      let word: string;
      if (v >= 0.5) word = "strong buying pressure";
      else if (v >= 0.15) word = "mild buying pressure";
      else if (v > -0.15) word = "balanced — no clear pressure either way";
      else if (v > -0.5) word = "mild selling pressure";
      else word = "strong selling pressure";
      return { plain: word, detail, goodness: clamp(v), raw: `${v >= 0 ? "+" : ""}${v.toFixed(2)}` };
    },
  },

  // Imbalance / anomaly z-score → "unusually one-sided … (top X%)".
  zscore: {
    label: { simple: "how unusual", pro: "z-score" },
    read(v, ctx) {
      const detail =
        "Z-score vs this symbol's own baseline — how many standard deviations today's reading is from normal. Descriptive unusualness, NOT a prediction.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const tail = zTail(Math.abs(v));
      const side = v > 0 ? "buying" : "selling";
      const plain = tail
        ? `unusually one-sided ${side} (${tail} vs its own history)`
        : "within its normal range";
      return {
        plain,
        detail,
        goodness: tail ? clamp(Math.sign(v) * Math.min(Math.abs(v) / 3, 1)) : 0,
        raw: `z=${v.toFixed(1)}`,
      };
    },
  },

  // VIX level → calm/nervous/fearful.
  vix: {
    label: { simple: "market nerves", pro: "VIX" },
    read(v, ctx) {
      const detail =
        "CBOE VIX (FRED daily close) — the option market's priced-in expectation of S&P 500 swings. Conventional bands: <15 calm, 15–20 normal, 20–30 nervous, 30+ fearful.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      let word: string;
      let good: number;
      if (v < 15) {
        word = "market calm — big swings not priced in";
        good = 0.6;
      } else if (v < 20) {
        word = "market normal — typical expected swings";
        good = 0.2;
      } else if (v < 30) {
        word = "market nervous — bigger swings priced in";
        good = -0.5;
      } else {
        word = "market fearful — violent swings priced in";
        good = -1;
      }
      return { plain: word, detail, goodness: good, raw: v.toFixed(1) };
    },
  },

  // Top-minus-bottom quintile/bucket spread (fraction, e.g. 0.012 = +1.2%).
  quintile_spread: {
    label: { simple: "does the ranking work", pro: "quintile spread" },
    read(v, ctx) {
      const detail =
        "Mean forward return of the top-ranked bucket minus the bottom-ranked bucket. Positive = higher-ranked symbols actually did better — the ranking carries information.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const s = v > 0 ? "+" : "";
      const word =
        v > 0
          ? "top-ranked beat bottom-ranked — the ranking is doing work"
          : v < 0
            ? "top-ranked did WORSE than bottom-ranked — the ranking is backwards here"
            : "no difference between top and bottom ranks";
      return {
        plain: `${word} (${s}${pct(v, 1)} avg)${smallSample(ctx)}`,
        detail,
        goodness: clamp(v * 40),
        raw: `${s}${pct(v, 2)}`,
      };
    },
  },

  // Reliability / calibration error (mean |predicted − realized| over bins).
  reliability: {
    label: { simple: "honesty of the %s", pro: "reliability" },
    read(v, ctx) {
      const detail =
        "Calibration error — average gap between predicted probability and realized frequency across bins. Lower is better; 0 = when it says 70%, it happens 70% of the time.";
      if (bad(v, ctx) || typeof v !== "number") return noRead(detail);
      const word = v < 0.05 ? "the stated %s closely match reality" : v < 0.12 ? "the stated %s roughly match reality" : "the stated %s drift from reality";
      return {
        plain: `${word} (error ${v.toFixed(3)}, lower is better)${smallSample(ctx)}`,
        detail,
        goodness: clamp((0.12 - v) * 10),
        raw: v.toFixed(3),
      };
    },
  },

  // Sample-size gate: value = n collected, ctx.minN = threshold.
  sample_gate: {
    label: { simple: "evidence collected", pro: "independent N" },
    read(v, ctx) {
      const detail =
        "Independent (symbol, UTC-day) resolutions collected toward the significance gate. Skill numbers stay withheld until the sample is big enough to mean anything.";
      const n = typeof v === "number" && Number.isFinite(v) ? v : null;
      if (ctx?.gated === true || n == null) return noRead(detail);
      const minN = ctx?.minN;
      if (minN != null && n < minN) {
        return {
          plain: `still collecting evidence (${n}/${minN} independent days) — verdicts stay withheld until then`,
          detail,
          goodness: null,
          raw: `${n}/${minN}`,
        };
      }
      return {
        plain: `enough evidence collected (${n} independent days${minN != null ? `, gate ${minN}` : ""})`,
        detail,
        goodness: minN != null ? 0.5 : null,
        raw: minN != null ? `${n}/${minN}` : String(n),
      };
    },
  },
};

/** The one entry point components use: metric key + raw value (+ context) →
 *  plain sentence, detail, goodness, formatted raw. Never throws; unknown /
 *  gated / non-finite input degrades to the honest "no read yet". */
export function readMetric(
  metric: MetricKey,
  value: number | string | null | undefined,
  ctx?: PlainCtx,
): PlainReading {
  const def = PLAIN[metric];
  if (!def) return noRead("unknown metric");
  return def.read(value, ctx);
}

/** Mode-appropriate label for a metric (table headers, stat labels). */
export function metricLabel(metric: MetricKey, mode: "simple" | "pro"): string {
  const def = PLAIN[metric];
  return def ? def.label[mode] : metric;
}

/** Goodness → CSS color var for the dot / value tint. */
export function goodnessColor(g: Goodness): string {
  if (g == null) return "var(--faint)";
  if (g > 0.15) return "var(--bid)";
  if (g < -0.15) return "var(--ask)";
  return "var(--dim)";
}
