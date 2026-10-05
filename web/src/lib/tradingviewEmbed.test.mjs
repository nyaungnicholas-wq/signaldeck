import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { mountTradingViewWidget, TV_EMBED_SRC } from "./tradingviewEmbed.ts";

// TradingView's embed script, when it runs, finds its widget with
// scriptElement.parentNode.querySelector(".tradingview-widget-container__widget").
// The old component detached the script itself in its effect cleanup
// (container.replaceChildren()), which React's dev double-mount and fast
// navigation trigger, so parentNode was null and every symbol page logged
// "Cannot read properties of null (reading 'querySelector')". These tests run
// the mount against a small fake DOM and simulate TradingView's lookup.

const tick = () => new Promise((r) => setTimeout(r, 0)); // the script is appended on the next task

function el(tag) {
  return {
    tagName: tag.toUpperCase(),
    className: "",
    style: {},
    children: [],
    parentNode: null,
    appendChild(c) {
      if (c.parentNode) c.remove();
      c.parentNode = this;
      this.children.push(c);
      return c;
    },
    remove() {
      const p = this.parentNode;
      if (p) {
        p.children = p.children.filter((x) => x !== this);
        this.parentNode = null;
      }
    },
    replaceChildren() {
      for (const c of this.children) c.parentNode = null;
      this.children = [];
    },
    querySelector(sel) {
      if (!sel.startsWith(".")) return null;
      const className = sel.slice(1);
      const stack = [this];
      while (stack.length) {
        const node = stack.pop();
        if (
          node.className
          .split(" ")
          .filter((c) => c.length)
          .includes(className)
        ) {
          return node;
        }
        for (let i = node.children.length - 1; i >= 0; i--) {
          stack.push(node.children[i]);
        }
      }
      return null;
    },
  };
}

const fakeDoc = { createElement: (tag) => el(tag) };

function tradingViewRuns(script) {
  if (script.parentNode === null) throw new TypeError("Cannot read properties of null (reading 'querySelector')");
  return script.parentNode.querySelector(".tradingview-widget-container__widget");
}

function scriptIn(node) {
  const stack = [node];
  while (stack.length) {
    const n = stack.pop();
    if (n.tagName === "SCRIPT") return n;
    for (let i = n.children.length - 1; i >= 0; i--) {
      stack.push(n.children[i]);
    }
  }
  return null;
}

test("the script and its widget share a parent", async () => {
  const container = el("div");
  const cleanup = mountTradingViewWidget(container, { symbol: "AAPL" }, fakeDoc);
  await tick();
  const script = scriptIn(container);
  assert.strictEqual(script.src, TV_EMBED_SRC);
  assert.strictEqual(script.async, true);
  assert.deepEqual(JSON.parse(script.textContent), { symbol: "AAPL" });
  const widget = tradingViewRuns(script);
  assert.strictEqual(widget.className, "tradingview-widget-container__widget");
  cleanup();
});

test("cleanup before the script runs leaves it a parent (no null querySelector)", async () => {
  const container = el("div");
  const cleanup = mountTradingViewWidget(container, { symbol: "AAPL" }, fakeDoc);
  await tick();
  const script = scriptIn(container);
  cleanup();
  assert.strictEqual(container.children.length, 0);
  assert.doesNotThrow(() => tradingViewRuns(script));
  const widget = tradingViewRuns(script);
  assert.strictEqual(widget.className, "tradingview-widget-container__widget");
});

test("React dev double-mount (mount, cleanup, mount) loads one script, never a discarded one", async () => {
  const container = el("div");
  const cleanup1 = mountTradingViewWidget(container, { symbol: "AAPL" }, fakeDoc);
  const discarded = container.children[0];
  cleanup1(); // same tick, as React's StrictMode does
  const cleanup2 = mountTradingViewWidget(container, { symbol: "MSFT" }, fakeDoc);
  await tick();
  assert.strictEqual(container.children.length, 1);
  assert.strictEqual(scriptIn(discarded), null, "the discarded mount loaded its script");
  const script2 = scriptIn(container);
  assert.deepEqual(JSON.parse(script2.textContent), { symbol: "MSFT" });
  cleanup2();
  assert.strictEqual(container.children.length, 0);
});

test("a symbol change after the script loaded leaves the old script its parent", async () => {
  const container = el("div");
  const cleanup1 = mountTradingViewWidget(container, { symbol: "AAPL" }, fakeDoc);
  await tick();
  const script1 = scriptIn(container);
  cleanup1();
  const cleanup2 = mountTradingViewWidget(container, { symbol: "MSFT" }, fakeDoc);
  await tick();
  assert.doesNotThrow(() => tradingViewRuns(script1));
  assert.strictEqual(container.children.length, 1);
  cleanup2();
});

test("the old pattern is the bug this guards against", () => {
  const container = el("div");
  const w = el("div");
  w.className = "tradingview-widget-container__widget";
  const s = el("script");
  container.appendChild(w);
  container.appendChild(s);
  container.replaceChildren();
  assert.throws(() => tradingViewRuns(s), /reading 'querySelector'/);
});

test("the component mounts through the helper and never detaches the script itself", () => {
  const src = readFileSync(new URL("../components/TradingViewChart.tsx", import.meta.url), "utf8");
  assert.match(src, /mountTradingViewWidget\(/);
  assert.doesNotMatch(src, /replaceChildren|createElement\(["']script["']\)/);
});