// Unit tests for the plain-English translation layer (src/lib/plain.ts).
// Runs on Node's built-in runner with native TypeScript type-stripping:
//   node --test src/lib/plain.test.mjs
// Deliberately a .mjs file so `next build` / tsc never sees it.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  PLAIN,
  readMetric,
  probVerdict,
  tierBadge,
  regimeSentence,
  zTail,
  confidenceWord,
  metricLabel,
  goodnessColor,
} from "./plain.ts";

const ALL_KEYS = [
  "cal_prob",
  "ic",
  "brier",
  "win_rate",
  "base_rate",
  "lift",
  "regime",
  "pressure",
  "zscore",
  "vix",
  "quintile_spread",
  "reliability",
  "sample_gate",
];

test("dictionary covers every required metric", () => {
  for (const k of ALL_KEYS) {
    assert.ok(PLAIN[k], `missing dictionary entry: ${k}`);
    assert.ok(PLAIN[k].label.simple.length > 0);
    assert.ok(PLAIN[k].label.pro.length > 0);
  }
});

// ── HONESTY EDGES: null / undefined / NaN / gated → "no read yet" ────────
test("every numeric metric degrades to 'no read yet' on null/NaN/gated", () => {
  for (const k of ALL_KEYS) {
    for (const v of [null, undefined, NaN, Infinity]) {
      if (k === "regime" && (v === NaN || v === Infinity)) continue; // regime takes strings
      const r = readMetric(k, v);
      assert.equal(r.plain, "no read yet — not enough data", `${k} with ${v}`);
      assert.equal(r.goodness, null, `${k} goodness must be null with ${v}`);
      assert.equal(r.raw, "—", `${k} raw must be em-dash with ${v}`);
    }
    // explicit gate wins even over a real number
    const gated = readMetric(k, k === "regime" ? "uptrend" : 0.62, { gated: true });
    assert.equal(gated.plain, "no read yet — not enough data", `${k} gated`);
    assert.equal(gated.goodness, null, `${k} gated goodness`);
  }
});

test("unknown metric key never throws", () => {
  const r = readMetric("not_a_metric", 1);
  assert.equal(r.plain, "no read yet — not enough data");
});

// ── cal_prob / verdicts ───────────────────────────────────────────────────
test("probVerdict: real calibrated prob → honest verdict", () => {
  const up = probVerdict(0.62);
  assert.equal(up.label, "LEANS UP");
  assert.equal(up.arrow, "▲");
  assert.equal(up.pct, 62);
  assert.equal(up.color, "var(--bid)");

  const down = probVerdict(0.38);
  assert.equal(down.label, "LEANS DOWN");
  assert.equal(down.arrow, "▼");

  const flat = probVerdict(0.52);
  assert.equal(flat.label, "NO CLEAR LEAN");

  const none = probVerdict(null);
  assert.equal(none.label, "NO READ YET");
  assert.equal(none.pct, null);
  assert.equal(none.confidence, null);

  const gatedV = probVerdict(0.9, { gated: true });
  assert.equal(gatedV.label, "NO READ YET", "gated prob must NOT produce a lean");
});

test("cal_prob sentence carries the real percent and confidence wording", () => {
  const r = readMetric("cal_prob", 0.62);
  assert.match(r.plain, /leans up — 62% chance of rising/);
  assert.match(r.plain, /slight lean|moderate lean/);
  assert.ok(r.goodness > 0);
  assert.equal(r.raw, "62.0%");
});

test("confidenceWord buckets", () => {
  assert.equal(confidenceWord(0.02), "basically a coin flip");
  assert.equal(confidenceWord(0.2), "slight lean");
  assert.equal(confidenceWord(0.4), "moderate lean");
  assert.equal(confidenceWord(0.8), "strong lean");
});

// ── IC ────────────────────────────────────────────────────────────────────
test("ic buckets: none/weak/decent/strong + inverted flag", () => {
  assert.match(readMetric("ic", 0.01).plain, /none/);
  assert.match(readMetric("ic", 0.03).plain, /weak/);
  assert.match(readMetric("ic", 0.07).plain, /decent/);
  assert.match(readMetric("ic", 0.15).plain, /strong/);
  assert.match(readMetric("ic", -0.08).plain, /INVERTED/);
  assert.ok(readMetric("ic", -0.08).goodness < 0);
  assert.ok(readMetric("ic", 0.15).goodness > 0);
});

test("ic small-sample caveat rides along in simple wording", () => {
  assert.match(readMetric("ic", 0.15, { n: 40 }).plain, /small sample — 40 obs/);
});

// ── Brier ─────────────────────────────────────────────────────────────────
test("brier wording anchors 0.25 = coin flip, lower is better", () => {
  assert.match(readMetric("brier", 0.18).plain, /clearly better than a coin flip/);
  assert.match(readMetric("brier", 0.25).plain, /about a coin flip/);
  assert.match(readMetric("brier", 0.3).plain, /WORSE than a coin flip/);
  assert.ok(readMetric("brier", 0.18).goodness > 0);
  assert.ok(readMetric("brier", 0.3).goodness < 0);
  assert.match(readMetric("brier", 0.25).plain, /lower is better/);
});

