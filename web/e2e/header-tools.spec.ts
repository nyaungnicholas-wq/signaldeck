import { test, expect } from "@playwright/test";
import type { BrowserContext } from "@playwright/test";

const SMOKE_USER = "e2e-smoke";
const SMOKE_PASS = "E2eSmoke!2026";

async function signIn(context: BrowserContext): Promise<void> {
  const headers = { "X-Signaldeck": "1" };
  const data = { username: SMOKE_USER, password: SMOKE_PASS };
  const reg = await context.request.post("/api/auth/register", { headers, data });
  if (reg.ok()) return;
  for (let attempt = 0; attempt < 3; attempt++) {
    const res = await context.request.post("/api/auth/login", { headers, data });
    if (res.ok()) return;
    if (res.status() !== 429) expect(res.ok(), `sign in failed: ${res.status()}`).toBe(true);
    await new Promise((r) => setTimeout(r, 2000));
  }
  const final = await context.request.post("/api/auth/login", { headers, data });
  expect(final.ok(), `sign in failed after retries: ${final.status()}`).toBe(true);
}

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ context }) => {
  await signIn(context);
  await context.addInitScript(() => {
    localStorage.setItem("sd-onboarded", "1");
    localStorage.removeItem("sd-goal");
  });
});

test("header folds the secondary controls behind one status disclosure", async ({ page }) => {
  await page.goto("/dashboard", { waitUntil: "domcontentloaded" });
  const btn = page.getByRole("button", { name: /^status/ });
  await expect(btn).toBeVisible();
  await expect(btn).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("dialog", { name: "status and view settings" })).toHaveCount(0);
  await btn.click();
  const dialog = page.getByRole("dialog", { name: "status and view settings" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/daemon (live|down|unknown)/i)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(btn).toHaveAttribute("aria-expanded", "false");
});

test("a visitor with no goal sees one onboarding surface on the dashboard", async ({ page }) => {
  await page.goto("/dashboard");
  await expect(page.getByRole("region", { name: "who this dashboard is arranged for" })).toBeVisible();
  await expect(page.getByText("GET SET UP")).toHaveCount(0);
  await expect(page.getByText("New here? Start with one question.")).toHaveCount(0);
});

test("choosing a goal reveals the setup checklist", async ({ page }) => {
  await page.goto("/dashboard");
  await page.getByRole("region", { name: "who this dashboard is arranged for" })
    .getByRole("button", { name: "I'm learning" }).click();
  await expect(page.getByText("GET SET UP")).toBeVisible({ timeout: 10000 });
});