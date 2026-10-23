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

const PUBLIC_PATHS = ["/", "/accuracy", "/proof", "/volatility", "/glossary", "/privacy", "/terms"];

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

// The daemon's 451 contract (daemon/internal/api, web/src/lib/withheld.ts),
// verbatim. A page must show these words, never an outage sentence.
const ALPACA_NOTICE =
  "Withheld: SignalDeck's prices, forecasts and statistics are derived from Alpaca market data, which is licensed for personal use only. They are shown only to the operator until Alpaca gives written permission.";
const KADOA_NOTICE =
  "Withheld: the congressional trading rows were compiled by Kadoa, whose terms do not permit sharing them without written permission. Collection has stopped and the stored rows are shown only to the operator.";

const LEDGER_FIXTURE = {
  intact: true,
  count: 1234,
  head: "abcdef0123456789abcdef",
  incremental: true,
  intactMeans: "The stored rows are internally consistent. That is not proof of when they were written.",
  computedAt: "2026-10-23T09:00:00Z",
};

const ME = {
  member: { id: 2, username: "member", isAdmin: false, member: true },
  admin: { id: 1, username: "operator", isAdmin: true, member: false },
} as const;

/** Answer every browser /api/* read the way a published daemon answers a
 *  non-operator: open routes get fixtures, the rest the 451 contract body. */
async function withheldDaemon(page: Page, role: "anon" | "member" | "admin"): Promise<void> {
  await page.route("**/api/**", (route) => {
    const p = new URL(route.request().url()).pathname;
    if (p === "/api/auth/me") {
      return role === "anon"
        ? route.fulfill({ status: 401, json: { error: "authentication required" } })
        : route.fulfill({ json: ME[role] });
    }
    if (p.startsWith("/api/auth/")) return route.fulfill({ json: { ok: true } });
    if (p === "/api/health") return route.fulfill({ json: { version: "smoke", uptimeS: 1, alpaca: true } });
    if (p === "/api/prereg") return route.fulfill({ json: PREREG_FIXTURE });
    if (p === "/api/ledger/verify") return route.fulfill({ json: LEDGER_FIXTURE });
    if (p === "/api/watchlist") return route.fulfill({ json: [] });
    if (p === "/api/ask") return route.fulfill({ status: 403, json: { error: "not available" } });
    const source = p === "/api/congress" ? "kadoa" : "alpaca";
    return route.fulfill({
      status: 451,
      json: { error: "withheld", reason: "licence", source, notice: source === "kadoa" ? KADOA_NOTICE : ALPACA_NOTICE },
    });
  });
}

