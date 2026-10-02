// Publisher guardrails (plan step 6): pins the hypothetical-performance copy and
// that the surfaces showing paper/backtest numbers actually render it.
//   node --test src/lib/hypothetical.test.mjs
// node --test strips types but cannot load JSX, so the render check reads the
// component sources rather than rendering them.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { HYPOTHETICAL_NOTE, HYPOTHETICAL_SHORT, WHAT_SIGNALDECK_IS } from "./hypothetical.ts";

const src = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("hypothetical note says it in the required words", () => {
  assert.equal(
    HYPOTHETICAL_NOTE,
    "Hypothetical performance. Simulated paper results, not trades in a real account; costs are estimated. Hypothetical results have inherent limitations and do not predict future results.",
  );
  assert.match(HYPOTHETICAL_SHORT, /^Backtest accuracy is hypothetical/);
  assert.match(HYPOTHETICAL_SHORT, /does not predict future results/);
  assert.equal(
    WHAT_SIGNALDECK_IS,
    "SignalDeck publishes the same statistical forecasts to every member. It does not know your portfolio and does not tell you what to buy or sell.",
  );
});

test("HypotheticalNote renders the shared copy, not its own", () => {
  const c = src("../components/HypotheticalNote.tsx");
  assert.match(c, /from "@\/lib\/hypothetical"/);
  assert.match(c, /short \? HYPOTHETICAL_SHORT : HYPOTHETICAL_NOTE/);
});

// Each surface that shows a paper P&L or a backtest-accuracy number to a member or visitor.
for (const [file, needle] of [
  ["../components/home/ProofStrip.tsx", "<HypotheticalNote "],
  ["../components/home/TodaysRead.tsx", "<HypotheticalNote short"],
  ["../components/home/VolRegimeLead.tsx", "<HypotheticalNote short"],
  ["../components/symbol/ValidatedSignalsPanel.tsx", "<HypotheticalNote short"],
  ["../components/symbol/MemberSymbolView.tsx", "<HypotheticalNote short"],
  ["../components/MemberWatchlist.tsx", "<HypotheticalNote short"],
  ["../app/accuracy/page.tsx", "<HypotheticalNote short"],
  ["../app/market/regimes/page.tsx", "<HypotheticalNote short"],
]) {
  test(`${file} renders the hypothetical note`, () => {
    const c = src(file);
    assert.ok(c.includes('import HypotheticalNote from "@/components/HypotheticalNote"'), "import missing");
    assert.ok(c.includes(needle), `${needle} missing`);
  });
}

test("/today renders ProofStrip (which carries the note) and the what-it-is line", () => {
  const c = src("../app/today/page.tsx");
  assert.ok(c.includes("<ProofStrip />"));
  assert.ok(c.includes("{WHAT_SIGNALDECK_IS}"));
  // a member is never pointed at the operator's trade page
  assert.ok(c.includes("<VolRegimeLead memberView />"));
});

test("members never see the symbol model called an agent", () => {
  const c = src("../components/symbol/SymbolAgentPanel.tsx");
  assert.ok(!c.includes("THIS SYMBOL&rsquo;S AGENT"));
  assert.ok(c.includes("{SYMBOL_MODEL_LABEL.toUpperCase()}"));
});

test("/account mounts the alerts panel with the digest copy and Telegram code instruction", () => {
  assert.ok(src("../app/account/page.tsx").includes("<AlertsPanel />"));
  const c = src("../components/account/AlertsPanel.tsx");
  assert.ok(c.includes("No prices. At most one a day. Unsubscribe from any email."));
  assert.ok(c.includes("within 15 minutes"));
  assert.ok(c.includes("prefs.telegramAvailable &&"), "Telegram must be gated on telegramAvailable");
});
