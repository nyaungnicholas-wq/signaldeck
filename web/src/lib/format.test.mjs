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
