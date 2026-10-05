import { expect, type BrowserContext } from "@playwright/test";

/**
 * THE SHARED E2E IDENTITY.
 *
 * Five specs had their own byte-identical copy of this helper, and each one
 * registers-or-logs-in at the start of its run. Registration and login are
 * non-GET, so they land on the daemon's WRITE-tier token bucket
 * (internal/api/security.go: `writeTier` -> `limiter.allow`), which is much
 * tighter than the read tier. Any single spec passes on its own; run the whole
 * suite and a later one gets `login as e2e-smoke failed: 429`.
 *
 * That is not a product defect and it is not a flaky test — it is this suite
 * out-running a limiter that is doing its job. But the suite is the documented
 * PRE-RELEASE MANUAL GATE (see playwright.config.ts), and a gate that cannot be
 * run to completion is the same as a gate nobody runs.
 *
 * So the retry HONOURS THE SERVER rather than guessing: the daemon sets
 * `Retry-After: 1` alongside its 429, and that header is what decides the wait.
 * Nothing is weakened — the assertion that authentication must ultimately
 * SUCCEED is unchanged, and a 429 is the server saying "ask again shortly",
 * which is a different fact from "these credentials are wrong". A 401 or a 500
 * still fails immediately and loudly, on the first response.
 */

export const SMOKE_USER = "e2e-smoke";
export const SMOKE_PASS = "E2eSmoke!2026";

/**
 * How many times to honour a Retry-After before calling the limiter a wall.
 *
 * Two specs had already grown their own retry before this was shared — 4
 * attempts at a flat 2500ms, with the measurement in the comment: the write
 * tier is burst 5, refill 2/s. Retry-After says 1 second, and at 2/s one second
 * restores two tokens, so a single wait is usually enough. The escalation below
 * exists because it was evidently NOT always enough for whoever wrote that
 * 2500ms, and a gate that fails on timing is worth over-provisioning.
 */
const MAX_429_RETRIES = 8;

/** Fallback when a 429 arrives without a usable Retry-After. */
const DEFAULT_RETRY_SECONDS = 1;

function retryAfterMs(headers: Record<string, string>, attempt: number): number {
  const raw = headers["retry-after"];
  const secs = raw === undefined ? NaN : Number(raw);
  // Clamp: a malformed or absurd header must not hang the suite.
  const use = Number.isFinite(secs) && secs > 0 && secs <= 30 ? secs : DEFAULT_RETRY_SECONDS;
  // Escalate a little per attempt, so a busy fleet holding the bucket down does
  // not burn every retry inside one refill window. Worst case ~22s, bounded.
  return use * 1000 + attempt * 500;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export async function loginAsSmokeUser(context: BrowserContext): Promise<void> {
  const headers = { "X-Signaldeck": "1" };
  const body = { username: SMOKE_USER, password: SMOKE_PASS };

  // REGISTER EXACTLY ONCE, then retry only the LOGIN.
  //
  // The first version of this retried the whole register-then-login pair, so
  // eight attempts meant SIXTEEN write-tier requests against a burst-5 bucket.
  // That did not just fail to help, it actively starved the specs that ran
  // afterwards: a full-suite run went from 4 failures to 3 different ones, with
  // two innocent specs newly timing out because the bucket never refilled. A
  // retry that adds load to the thing it is waiting for is not a retry.
  //
  // Registration can only succeed once per database anyway — every later call
  // is a guaranteed rejection that costs a token for nothing.
  const reg = await context.request.post("/api/auth/register", { headers, data: body });
  if (reg.ok()) return;

  let lastStatus = reg.status();
  // If registration itself was limited, the bucket is already empty — start by
  // waiting the interval that response asked for, not by firing again at once.
  let wait = reg.status() === 429 ? retryAfterMs(reg.headers(), 0) : 0;

  for (let attempt = 0; attempt <= MAX_429_RETRIES; attempt++) {
    if (wait > 0) await sleep(wait);
    const login = await context.request.post("/api/auth/login", { headers, data: body });
    if (login.ok()) return;
    lastStatus = login.status();
    if (lastStatus !== 429) break; // a real refusal — report it now, do not grind
    wait = retryAfterMs(login.headers(), attempt); // the server's own interval
  }

  expect(
    false,
    lastStatus === 429
      ? `login as ${SMOKE_USER} kept hitting the write-tier rate limiter (429) after ` +
        `${MAX_429_RETRIES} Retry-After waits — the daemon is limiting, not refusing`
      : `login as ${SMOKE_USER} failed: ${lastStatus}`,
  ).toBe(true);
}
