// Publication-refusal contract for /accuracy and /api/accuracy.
//
// WHAT THESE PIN. Audit F-1 (2026-08-03): the /accuracy page rendered a REFUSED
// registry as a successful one — blank fields, no refusal message — so a
// grading outage was indistinguishable from a quiet week. And the 2026-08-03
// registry/evidence split: a model the record had condemned read as merely
// INSUFFICIENT once its window thinned below the 10-block floor.
//
// WHY THESE DO NOT STUB /api/accuracy. The page's publication gate is a SERVER
// component fetch, deliberately — a client-side gate would paint the stale
// table first and the refusal a moment later, which is the same defect with
// extra steps. Playwright's page.route intercepts browser traffic only, so a
// route stub here would silently test nothing at all. These therefore assert
// INVARIANTS that must hold in whichever state the stack is really in, and the
// row-level verdict rules are covered hermetically in Go
// (internal/publication/verdict_test.go, internal/store/publication_test.go).

import { test, expect } from "@playwright/test";

const REFUSED = new Set(["REFUSED", "REFUSED_STALE"]);

test("/api/accuracy either publishes with a grade stamp or refuses with a reason", async ({
  request,
}) => {
  const res = await request.get("/api/accuracy");
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

test("a refused registry shows the refusal and no accuracy figures", async ({ page, request }) => {
  const refused = !(await request.get("/api/accuracy")).ok();
  test.skip(!refused, "the grader is currently fresh; the refusal path is exercised when it is not");

  await page.goto("/accuracy");

  const banner = page.getByTestId("accuracy-status-banner").first();
  await expect(banner).toBeVisible();
  await expect(banner).toHaveAttribute("data-status", /REFUSED/);

  // The heart of F-1: refusing must REMOVE the numbers, not decorate them. No
  // percentage may appear anywhere on a refused page.
  await expect(page.locator("body")).not.toContainText(/\d+\.\d%/);
});

test("a condemned model is never downgraded to INSUFFICIENT", async ({ page, request }) => {
  const res = await request.get("/api/accuracy");
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

test("every non-OK row states why", async ({ request }) => {
  const res = await request.get("/api/accuracy");
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
