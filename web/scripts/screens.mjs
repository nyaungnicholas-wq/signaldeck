// Screenshot sweep for visual QA. Usage: node web/scripts/screens.mjs [--base http://127.0.0.1:8323] [--out <dir>] [--routes a,b,c] [--widths 1440,390]
import { chromium } from "playwright";
import { mkdirSync, writeFileSync } from "fs";
import { join } from "path";

const args = process.argv.slice(2);
const parseFlag = (name, def) => {
  const i = args.indexOf(`--${name}`);
  return i !== -1 && i + 1 < args.length ? args[i + 1] : def;
};
const parseBool = (name, def) => {
  const i = args.indexOf(`--${name}`);
  return i !== -1 ? true : def;
};
const base = parseFlag("base", "http://127.0.0.1:8323");
const outDir = parseFlag("out", "screens");
const routesStr = parseFlag("routes", "/,/accuracy,/proof,/volatility,/glossary,/health,/login,/dashboard,/market/overview,/market/signals,/market/breadth,/market/regimes,/market/macro,/watchlist,/watchlist/compare?a=NVDA&b=AMD,/s/stocks/SPY,/s/crypto/BTC%2FUSD,/intel/news,/intel/filings,/lab/paper,/lab/track-record,/lab/honesty,/lab/backtest,/lab/forecasts,/lab/live,/advanced,/welcome");
const widthsStr = parseFlag("widths", "1440,390");
const user = parseFlag("user", "e2e-smoke");
const pass = parseFlag("pass", "E2eSmoke!2026");
const noLogin = parseBool("no-login", false);
const routes = routesStr.split(",").map(r => r.trim());
const widths = widthsStr.split(",").map(w => parseInt(w, 10)).filter(w => !isNaN(w));

const slug = (route) => {
  let s = route.replace(/[^a-z0-9]/gi, "_");
  if (s.startsWith("_")) s = s.slice(1);
  return s === "" ? "home" : s;
};

(async () => {
  let browser;
  try {
    browser = await chromium.launch();
    const records = [];
    mkdirSync(outDir, { recursive: true });

    for (const w of widths) {
      const isMobile = w < 768;
      const context = await browser.newContext({
        viewport: { width: w, height: w < 768 ? 844 : 900 },
        isMobile,
        hasTouch: isMobile,
      });

      if (!noLogin) {
        const register = await context.request.post(`${base}/api/auth/register`, {
          headers: { "X-Signaldeck": "1" },
          data: { username: user, password: pass },
        });
        if (!register.ok()) {
          let loginOk = false;
          for (let attempt = 0; attempt < 3; attempt++) {
            const login = await context.request.post(`${base}/api/auth/login`, {
              headers: { "X-Signaldeck": "1" },
              data: { username: user, password: pass },
            });
            if (login.ok()) {
              loginOk = true;
              break;
            }
            if (login.status() === 429) await new Promise(r => setTimeout(r, 2000));
          }
          if (!loginOk) console.warn(`[WARN] Login failed for width ${w}; continuing anonymously`);
        }
      }

      for (const route of routes) {
        const page = await context.newPage();
        let consoleErrors = 0;
        let pageErrors = 0;
        page.on("console", msg => { if (msg.type() === "error") consoleErrors++; });
        page.on("pageerror", () => pageErrors++);
        await page.addInitScript(() => { try { localStorage.setItem("sd-onboarded", "1"); } catch {} });

        let start = Date.now();
        let status = null;
        try {
          const resp = await page.goto(base + route, { waitUntil: "networkidle", timeout: 45000 });
          status = resp ? resp.status() : null;
        } catch (e) {
          if (e.name === "TimeoutError") {
            try {
              const resp = await page.goto(base + route, { waitUntil: "load", timeout: 45000 });
              status = resp ? resp.status() : null;
            } catch { status = null; }
          } else {
            status = null;
          }
        }
        await page.waitForTimeout(1500);
        const elapsed = Date.now() - start;
        const title = await page.title();
        const url = page.url();
        const fileName = `${w}_${slug(route)}.png`;
        await page.screenshot({ path: join(outDir, fileName), fullPage: true });
        records.push({ route, width: w, status, title, url, consoleErrors, pageErrors, elapsedMs: elapsed });
        console.log(`${w} ${status ?? "-"} ${elapsed}ms ${route} -> ${title}`);
        await page.close();
      }
      await context.close();
    }
    writeFileSync(join(outDir, "index.json"), JSON.stringify(records, null, 2));
    // natural exit: the finally block closes the browser
  } catch (err) {
    console.error("Failed to launch browser:", err);
    process.exit(1);
  } finally {
    if (browser) await browser.close();
  }
})();