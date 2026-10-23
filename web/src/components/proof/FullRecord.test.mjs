// Renders <FullRecord> to HTML and checks what a reader gets.
//   node --test web/src/components/proof/FullRecord.test.mjs

import { test, after } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";
import ts from "typescript";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

const here = path.dirname(fileURLToPath(import.meta.url));
const web = path.resolve(here, "..", "..", "..");
const out = path.join(web, "node_modules", ".cache", `fullrecord-test-${process.pid}`);
mkdirSync(out, { recursive: true });
after(() => rmSync(out, { recursive: true, force: true }));

const url = (p) => pathToFileURL(p).href;

function build(name, rewrites) {
  const src = readFileSync(path.join(here, `${name}.tsx`), "utf8");
  let js = ts.transpileModule(src, {
    compilerOptions: { jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  for (const [from, to] of Object.entries(rewrites)) js = js.split(`"${from}"`).join(`"${to}"`);
  const file = path.join(out, `${name}.mjs`);
  writeFileSync(file, js);
  return file;
}

const rewrites = { "@/lib/specfields": url(path.join(here, "..", "..", "lib", "specfields.ts")) };
const fullRecordFile = build("FullRecord", rewrites);
const { default: FullRecord } = await import(url(fullRecordFile));

const html = (el) => renderToStaticMarkup(el);
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#x27;");

test("a withheld record never reads as frozen empty", () => {
  const record = {
    seq: 7,
    kind: "grading-protocol",
    spec: null,
    specHash: "a".repeat(64),
    entryHash: "b".repeat(64),
    prevHash: "c".repeat(64),
    specWithheld: true,
  };
  const h = html(createElement(FullRecord, { record }));
  assert.doesNotMatch(h, /frozen empty/);
  assert.doesNotMatch(h, /This record froze no spec fields/);
  assert.ok(h.includes(esc("Wording withheld under the data licence")));
  assert.ok(h.includes(esc("cannot be recomputed from this view")));
  assert.ok(h.includes(esc("shown only to the operator")));
  assert.doesNotMatch(h, /served as JSON at/);
  assert.ok(h.includes(record.specHash));
});

test("a withheld structural claim shows its wording without the empty marker", () => {
  const record = {
    seq: 8,
    kind: "trend21",
    spec: { kind: "trend21", question: "Still above the 200-day mean in 21 sessions?", horizonDays: 21, resolution: "" },
    specHash: "d".repeat(64),
    specWithheld: true,
  };
  const h = html(createElement(FullRecord, { record }));
  assert.ok(h.includes(esc("Still above the 200-day mean in 21 sessions?")));
  assert.doesNotMatch(h, /frozen empty/);
  assert.ok(h.includes(esc("cannot be recomputed from this view")));
});

test("the operator's record keeps the frozen-empty marker and the recompute path", () => {
  const record = {
    seq: 9,
    kind: "grading-protocol",
    spec: null,
    specHash: "e".repeat(64),
  };
  const h = html(createElement(FullRecord, { record }));
  assert.ok(h.includes(esc("null (frozen empty)")));
  assert.ok(h.includes(esc("served as JSON at")));
  assert.doesNotMatch(h, /Wording withheld/);
  assert.doesNotMatch(h, /cannot be recomputed/);
});