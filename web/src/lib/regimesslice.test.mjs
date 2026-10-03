// REGIMES-SIZE (2026-10-02): a member's /api/regimes is sliced to the top 25
// rows of each kind unless the call names symbols or a page, so every member
// surface must ask for the slice it renders. A bare call still answers 200 and
// silently drops rows (watchlist chips, the symbol view's stack).
//   node --test src/lib/regimesslice.test.mjs
// node --test cannot load JSX, so this reads the component sources.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("member surfaces ask /api/regimes for the slice they render", () => {
  for (const [file, want] of [
    ["../components/MemberWatchlist.tsx", /structuralRegimes\(\{ symbols: syms \}\)/],
    ["../components/symbol/ValidatedSignalsPanel.tsx", /structuralRegimes\(\{ symbols: \[symbol\] \}\)/],
    ["../components/home/TodaysRead.tsx", /structuralRegimes\(symKey \? \{ symbols: symKey\.split\(","\) \} : undefined\)/],
    ["../app/market/regimes/page.tsx", /structuralRegimesWithEarnings\(\{ kind, offset: all\.length, limit: 100 \}\)/],
  ]) {
    const c = src(file);
    assert.match(c, want, file);
    assert.doesNotMatch(c, /structuralRegimes\(\)/, `${file}: a bare structuralRegimes() gets only the top of each kind`);
  }
});

test("/market/regimes counts from the daemon's totals, not the rows it was sent", () => {
  const page = src("../app/market/regimes/page.tsx");
  assert.match(page, /stats\?\.\[kind\]\?\.count \?\? rows\.length/);
  assert.doesNotMatch(page, /\$\{(trend|liq|vol)Forecasts\.length\} active forecasts/);
  assert.match(page, /total=\{countOf\(k, data\.forecasts\[k\] \?\? \[\]\)\}/);
});
