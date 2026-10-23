// Renders <Withheld> and <ErrorState> to HTML and checks what a reader gets.
//   node --test src/components/Withheld.test.mjs
// node cannot load .tsx, so the two components are transpiled with the
// project's own TypeScript into node_modules/.cache (where `react` resolves),
// with next/link swapped for a plain <a> and the @/ aliases pointed at files.

import { test, after } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";
import ts from "typescript";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { WITHHELD_NOTICES } from "../lib/withheld.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const web = path.resolve(here, "..", "..");
const out = path.join(web, "node_modules", ".cache", `withheld-test-${process.pid}`);
mkdirSync(out, { recursive: true });
after(() => rmSync(out, { recursive: true, force: true }));

const url = (p) => pathToFileURL(p).href;
writeFileSync(
  path.join(out, "link.mjs"),
  'import { createElement } from "react";\nexport default function Link({ href, children, ...rest }) { return createElement("a", { href, ...rest }, children); }\n',
);

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

const common = { "next/link": url(path.join(out, "link.mjs")), "@/lib/withheld": url(path.join(here, "..", "lib", "withheld.ts")) };
const withheldFile = build("Withheld", common);
const errorStateFile = build("ErrorState", { ...common, "@/components/Withheld": url(withheldFile) });
const { default: Withheld } = await import(url(withheldFile));
const { default: ErrorState } = await import(url(errorStateFile));

const html = (el) => renderToStaticMarkup(el);
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#x27;");
const OUTAGE = /temporarily unavailable|unreachable|daemon running|on our side|>Retry</i;

test("Withheld renders the notice as a status, with the reason link", () => {
  const h = html(createElement(Withheld, { notice: WITHHELD_NOTICES.alpaca, source: "alpaca", title: "The live record" }));
  assert.match(h, /role="status"/);
  assert.match(h, /data-testid="withheld"/);
  assert.match(h, /data-source="alpaca"/);
  assert.ok(h.includes(esc(WITHHELD_NOTICES.alpaca)), "the notice, verbatim");
  assert.match(h, /href="\/terms#data-sources"/);
  assert.match(h, /keeps\s+running privately/);
  assert.match(h, /aria-label="The live record: withheld"/);
  assert.doesNotMatch(h, OUTAGE);
});

test("compact Withheld keeps the notice and the link, drops the long explanation", () => {
  const h = html(createElement(Withheld, { notice: WITHHELD_NOTICES.kadoa, source: "kadoa", compact: true }));
  assert.ok(h.includes(esc(WITHHELD_NOTICES.kadoa)));
  assert.match(h, /data-source="kadoa"/);
  assert.match(h, /href="\/terms#data-sources"/);
  assert.doesNotMatch(h, /keeps\s+running privately/);
});

test("ErrorState given a withheld message renders Withheld, not an error", () => {
  // The shape most pages produce: setErr(e.message), then <ErrorState message={err} retry={...} />.
  const h = html(createElement(ErrorState, { message: WITHHELD_NOTICES.alpaca, retry: () => {} }));
  assert.match(h, /data-testid="withheld"/);
  assert.match(h, /role="status"/);
  assert.doesNotMatch(h, /role="alert"/);
  assert.doesNotMatch(h, OUTAGE);
});

test("ErrorState still reports a real failure as one", () => {
  const h = html(createElement(ErrorState, { message: "API 500: /api/dashboard", retry: () => {} }));
  assert.match(h, /role="alert"/);
  assert.match(h, />Retry</);
  assert.doesNotMatch(h, /data-testid="withheld"/);
});
