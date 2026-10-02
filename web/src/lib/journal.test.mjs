// Member call journal (plan step 8): the withholding rule, the footer copy, and
// that the page is a member page that renders them.
//   node --test src/lib/journal.test.mjs
// node --test cannot load JSX, so the page checks read the sources.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { JOURNAL_FOOTER, HORIZONS, statsHeadline, beatsDrift, statusLabel } from "./journal.ts";

const src = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const stats = (o) => ({
  resolved: 0, callDays: 0, hits: 0, misses: 0, open: 0, void: 0, withdrawn: 0,
  hitRate: null, ciLow: null, ciHigh: null, baselineUpRate: null, withheld: true, minN: 30, ...o,
});

test("below 30 call days no rate is shown", () => {
  assert.equal(statsHeadline(stats({ resolved: 12, callDays: 12, hits: 9 })), "Not enough call days yet (12/30; 12 resolved calls)");
  // The floor counts call days, not calls: 58 calls on 29 days is still withheld.
  assert.equal(statsHeadline(stats({ resolved: 58, callDays: 29 })), "Not enough call days yet (29/30; 58 resolved calls)");
  // The copy names the unit, never claims the days are independent (5- and
  // 21-session calls on nearby days overlap).
  assert.doesNotMatch(statsHeadline(stats({ resolved: 58, callDays: 29 })), /independent/);
  // Withheld wins even if a rate were present.
  assert.equal(
    statsHeadline(stats({ resolved: 29, callDays: 29, hitRate: 0.9, ciLow: 0.7, ciHigh: 0.97 })),
    "Not enough call days yet (29/30; 29 resolved calls)",
  );
  assert.equal(beatsDrift(stats({ resolved: 29, callDays: 29 })), null);
});

test("from 30 the rate, interval and always-up baseline are shown", () => {
  const s = stats({ resolved: 40, callDays: 40, hits: 28, hitRate: 0.7, ciLow: 0.5457, ciHigh: 0.8193, baselineUpRate: 0.55, withheld: false });
  assert.equal(
    statsHeadline(s),
    'Hit rate 70% (95% interval 55%–82%, counting each of 40 call days once) over 40 resolved calls; "always up" over the same calls: 55%',
  );
  assert.equal(beatsDrift(s), null, "baseline inside the interval: no claim");
  assert.equal(beatsDrift({ ...s, baselineUpRate: 0.5 }), true);
  assert.equal(beatsDrift({ ...s, baselineUpRate: 0.9 }), false);
});

test("footer and horizons say what was specified", () => {
  assert.equal(
    JOURNAL_FOOTER,
    "Your calls, graded by the same rules SignalDeck grades itself with. Prices are not shown: the market data is licensed. Not financial advice.",
  );
  assert.deepEqual(HORIZONS.map((h) => h.value), [1, 5, 21]);
  assert.equal(statusLabel({ status: "resolved", outcome: "miss" }), "Miss");
  assert.equal(statusLabel({ status: "void" }), "Void");
});

test("/journal is a member page in the member nav, and renders the footer and confirm step", () => {
  assert.match(src("./memberPages.ts"), /"\/journal"/);
  assert.match(src("../components/Shell.tsx"), /href: "\/journal", label: "MY CALLS"/);
  const page = src("../components/MemberJournal.tsx");
  for (const needle of ["JOURNAL_FOOTER", "JOURNAL_IMMUTABLE", "statsHeadline", "api.journalSymbols", "api.journalCall", "api.journalWithdraw"]) {
    assert.ok(page.includes(needle), `MemberJournal.tsx does not use ${needle}`);
  }
  // The picker offers exactly what POST /api/journal accepts: the daemon's
  // journal symbols (ETFs included), not the SEC directory, which has no ETFs.
  assert.doesNotMatch(page, /companiesList/);
  // The licence line: the page never asks for or prints a price.
  assert.doesNotMatch(page, /fmtPrice|lastClose|entryPrice|exitPrice/);
  assert.match(src("../app/journal/page.tsx"), /MemberJournal/);
});

test("member symbol page mounts the FINRA short panels only when the daemon opens them", () => {
  const view = src("../components/symbol/MemberSymbolView.tsx");
  assert.match(view, /memberFinra === true/);
  for (const panel of ["ShortInterestPanel", "ShortVolumePanel"]) {
    assert.ok(view.includes(`{finra && <${panel} `), `${panel} is mounted without the memberFinra gate`);
  }
});
