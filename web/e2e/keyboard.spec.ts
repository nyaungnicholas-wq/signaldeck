import { test, expect } from "@playwright/test";
import { loginAsSmokeUser } from "./smokeuser";

// ─────────────────────────────────────────────────────────────────────────
// KEYBOARD OPERABILITY
//
// Guards the class of bug the UX audit found once it started measuring
// whether a control can be OPERATED rather than only how it is described:
// tables that sorted via onClick on a bare <th>, and a row whose only click
// target was the <tr> itself. Both were fully usable with a mouse and
// completely unusable without one.
//
// The audit counts these; these tests prove the specific ones stay fixed.
// ─────────────────────────────────────────────────────────────────────────


/** Every sortable header on the page must be a <th aria-sort> with a button. */
async function sortHeadersAreOperable(
  page: import("@playwright/test").Page,
  route: string,
): Promise<void> {
  await page.goto(route, { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(2500);

  const sortable = page.locator("main th[aria-sort]");
  const count = await sortable.count();
  if (count === 0) test.skip(true, `no table rendered on ${route}`);

  for (let i = 0; i < count; i++) {
    const th = sortable.nth(i);
    await expect(th.getByRole("button"), `${route} header ${i} needs a real button`).toHaveCount(1);
  }

  // Keyboard actually sorts: focus the first header's button, press Enter,
  // and the announced state must change.
  const first = sortable.first();
  const before = await first.getAttribute("aria-sort");
  const button = first.getByRole("button");
  await button.focus();
  await expect(button).toBeFocused();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(500);
  const after = await first.getAttribute("aria-sort");
  expect(
    after,
    `${route}: pressing Enter on a sort header must change the announced order`,
  ).not.toBe(before);
}

test.describe("keyboard operability", () => {
  test.beforeEach(async ({ context }) => {
    await loginAsSmokeUser(context);
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));
  });

  test("insiders sort headers work by keyboard", async ({ page }) => {
    await sortHeadersAreOperable(page, "/intel/insiders");
  });

  test("shorts sort headers work by keyboard", async ({ page }) => {
    await sortHeadersAreOperable(page, "/intel/shorts");
  });

  test("smart-money sort headers work by keyboard", async ({ page }) => {
    await sortHeadersAreOperable(page, "/intel/smart-money");
  });

  test("smart-money rows are pickable without a mouse", async ({ page }) => {
    await page.goto("/intel/smart-money", { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(2500);

    const picker = page.locator("main tbody button[aria-label^='Filter to ']").first();
    if ((await picker.count()) === 0) test.skip(true, "no smart-money rows stored");

    // The row's action used to live only on <tr onClick>, which no keyboard
    // can reach. It must now be a focusable control.
    await picker.focus();
    await expect(picker).toBeFocused();
  });
});
