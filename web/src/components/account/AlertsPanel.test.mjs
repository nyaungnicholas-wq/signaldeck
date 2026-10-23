// Renders <AlertsBody> to HTML and checks what a member vs operator sees.
//   node --test src/components/account/AlertsPanel.test.mjs

import { test, after } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";
import ts from "typescript";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

const here = path.dirname(fileURLToPath(import.meta.url));
const components = path.resolve(here, "..");
const lib = path.resolve(here, "..", "..", "lib");
const web = path.resolve(here, "..", "..", "..");
const out = path.join(web, "node_modules", ".cache", `alerts-test-${process.pid}`);
mkdirSync(out, { recursive: true });
after(() => rmSync(out, { recursive: true, force: true }));

const url = (p) => pathToFileURL(p).href;

writeFileSync(
  path.join(out, "link.mjs"),
  'import { createElement } from "react";\nexport default function Link({ href, children, ...rest }) { return createElement("a", { href, ...rest }, children); }\n',
);

writeFileSync(
  path.join(out, "api.mjs"),
  'export const api = {};\nexport class ApiError extends Error { constructor(status, message) { super(message); this.status = status; } }\n',
);

function build(srcPath, outName, rewrites) {
  const src = readFileSync(srcPath, "utf8");
  let js = ts.transpileModule(src, {
    compilerOptions: { jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  for (const [from, to] of Object.entries(rewrites)) js = js.split(`"${from}"`).join(`"${to}"`);
  const file = path.join(out, `${outName}.mjs`);
  writeFileSync(file, js);
  return file;
}

const withheldRewrites = {
  "next/link": url(path.join(out, "link.mjs")),
  "@/lib/withheld": url(path.join(lib, "withheld.ts")),
};
const withheldFile = build(path.join(components, "Withheld.tsx"), "Withheld", withheldRewrites);

const alertsRewrites = {
  "@/lib/api": url(path.join(out, "api.mjs")),
  "@/components/Withheld": url(withheldFile),
  "@/lib/withheld": url(path.join(lib, "withheld.ts")),
};
const alertsFile = build(path.join(here, "AlertsPanel.tsx"), "AlertsPanel", alertsRewrites);

const { AlertsBody } = await import(url(alertsFile));

const html = (el) => renderToStaticMarkup(el);
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#x27;");

const NOTICE = "Withheld: the daily digest is paused for member accounts. Its reads are derived from Alpaca market data, which is licensed for personal use only, so it goes only to the operator until Alpaca gives written permission.";

const base = { emailDigest: false, emailVerified: true, telegramLinked: false, telegramAvailable: true, mailAvailable: true };

const body = (prefs) =>
  html(
    createElement(AlertsBody, {
      prefs,
      loadErr: null,
      busy: null,
      link: null,
      actionErr: null,
      onEmail() {},
      onConnect() {},
      onUnlink() {},
      onConfirmLink() {},
    }),
  );

test("a member is told the digest is withheld, not offered a switch", () => {
  const h = body({ ...base, digestWithheld: true, digestNotice: NOTICE });
  assert.ok(h.includes(esc(NOTICE)));
  assert.match(h, /data-testid="withheld"/);
  assert.match(h, /role="status"/);
  assert.doesNotMatch(h, /Send me the daily digest/);
  assert.doesNotMatch(h, /Connect Telegram/);
  assert.doesNotMatch(h, /One email each trading morning/);
  assert.doesNotMatch(h, /Turn the email digest off/);
  assert.doesNotMatch(h, /Disconnect Telegram/);
});

test("a member who had it on can still turn it off", () => {
  const h = body({ ...base, digestWithheld: true, digestNotice: NOTICE, emailDigest: true, telegramLinked: true });
  assert.match(h, /Turn the email digest off/);
  assert.match(h, /Disconnect Telegram/);
  assert.doesNotMatch(h, /Send me the daily digest/);
  assert.doesNotMatch(h, /The same daily read arrives there/);
});

test("the operator keeps the switches", () => {
  const h = body(base);
  assert.match(h, /Send me the daily digest/);
  assert.match(h, /Connect Telegram/);
  assert.doesNotMatch(h, /data-testid="withheld"/);
});