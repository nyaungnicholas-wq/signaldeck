// node --test src/lib/publicForecasts.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { pivotForecasts, strengthText, daysSince, STALE_AFTER_DAYS } from "./publicForecasts.ts";

test("rows pivot to one line per symbol with kinds in reading order", () => {
  const { kinds, lines } = pivotForecasts([
    { symbol: "MSFT", kind: "vol63", horizonDays: 63, regime: "calm", conviction: 0.4, ts: 1 },
    { symbol: "AAPL", kind: "vol21", horizonDays: 21, regime: "elevated", conviction: 0.16, ts: 1 },
    { symbol: "AAPL", kind: "trend21", horizonDays: 21, regime: "uptrend", conviction: 0.9, ts: 1 },
    { symbol: "AAPL", kind: "newkind", horizonDays: 5, regime: "x", conviction: 0.5, ts: 1 },
    null,
  ]);
  assert.deepEqual(kinds, ["trend21", "vol21", "vol63", "newkind"]);
  assert.deepEqual(lines.map((l) => l.symbol), ["AAPL", "MSFT"]);
  assert.equal(lines[0].cells.trend21.regime, "uptrend");
  assert.equal(lines[1].cells.vol21, undefined, "a call that was not made is absent, not invented");
});

test("strength is a whole percent and never a fake zero", () => {
  assert.equal(strengthText(0.1599999), "16%");
  assert.equal(strengthText(1.2), "100%");
  assert.equal(strengthText(NaN), "");
  assert.equal(strengthText(undefined), "");
});

test("data age is counted from the trading date, and a long weekend is not stale", () => {
  const now = Date.parse("2026-11-16T12:00:00Z"); // a Monday
  assert.equal(daysSince("2026-11-13", now), 3); // Friday's data on Monday
  assert.ok(daysSince("2026-11-13", now) <= STALE_AFTER_DAYS);
  assert.ok(daysSince("2026-11-11", now) > STALE_AFTER_DAYS);
  assert.equal(daysSince(undefined, now), null);
  assert.equal(daysSince("11/13/2026", now), null);
});
