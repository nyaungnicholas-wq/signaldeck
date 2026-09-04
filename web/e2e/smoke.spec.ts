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
  // "/" is the PUBLIC landing page now, not the deck -- it must NOT gate.
  // The authenticated dashboard moved to /dashboard.
  { path: "/", gated: false },
  { path: "/dashboard", gated: true },
  { path: "/market/overview", gated: true },
  { path: "/market/signals", gated: true },
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
    // The first-run tour is a modal overlay that swallows clicks; a real user
    // dismisses it once. This test clicks Retry, so it needs the same guard the
    // other click-driven specs already use — without it the tour dialog
    // intercepts the pointer event and the click never lands.
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));
    await page.goto("/dashboard");
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

// ── (7) COMPARE: symbols can be changed repeatedly, not just once ──

// Regression for the 2026-07-25 bug: symbols could be changed at most once per
// page load. navigate() went through router.replace() to the LEGACY /compare
// path that next.config 307s here; the router resolved that redirect once and
// dropped every later replace(). Underneath sat a second defect — on 16.2.10
// router.replace/push with only the query changed is dropped outright once
// this route has settled — so the page now writes the URL with the native
// History API. Three consecutive changes is the assertion: one alone passed
// even with the bug present.

test.describe("compare page symbol changes", () => {
  test("swap and submit keep working after the first change", async ({ page, context }) => {
    // The daemon's password hashing makes /api/auth/login cost tens of seconds
    // on this box; the default 30s test budget expires inside the login alone.
    test.setTimeout(180000);
    await loginAsSmokeUser(context);
    // The first-run tour is a modal overlay that swallows clicks; a real
    // returning user has this flag set.
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));

    // Every refetch the page performs, recorded from the proxied daemon calls.
    const reportCalls: string[] = [];
    page.on("request", (req) => {
      const u = new URL(req.url());
      if (u.pathname === "/api/signal-report") reportCalls.push(u.searchParams.get("symbol") ?? "");
    });

    await page.goto("/watchlist/compare?a=NVDA&b=AMD");
    // Scoped to the picker form so the page's other controls (peek buttons,
    // command palette) can never satisfy these role queries.
    const form = page.locator("main form");
    const symA = form.getByLabel("symbol A");
    const symB = form.getByLabel("symbol B");
    const submit = form.getByRole("button", { name: "compare" });
    const swap = form.getByRole("button", { name: "swap" });
    // AuthGate resolves the session before the page paints — allow for it.
    await expect(symA).toHaveValue("NVDA", { timeout: 30000 });
    await expect(symB).toHaveValue("AMD");

    // Drive the form only once the first pair has actually rendered. Typing
    // into the page mid-load races its own hydration and first fetch, which
    // makes this spec flaky for reasons that have nothing to do with the bug.
    // exact:true matters — the default substring match is case-insensitive and
    // would match "aligned trade context" in the purpose banner, which paints
    // immediately and so gates nothing.
    await expect(page.getByText("TRADE CONTEXT DETAIL", { exact: true })).toBeVisible({ timeout: 60000 });

    // change 1 — submit a new A
    await symA.fill("MSFT");
    await submit.click();
    await expect(page).toHaveURL(/\/watchlist\/compare\?a=MSFT&b=AMD$/);
    await expect(symA).toHaveValue("MSFT");

    // change 2 — swap (the one that used to die)
    await swap.click();
    await expect(page).toHaveURL(/\/watchlist\/compare\?a=AMD&b=MSFT$/);
    await expect(symA).toHaveValue("AMD");
    await expect(symB).toHaveValue("MSFT");

    // change 3 — submit again, proving it is not a one-per-load allowance
    await symA.fill("TSLA");
    await submit.click();
    await expect(page).toHaveURL(/\/watchlist\/compare\?a=TSLA&b=MSFT$/);
    await expect(symA).toHaveValue("TSLA");

    // The URL changing is only half the fix: the data must actually refetch.
    await expect
      .poll(() => reportCalls.filter((s) => s === "TSLA").length, { timeout: 15000 })
      .toBeGreaterThan(0);
    expect(reportCalls).toContain("MSFT");
  });
});

