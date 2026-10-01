// Guards the daemon's 503 {"error":"warming"} handling: untilWarm's termination, and get() waiting it out.
// Run with: node --test src/lib/warming.test.mjs
// This is .mjs so next build / tsc never see it.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import { untilWarm, isWarming } from "./warming.ts";
import ts from "typescript";
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const warmErr = (n, retryAfterMs) => Object.assign(new Error("warming " + n), { status: 503, code: "warming", retryAfterMs });

test("A1 cap reached: the last warming error is rethrown", async () => {
  const realNow = Date.now;
  let clock = 1_000_000;
  Date.now = () => clock;
  try {
    const sleeps = [];
    let calls = 0;
    let last;
    const fn = async () => {
      calls++;
      if (calls > 1000) throw new Error("runaway");
      last = warmErr(calls, 30_000);
      throw last;
    };
    const sleep = async (ms) => {
      sleeps.push(ms);
      clock += ms;
    };
    await assert.rejects(
      untilWarm(fn, { capMs: 100_000, sleep }),
      (e) => e === last
    );
    assert.strictEqual(calls, 5);
    assert.deepStrictEqual(sleeps, [30000, 30000, 30000, 10000]);
  } finally {
    Date.now = realNow;
  }
});

test("A2 a non-warming error is rethrown at once", async () => {
  let sleepCalls = 0;
  let onWarmingCalls = 0;
  const boom = Object.assign(new Error("boom"), { status: 500 });
  await assert.rejects(
    untilWarm(
      async () => {
        throw boom;
      },
      {
        sleep: () => {
          sleepCalls++;
          return Promise.resolve();
        },
        onWarming: () => {
          onWarmingCalls++;
        },
      }
    ),
    (e) => e === boom
  );
  assert.strictEqual(sleepCalls, 0);
  assert.strictEqual(onWarmingCalls, 0);
  sleepCalls = 0;
  onWarmingCalls = 0;
  const busy = Object.assign(new Error("busy"), { status: 503, code: "busy" });
  await assert.rejects(
    untilWarm(
      async () => {
        throw busy;
      },
      {
        sleep: () => {
          sleepCalls++;
          return Promise.resolve();
        },
        onWarming: () => {
          onWarmingCalls++;
        },
      }
    ),
    (e) => e === busy
  );
  assert.strictEqual(sleepCalls, 0);
  assert.strictEqual(onWarmingCalls, 0);
});

test("A3 alive() false during the wait: no further fn call", async () => {
  let dead = false;
  let calls = 0;
  let onWarmingCalls = 0;
  let firstErr;
  await assert.rejects(
    untilWarm(
      async () => {
        calls++;
        const e = warmErr(calls, 5000);
        firstErr ??= e;
        throw e;
      },
      {
        alive: () => !dead,
        sleep: async () => {
          dead = true;
        },
        onWarming: () => {
          onWarmingCalls++;
        },
      }
    ),
    (e) => e === firstErr
  );
  assert.strictEqual(calls, 1);
  assert.strictEqual(onWarmingCalls, 1);
});

test("A4 Retry-After is honoured", async () => {
  let sleeps = [];
  let onWarmingCalls = 0;
  let callCount = 0;
  const fn = async () => {
    callCount++;
    if (callCount === 1) throw warmErr(1, 7000);
    return "ok";
  };
  const sleep = (ms) => {
    sleeps.push(ms);
    return Promise.resolve();
  };
  const result = await untilWarm(fn, { sleep, onWarming: () => onWarmingCalls++ });
  assert.strictEqual(result, "ok");
  assert.deepStrictEqual(sleeps, [7000]);
  assert.strictEqual(onWarmingCalls, 1);
  sleeps = [];
  onWarmingCalls = 0;
  callCount = 0;
  const fn2 = async () => {
    callCount++;
    if (callCount === 1) throw warmErr(1);
    return "ok";
  };
  await untilWarm(fn2, { sleep, onWarming: () => onWarmingCalls++ });
  assert.deepStrictEqual(sleeps, [30000]);
  assert.strictEqual(onWarmingCalls, 1);
});

