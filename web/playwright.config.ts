import { defineConfig, devices } from "@playwright/test";

// Smoke suite for the SignalDeck web app. The production build in .next is
// served on a dedicated port (8329) so a dev server on 8323 is undisturbed.
// workers: 1 — the daemon rate-limits reads at ~10 rps; parallel specs trip it.
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
