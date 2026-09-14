import { test, expect } from "@playwright/test";
import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { scoreSite, JARGON, type PageMeasurement } from "../src/lib/rubric";
import { loginAsSmokeUser } from "./smokeuser";


const ROUTES: readonly string[] = [
  "/",          // the PUBLIC landing page
  "/dashboard", // the authenticated deck, which "/" used to be
  "/welcome",
  "/advanced",
  "/glossary",
  "/watchlist",
  "/watchlist/compare",
  "/market/overview",
  "/market/breadth",
  "/market/signals",
  "/market/regimes",
  "/market/macro",
  "/market/activity",
  "/market/trends",
  "/market/unusual",
  "/signals",
  "/intel/news",
  "/intel/companies",
  "/intel/congress",
  "/intel/insiders",
  "/intel/institutions",
  "/intel/filings",
  "/intel/shorts",
  "/intel/smart-money",
  "/lab/backtest",
  "/lab/track-record",
  "/lab/portfolio",
  "/lab/risk",
  "/lab/options",
  "/lab/pairs",
  "/lab/paper",
  "/lab/scenario",
  "/lab/forecasts",
  "/lab/strategies",
  "/lab/confluence",
  "/lab/honesty",
  "/lab/research",
  "/lab/sentiment",
  "/lab/insights",
  "/lab/live",
  "/proof",
  "/accuracy",
  "/hud",

  // ADDED 2026-09-13. The list above measured 43 of the app's 59 page routes and
  // the score was published as the app's health, so 16 routes were graded by
  // omission. One of them, /volatility, is on the SEVEN-ROUTE PUBLIC SURFACE —
  // an anonymous visitor could reach a page the self-grade had never looked at.
  // The whole /lab/system family was missing too, which is the part of the app
  // that reports on the app.
  "/health",
  "/volatility",
  "/intel/company",
  "/lab/debate",
  "/lab/desk",
  "/lab/evolution",
  "/lab/graph",
  "/lab/memory",
  "/lab/optimizer",
  "/lab/signal-backtest",
  "/lab/system",
  "/lab/system/agents",
  "/lab/system/ai",
  "/lab/system/quality",
  // The two dynamic routes, with concrete params — a symbol page and its report
  // are real destinations users reach from every table on the site.
  "/s/stocks/AAPL",
  "/signals/report/stocks/AAPL",

  // DELIBERATELY NOT CRAWLED: /login. This suite authenticates before it
  // crawls (loginAsSmokeUser), and /login now redirects an already-signed-in
  // visitor to /dashboard, so including it would silently measure /dashboard a
  // second time and report the result under the wrong route name.
] as const;

const ADVANCED = (route: string) => route.startsWith("/lab") || route.startsWith("/intel") || route === "/advanced";

