// THE RELEASE GATE ops/web-guard.ps1 ALREADY POINTED AT.
//
// That file tells its reader that "hydration, client navigation and interaction
// are proven by web/e2e/release-smoke.spec.ts at release time, not by a task
// that runs every five minutes". This file did not exist, so nothing proved it.
//
// What stood in for it was the mobile block in e2e/smoke.spec.ts: for each
// public page, assert there is no redirect to /login, that a header is visible,
// and that the page does not scroll sideways. A CRASHED page satisfies all
// three. Next's error boundary (src/app/error.tsx) renders inside the same
// layout, keeps the same header and does not overflow. On 2026-09-15 /proof was
// serving exactly that — the ledger, the registrations and the track record all
// replaced by "This page could not be loaded." — and the suite was green.
//
// So this spec asserts the opposite of a crash: that the page rendered its own
// content and that nothing threw. It supplies its own API responses with
// page.route and needs no daemon, no database and no login, because a release
// gate that only passes when one machine's daemon is healthy is a gate nobody
// can run.

import { test, expect, type Page } from "@playwright/test";

const PUBLIC_PATHS = ["/", "/accuracy", "/proof", "/volatility", "/glossary"];

const BOUNDARY_HEADING = "This page could not be loaded.";

// How long a page gets to change its mind after the shell paints.
//
// This is deliberately a fixed settle and NOT waitForLoadState("networkidle").
// /proof's ledger panel verifies a half-million-entry hash chain, and the daemon
// answers that with a designed 30s refusal ("ledger verification exceeded 30s"),
// so the page legitimately never goes network-quiet: measured 2026-09-16, the
// idle wait timed out twice on a page that was rendering perfectly and failed a
// release. The question here is only "did the boundary replace the page once the
// fetches resolved", and 2.5s is ample for that — the crash this guards against
// rendered the boundary within about a second of the payload arriving.
const SETTLE_MS = 2500;

/** Collect uncaught page errors. An uncaught render error is the signal the
 *  previous gate had no way to see: React unmounts the tree and the boundary
 *  takes its place, which from the outside looks like a page. */
function watchForCrashes(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  return errors;
}

// A realistic /api/prereg payload. THREE record kinds with different spec
// shapes, because assuming one shape is exactly what broke the page: the
// response was typed as a string, `spec.length` on an object is undefined, and
// the raw object reached React as a child.
const PREREG_FIXTURE = {
  chainVerified: true,
  registeredBefore: true,
  firstGradableOn: "2026-08-07",
  whatThisIs:
    "The claim each structural predictor made, frozen before any of its forecasts resolved.",
  appendOnly:
    "An amended claim is a new record, never an update — a change stays visible as a change.",
  records: [
    {
      seq: 1,
      ts: 1753000000,
      kind: "trend21",
      registeredOn: "2026-07-20",
      beforeFirstGradable: true,
      specHash: "1a2b3c4d5e6f7a8b9c0d",
      prevHash: "",
      entryHash: "aaaa1111bbbb2222cccc",
      note: "initial registration",
      spec: {
        kind: "trend21",
        question:
          "Will the stock still be on its current side of its 200-day moving average in 21 trading sessions?",
        horizonDays: 21,
        resolution:
          "Compare the close 21 sessions after the call against the 200-day SMA computed at that later bar.",
        bands: [
          { minConviction: 0.0, claimedAccuracy: 0.731 },
          { minConviction: 0.8, claimedAccuracy: 0.946 },
        ],
        baseline: "Naive persistence scores 0.700 over the same window.",
        knownWeakness: "Two years of history, six quarterly clusters.",
      },
    },
    {
      seq: 2,
      ts: 1757000000,
      kind: "auto-retire-rule",
      registeredOn: "2026-09-04",
      beforeFirstGradable: null,
      specHash: "9f8e7d6c5b4a39281706",
      prevHash: "aaaa1111bbbb2222cccc",
      entryHash: "dddd3333eeee4444ffff",
      note: "retirement rule",
      // The kind whose fields the API used to throw away entirely.
      spec: {
        model: "trend21",
        minIndependentN: 200,
        minDistinctDays: 30,
        criterion: "lower bound of the Wilson interval below the claimed band floor",
        action: "stop publishing the predictor and record the retirement",
        registered: "2026-09-04",
      },
    },
    {
      seq: 3,
      ts: 1754500000,
      kind: "grading-protocol",
      registeredOn: "2026-08-01",
      beforeFirstGradable: null,
      specHash: "0011223344556677889a",
      prevHash: "dddd3333eeee4444ffff",
      entryHash: "bbbb5555cccc6666dddd",
      note: "grading protocol",
      spec: {
        grader: "tools/accuracy_registry.py",
        graderCommit: "9d3c109bfe06f337975fc4c9ea2697a67df1800a",
        graderSha256: "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd6",
        maxAlpha: 0.05,
        refusal: "Withhold rather than publish when the window contains a collapsed cross-section.",
      },
    },
  ],
};

