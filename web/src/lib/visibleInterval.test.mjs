// Guards visibleInterval: network polls send nothing from a hidden tab and fire
// once when the tab is visible again. node --test src/lib/visibleInterval.test.mjs
// A .mjs file so next build / tsc never see it.

import { test } from "node:test";
import assert from "node:assert/strict";
import { visibleInterval } from "./visibleInterval.ts";

class FakeDoc extends EventTarget {
  hidden = false;
}
const doc = new FakeDoc();
globalThis.document = doc;
const flip = (hidden) => {
  doc.hidden = hidden;
  doc.dispatchEvent(new Event("visibilitychange"));
};

function start(t) {
  t.mock.timers.enable({ apis: ["setInterval"] });
  doc.hidden = false;
  const box = { n: 0 };
  box.stop = visibleInterval(() => box.n++, 30_000);
  return box;
}

test("a visible tab polls on the interval", (t) => {
  const p = start(t);
  t.mock.timers.tick(30_000);
  assert.equal(p.n, 1);
  t.mock.timers.tick(30_000);
  assert.equal(p.n, 2);
  t.mock.timers.tick(29_999);
  assert.equal(p.n, 2);
  p.stop();
});

test("a hidden tab sends nothing", (t) => {
  const p = start(t);
  doc.hidden = true;
  t.mock.timers.tick(90_000);
  assert.equal(p.n, 0);
  p.stop();
});

test("coming back into view polls once, going hidden does not", (t) => {
  const p = start(t);
  flip(true);
  assert.equal(p.n, 0);
  t.mock.timers.tick(60_000);
  assert.equal(p.n, 0);
  flip(false);
  assert.equal(p.n, 1);
  t.mock.timers.tick(30_000);
  assert.equal(p.n, 2);
  p.stop();
});

test("cleanup stops the interval and the listener", (t) => {
  const p = start(t);
  p.stop();
  t.mock.timers.tick(90_000);
  assert.equal(p.n, 0);
  flip(true);
  flip(false);
  assert.equal(p.n, 0);
});
