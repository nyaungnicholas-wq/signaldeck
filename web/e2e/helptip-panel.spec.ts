// Guards <HelpTip> popovers rendered inside a .panel. .panel has a
// backdrop-filter, which makes it the containing block for position:fixed
// descendants, so a popover placed from viewport coords landed off-screen
// until it was portalled to <body>. Part of the manual e2e gate (see
// playwright.config.ts): it logs in through a live daemon like the others.
import { test, expect } from "@playwright/test";
import { loginAsSmokeUser } from "./smokeuser";

// Each page names one tip that must render before counting, so a slow fetch
// can't shrink the scan to whatever happened to load first. /today's is in
// ProofStrip, the panel the bug was reported in.
const PAGES = [
  { path: "/today", anchor: "What paper P&L means" },
  { path: "/s/stocks/AAPL", anchor: "why these are the validated ones" },
];
const VIEWPORTS = [
  { name: "desktop", width: 1280, height: 720 },
  { name: "mobile", width: 375, height: 812 },
];

for (const vp of VIEWPORTS) {
  for (const { path, anchor } of PAGES) {
    test(`helptip popovers inside .panel open on-screen on ${path} @ ${vp.name}`, async ({ page: p, context }) => {
      await p.setViewportSize({ width: vp.width, height: vp.height });
      await loginAsSmokeUser(context);
      await p.goto(path);
      const tips = p.locator(".panel button[aria-controls^='helptip']");
      await expect(p.locator(".panel").getByRole("button", { name: anchor })).toBeVisible({ timeout: 60000 });
      const n = await tips.count();
      expect(n, "no HelpTip inside a .panel was found - the check would be vacuous").toBeGreaterThan(0);
      for (let i = 0; i < n; i++) {
        const btn = tips.nth(i);
        await btn.scrollIntoViewIfNeeded();
        await btn.click();
        const id = await btn.getAttribute("aria-controls");
        expect(id, "button missing aria-controls").not.toBeNull();
        const pop = p.locator(`[id="${id}"]`);
        await expect(pop).toBeVisible();
        await expect(pop).toHaveCSS("visibility", "visible");
        const geo = await p.evaluate((popId) => {
          const b = document.querySelector(`[aria-controls="${popId}"]`)!.getBoundingClientRect();
          const r = document.getElementById(popId)!.getBoundingClientRect();
          return {
            b: { top: b.top, bottom: b.bottom },
            r: { top: r.top, bottom: r.bottom, left: r.left, right: r.right },
            vw: window.innerWidth,
            vh: window.innerHeight,
          };
        }, id!);
        const at = `${await btn.getAttribute("aria-label")}: ${JSON.stringify(geo)}`;
        expect(geo.r.top, `popover above viewport for ${at}`).toBeGreaterThanOrEqual(0);
        expect(geo.r.left, `popover left of viewport for ${at}`).toBeGreaterThanOrEqual(0);
        expect(geo.r.right, `popover right of viewport for ${at}`).toBeLessThanOrEqual(geo.vw);
        expect(geo.r.bottom, `popover below viewport for ${at}`).toBeLessThanOrEqual(geo.vh);
        const gap = Math.min(Math.abs(geo.r.top - geo.b.bottom), Math.abs(geo.b.top - geo.r.bottom));
        expect(gap, `popover not next to its button for ${at}`).toBeLessThanOrEqual(16);
        await p.keyboard.press("Escape");
        await expect(pop).toBeHidden();
      }
      console.log(`HELPTIP OK ${path} ${vp.name} checked=${n}`);
    });
  }
}
