// Crypto pause label and predictor match (src/lib/cryptoPause.ts).
//   node --test src/lib/cryptoPause.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { CRYPTO_PAUSED_DATE, CRYPTO_PAUSED_LABEL, isCryptoPredictor } from "./cryptoPause.ts";

test("label carries the exact pause date", () => {
  assert.equal(CRYPTO_PAUSED_LABEL, "crypto paused since 22 October 2026");
  // The agents page builds "retired <date>" from the date alone; it must be the same date.
  assert.equal(CRYPTO_PAUSED_DATE, "22 October 2026");
});

test("crypto predictors and their #persist benchmarks match", () => {
  for (const p of ["trend21-crypto", "liquidity21-crypto", "trend21-crypto#persist", "liquidity21-crypto#persist"]) {
    assert.equal(isCryptoPredictor(p), true, p);
  }
});

test("stock predictors and near-misses do not match", () => {
  for (const p of ["trend21", "trend21#persist", "liquidity21-stocks", "cryptoish", "crypto", "x-cryptocurrency", "", null, undefined]) {
    assert.equal(isCryptoPredictor(p), false, String(p));
  }
});
