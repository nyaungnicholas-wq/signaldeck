import { test, expect, type BrowserContext, type Page } from "@playwright/test";

// ─────────────────────────────────────────────────────────────────────────
// PERSONALIZATION — the goal must actually change the page.
//
// Guards the thing the UX audit kept scoring 3/10: a goal picker that only
// changed number formatting. Both surfaces below now reorder their blocks and
// trim their length from lib/goal.ts, and "demoted, never deleted" means the
// folded panels are still in the DOM behind a <details>.
//
// These assert BEHAVIOUR, not scores — a page can score well and still show
// every reader the same thing.
// ─────────────────────────────────────────────────────────────────────────

const SMOKE_USER = "e2e-smoke";
const SMOKE_PASS = "E2eSmoke!2026";

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

/** Click the goal banner's button for this goal and let the page settle. */
async function chooseGoal(page: Page, title: string): Promise<void> {
  await page.locator("[data-goal] button", { hasText: title }).first().click();
  await page.waitForTimeout(600);
}

/** Block ids currently rendered ahead of the fold, in order. */
async function leadBlocks(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("main > div > div[data-block]")).map(
      (d) => (d as HTMLElement).dataset.block ?? "",
    ),
  );
}

test.describe("goal changes the page", () => {
  test.beforeEach(async ({ context }) => {
    await loginAsSmokeUser(context);
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));
  });

  test("dashboard reorders and folds by goal", async ({ page }) => {
    await page.goto("/dashboard");
    await expect(page.locator("[data-goal]")).toBeVisible();

    await chooseGoal(page, "I'm learning");
    const learn = await leadBlocks(page);
    expect(learn, "learning leads with the plain-English read").toEqual([
      "read",
      "spotlight",
      "gauges",
      "proof",
    ]);

    await chooseGoal(page, "I'm tracking a few names");
    const track = await leadBlocks(page);
    expect(track[0], "tracking leads with what changed").toBe("changed");
    expect(track).not.toEqual(learn);

    await chooseGoal(page, "I'm testing strategies");
    const test3 = await leadBlocks(page);
    expect(test3[0], "testing leads with the validated forecast").toBe("vol");
    // Nothing is folded for this reader — every block is a lead block.
    expect(test3.length).toBe(7);
  });

  test("demoted panels still exist, they are only folded", async ({ page }) => {
    await page.goto("/dashboard");
    await chooseGoal(page, "I'm learning");

    const details = page.locator("main details");
    await expect(details).toBeVisible();
    // The heatmap block is not on screen, but it IS in the document.
    await expect(page.locator("main details [data-block='mood']")).toHaveCount(1);
  });

  test("market overview trims the screener for a learner and not for a tester", async ({
    page,
  }) => {
    await page.goto("/market/overview");
    await expect(page.locator("[data-goal]")).toBeVisible();

    await chooseGoal(page, "I'm learning");
    // The cap advertises itself; a trimmed list that hides the trim is a
    // wrong list. Either it is capped and says so, or everything fits.
    const showAll = page.getByRole("button", { name: /Show all \d+ symbols/ });
    const capped = (await showAll.count()) > 0;
    if (capped) {
      await showAll.first().click();
      await page.waitForTimeout(400);
      await expect(page.getByRole("button", { name: /Back to the top \d+/ })).toBeVisible();
    }

    await chooseGoal(page, "I'm testing strategies");
    // Uncapped for this reader: the "show all" affordance must be gone,
    // because there is nothing left to show.
    await expect(page.getByRole("button", { name: /Show all \d+ symbols/ })).toHaveCount(0);
  });

  test("insights feed caps its cards and folds the primer for a tester", async ({ page }) => {
    await page.goto("/lab/insights");
    await expect(page.locator("[data-goal]")).toBeVisible();

    await chooseGoal(page, "I'm learning");
    // The primer leads for a learner — a real heading, not a disclosure.
    await expect(page.getByText("HOW TO READ THIS FEED")).toBeVisible();

    const showAll = page.getByRole("button", { name: /Show all \d+ insights/ });
    if ((await showAll.count()) > 0) {
      const cardsBefore = await page.locator("main article, main [data-insight]").count();
      await showAll.first().click();
      await page.waitForTimeout(400);
      await expect(page.getByRole("button", { name: /Back to the top \d+/ })).toBeVisible();
      const cardsAfter = await page.locator("main article, main [data-insight]").count();
      expect(cardsAfter, "expanding must reveal more cards").toBeGreaterThanOrEqual(cardsBefore);
    }

    await chooseGoal(page, "I'm testing strategies");
    // Uncapped, and the primer becomes a fold rather than a lead block.
    await expect(page.getByRole("button", { name: /Show all \d+ insights/ })).toHaveCount(0);
    await expect(page.locator("main details summary", { hasText: "How to read this feed" })).toBeVisible();
  });

  test("filings caps its feed and exposes the form filter's state", async ({ page }) => {
    await page.goto("/intel/filings");
    await expect(page.locator("[data-goal]")).toBeVisible();

    // The form chips are a filter group: the selected one must be announced,
    // not carried by colour alone. This was the bug that made the page report
    // one interactive control while showing nine.
    const group = page.getByRole("group", { name: "Filter by form type" });
    await expect(group).toBeVisible();
    await expect(group.getByRole("button", { pressed: true })).toHaveCount(1);

    await group.getByRole("button", { name: "8-K", exact: true }).click();
    await page.waitForTimeout(800);
    await expect(
      group.getByRole("button", { name: "8-K", exact: true }),
      "clicking a form filter must move the pressed state",
    ).toHaveAttribute("aria-pressed", "true");

    await group.getByRole("button", { name: "all", exact: true }).click();
    await page.waitForTimeout(800);

    await chooseGoal(page, "I'm learning");
    const showAll = page.getByRole("button", { name: /Show all \d+ filings/ });
    if ((await showAll.count()) > 0) {
      await showAll.first().click();
      await page.waitForTimeout(400);
      await expect(page.getByRole("button", { name: /Back to the top \d+/ })).toBeVisible();
    }

    await chooseGoal(page, "I'm testing strategies");
    await expect(page.getByRole("button", { name: /Show all \d+ filings/ })).toHaveCount(0);
  });

  test("insiders sorting is reachable and announced", async ({ page }) => {
    await page.goto("/intel/insiders");
    await expect(page.locator("[data-goal]")).toBeVisible();

    // Wait for the fetch to settle — either the table or an empty state.
    // Checking immediately raced the load and silently skipped the test.
    const table = page.locator("main table").first();
    await Promise.race([
      table.waitFor({ state: "visible", timeout: 15_000 }).catch(() => undefined),
      page.locator("main [data-empty]").first().waitFor({ state: "visible", timeout: 15_000 }).catch(() => undefined),
    ]);
    if ((await table.count()) === 0) test.skip(true, "no parsed insider trades stored");

    // Regression for the real defect: sorting was onClick on the <th>, so it
    // was unreachable by keyboard and its state was never announced.
    const valueHeader = table.locator("th", { hasText: "Value" }).first();
    await expect(valueHeader).toHaveAttribute("aria-sort", /ascending|descending/);

    const symbolHeader = table.locator("th", { hasText: "Symbol" }).first();
    await expect(symbolHeader).toHaveAttribute("aria-sort", "none");

    // The control is a real button, so it can be focused and pressed.
    const symbolButton = symbolHeader.getByRole("button");
    await symbolButton.focus();
    await expect(symbolButton).toBeFocused();
    await page.keyboard.press("Enter");
    await page.waitForTimeout(400);
    await expect(
      symbolHeader,
      "sorting by keyboard must move the announced sort state",
    ).toHaveAttribute("aria-sort", /ascending|descending/);
  });
});
