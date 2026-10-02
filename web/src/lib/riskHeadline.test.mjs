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
  assert.match(r.text, /being graded live: 18 of 60 trading days \(next day\); 14 of 60 trading days \(next week\)/);
  assert.match(r.text, /becomes this page’s headline if it passes; if it fails, this says so/);
  // Not hard-coded: other counts come straight through.
  assert.match(riskHeadline(rec({ horizon: 1, distinctDays: 33, verdict: "INSUFFICIENT" })).text, /33 of 60/);
});

test("PASS: keyed on the verdict string; leads, with the graded numbers", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 61, verdict: "BEATS THE NULLS", vsRandomWalk: -0.0421, vsEwma: -0.0123 }));
  assert.equal(r.state, "pass");
  assert.equal(r.lead, true);
  assert.equal(r.vsRandomWalk, -0.0421);
  assert.match(r.text, /beaten its pre-registered baselines in a live test/);
  assert.match(r.text, /random walk −0\.042, versus RiskMetrics EWMA −0\.012/);
  assert.match(r.text, /graded over 61 trading days/);
});

test("the decision ignores the numbers: winning numbers without the pass verdict do not promote", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 61, verdict: "ACCRUING", vsRandomWalk: -0.5, vsEwma: -0.5 }));
  assert.equal(r.state, "accruing");
  assert.equal(r.lead, false);
  assert.doesNotMatch(r.text, /beaten/);
});

test("past the floor the true day count shows, not a clamped 60", () => {
  const r = riskHeadline(rec({ horizon: 1, distinctDays: 64, verdict: "ACCRUING" }));
  assert.match(r.text, /64 trading days \(floor 60\), awaiting its grade \(next day\)/);
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
    assert.match(r.text, /did not beat its pre-registered baselines/);
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
    assert.doesNotMatch(r.text, /beaten|did not beat/);
  }
});
