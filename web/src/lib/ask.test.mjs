// Ask the data (plan step 10): citation chips, the footer copy, and that the
// page, nav and member allowlist carry it.
//   node --test src/lib/ask.test.mjs
// node --test cannot load JSX, so the page checks read the sources.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { ASK_FOOTER, splitCitations, formatCell } from "./ask.ts";

const src = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("citation groups split out of the answer text", () => {
  assert.deepEqual(splitCitations("AAPL is up [q1:r1, q1:r2]. Calm [q2:r1]."), [
    { text: "AAPL is up " },
    { ids: ["q1:r1", "q1:r2"] },
    { text: ". Calm " },
    { ids: ["q2:r1"] },
    { text: "." },
  ]);
  // Brackets that are not citations stay text (the daemon refuses such answers anyway).
  assert.deepEqual(splitCitations("see [note] here"), [{ text: "see [note] here" }]);
  assert.deepEqual(splitCitations(""), []);
  // A bare id is a chip too, and an id repeated in one group renders once
  // (two chips with one key would be a React key collision).
  assert.deepEqual(splitCitations("calm q1:r1 and [q1:r2, q1:r2]"), [
    { text: "calm " },
    { ids: ["q1:r1"] },
    { text: " and " },
    { ids: ["q1:r2"] },
  ]);
  const page = src("../components/AskData.tsx");
  assert.match(page, /key=\{`\$\{i\}-\$\{j\}-\$\{id\}`\}/, "chip keys are unique per position");
});

test("cells: unix-second *_ts as UTC, null as a dash, the rest verbatim", () => {
  assert.equal(formatCell("computed_ts", 1790812800), "2026-10-01 00:00 UTC");
  assert.equal(formatCell("n", 1790812800), "1790812800");
  assert.equal(formatCell("equity", null), "—");
  assert.equal(formatCell("regime", "uptrend"), "uptrend");
});

test("the footer says where answers come from and that they are not advice", () => {
  assert.equal(
    ASK_FOOTER,
    "Answers come only from SignalDeck's own records and cite every row used. Not financial advice.",
  );
  const page = src("../components/AskData.tsx");
  assert.match(page, /\{ASK_FOOTER\}/);
  assert.match(page, /api\.askStatus\(\)/);
  assert.match(page, /api\.ask\(/);
  assert.match(page, /status\.available/, "the form waits for the daemon's verdict");
});

test("operators get ASK in the nav; members only while the daemon offers it", () => {
  const shell = src("../components/Shell.tsx");
  assert.match(shell, /href: "\/ask", label: "ASK"/);
  assert.match(shell, /askOpen \? MEMBER_NAV : MEMBER_NAV\.filter\(\(n\) => n\.href !== "\/ask"\)/);
  assert.match(src("./memberPages.ts"), /"\/ask"/);
  assert.match(src("../app/ask/page.tsx"), /<AskData \/>/);
});
