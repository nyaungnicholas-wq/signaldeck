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
      // panels load at different times; measure once the tip count and the page
      // height stop changing (a reflow after placement would move the button)
      let prev = "";
      await expect
        .poll(async () => {
          const c = `${await tips.count()}/${await p.evaluate(() => document.documentElement.scrollHeight)}`;
          const stable = c === prev;
          prev = c;
          return stable;
        }, { timeout: 60000, intervals: [2000] })
        .toBe(true);
      const n = await tips.count();
      expect(n, "no HelpTip inside a .panel was found - the check would be vacuous").toBeGreaterThan(0);
      for (let i = 0; i < n; i++) {
        const btn = tips.nth(i);
        // mid-screen is the worst case: a tall tip may fit neither below nor above
        await btn.evaluate((el) => el.scrollIntoView({ block: "center", behavior: "instant" }));
        await btn.click();
        const id = await btn.getAttribute("aria-controls");
        expect(id, "button missing aria-controls").not.toBeNull();
        const pop = p.locator(`[id="${id}"]`);
        await expect(pop).toBeVisible();
        await expect(pop).toHaveCSS("visibility", "visible");
        await pop.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)));
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
        expect(geo.r.top, `popover above viewport for ${at}`).toBeGreaterThanOrEqual(8);
        expect(geo.r.left, `popover left of viewport for ${at}`).toBeGreaterThanOrEqual(0);
        expect(geo.r.right, `popover right of viewport for ${at}`).toBeLessThanOrEqual(geo.vw);
        expect(geo.r.bottom, `popover below viewport for ${at}`).toBeLessThanOrEqual(geo.vh - 8);
        const gap = Math.min(Math.abs(geo.r.top - geo.b.bottom), Math.abs(geo.b.top - geo.r.bottom));
        expect(gap, `popover not next to its button for ${at}`).toBeLessThanOrEqual(16);
        await p.keyboard.press("Escape");
        await expect(pop).toBeHidden();
      }
      console.log(`HELPTIP OK ${path} ${vp.name} checked=${n}`);
    });
  }
}

// IndicatorMenu (operator symbol page, inside the chart .panel) uses the same
// fixed-from-button-rect placement, so it had the same bug. Members never see
// the operator page; the test tells the client this session is not a member
// (the daemon still decides what data it gets) so the real component renders.
// Two scroll positions: button mid-screen (no room below -> opens above,
// height capped) and button near the top (opens below).
for (const vp of VIEWPORTS) {
  test(`indicator menu inside the chart .panel opens on-screen @ ${vp.name}`, async ({ page: p, context }) => {
    await p.setViewportSize({ width: vp.width, height: vp.height });
    await loginAsSmokeUser(context);
    await context.route(/\/api\/(auth\/)?me(\?|$)/, async (route) => {
      const r = await route.fetch();
      await route.fulfill({ response: r, json: { ...(await r.json()), member: false } });
    });
    await p.goto("/s/stocks/AAPL");
    const btn = p.locator(".panel").getByRole("button", { name: /^Indicators/ });
    await expect(btn).toBeVisible({ timeout: 60000 });
    // the first-run tour is a modal overlay that mounts late; skip it if it shows
    const tour = p.getByRole("dialog", { name: /welcome tour/ });
    await tour.waitFor({ timeout: 5000 }).then(
      () => tour.getByRole("button", { name: "Skip tour" }).click(),
      () => {},
    );
    await expect(tour).toBeHidden();
    // the operator page reflows while its ~27 panels load; measure a settled page
    let last = NaN;
    await expect
      .poll(async () => {
        const h = await p.evaluate(() => document.documentElement.scrollHeight + "/" + document.querySelector("main")?.getBoundingClientRect().height);
        const same = h === String(last);
        last = h as unknown as number;
        return same;
      }, { timeout: 60000, intervals: [1500] })
      .toBe(true);
    for (const at of ["center", "top"] as const) {
      // instant scrolls: the site sets scroll-behavior:smooth
      // "top" parks the button 120px down, clear of the sticky header (at y=0 the
      // header covers it and the click re-scrolls the page)
      await btn.evaluate((el, at) => {
        if (at === "top") window.scrollBy({ top: el.getBoundingClientRect().top - 120, behavior: "instant" });
        else el.scrollIntoView({ block: "center", behavior: "instant" });
      }, at);
      const bTop = await btn.evaluate((el) => el.getBoundingClientRect().top);
      if (at === "top") expect(bTop, "could not scroll the button near the top").toBeLessThan(200);
      await btn.click();
      const pop = p.getByRole("dialog", { name: "chart indicators" });
      await expect(pop).toHaveCSS("visibility", "visible");
      // .pop-in starts at translateY(-4px); measure the resting position
      await pop.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)));
      const g = await p.evaluate(() => {
        const b = [...document.querySelectorAll(".panel button")].find((x) => /^Indicators/.test(x.textContent!.trim()))!.getBoundingClientRect();
        const r = document.querySelector('[role="dialog"][aria-label="chart indicators"]')!.getBoundingClientRect();
        return { bTop: b.top, bBottom: b.bottom, top: r.top, bottom: r.bottom, left: r.left, right: r.right, vw: innerWidth, vh: innerHeight };
      });
      const where = `${at}: ${JSON.stringify(g)}`;
      expect(Math.abs(g.bTop - bTop), `page moved during the click (${where})`).toBeLessThan(2);
      expect(g.top, `menu above viewport (${where})`).toBeGreaterThanOrEqual(8);
      expect(g.bottom, `menu below viewport (${where})`).toBeLessThanOrEqual(g.vh - 8);
      expect(g.left, `menu left of viewport (${where})`).toBeGreaterThanOrEqual(0);
      expect(g.right, `menu right of viewport (${where})`).toBeLessThanOrEqual(g.vw);
      const gap = Math.min(Math.abs(g.top - g.bBottom), Math.abs(g.bTop - g.bottom));
      expect(gap, `menu not next to its button (${where})`).toBeLessThanOrEqual(16);
      // a click inside the portalled menu must not count as an outside click
      await pop.locator("input[type=checkbox]").first().click();
      await expect(pop).toBeVisible();
      await p.keyboard.press("Escape");
      await expect(pop).toBeHidden();
      console.log(`INDMENU OK ${vp.name} ${at} top=${Math.round(g.top)} bottom=${Math.round(g.bottom)}`);
    }
  });
}
