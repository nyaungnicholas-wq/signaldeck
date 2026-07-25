import { test, expect, type Page, type BrowserContext } from "@playwright/test";

// ─────────────────────────────────────────────────────────────────────────
// SignalDeck smoke suite.
//
// Auth facts (verified against the live daemon on 2026-07-09):
//   - POST /api/auth/register is OPEN (200). A throwaway user `e2e-smoke`
//     (password E2eSmoke!2026) exists; authed specs register-or-login with it.
//   - Every hub route except /login is client-side gated by AuthGate: the
//     document itself returns HTTP 200, then a 401 from /api/auth/me triggers
//     router.replace("/login").
// ─────────────────────────────────────────────────────────────────────────

const SMOKE_USER = "e2e-smoke";
const SMOKE_PASS = "E2eSmoke!2026";

/** Log the shared context in as the throwaway user (register 409/4xx → login).
 *  Goes through the Next proxy origin so the session cookie lands on :8329. */
async function loginAsSmokeUser(context: BrowserContext): Promise<void> {
  const headers = { "X-Signaldeck": "1" };
  const reg = await context.request.post("/api/auth/register", {
    headers,
    data: { username: SMOKE_USER, password: SMOKE_PASS },
  });
  if (reg.ok()) return;
  const login = await context.request.post("/api/auth/login", {
    headers,
    data: { username: SMOKE_USER, password: SMOKE_PASS },
  });
  expect(login.ok(), `login as ${SMOKE_USER} failed: ${login.status()}`).toBe(true);
}

async function noHorizontalScroll(page: Page): Promise<void> {
  const { scrollWidth, innerWidth } = await page.evaluate(() => ({
    scrollWidth: document.scrollingElement!.scrollWidth,
    innerWidth: window.innerWidth,
  }));
  expect(
    scrollWidth,
    `horizontal overflow: scrollWidth ${scrollWidth} > innerWidth ${innerWidth}`,
  ).toBeLessThanOrEqual(innerWidth);
}

// ── (1) login page renders; inputs have programmatically-associated labels ──

test.describe("login page", () => {
  test("renders with labelled inputs", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "SIGN IN" })).toBeVisible();

    // getByLabel resolving proves the <label for>/<input id> association.
    const username = page.getByLabel("USERNAME");
    const password = page.getByLabel("PASSWORD");
    await expect(username).toBeVisible();
    await expect(password).toBeVisible();
    await expect(username).toHaveAttribute("name", "username");
    await expect(password).toHaveAttribute("name", "password");

    // Every input on the page must resolve to some associated label.
    const inputs = page.locator("input");
    const n = await inputs.count();
    for (let i = 0; i < n; i++) {
      const id = await inputs.nth(i).getAttribute("id");
      expect(id, `input #${i} has no id (no label association possible)`).toBeTruthy();
      await expect(page.locator(`label[for="${id}"]`)).toHaveCount(1);
    }
  });

  // ── (2) invalid credentials → role=alert appears ──
  test("invalid credentials shows role=alert", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel("USERNAME").fill("definitely-not-a-user");
    await page.getByLabel("PASSWORD").fill("wrong-password-123");
    await page.getByRole("button", { name: "SIGN IN" }).click();
    // #login-error is the page's own alert box — a bare getByRole("alert")
    // also matches Next's route announcer and trips strict mode.
    const alert = page.locator("#login-error");
    await expect(alert).toBeVisible({ timeout: 10000 });
    await expect(alert).toHaveAttribute("role", "alert");
  });

  // ── (3) keyboard-only: Tab order reaches username → password → submit ──
  test("tab order: username → password → submit", async ({ page }) => {
    await page.goto("/login");
    const username = page.getByLabel("USERNAME");
    const password = page.getByLabel("PASSWORD");
    const submit = page.getByRole("button", { name: "SIGN IN" });

    // Pure keyboard flow from the page's initial focus state: the username
    // field is the autofocus target, so a keyboard user lands there on load.
    await expect(username).toBeFocused();
    await page.keyboard.type("kbd-user");

    await page.keyboard.press("Tab");
    await expect(password).toBeFocused();
    await page.keyboard.type("kbd-pass");

    // Both fields filled → the submit button is enabled and next in tab order.
    await page.keyboard.press("Tab");
    await expect(submit).toBeFocused();
  });
});

