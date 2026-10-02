// Ledger copy must not claim more than the daemon proves.
//   node --test src/lib/proofcopy.test.mjs
//
// The daemon's own tamperEvidence.claim: deleting every row and re-appending a
// fabricated chain verifies intact. So "intact" cannot be glossed as "nothing
// was silently edited or deleted", and /proof's headline count (lv.count) is the
// chain length — on the incremental path only the suffix was re-hashed.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8").replace(/\s+/g, " ");

for (const p of ["../components/home/ProofStrip.tsx", "../app/lab/track-record/page.tsx"]) {
  test(`${p} scopes "intact" the way the daemon does`, () => {
    const s = read(p);
    assert.doesNotMatch(s, /silently edited or deleted/);
    assert.match(s, /re-appending a fabricated chain also verifies intact/);
  });
}

test("/proof does not label the chain length as entries verified", () => {
  const s = read("../app/proof/page.tsx");
  assert.doesNotMatch(s, /entries verified/);
  assert.match(s, /entries in chain/);
});

test("/proof reports the local anchor separately from proven anteriority", () => {
  const s = read("../app/proof/page.tsx");
  // provenAnteriorThroughSeq is null while any anchor fails; reading it here
  // printed "no anchor currently reproduces" when a newer one did.
  assert.match(s, /localAnchorReproducesThroughSeq/);
  assert.match(s, /older signed anchor no longer reproduces/);
});
