// Regression tests for the refusal summariser (src/lib/refusal.ts).
//   node --test src/lib/refusal.test.mjs
// A .mjs file so `next build` / tsc never sees it, matching plain.test.mjs.

import { test } from "node:test";
import assert from "node:assert/strict";
import { summarizeRefusal } from "./refusal.ts";

// The verbatim reason the live /api/accuracy returned on 2026-09-20. Captured,
// not hand-written: the defect this guards against was a literal U+0008 in the
// date regex, which only a real reason string exercises.
const LIVE_COLLAPSE_REASON = "grader has been refusing since 2026-09-13T14:43:41: publication gate: the graded window contains 18 collapsed cross-section(s) of 82 day(s): 1d 2026-07-27 (6 distinct across 330 symbols), 1d 2026-07-28 (8 distinct across 330 symbols), 1d 2026-07-29 (13 distinct across 328 symbols), 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld. The window starts at the survivorship epoch and does not roll forward, so a collapsed day stays in it: this clears when the window is re-registered, not by waiting for more grades.";

test("collapse summary reports the real date range", () => {
	const { headline, detail } = summarizeRefusal(LIVE_COLLAPSE_REASON);
	// This is the regression: with the backspace in the pattern the regex matched
	// nothing and betweenClause was empty, so the dates vanished from the headline.
	assert.match(headline, /between 2026-07-26 and 2026-08-06/);
	assert.match(headline, /^18 of the 82 graded day-horizons/);
	assert.equal(detail, LIVE_COLLAPSE_REASON);
});

test("collapse summary does not promise the window ages out", () => {
	const { headline } = summarizeRefusal(LIVE_COLLAPSE_REASON);
	assert.ok(!/until the window clears/.test(headline), headline);
	assert.match(headline, /does not roll forward/);
	assert.match(headline, /waiting does not clear this/);
	// and it must not invite re-registration as an automatic remedy
	assert.ok(!/re-register/i.test(headline), headline);
});

test("the refused-since timestamp is never mistaken for a collapsed day", () => {
	// "2026-09-13T14:43:41" precedes the list; no 1d/1w token introduces it, so it
	// must not widen the range. The earliest date stays 2026-07-26.
	const { headline } = summarizeRefusal(LIVE_COLLAPSE_REASON);
	assert.ok(!headline.includes("2026-09-13"), headline);
});

test("a digit-suffixed token does not fake a horizon", () => {
	// \\b must not match inside "331d": that is a symbol count, not a horizon.
	const r = "the graded window contains 1 collapsed cross-section(s) of 2 day(s): 331d 2099-01-01 (3 distinct)";
	const { headline } = summarizeRefusal(r);
	assert.ok(!headline.includes("2099-01-01"), headline);
});

test("other refusal shapes still summarise", () => {
	assert.match(summarizeRefusal("the last successful grade was 40h ago").headline, /40h old/);
	assert.match(summarizeRefusal("registry unavailable").headline, /could not be read/);
	assert.equal(summarizeRefusal("").headline, "Publication refused.");
	assert.equal(summarizeRefusal(null).headline, "Publication refused.");
});