// ── win rate & base rate & lift ───────────────────────────────────────────
test("win_rate is only 'good' relative to the base rate", () => {
  const beats = readMetric("win_rate", 0.58, { baseRate: 0.52 });
  assert.match(beats.plain, /beats the 52% base rate/);
  assert.ok(beats.goodness > 0);
  const loses = readMetric("win_rate", 0.5, { baseRate: 0.54 });
  assert.match(loses.plain, /LOSES to the 54% base rate/);
  assert.ok(loses.goodness < 0);
  const noBase = readMetric("win_rate", 0.58);
  assert.equal(noBase.goodness, null, "no benchmark → no good/bad claim");
});

test("lift wording: edge vs guessing", () => {
  assert.match(readMetric("lift", 0.12).plain, /solid edge vs guessing/);
  assert.match(readMetric("lift", -0.02).plain, /no edge vs guessing/);
  assert.ok(readMetric("lift", -0.02).goodness < 0);
});

// ── regime / pressure / zscore / vix ─────────────────────────────────────
test("regime labels become sentences; unknown labels humanize, never invent", () => {
  assert.match(regimeSentence("uptrend"), /steady uptrend/);
  assert.match(regimeSentence("squeeze"), /quiet/);
  assert.equal(regimeSentence("weird_new:label"), "weird new label");
  assert.equal(readMetric("regime", "").plain, "no read yet — not enough data");
  assert.ok(readMetric("regime", "downtrend").goodness < 0);
});

test("pressure buckets match the daemon's verdict wording", () => {
  assert.match(readMetric("pressure", 0.6).plain, /strong buying pressure/);
  assert.match(readMetric("pressure", -0.6).plain, /strong selling pressure/);
  assert.match(readMetric("pressure", 0).plain, /balanced/);
});

test("zscore → 'unusually one-sided buying (top 1%)' style wording", () => {
  assert.equal(zTail(2.4), "top 1%");
  assert.equal(zTail(1.0), null);
  assert.match(readMetric("zscore", 2.4).plain, /unusually one-sided buying \(top 1%/);
  assert.match(readMetric("zscore", -2.1).plain, /unusually one-sided selling \(top 2%/);
  assert.match(readMetric("zscore", 0.5).plain, /within its normal range/);
});

test("vix bands: calm / normal / nervous / fearful", () => {
  assert.match(readMetric("vix", 12).plain, /calm/);
  assert.match(readMetric("vix", 17).plain, /normal/);
  assert.match(readMetric("vix", 24).plain, /nervous/);
  assert.match(readMetric("vix", 35).plain, /fearful/);
  assert.ok(readMetric("vix", 35).goodness < 0);
});

// ── quintile spread / reliability ────────────────────────────────────────
test("quintile_spread sign carries the verdict", () => {
  assert.match(readMetric("quintile_spread", 0.012).plain, /ranking is doing work/);
  assert.match(readMetric("quintile_spread", -0.01).plain, /backwards/);
});

test("reliability: lower is better", () => {
  assert.match(readMetric("reliability", 0.03).plain, /closely match/);
  assert.match(readMetric("reliability", 0.2).plain, /drift from reality/);
});

// ── sample gates ──────────────────────────────────────────────────────────
test("sample_gate → 'still collecting evidence (k/N days)'", () => {
  const r = readMetric("sample_gate", 12, { minN: 40 });
  assert.match(r.plain, /still collecting evidence \(12\/40 independent days\)/);
  assert.equal(r.goodness, null, "a gate is not good or bad");
  assert.equal(r.raw, "12/40");
  const done = readMetric("sample_gate", 55, { minN: 40 });
  assert.match(done.plain, /enough evidence collected/);
});

// ── tier badge ───────────────────────────────────────────────────────────
test("tierBadge is honest about fallback tiers", () => {
  assert.match(tierBadge("global", 12, 40).label, /still learning 12\/40 — using global model/);
  assert.match(tierBadge("regime", 5, 40).label, /using regime model/);
  assert.match(tierBadge("personal", 60, 40).label, /own model/);
  assert.match(tierBadge("static", 0, 0).label, /static prior/);
  assert.match(tierBadge(undefined).label, /unknown/);
});

// ── labels & colors ──────────────────────────────────────────────────────
test("metricLabel switches wording by mode", () => {
  assert.equal(metricLabel("cal_prob", "pro"), "cal P(up)");
  assert.equal(metricLabel("cal_prob", "simple"), "chance of rising");
  assert.equal(metricLabel("ic", "simple"), "signal quality");
});

test("goodnessColor maps to design tokens", () => {
  assert.equal(goodnessColor(null), "var(--faint)");
  assert.equal(goodnessColor(0.8), "var(--bid)");
  assert.equal(goodnessColor(-0.8), "var(--ask)");
  assert.equal(goodnessColor(0.05), "var(--dim)");
});
