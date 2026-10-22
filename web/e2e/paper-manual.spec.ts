// manual paper book feature (2026-09-07)
import { test, expect } from "@playwright/test";
import { loginAsSmokeUser } from "./smokeuser";


test.describe("manual paper book", () => {
  test("order via API is reflected in the book and the page shows the order form", async ({ page, context }) => {
    // The first-run tour is a modal that swallows clicks; mark it seen like the other specs do.
    await context.addInitScript(() => localStorage.setItem("sd-onboarded", "1"));
    await loginAsSmokeUser(context);
    // Crypto is paused (2026-10-21, no licensed crypto data source): the server refuses it outright.
    const crypto = await context.request.post("/api/paper/order", { headers: {"X-Signaldeck": "1", "Content-Type": "application/json"}, data: { symbol: "BTC/USD", market: "crypto", side: "buy", qty: 0.001 } });
    expect(crypto.status()).toBe(422);
    expect(await crypto.text()).toContain("crypto is paused");
    // A stock order fills against a fresh 1m bar (200) or is refused while the market is closed (409).
    const res = await context.request.post("/api/paper/order", { headers: {"X-Signaldeck": "1", "Content-Type": "application/json"}, data: { symbol: "AAPL", market: "stocks", side: "buy", qty: 1 } });
    expect([200, 409], `unexpected status ${res.status()}: ${await res.text()}`).toContain(res.status());
    if (res.status() === 200) { const body = await res.json(); expect(body.ok).toBe(true); expect(body.fill.Side).toBe("buy"); expect(body.fill.Cost).toBeGreaterThan(0); }
    const book = await context.request.get("/api/paper?strategy=manual");
    expect(book.ok()).toBe(true);
    const b = await book.json();
    expect(b.manual).toBe(true);
    expect(b.strategies).toContain("manual");
    if (res.status() === 200) { expect(JSON.stringify(b.trades)).toContain("AAPL"); }
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