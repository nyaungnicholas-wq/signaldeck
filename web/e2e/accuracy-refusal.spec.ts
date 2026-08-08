import { test, expect } from "@playwright/test";
import type { BrowserContext } from "@playwright/test";

const REFUSED = new Set(["REFUSED", "REFUSED_STALE"]);

const SMOKE_USER = "e2e-smoke";
const SMOKE_PASS = "E2eSmoke!2026";

// These specs used to call /api/accuracy anonymously, which worked only while
// SIGNALDECK_PUBLIC_READS defaulted OPEN. It no longer does — daemon/.env
// allowlists a public tunnel hostname, so unauthenticated reads are refused and
// the endpoint answers {"error":"authentication required"}. That turned the
// first assertion (body.status is a string) into a failure and made the other
// three skip on `!res.ok()`, so the publication-refusal contract silently
// stopped being checked at all.
//
// Every request below goes through page.request, NOT the standalone `request`
// fixture: they are separate contexts, so authenticating one leaves the other
// anonymous — and signing in to both doubled the write-tier requests straight
// into the daemon's rate limiter (burst 5, refill 2/s), which answered 429.
async function signIn(context: BrowserContext): Promise<void> {
  const headers = { "X-Signaldeck": "1" };
  const data = { username: SMOKE_USER, password: SMOKE_PASS };
  // One retry: the limiter is shared across every anonymous caller, and the
  // suite's other specs sign in at the same moment. A 429 here is the limiter
  // working, not a broken credential.
  for (let attempt = 0; attempt < 2; attempt++) {
    const res = await context.request.post("/api/auth/login", { headers, data });
    if (res.ok()) return;
    if (res.status() !== 429) {
      expect(res.ok(), `sign in as ${SMOKE_USER} failed: ${res.status()}`).toBe(true);
    }
    await new Promise((r) => setTimeout(r, 2000));
  }
  const final = await context.request.post("/api/auth/login", { headers, data });
  expect(final.ok(), `sign in as ${SMOKE_USER} failed after retries: ${final.status()}`).toBe(true);
}

test.beforeEach(async ({ context }) => {
  await signIn(context);
});

test("/api/accuracy either publishes with a grade stamp or refuses with a reason", async ({ page }) => {
  const res = await page.request.get("/api/accuracy");
  const body = await res.json();

  expect(typeof body.status).toBe("string");

  if (res.ok()) {
    expect(body.status).toBe("OK");
    expect(body.grader_fresh).toBe(true);
    // Publishing without saying which grade produced these numbers is the
    // provenance gap the registry exists to close.
    expect(body.graded_at, "a published registry must name when it was graded").toBeTruthy();
  } else {
    expect(res.status()).toBe(503);
    expect(REFUSED.has(body.status), `unexpected refusal status ${body.status}`).toBe(true);
    expect(body.grader_fresh).toBe(false);
    // A refusal a reader cannot act on is barely better than silence.
    expect(body.reason, "a refusal must carry its reason").toBeTruthy();
    // Fail-closed: a refusal must not smuggle numbers out alongside it.
    expect(body.rows ?? [], "a refusal must publish no rows").toHaveLength(0);
  }
});

test("a refused registry shows the refusal and no accuracy figures", async ({ page }) => {
  const refused = !(await page.request.get("/api/accuracy")).ok();
  test.skip(!refused, "the grader is currently fresh; the refusal path is exercised when it is not");

  await page.goto("/accuracy");

  const banner = page.getByTestId("accuracy-status-banner").first();
  await expect(banner).toBeVisible();
  await expect(banner).toHaveAttribute("data-status", /REFUSED/);

  // The heart of F-1: refusing must REMOVE the numbers, not decorate them. No
  // percentage may appear anywhere on a refused page.
  await expect(page.locator("body")).not.toContainText(/\d+\.\d%/);
});

test("a condemned model is never downgraded to INSUFFICIENT", async ({ page }) => {
  const res = await page.request.get("/api/accuracy");
  test.skip(!res.ok(), "publication is refused; there are no rows to check");

  const { rows = [] } = await res.json();
  const retired = rows.filter((r: { retired: boolean }) => r.retired);
  test.skip(retired.length === 0, "nothing is currently retired");

  // Retirement outranks thin evidence. A retired row whose current window has
  // fallen below the floors must still read RETIRED — the substitution of
  // INSUFFICIENT for RETIRED is precisely the 2026-08-03 defect.
  for (const r of retired) {
    expect(
      r.publication_status,
      `${r.predictor} (${r.horizon}) is retired but publishes as ${r.publication_status}`,
    ).not.toBe("INSUFFICIENT");
    expect(r.retirement_sticky, "a retired row must be marked sticky").toBe(true);
  }

  await page.goto("/accuracy");
  await expect(page.getByTestId("accuracy-status-banner").first()).toBeVisible();
  await expect(page.locator('[data-status="RETIRED"]').first()).toBeVisible();
});

test("every non-OK row states why", async ({ page }) => {
  const res = await page.request.get("/api/accuracy");
  test.skip(!res.ok(), "publication is refused; covered by the refusal test");

  const { rows = [] } = await res.json();
  for (const r of rows) {
    if (r.publication_status === "OK") continue;
    expect(
      (r.reasons ?? []).length,
      `${r.predictor} (${r.horizon}) publishes ${r.publication_status} with no reason`,
    ).toBeGreaterThan(0);
  }
});