test("A5 isWarming", () => {
  assert.strictEqual(isWarming(warmErr(1)), true);
  assert.strictEqual(isWarming(Object.assign(new Error("x"), { status: 503, code: "busy" })), false);
  assert.strictEqual(isWarming(Object.assign(new Error("x"), { status: 500, code: "warming" })), false);
  assert.strictEqual(isWarming({ status: 503, code: "warming" }), false);
  assert.strictEqual(isWarming(null), false);
  assert.strictEqual(isWarming(undefined), false);
});

const dir = mkdtempSync(join(tmpdir(), "sd-warming-"));
after(() => rmSync(dir, { recursive: true, force: true }));
const js = (name) => {
  const filePath = new URL("./" + name, import.meta.url);
  const source = readFileSync(filePath, "utf8");
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  return output.replace(/from "\.\/(freshness|warming)"/g, 'from "./$1.mjs"');
};
writeFileSync(join(dir, "warming.mjs"), js("warming.ts"));
writeFileSync(join(dir, "api.mjs"), js("api.ts"));
const freshnessStub = `
export const counts = { failure: 0, success: 0 };
export function recordFailure() { counts.failure++; }
export function recordSuccess() { counts.success++; }
export function getConsecutiveFailures() { return 0; }
export function onRetry() { return () => {}; }
`;
writeFileSync(join(dir, "freshness.mjs"), freshnessStub);
globalThis.window = globalThis;
const calls = [];
const queue = [];
globalThis.fetch = async (url) => {
  calls.push(String(url));
  const next = queue.shift();
  if (!next) throw new Error("unexpected fetch " + url);
  return next();
};
const api = await import(pathToFileURL(join(dir, "api.mjs")).href);
const { counts } = await import(pathToFileURL(join(dir, "freshness.mjs")).href);
const warming = () =>
  new Response(JSON.stringify({ error: "warming" }), {
    status: 503,
    headers: { "retry-after": "1", "content-type": "application/json" },
  });
const ok = (body) => () =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
const fail = (status, error) => () =>
  new Response(JSON.stringify({ error }), {
    status,
    headers: { "content-type": "application/json" },
  });

test("B1 get() waits out a warming 503 once for every caller", async () => {
  const startCalls = calls.length;
  const startFailure = counts.failure;
  queue.push(warming, ok({ intact: true, count: 3 }));
  const told = [];
  const a = api.ledgerVerify(() => told.push("a"));
  const b = api.ledgerVerify(() => told.push("b"));
  const [ra, rb] = await Promise.all([a, b]);
  assert.deepStrictEqual(ra, { intact: true, count: 3 });
  assert.deepStrictEqual(rb, { intact: true, count: 3 });
  assert.strictEqual(calls.length - startCalls, 2);
  assert.strictEqual(counts.failure - startFailure, 0);
  assert.deepStrictEqual(told.sort(), ["a", "b"]);
  assert.deepStrictEqual(await api.ledgerVerify(), { intact: true, count: 3 });
  assert.strictEqual(calls.length - startCalls, 2);
});

test("B2 a non-warming 503 is not waited out", async () => {
  const startCalls = calls.length;
  const startFailure = counts.failure;
  queue.push(fail(503, "verification exceeded 30s"));
  await assert.rejects(
    api.trackRecord("1w"),
    (e) => e.status === 503 && e.message === "verification exceeded 30s"
  );
  await new Promise((r) => setTimeout(r, 50));
  assert.strictEqual(calls.length - startCalls, 1);
  assert.strictEqual(counts.failure - startFailure, 1);
});

test("B3 a 401 is unchanged", async () => {
  const startCalls = calls.length;
  const startFailure = counts.failure;
  queue.push(fail(401, "sign in"));
  await assert.rejects(api.trackRecord("1h"), (e) => e.status === 401);
  assert.strictEqual(calls.length - startCalls, 1);
  assert.strictEqual(counts.failure - startFailure, 0);
});