// ── (4) navigation: private workspace — unauthenticated visitors are sent to
//        /login and never shown app nav (no dashboard flash, no bouncing links) ──

// AuthGate gates every non-/login route client-side (401 on /api/auth/me →
// router.replace("/login")); the document itself is HTTP 200 for all six. While
// the session is unknown a "checking session…" placeholder renders — the app
// chrome (and its nav) never paints for an anonymous visitor.
const HUB_ROUTES: { path: string; gated: boolean }[] = [
  { path: "/", gated: true },
  { path: "/markets/screener", gated: true },
  { path: "/signals/predictions", gated: true },
  { path: "/intel/filings", gated: true },
  { path: "/lab/backtest", gated: true },
  { path: "/login", gated: false },
];

test.describe("navigation (unauthenticated)", () => {
  for (const { path, gated } of HUB_ROUTES) {
    test(`${path} → 200${gated ? ", redirects to /login (no nav exposed)" : " (login, no nav)"}`, async ({
      page,
    }) => {
      const res = await page.goto(path);
      expect(res, `no response for ${path}`).toBeTruthy();
      expect(res!.status()).toBe(200);
      if (gated) {
        // Gated route → the anonymous visitor is redirected to /login.
        await page.waitForURL("**/login", { timeout: 15000 });
      }
      await expect(page).toHaveURL(/\/login$/);
      await expect(page.getByRole("heading", { name: "SIGN IN" })).toBeVisible();
      // The public login page exposes NO hub navigation — the whole point of
      // the private model is that anonymous users never see (or click) nav.
      await expect(page.locator("header nav")).toHaveCount(0);
    });
  }
});

// ── (5) offline banner on the authed dashboard ──

test.describe("offline banner", () => {
  test("appears when offline, clears on reconnect + retry", async ({ page, context }) => {
    await loginAsSmokeUser(context);
    await page.goto("/");
    // Authed: we must stay on the dashboard, not bounce to /login.
    await expect(page.locator("header nav").first()).toBeVisible();
    await expect(page).not.toHaveURL(/\/login/);

    const banner = page.getByRole("status").filter({ hasText: "offline" });
    await expect(banner).toBeHidden();

    await context.setOffline(true);
    await expect(banner).toBeVisible({ timeout: 10000 });

    await context.setOffline(false);
    // Reconnecting clears navigator.onLine, but a failure streak (≥3 failed
    // polls while offline) can keep the store offline — Retry re-fires every
    // poll immediately and a success resets the streak.
    if (await banner.isVisible()) {
      await banner.getByRole("button", { name: "Retry" }).click();
    }
    await expect(banner).toBeHidden({ timeout: 15000 });
  });
});

// ── (6) mobile 375px: no horizontal scroll on /login and / ──

test.describe("mobile 375px viewport", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("/login has no horizontal scroll", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "SIGN IN" })).toBeVisible();
    await noHorizontalScroll(page);
  });

  test("/ (authed dashboard) has no horizontal scroll", async ({ page, context }) => {
    await loginAsSmokeUser(context);
    await page.goto("/");
    // At 375px the desktop nav is CSS-hidden (lg:flex); the mobile chrome is
    // the header bar with a hamburger toggle controlling #mobile-nav.
    await expect(page.locator("header").first()).toBeVisible();
    await expect(page.locator('[aria-controls="mobile-nav"]')).toBeVisible();
    await expect(page).not.toHaveURL(/\/login/);
    // The dashboard polls the daemon continuously, so "networkidle" never
    // settles — a short fixed pause lets the first data paint land instead.
    await page.waitForTimeout(2000);
    await noHorizontalScroll(page);
  });
});
