// The 451 licence withholding, end to end through the client.
//   node --test src/lib/withheld.test.mjs
// A .mjs file so `next build` / tsc never sees it, matching the other tests.

import { test } from "node:test";
import assert from "node:assert/strict";
import { parseWithheld, withheldFromMessage, WITHHELD_NOTICES, WITHHELD_HREF } from "./withheld.ts";
import { classifyPublicationFetch } from "./publicationfetch.ts";
import { ApiError, api, congress, isAuthError, isLicenceRefusal, ledgerVerify, trackRecord, withheldOf } from "./api.ts";

// The contract body, verbatim, as the daemon half sends it.
const ALPACA = {
  error: "withheld",
  reason: "licence",
  source: "alpaca",
  notice: WITHHELD_NOTICES.alpaca,
};
const KADOA = { error: "withheld", reason: "licence", source: "kadoa", notice: WITHHELD_NOTICES.kadoa };

// Outage words a 451 must never be rendered with.
const OUTAGE = /temporarily unavailable|unreachable|daemon running|API 451|\{/i;

/** Run fn with global fetch answering every request with (status, body). */
async function withFetch(status, body, fn) {
  const real = globalThis.fetch;
  const seen = [];
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), method: init?.method ?? "GET" });
    return new Response(typeof body === "string" ? body : JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    });
  };
  try {
    return await fn(seen);
  } finally {
    globalThis.fetch = real;
  }
}

test("the contract notices are the owner's wording and start with Withheld:", () => {
  assert.match(WITHHELD_NOTICES.alpaca, /^Withheld: SignalDeck's prices, forecasts and statistics are derived from Alpaca market data/);
  assert.match(WITHHELD_NOTICES.alpaca, /shown only to the operator until Alpaca gives written permission\.$/);
  assert.match(WITHHELD_NOTICES.kadoa, /^Withheld: the congressional trading rows were compiled by Kadoa/);
  assert.match(WITHHELD_NOTICES.kadoa, /Collection has stopped and the stored rows are shown only to the operator\.$/);
  assert.equal(WITHHELD_HREF, "/terms#data-sources");
});

test("parseWithheld reads the contract body", () => {
  assert.deepEqual(parseWithheld(451, ALPACA), { source: "alpaca", notice: WITHHELD_NOTICES.alpaca });
  assert.deepEqual(parseWithheld(451, KADOA), { source: "kadoa", notice: WITHHELD_NOTICES.kadoa });
});

test("only 451 is a withholding", () => {
  for (const s of [200, 401, 403, 404, 429, 500, 502, 503]) assert.equal(parseWithheld(s, ALPACA), null, `status ${s}`);
});

test("a 451 without the contract body still withholds, in reader words", () => {
  // The pre-contract /api/bars body: an operator sentence naming an env var.
  const old = parseWithheld(451, { error: "raw bar export is disabled ... Set SIGNALDECK_ALLOW_RAW_EXPORT=true ONLY if" });
  assert.equal(old.source, "unknown");
  assert.match(old.notice, /^Withheld: /);
  assert.doesNotMatch(old.notice, /SIGNALDECK_ALLOW_RAW_EXPORT/);
  // No body at all, and a known source with no notice.
  assert.match(parseWithheld(451, null).notice, /^Withheld: /);
  assert.equal(parseWithheld(451, { error: "withheld", source: "kadoa" }).notice, WITHHELD_NOTICES.kadoa);
  // A daemon notice without the prefix is prefixed, so the message test finds it.
  assert.equal(parseWithheld(451, { error: "withheld", source: "x", notice: "Not shared." }).notice, "Withheld: Not shared.");
});

test("withheldFromMessage recovers the result from a message string", () => {
  assert.deepEqual(withheldFromMessage(WITHHELD_NOTICES.kadoa), { source: "kadoa", notice: WITHHELD_NOTICES.kadoa });
  assert.equal(withheldFromMessage(WITHHELD_NOTICES.alpaca)?.source, "alpaca");
  for (const m of [null, undefined, "", "authentication required", "API 503: /api/x", "daemon unreachable"]) {
    assert.equal(withheldFromMessage(m), null, String(m));
  }
});

test("get(): a 451 becomes a typed withholding, not an error message", async () => {
  await withFetch(451, ALPACA, async () => {
    const e = await trackRecord("1d").then(() => null, (x) => x);
    assert.ok(e instanceof ApiError, "rejects with ApiError");
    assert.equal(e.status, 451);
    assert.equal(e.code, "withheld");
    assert.deepEqual(e.withheld, { source: "alpaca", notice: WITHHELD_NOTICES.alpaca });
    assert.deepEqual(withheldOf(e), e.withheld);
    // The message IS the notice, so pages that keep only e.message still render it.
    assert.equal(e.message, WITHHELD_NOTICES.alpaca);
    assert.doesNotMatch(e.message, OUTAGE);
    assert.equal(isLicenceRefusal(e), true);
    assert.equal(isAuthError(e), false);
  });
});

test("get(): the kadoa source survives the trip", async () => {
  await withFetch(451, KADOA, async () => {
    const e = await congress("AAPL").then(() => null, (x) => x);
    assert.equal(withheldOf(e)?.source, "kadoa");
    assert.equal(withheldFromMessage(e.message)?.source, "kadoa");
  });
});

test("post(): a 451 on a write is the same typed withholding", async () => {
  await withFetch(451, ALPACA, async (seen) => {
    const e = await api.journalCall({ symbol: "AAPL", market: "stocks", call: "up", horizon: 5, note: "" }).then(() => null, (x) => x);
    assert.equal(seen[0]?.method, "POST");
    assert.equal(withheldOf(e)?.notice, WITHHELD_NOTICES.alpaca);
    assert.equal(e.message, WITHHELD_NOTICES.alpaca);
  });
});

test("401, 403 and 503 are NOT withholdings", async () => {
  for (const [status, body] of [
    [401, { error: "authentication required" }],
    [403, { error: "forbidden" }],
    [503, { error: "verification exceeded 30s" }],
  ]) {
    await withFetch(status, body, async () => {
      const e = await ledgerVerify().then(() => null, (x) => x);
      assert.ok(e instanceof ApiError, `status ${status}`);
      assert.equal(e.status, status);
      assert.equal(withheldOf(e), null, `status ${status} must not be withheld`);
      assert.equal(withheldFromMessage(e.message), null, `status ${status} message`);
    });
  }
});

test("the server-side publication fetch classifies 451 as withheld, not unavailable", () => {
  const got = classifyPublicationFetch({ ok: false, status: 451 }, ALPACA);
  assert.deepEqual(got, { kind: "withheld", source: "alpaca", notice: WITHHELD_NOTICES.alpaca });
  // And it still tells the other cases apart.
  assert.equal(classifyPublicationFetch({ ok: false, status: 401 }, {}).kind, "private");
  assert.equal(classifyPublicationFetch({ ok: false, status: 502 }, { error: "daemon unreachable" }).kind, "unavailable");
});