async function expectNoOutageWording(page: Page): Promise<void> {
  const body = page.locator("body");
  for (const w of ["temporarily unavailable", "unreachable", "is the daemon running"]) {
    await expect(body).not.toContainText(w, { ignoreCase: true });
  }
}

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

  // Legal text guards. The Terms listed Kraken and Coinbase as live sources after
  // crypto was paused, and nothing failed; the Privacy Notice must keep stating
  // the backup encryption it promises and its draft status.
  // Retired sources may be named in the retired list, never in the in-use sentence
  // (the first paragraph of the Data sources section).
  test("/terms in-use sources name no retired source", async ({ page }) => {
    await page.goto("/terms");
    const inUse = page.getByRole("region", { name: "data sources and third-party services" }).locator("p").first();
    // Positive first, so the negative checks below cannot pass on an empty page.
    await expect(inUse).toContainText("Alpaca Markets");
    for (const retired of ["Kraken", "Coinbase", "Hyperliquid", "FINRA", "Cboe", "FRED", "Kadoa", "NVIDIA"]) {
      await expect(inUse).not.toContainText(retired);
    }
  });

  // The 23 October 2026 licence position, in the words a reader can hold us to.
  // Every withheld notice links to #data-sources, so the anchor is part of it.
  test("/terms states what is withheld, for whom, and why", async ({ page }) => {
    await page.goto("/terms");
    const body = page.locator("body");
    await expect(body).toContainText("Draft, pending legal review");
    await expect(body).toContainText("Last updated 23 October 2026");
    const sources = page.locator("#data-sources");
    await expect(sources).toHaveAttribute("aria-label", "data sources and third-party services");
    await expect(sources).toContainText("withheld from everyone except the operator until Alpaca gives written permission");
    await expect(sources).toContainText("member accounts see the same notice as visitors");
    await expect(sources).toContainText("collection from Kadoa has stopped");
    await expect(sources).toContainText("headlines are no longer sent to NVIDIA");
  });

  test("/privacy states backup encryption, the deletion record and its draft status", async ({ page }) => {
    await page.goto("/privacy");
    const body = page.locator("body");
    await expect(body).toContainText("AES-256-GCM");
    await expect(body).toContainText("Draft, pending legal review");
    await expect(body).toContainText("Last updated 23 October 2026");
    // The deletion record's own wording: "creation time" alone also matches the
    // collected-data table, so it would pass with the record misdescribed.
    await expect(body).toContainText("email address, username and creation time");
    await expect(body).toContainText("withheld from everyone except the operator until Alpaca gives written permission");
    await expect(body).toContainText("headlines are no longer sent to NVIDIA");
  });

  // ── THE WITHHELD STATE (HTTP 451, web/src/lib/withheld.ts) ────────────────
  // Every browser read below is answered by withheldDaemon: the routes that
  // stay open get fixtures, everything else gets the daemon's 451 contract
  // body, exactly as a published deployment answers a non-operator. The
  // server-rendered pages (/, /accuracy, /volatility) fetch the daemon from the
  // Next server, which page.route cannot reach; scratch walks against a stub
  // daemon cover those.
  test("/proof shows the licence notice, not an outage, and keeps the chain", async ({ page }) => {
    const crashes = watchForCrashes(page);
    await withheldDaemon(page, "anon");
    await page.goto("/proof");
    const notice = page.getByTestId("withheld").first();
    await expect(notice).toBeVisible();
    await expect(notice).toHaveAttribute("data-source", "alpaca");
    await expect(notice).toContainText(ALPACA_NOTICE);
    await expect(notice.getByRole("link", { name: "Why this is withheld" })).toHaveAttribute("href", "/terms#data-sources");
    // The parts that stay open still render: the registrations and the chain.
    await expect(page.getByText("200-day moving average", { exact: false }).first()).toBeVisible();
    await expect(page.getByText("entries in chain")).toBeVisible();
    await expectNoOutageWording(page);
    await expect(page.getByRole("heading", { name: BOUNDARY_HEADING })).toHaveCount(0);
    expect(crashes.join(" | ")).toBe("");
  });

  test("/today tells a member once, instead of four empty cards", async ({ page }) => {
    const crashes = watchForCrashes(page);
    await withheldDaemon(page, "member");
    await page.goto("/today");
    await expect(page.getByRole("heading", { name: "Today" })).toBeVisible();
    const notice = page.getByTestId("withheld");
    await expect(notice.first()).toBeVisible();
    await page.waitForTimeout(SETTLE_MS);
    await expect(notice).toHaveCount(1);
    await expect(notice).toContainText(ALPACA_NOTICE);
    await expect(page.locator("body")).not.toContainText("proof data unavailable");
    await expectNoOutageWording(page);
    expect(crashes.join(" | ")).toBe("");
  });

  test("/intel/congress shows the Kadoa notice in place of the table", async ({ page }) => {
    const crashes = watchForCrashes(page);
    await withheldDaemon(page, "admin");
    await page.goto("/intel/congress");
    const notice = page.locator('[data-testid="withheld"][data-source="kadoa"]');
    await expect(notice).toBeVisible();
    await expect(notice).toContainText(KADOA_NOTICE);
    // No zeroed stat tiles and no empty table pretending nothing was disclosed.
    await expect(page.getByText("Disclosures Shown")).toHaveCount(0);
    await expect(page.getByText("No stored trades")).toHaveCount(0);
    await expectNoOutageWording(page);
    expect(crashes.join(" | ")).toBe("");
  });
});
