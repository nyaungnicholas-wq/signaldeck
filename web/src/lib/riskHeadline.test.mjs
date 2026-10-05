// Plan step 9: the "Risk first" decision (src/lib/riskHeadline.ts).
//   node --test src/lib/riskHeadline.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { riskHeadline } from "./riskHeadline.ts";

const rec = (h1, h5 = { horizon: 5, distinctDays: 14, verdict: "INSUFFICIENT" }) => ({
  asOf: 1, evidence: "LIVE", minDistinctDays: 60, horizons: [h1, h5], caveat: "x",
});

test("INSUFFICIENT: a progress strip with the API's own per-horizon counts, no promotion", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 18, verdict: "INSUFFICIENT", vsEwma: null }));
  assert.equal(r.state, "insufficient");
  assert.equal(r.lead, false);
  assert.match(r.text, /being graded live against a pre-registered test: 18 of 60 trading days \(next day\); 14 of 60 trading days \(next week\)/);
  // H-11: no claim the verdict has not earned.
  assert.doesNotMatch(r.text, /most predictable/);
  assert.match(r.text, /becomes this page’s headline if it passes; if it fails, this says so/);
  // Not hard-coded: other counts come straight through.
  assert.match(riskHeadline(rec({ horizon: 1, distinctDays: 33, verdict: "INSUFFICIENT" })).text, /33 of 60/);
});

test("PASS: keyed on the verdict string; leads, with the registered statistic", () => {
  // The pooled row means sit beside the grade on purpose: the copy must print
  // grade.headline.meanDiff, never vsEwma / vsRandomWalk.
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 61, verdict: "BEATS THE NULLS",
    vsRandomWalk: -0.0421, vsEwma: -0.5, grade: { headline: { meanDiff: -0.0123 } } }));
  assert.equal(r.state, "pass");
  assert.equal(r.lead, true);
  assert.equal(r.meanDiff, -0.0123);
  assert.match(r.text, /next-day realized volatility forecast passed its pre-registered live test against RiskMetrics EWMA/);
  assert.match(r.text, /mean daily QLIKE loss difference −0\.012 \(negative favours the forecast\) over 61 trading days/);
  assert.match(r.text, /not a guarantee of future accuracy/);
  // Only horizon 1 is graded; nothing claims the next-week forecast, or a random walk.
  assert.doesNotMatch(r.text, /next.week|random walk|−0\.500|−0\.042/);
});

test("PASS without the registered statistic: no number at all, never the pooled one", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 61, verdict: "BEATS THE NULLS", vsEwma: -0.5 }));
  assert.equal(r.state, "pass");
  assert.equal(r.meanDiff, undefined);
  assert.match(r.text, /against RiskMetrics EWMA over 61 trading days\. That is a test result/);
  assert.doesNotMatch(r.text, /−0\.500/);
});

test("the decision ignores the numbers: winning numbers without the pass verdict do not promote", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 61, verdict: "ACCRUING", vsRandomWalk: -0.5, vsEwma: -0.5 }));
  assert.equal(r.state, "accruing");
  assert.equal(r.lead, false);
  assert.doesNotMatch(r.text, /passed|beat/);
});

test("past the floor the true day count shows, not a clamped 60", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 64, verdict: "ACCRUING" }));
  assert.match(r.text, /64 trading days \(floor 60\), awaiting its grade \(next day\)/);
  assert.doesNotMatch(r.text, /most predictable/);
  assert.doesNotMatch(r.text, /60 of 60/);
  assert.match(r.text, /14 of 60 trading days \(next week\)/);
});

test("the headline cell is horizon 1: a passing horizon 5 alone does not promote", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 61, verdict: "NO SKILL DEMONSTRATED" },
    { horizon: 5, distinctDays: 61, verdict: "BEATS THE NULLS" }));
  assert.equal(r.state, "fail");
  assert.equal(r.lead, false);
});

test("FAIL: both registered failure verdicts say so plainly, with no promotion", () => {
  for (const v of ["NO SKILL DEMONSTRATED", "ESTIMATOR ARTIFACT"]) {
    const r = riskHeadline(rec({ horizon: 1, distinctDays: 60, verdict: v }));
    assert.equal(r.state, "fail", v);
    assert.equal(r.lead, false);
    assert.match(r.text, /did not beat its pre-registered baseline, RiskMetrics EWMA/);
    assert.match(r.text, new RegExp(v.toLowerCase()));
  }
});

test("missing or malformed payloads fall back to 'not available' with no claim", () => {
  const bad = [
    undefined, null, "oops", 42, {}, { minDistinctDays: 60 }, { minDistinctDays: 60, horizons: "x" },
    { minDistinctDays: 0, horizons: [] },
    { minDistinctDays: 60, horizons: [] },
    { minDistinctDays: 60, horizons: [{ horizon: 5, distinctDays: 3, verdict: "INSUFFICIENT" }] },
    { minDistinctDays: 60, horizons: [{ horizon: 1, distinctDays: "18", verdict: "INSUFFICIENT" }] },
    { minDistinctDays: 60, horizons: [null] },
    rec({ horizon: 1, distinctDays: 60, verdict: "PASS" }),
    rec({ horizon: 1, distinctDays: 60, verdict: "beats the nulls" }),
  ];
  for (const b of bad) {
    const r = riskHeadline(b);
    assert.equal(r.state, "unavailable", JSON.stringify(b));
    assert.equal(r.lead, false);
    assert.match(r.text, /not available/);
    assert.doesNotMatch(r.text, /passed|beat/);
  }
});

test("H-8: a verdict badge is green only for the registered pass, and the page uses that rule", async () => {
  const { verdictTone } = await import("./riskHeadline.ts");
  assert.equal(verdictTone("BEATS THE NULLS"), "ok");
  for (const v of ["NO SKILL DEMONSTRATED", "ESTIMATOR ARTIFACT", "ACCRUING", "SECONDARY", "INSUFFICIENT", "beats the nulls", ""]) {
    assert.equal(verdictTone(v), "warn", v);
  }
  const { readFileSync } = await import("node:fs");
  const page = readFileSync(new URL("../app/volatility/page.tsx", import.meta.url), "utf8");
  assert.ok(page.includes('verdictTone(h.verdict) === "ok"'), "the volatility badge must colour by verdictTone");
  assert.ok(!page.includes('insufficient ? "var(--warn)" : "var(--ok)"'), "sufficiency alone must not turn the badge green");
});

test("RV-COPY: the pass block marks the next-week forecast as not graded", async () => {
  const { readFileSync } = await import("node:fs");
  const c = readFileSync(new URL("../components/home/RiskFirst.tsx", import.meta.url), "utf8");
  assert.ok(c.includes("Next week (not graded)"));
  assert.ok(c.includes("the next-week figure is shown ungraded"));
});
