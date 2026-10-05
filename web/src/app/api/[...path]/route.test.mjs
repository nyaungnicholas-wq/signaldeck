// Guards the API proxy's failure log: it must never print a request's query
// string (an emailed unsubscribe token or a member's search rides in it), nor
// the raw error, which can quote the full upstream URL.
// Run with: npm test (a bracketed path is a glob to node --test, so name it via the npm glob).
// This is .mjs so next build / tsc never see it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "route.ts"), "utf8");

test("every console call in the proxy logs no query and no raw upstream URL", () => {
  const calls = src.match(/console\.(error|warn|log|info)\([^;]*\);/g) ?? [];
  assert.ok(calls.length >= 1, "no console call found: the scan is not looking at the proxy");
  for (const c of calls) {
    assert.ok(!/\$\{upstream\}/.test(c), `logs the full upstream URL: ${c}`);
    assert.ok(!/search|nextUrl|req\.url|\.href/.test(c), `logs the query or full URL: ${c}`);
    assert.ok(!/,\s*err\s*\)/.test(c), `logs the raw error object: ${c}`);
  }
  assert.match(src, /\[api-proxy\] \$\{req\.method\} \$\{upstreamPath\} failed/);
});

test("upstreamPath carries no query", () => {
  const m = src.match(/const upstreamPath = (`[^`]*`);/);
  assert.ok(m, "upstreamPath not found");
  assert.ok(!/search|\?/.test(m[1]), `upstreamPath includes a query: ${m[1]}`);
});

// The daemon's attachment routes (account export, the CSV exports) name their
// file in Content-Disposition; a proxy that drops it serves them as a page
// instead of a download (2026-10-05 audit).
test("the proxy passes Content-Disposition through", () => {
  const m = src.match(/const RESPONSE_HEADERS = \[([^\]]*)\]/);
  assert.ok(m, "RESPONSE_HEADERS not found");
  assert.match(m[1], /"content-disposition"/);
});