test("ux audit", async ({ page, context }) => {
  test.setTimeout(900_000);
  await loginAsSmokeUser(context);

  // Mark onboarding as seen before crawling. Without this the first-run tour
  // modal is open on all 42 routes and the crawl measures a state a real
  // person is in exactly once — everything gated on "has seen the tour"
  // (notably the next-step dock) would be invisible and score zero forever.
  // No goal is set: that is a genuine choice the audit should keep measuring.
  await context.addInitScript(() => {
    try {
      localStorage.setItem("sd-onboarded", "1");
    } catch {
      // Storage blocked in this context — the tour will show and the crawl
      // will under-report guidance. Better than failing the run.
    }
  });

  const measurements: PageMeasurement[] = [];

  for (const route of ROUTES) {
    try {
      // NOT networkidle: the ticker tape and the live panels poll forever, so
      // the network never goes idle and every route would burn its full
      // timeout before measuring. Load the document, then give the client
      // panels a fixed window to paint.
      await page.goto(route, { waitUntil: "domcontentloaded", timeout: 30_000 });

      // Loading state, caught in the act. Sampled immediately, before the
      // settle wait — once data lands the skeletons are gone and the happy
      // path cannot tell you whether the page ever had one.
      const hasLoading = await page
        .evaluate(
          () =>
            document.querySelector(
              "[data-skeleton], .skeleton, .skeleton-bar, [aria-busy='true'], [role=status]",
            ) !== null,
        )
        .catch(() => false);

      await page
        .waitForLoadState("load", { timeout: 15_000 })
        .catch(() => undefined); // a stalled sub-resource must not fail the route
      await page.waitForTimeout(2_500);

      // Scroll the whole page before measuring. <Reveal> renders
      // `{visible && children}` behind an IntersectionObserver, so every
      // section below the fold is ABSENT FROM THE DOM until it is scrolled
      // into view. Measuring without this reports a fraction of the real page
      // — /lab/insights read 190 words when it actually renders 11,977 — and
      // silently rewards pages for hiding their own content.
      await page
        .evaluate(async () => {
          const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
          let previous = -1;
          // Stop when the page stops growing; the cap is a runaway guard for
          // an infinite-scroll surface, not an expected exit.
          for (let i = 0; i < 40 && document.body.scrollHeight !== previous; i++) {
            previous = document.body.scrollHeight;
            window.scrollTo(0, document.body.scrollHeight);
            await sleep(150);
          }
          window.scrollTo(0, 0);
          await sleep(150);
        })
        .catch(() => undefined);
      await page.waitForTimeout(600);
      const raw = await page.evaluate(
        (jargon: string[]) => {
          const main = document.querySelector("main");
          if (!main) {
            return {
              title: document.title,
              words: 0,
              screens: 0,
              links: 0,
              buttons: 0,
              inputs: 0,
              viewControls: 0,
              numbers: 0,
              hasPurpose: false,
              hasNextStep: false,
              // No <main> means no content — so no jargon went unexplained.
              // Flagging every term here would punish a page for being empty.
              jargonUncovered: [] as string[],
              unnamedControls: 0,
              headingSkips: 0,
              smallTargets: 0,
              mouseOnlyControls: 0,
              imagesNoAlt: 0,
              liveRegions: 0,
              hasEmptyState: false,
              hasLoading: false,
              hasError: false,
              personalized: false,
            };
          }

          const text = main.innerText.trim();
          const words = text ? text.split(/\s+/).length : 0;
          const screens = +(
            document.body.scrollHeight / window.innerHeight
          ).toFixed(2);

          const links = main.querySelectorAll("a").length;
          const buttons = main.querySelectorAll("button").length;
          const inputs = main.querySelectorAll(
            "input,select,textarea"
          ).length;
          // Controls that change what this page shows. The ARIA state
          // attributes are the discriminator: they are how a control says "I
          // toggle/select something" to a screen reader, so requiring them
          // means the audit rewards described controls, not just <button>
          // elements that might only navigate.
          const viewControls = main.querySelectorAll(
            "input,select,textarea," +
              "[aria-pressed],[aria-sort],[aria-selected],[role=tab],[role=switch],[role=radio]," +
              "summary"
          ).length;
          const numbers = (
            text.match(/[-+]?\d[\d,]*\.?\d*%?/g) || []
          ).length;

          // hasPurpose
          let hasPurpose = false;
          const purposeEl = main.querySelector(
            "[data-purpose], [aria-label*='purpose' i]"
          );
          if (purposeEl) {
            hasPurpose = true;
          } else {
            const paragraphs = Array.from(
              main.querySelectorAll(":scope > * > p, :scope > p")
            ).slice(0, 3);
            for (const p of paragraphs) {
              const pText = p.textContent || "";
              if (
                pText.length >= 40 &&
                pText.length <= 400 &&
                /\s/.test(pText) &&
                (pText.includes(".") || !/\d$/.test(pText))
              ) {
                hasPurpose = true;
                break;
              }
            }
          }

          // hasNextStep
          // Document-wide, not main-only: the "what to do next" dock is a
          // sibling of <main>, and a next step the reader can see on this page
          // counts whichever element it lives in. Header and nav are still
          // excluded below — global navigation is not guidance.
          const interactive = document.querySelectorAll("a, button");
          const hasNextStep = Array.from(interactive).some((el) => {
            const accessibleText =
              el.getAttribute("aria-label") ||
              el.getAttribute("title") ||
              (el.textContent || "").trim();
            if (
              // Anchored: a next-step control leads with its verb. The
              // apostrophe class covers both the typed ' and the rendered ’.
              /^(next step|start here|what.?s next|try this|do this|show me|continue|get started|see how|see the|investigate|read a|open |run |explore|take me)/i.test(
                accessibleText
              )
            ) {
              const parent = el.parentElement;
              return !(
                parent &&
                (parent.tagName === "HEADER" ||
                  parent.tagName === "NAV" ||
                  parent.closest("header") ||
                  parent.closest("nav"))
              );
            }
            return false;
          });

          // jargonUncovered
          const jargonUncovered: string[] = [];
          const mainText = main.innerText.toLowerCase();
          const glossaryLinks = main.querySelectorAll('a[href^="/glossary"]');
          for (const term of jargon) {
            if (!mainText.includes(term.toLowerCase())) continue;
            let covered = false;
            // A glossary link only covers the term it actually points at. One
            // generic "Glossary" link in a corner is not an explanation of
            // every term on the page — treating it as one would make this
            // metric read 0 everywhere and measure nothing.
            for (const a of Array.from(glossaryLinks)) {
              const needle = term.toLowerCase();
              const href = (a.getAttribute("href") || "").toLowerCase();
              const label = (a.textContent || "").toLowerCase();
              if (href.includes(needle.replace(/\s+/g, "-")) || label.includes(needle)) {
                covered = true;
                break;
              }
            }
            if (!covered) {
              const allElements = main.querySelectorAll("*");
              for (const el of allElements) {
                if (
                  el.hasAttribute("title") ||
                  el.hasAttribute("aria-describedby") ||
                  el.hasAttribute("data-tip") ||
                  el.hasAttribute("data-help")
                ) {
                  const elText = el.textContent?.toLowerCase() || "";
                  if (elText.includes(term.toLowerCase())) {
                    covered = true;
                    break;
                  }
                }
              }
            }
            if (!covered) jargonUncovered.push(term);
          }

          // unnamedControls
          let unnamedControls = 0;
          const allInteractive = main.querySelectorAll("button, a");
          for (const el of Array.from(allInteractive)) {
            const trimmedText = (el.textContent || "").trim();
            const hasAriaLabel = el.hasAttribute("aria-label");
            const hasTitle = el.hasAttribute("title");
            const hasAriaLabelledby = el.hasAttribute("aria-labelledby");
            if (
              !trimmedText &&
              !hasAriaLabel &&
              !hasTitle &&
              !hasAriaLabelledby
            ) {
              unnamedControls++;
            }
          }

          // headingSkips
          const headings = main.querySelectorAll(
            "h1,h2,h3,h4,h5,h6"
          );
          let headingSkips = 0;
          let prevLevel = 0;
          for (const h of Array.from(headings)) {
            const level = parseInt(h.tagName.charAt(1), 10);
            if (prevLevel && level - prevLevel > 1) {
              headingSkips++;
            }
            prevLevel = level;
          }

          // smallTargets
          let smallTargets = 0;
          const allInteractiveRects = main.querySelectorAll(
            "a, button, input, select, textarea"
          );
          for (const el of Array.from(allInteractiveRects)) {
            const rect = el.getBoundingClientRect();
            if (rect.width > 0 && Math.min(rect.width, rect.height) < 40) {
              smallTargets++;
            }
          }

          // mouseOnlyControls — looks clickable, cannot be tabbed to.
          // `cursor: pointer` is the strongest available signal that the
          // author intended a click; if the element is not natively focusable
          // and has no tabindex, the click is mouse-only. Excludes elements
          // that merely CONTAIN a real control, which are usually just styled
          // wrappers around a focusable child.
          const FOCUSABLE = "a[href],button,input,select,textarea,summary,[tabindex]";
          let mouseOnlyControls = 0;
          for (const el of Array.from(main.querySelectorAll<HTMLElement>("*"))) {
            if (getComputedStyle(el).cursor !== "pointer") continue;
            if (el.matches(FOCUSABLE)) continue;
            if (el.closest(FOCUSABLE) !== null) continue;
            if (el.querySelector(FOCUSABLE) !== null) continue;
            // COUNT THE OUTERMOST ONE ONLY. `cursor: pointer` inherits, so a
            // single clickable <tr> would otherwise count itself plus every
            // cell, bar and span inside it — one defect reported as ten. The
            // first version of this check read 1,116 for what was really about
            // a hundred controls.
            const parent = el.parentElement;
            if (
              parent !== null &&
              parent !== main &&
              getComputedStyle(parent).cursor === "pointer" &&
              !parent.matches(FOCUSABLE)
            ) {
              continue;
            }
            mouseOnlyControls++;
          }

          // imagesNoAlt
          const imagesNoAlt = main.querySelectorAll(
            "img:not([alt])"
          ).length;

          // Live regions inside <main> only. The shell's freshness badge
          // announces app-wide staleness, which is right but global — counting
          // it would score every page 10/10 and stop measuring whether THIS
          // page tells you when its own numbers changed.
          const liveRegions = main.querySelectorAll(
            "[aria-live], [role=status], [role=alert]"
          ).length;

          // Marker first, wording second. Matching on copy alone tunes the
          // crawler to this app's phrasing and quietly breaks on a rewrite;
          // [data-empty] is a contract a component opts into.
          const hasEmptyState =
            main.querySelector("[data-empty]") !== null ||
            /nothing (yet|here)|no data|no results|not enough|still accruing|once (you|there)|empty/i.test(
              main.innerText
            );

          // hasLoading / hasError are NOT measurable here: by the time this
          // runs the page has loaded successfully, so neither state is on
          // screen. They are probed separately — loading before the settle
          // wait, error with the API forced to fail. Both are overwritten by
          // the caller; these values are placeholders only.
          const hasLoading = false;
          const hasError = false;

          // Personalization means the page actually renders something that
          // depends on the reader's mode — not merely that a mode attribute
          // exists on <html>, which is true on every page and graded nothing.
          const personalized =
            main.querySelector("[data-plain], [data-pro-only], [data-goal]") !== null;

          return {
            title: document.title,
            words,
            screens,
            links,
            buttons,
            inputs,
            viewControls,
            numbers,
            hasPurpose,
            hasNextStep,
            jargonUncovered,
            unnamedControls,
            headingSkips,
            smallTargets,
            mouseOnlyControls,
            imagesNoAlt,
            liveRegions,
            hasEmptyState,
            hasLoading,
            hasError,
            personalized,
          };
        },
        [...JARGON]
      );
      // Empty state, provoked rather than hoped for. `hasEmptyState` from the
      // main pass only reads true when a page HAPPENS to have no data today —
      // the same happy-path blindness that made loading and error meaningless.
      // Serving 200 with an empty payload asks the real question: when there
      // is nothing to show, does this page say so, or go blank?
      let hasEmptyState = false;
      try {
        await page.route("**/api/**", (r) =>
          r.fulfill({ status: 200, contentType: "application/json", body: "[]" }),
        );
        await page.reload({ waitUntil: "domcontentloaded", timeout: 20_000 });
        await page.waitForTimeout(1_800);
        hasEmptyState = await page.evaluate(() => {
          const main = document.querySelector("main");
          if (!main) return false;
          return (
            main.querySelector("[data-empty]") !== null ||
            /nothing (yet|here)|no data|no results|no .{0,20} (yet|stored)|not enough|still accruing|once (you|there)|empty/i.test(
              main.innerText,
            )
          );
        });
      } catch {
        hasEmptyState = false; // a crash on empty data is itself a missing empty state
      } finally {
        await page.unroute("**/api/**").catch(() => undefined);
      }

      // Error state, provoked rather than hoped for. Force every API call to
      // fail and reload: a page that still looks normal is a page that will
      // quietly show a stale or empty panel when the daemon is down.
      let hasError = false;
      try {
        await page.route("**/api/**", (r) =>
          r.fulfill({ status: 500, contentType: "application/json", body: '{"error":"forced"}' }),
        );
        await page.reload({ waitUntil: "domcontentloaded", timeout: 20_000 });
        await page.waitForTimeout(1_800);
        hasError = await page.evaluate(() => {
          const main = document.querySelector("main");
          if (!main) return false;
          return (
            main.querySelector("[role=alert]") !== null ||
            /could not|couldn.t|failed|unavailable|try again|went wrong|no data|error/i.test(
              main.innerText,
            )
          );
        });
      } catch {
        hasError = false; // a crash under failure is itself a missing error state
      } finally {
        await page.unroute("**/api/**").catch(() => undefined);
      }

      measurements.push({
        ...raw,
        hasLoading,
        hasEmptyState,
        hasError,
        route,
        advanced: ADVANCED(route),
      });
    } catch (err) {
      measurements.push({
        route,
        title: "",
        words: 0,
        screens: 0,
        links: 0,
        buttons: 0,
        inputs: 0,
        viewControls: 0,
        numbers: 0,
        hasPurpose: false,
        hasNextStep: false,
        jargonUncovered: [],
        unnamedControls: 0,
        headingSkips: 0,
        smallTargets: 0,
        mouseOnlyControls: 0,
        imagesNoAlt: 0,
        liveRegions: 0,
        hasEmptyState: false,
        hasLoading: false,
        hasError: false,
        personalized: false,
        advanced: ADVANCED(route),
        crawlError: String(err),
      });
    }
  }

  const site = scoreSite(measurements, undefined, new Date().toISOString());
  const publicPath = resolve(__dirname, "../public/ux-score.json");
  mkdirSync(dirname(publicPath), { recursive: true });
  writeFileSync(publicPath, JSON.stringify(site, null, 2));

  const mdPath = resolve(__dirname, "../UX_SCORE.md");
  const levelLabel = site.level.label;
  const levelMeaning = site.level.meaning;
  const mdLines = [
    `# SignalDeck UX Self-Grade`,
    ``,
    `Generated ${site.generatedAt}.`,
    `Overall score: ${site.overall.toFixed(1)}/100 — ${levelLabel}`,
    `${levelMeaning}`,
    ``,
    `## Categories`,
    `| Category | Score /10 | From pages | From behaviour | Worst routes |`,
    `|----------|-----------|------------|----------------|--------------|`,
  ];

  for (const cat of site.categories) {
    const score = cat.score === null ? "—" : cat.score.toFixed(1);
    const fromPages =
      cat.fromPages === null ? "—" : cat.fromPages.toFixed(1);
    const fromSignals =
      cat.fromSignals === null ? "—" : cat.fromSignals.toFixed(1);
    const worst = cat.worstRoutes.length ? cat.worstRoutes.join(", ") : "—";
    mdLines.push(
      `| ${cat.category.label} | ${score} | ${fromPages} | ${fromSignals} | ${worst} |`
    );
  }

  mdLines.push(``);
  mdLines.push(`## Weakest area`);
  if (site.weakest) {
    mdLines.push(
      `${site.weakest.category.label}: ${site.weakest.score === null ? "—" : site.weakest.score.toFixed(1)}/10`
    );
  }
  mdLines.push(``);
  mdLines.push(site.recommendation);

  const worstPages = site.pages
    .filter((p) => !p.measurement.crawlError)
    .sort((a, b) => a.overall - b.overall);
  const topWorst = worstPages.slice(0, 20);
  const omitted = worstPages.length - topWorst.length;

  mdLines.push(``);
  mdLines.push(`## Pages, worst first`);
  mdLines.push(
    `| Route | Score | Words | Screens | Controls | Jargon | A11y defects |`
  );
  mdLines.push(
    `|-------|-------|-------|---------|----------|--------|--------------|`
  );

  for (const p of topWorst) {
    const m = p.measurement;
    const controls = m.links + m.buttons + m.inputs;
    const a11y = m.unnamedControls + m.headingSkips + m.smallTargets + m.imagesNoAlt;
    mdLines.push(
      `| ${m.route} | ${p.overall.toFixed(1)} | ${m.words} | ${m.screens} | ${controls} | ${m.jargonUncovered.length} | ${a11y} |`
    );
  }
  if (omitted > 0) {
    mdLines.push(``);
    mdLines.push(`${omitted} routes omitted.`);
  }

  writeFileSync(mdPath, mdLines.join("\n"));

  console.log(
    `${site.overall.toFixed(1)}/100 ${site.level.label} — weakest: ${site.weakest?.category.label ?? "none"}`
  );

  expect(
    measurements.filter((m) => !m.crawlError).length
  ).toBeGreaterThan(ROUTES.length / 2);
});