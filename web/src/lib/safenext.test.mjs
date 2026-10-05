// Open-redirect guard for the post-sign-in resume path.
//   node --test src/lib/safenext.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { safeNext } from "./safenext.ts";

test("same-origin paths resume", () => {
  assert.equal(safeNext("/watchlist"), "/watchlist");
  assert.equal(safeNext("/s/stocks/AAPL?tab=1"), "/s/stocks/AAPL?tab=1");
});

test("anything that leaves the origin is refused", () => {
  for (const bad of ["//evil.example", "/\\evil.example", "https://evil.example", "javascript:alert(1)", "evil", "", null, undefined, "/\tx", "/" + "a".repeat(600)]) {
    assert.equal(safeNext(bad), null, String(bad));
  }
});

test("the sign-in page never resumes to itself", () => {
  assert.equal(safeNext("/login"), null);
  assert.equal(safeNext("/login?next=/x"), null);
});
