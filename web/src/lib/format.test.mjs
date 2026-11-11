// Guards signColor: a value the daemon withheld (absent) is neutral, never red.
// node --test src/lib/format.test.mjs. A .mjs file so next build / tsc never see it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { signColor } from "./format.ts";

test("signColor: up, down, and absent", () => {
  assert.equal(signColor(0.012), "var(--bid)");
  assert.equal(signColor(0), "var(--bid)");
  assert.equal(signColor(-0.012), "var(--ask)");
  for (const v of [undefined, null, NaN]) assert.equal(signColor(v), "var(--dim)");
});

// The daemon sends fractions. A 2% loss on $50,000 used to show as $10 and a
// 10% gain as "+0.10%".
import { fmtFracPct, fracUSD } from "./format.ts";

test("fractions from the daemon render as percent and dollars", () => {
  assert.equal(fmtFracPct(0.10), "+10.00%");
  assert.equal(fmtFracPct(-0.02), "-2.00%");
  assert.equal(fmtFracPct(0.02, false), "2.00%");
  assert.equal(fmtFracPct(NaN), "—");
  assert.equal(fracUSD(-0.02, 50000), -1000);
});
