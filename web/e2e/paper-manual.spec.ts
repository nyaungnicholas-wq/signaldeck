// manual paper book feature (2026-09-07)
import { test, expect, type BrowserContext } from "@playwright/test";

const SMOKE_USER = "e2e-smoke";
const SMOKE_PASS = "E2eSmoke!2026";

async function loginAsSmokeUser(context: BrowserContext): Promise<void> {
  const headers = { "X-Signaldeck": "1" };
  const reg = await context.request.post("/api/auth/register", {
    headers,
    data: { username: SMOKE_USER, password: SMOKE_PASS },
  });
  if (reg.ok()) return;
  let login = await context.request.post("/api/auth/login", {
    headers,
    data: { username: SMOKE_USER, password: SMOKE_PASS },
  });
  for (let attempt = 0; attempt < 4 && login.status() === 429; attempt++) { // 429 = the shared write-tier limiter (burst 5, refill 2/s), not a bad credential
    await new Promise((r) => setTimeout(r, 2500));
    login = await context.request.post("/api/auth/login", { headers, data: { username: SMOKE_USER, password: SMOKE_PASS } });
  }
  expect(login.ok(), `login as ${SMOKE_USER} failed: ${login.status()}`).toBe(true);
}

test.describe("manual paper book", () => {
  test("order via API is reflected in the book and the page shows the order form", async ({ page, context }) => {
    // The first-run tour is a modal that swallows clicks; mark it seen like the other specs do.
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));
    await loginAsSmokeUser(context);
    const res = await context.request.post("/api/paper/order", { headers: {"X-Signaldeck": "1", "Content-Type": "application/json"}, data: { symbol: "BTC/USD", market: "crypto", side: "buy", qty: 0.001 } });
    expect([200, 409], `unexpected status ${res.status()}: ${await res.text()}`).toContain(res.status());
    if (res.status() === 200) { const body = await res.json(); expect(body.ok).toBe(true); expect(body.fill.Side).toBe("buy"); expect(body.fill.Cost).toBeGreaterThan(0); }
    const book = await context.request.get("/api/paper?strategy=manual");
    expect(book.ok()).toBe(true);
    const b = await book.json();
    expect(b.manual).toBe(true);
    expect(b.strategies).toContain("manual");
    if (res.status() === 200) { expect(JSON.stringify(b.trades)).toContain("BTC/USD"); }
    await page.goto("/lab/paper");
    await page.getByRole("button", { name: "MANUAL" }).click();
    await expect(page.getByText("MANUAL ORDER (SIMULATED)")).toBeVisible();
    await expect(page.getByRole("button", { name: "PLACE SIMULATED ORDER" })).toBeVisible();
  });
  test("anonymous order is refused", async ({ browser }) => {
    const ctx = await browser.newContext();
    const res = await ctx.request.post("/api/paper/order", { headers: {"X-Signaldeck": "1", "Content-Type": "application/json"}, data: { symbol: "BTC/USD", market: "crypto", side: "buy", qty: 0.001 } });
    expect(res.status()).toBe(401);
    await ctx.close();
  });
});