test.describe("release smoke", () => {
  for (const path of PUBLIC_PATHS) {
    test(`${path} renders its own content, not the error boundary`, async ({ page }) => {
      const crashes = watchForCrashes(page);
      await page.goto(path);
      await expect(page.locator("header").first()).toBeVisible();
      // SETTLE FIRST. These pages fetch after mount and the boundary appears
      // only when one of those renders throws, so asserting its absence the
      // instant the shell paints measures the skeleton and nothing else.
      // Measured: a /proof build that crashed on real data passed that check
      // and was caught two assertions later.
      await page.waitForTimeout(SETTLE_MS);
      await expect(page.getByRole("heading", { name: BOUNDARY_HEADING })).toHaveCount(0);
      expect(crashes.join(" | ")).toBe("");
    });
  }

  test("/proof renders a real registration payload without crashing", async ({ page }) => {
    const crashes = watchForCrashes(page);
    await page.route("**/api/prereg", (route) => route.fulfill({ json: PREREG_FIXTURE }));
    await page.goto("/proof");

    // Content first, deliberately: these assertions auto-wait for the fetch to
    // resolve, and the boundary check below is only meaningful after it has.
    //
    // The payload was RENDERED, not merely survived. This text lives inside the
    // first record's spec object.
    await expect(page.getByText("200-day moving average", { exact: false }).first()).toBeVisible();

    // And the retirement rule's own fields reached the reader. This is the
    // assertion that fails the moment anything starts decoding every payload
    // through one struct again: that record used to arrive with an empty
    // question, a zero horizon and null bands.
    await expect(page.getByText("Wilson interval", { exact: false }).first()).toBeVisible();

    // Two of the three records have no comparable date. Null is NOT EVALUATED
    // and must never render as "no".
    await expect(page.getByText("not evaluated", { exact: false }).first()).toBeVisible();

    await expect(page.getByRole("heading", { name: BOUNDARY_HEADING })).toHaveCount(0);
    expect(crashes.join(" | ")).toBe("");
  });

  test("/proof still renders itself when the daemon is unreachable", async ({ page }) => {
    // An unavailable chain must degrade to an in-page notice, never to a blank
    // page. That is the difference between "we cannot show you this" and "this
    // site is broken", and only one of them is a finding about the project.
    const crashes = watchForCrashes(page);
    await page.route("**/api/prereg", (route) =>
      route.fulfill({ status: 502, json: { error: "daemon unreachable" } }),
    );
    await page.goto("/proof");
    await expect(page.locator("header").first()).toBeVisible();
    await page.waitForTimeout(SETTLE_MS);
    await expect(page.getByRole("heading", { name: BOUNDARY_HEADING })).toHaveCount(0);
    expect(crashes.join(" | ")).toBe("");
  });
});
