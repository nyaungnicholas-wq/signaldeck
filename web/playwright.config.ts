import { defineConfig, devices } from "@playwright/test";

// Smoke suite for the SignalDeck web app. The production build in .next is
// served on a dedicated port (8329) so a dev server on 8323 is undisturbed.
// workers: 1 — the daemon rate-limits reads at ~10 rps; parallel specs trip it.
//
// WHY THIS IS NOT IN .github/workflows/ci.yml, recorded here because its
// absence keeps getting filed as an oversight. It is not one. This is an
// INTEGRATION suite against a live system, not a CI-runnable unit suite:
// webServer below starts the web tier, but smoke.spec.ts registers and logs in
// through the Next proxy to the DAEMON on :8322, and 5 of the 8 specs call
// /api/. A GitHub runner has no daemon, and the database it reads is ~6 GB and
// gitignored, so the suite cannot mean there what it means here.
//
// It is a PRE-RELEASE MANUAL GATE instead — docs/PUBLIC_RELEASE_PLAN.md — run
// against a real daemon with `npm run e2e`. Wiring it into CI would need the
// daemon built, booted on a temp database and seeded, and a browser download
// on every run; a suite that flakes in CI gets switched off, which is strictly
// worse than one that is run deliberately and read.
export default defineConfig({
  testDir: "e2e",
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:8329",
    ...devices["Desktop Chrome"],
  },
  webServer: {
    command: "npm run start -- -p 8329",
    url: "http://127.0.0.1:8329/login",
    reuseExistingServer: false,
    timeout: 120000,
  },
});
