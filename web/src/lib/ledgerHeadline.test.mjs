// Regression tests for the /proof ledger headline (src/lib/ledgerHeadline.ts).
//   node --test src/lib/ledgerHeadline.test.mjs
// A .mjs file so `next build` / tsc never sees it, matching refusal.test.mjs.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { ledgerHeadline } from "./ledgerHeadline.ts";

test("an intact chain with no failing anchor reads green", () => {
  assert.deepEqual(ledgerHeadline({ intact: true }), { tone: "ok", text: "Chain intact" });
  assert.deepEqual(ledgerHeadline({ intact: true, tamperEvidence: {} }), { tone: "ok", text: "Chain intact" });
  assert.deepEqual(ledgerHeadline({ intact: true, tamperEvidence: { failingAnchors: 0 } }), { tone: "ok", text: "Chain intact" });
});

test("a consistent chain whose signed anchor stopped reproducing is NOT green", () => {
  assert.deepEqual(ledgerHeadline({ intact: true, tamperEvidence: { failingAnchors: 1 } }), { tone: "bad", text: "Chain consistent, but 1 signed anchor no longer reproduces" });
  assert.deepEqual(ledgerHeadline({ intact: true, tamperEvidence: { failingAnchors: 2 } }), { tone: "bad", text: "Chain consistent, but 2 signed anchors no longer reproduce" });
});

test("a broken chain names the seq whatever the anchors say", () => {
  assert.deepEqual(ledgerHeadline({ intact: false, brokenAtSeq: 7, tamperEvidence: { failingAnchors: 2 } }), { tone: "bad", text: "BROKEN at #7" });
  assert.deepEqual(ledgerHeadline({ intact: false, brokenAtSeq: 12 }), { tone: "bad", text: "BROKEN at #12" });
});

test("the round-4 tamper payload's headline is red", () => {
  // Shape of the regenerated-chain payload a verifier measured: intact, one failing anchor.
  assert.deepEqual(ledgerHeadline({ intact: true, count: 14, tamperEvidence: { localAnchorsReproduce: false, failingAnchors: 1, firstFailingSeq: 12, provenAnteriorThroughSeq: 14, provenAnteriorThroughCount: 14 } }), { tone: "bad", text: "Chain consistent, but 1 signed anchor no longer reproduces" });
});

test("a broken chain with no seq in the payload says BROKEN, never #undefined", () => {
  assert.deepEqual(ledgerHeadline({ intact: false }), { tone: "bad", text: "BROKEN" });
});

// PROOFSTRIP: the /today chip and the /lab/track-record chip read the
// /api/track-record ledger block, and used to colour it by `intact` alone, so a
// regenerated chain whose signed anchor fails read green there while /proof
// read red. Both must go through ledgerHeadline.
test("the track-record ledger block's tamper evidence turns the chip red", () => {
  const block = { intact: true, count: 14, head: "ab", tamperEvidence: { failingAnchors: 1 } };
  assert.equal(ledgerHeadline(block).tone, "bad");
});

for (const p of ["../components/home/ProofStrip.tsx", "../app/lab/track-record/page.tsx"]) {
  test(`${p} reads the ledger chip through ledgerHeadline`, () => {
    const s = readFileSync(new URL(p, import.meta.url), "utf8").replace(/\s+/g, " ");
    assert.match(s, /ledgerHeadline\((tr|current)\.ledger\)/);
    assert.doesNotMatch(s, /ledger\.intact \?/);
  });
}