// ── (6) mobile 375px: no horizontal scroll on the public pages or the deck ──

test.describe("mobile 375px viewport", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("/login has no horizontal scroll", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "SIGN IN" })).toBeVisible();
    await noHorizontalScroll(page);
  });

  // The PUBLIC pages, anonymously, at phone width. These are the ones a
  // stranger actually lands on, and they were never covered: the only mobile
  // assertions were /login and the authed dashboard.
  //
  // A wide table is fine here as long as it scrolls inside its own
  // .table-wrap; what must never happen is the PAGE scrolling sideways.
  for (const path of ["/", "/accuracy", "/proof", "/glossary", "/volatility"]) {
    test(`${path} has no horizontal scroll for an anonymous visitor`, async ({ page }) => {
      await page.goto(path);
      await expect(page).not.toHaveURL(/\/login/);
      await expect(page.locator("header").first()).toBeVisible();
      await noHorizontalScroll(page);
    });
  }

  test("/dashboard (authed) has no horizontal scroll", async ({ page, context }) => {
    await loginAsSmokeUser(context);
    await page.goto("/dashboard");
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

// ── (8) LAB research surfaces render their measured numbers ──

// The three studies wired into the Lab hub on 2026-07-25 (options vol edge,
// sentiment correlation, pairs / H018). Each is a client component that paints
// a skeleton until its daemon fetch lands, so a 200 on the document proves
// nothing — these assert content that only exists AFTER the payload arrives,
// and specifically the honesty-bearing content: the pairs verdict, the
// sentiment page's partial-vs-raw framing, and the options page's own panels.
test.describe("lab research surfaces", () => {
  test("options, sentiment and pairs each paint real content", async ({ page, context }) => {
    // Login alone costs tens of seconds on this box (deliberate password
    // hashing cost), and this test then loads three data-backed pages.
    test.setTimeout(240000);
    await loginAsSmokeUser(context);
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));

    await page.goto("/lab/pairs");
    await expect(
      page.getByRole("heading", { name: "Pairs Cointegration Study", exact: true }),
    ).toBeVisible({ timeout: 30000 });
    // The verdict badge and the mechanism contrast are the page's reason to
    // exist; if the fetch failed, ErrorState renders instead and both vanish.
    await expect(page.getByText("DO NOT SHIP", { exact: true })).toBeVisible({ timeout: 30000 });
    await expect(page.getByText("persists across windows")).toBeVisible();
    // The tab is reachable from the hub, not just by URL.
    // SIMPLE is the default view, so the tab renders its plain-English name
    // (src/lib/labels.ts). PRO would show "PAIRS".
    await expect(page.getByRole("link", { name: "Paired trades", exact: true })).toBeVisible();

    await page.goto("/lab/sentiment");
    await expect(
      page.getByRole("heading", { name: "SENTIMENT ANALYSIS", exact: true }),
    ).toBeVisible({ timeout: 30000 });
    await expect(page.getByText("DATA COVERAGE")).toBeVisible({ timeout: 30000 });

    await page.goto("/lab/options");
    await expect(page.getByRole("heading", { name: "OPTIONS", exact: true })).toBeVisible({
      timeout: 30000,
    });
  });
});

// ── (9) MARKET BREADTH: the index/sector regime view ──

// The page's whole reason to exist is the breadth split; a 200 on the document
// says nothing, since it paints a skeleton until the daemon call lands.
test.describe("market breadth", () => {
  test("renders the breadth panel and the basket tables", async ({ page, context }) => {
    test.setTimeout(240000);
    await loginAsSmokeUser(context);
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));

    await page.goto("/market/breadth");
    await expect(page.getByRole("heading", { name: "Breadth", exact: true })).toBeVisible({
      timeout: 30000,
    });
    await expect(page.getByText("SECTOR BREADTH")).toBeVisible({ timeout: 30000 });
    await expect(page.getByText("Indices", { exact: true })).toBeVisible();
    // The tab is reachable from the Market hub, not only by URL. SIMPLE is the
    // default view, so it renders the plain-English name (src/lib/labels.ts).
    await expect(
      page.getByRole("link", { name: "How broad the move is", exact: true }),
    ).toBeVisible();
  });
});
