import type { NextConfig } from "next";

// Single-URL setup: the browser only ever talks to the web app (:8323).
// Every /api/* request is proxied server-side to the data daemon (:8322) by
// the route handler at src/app/api/[...path]/route.ts (reads
// SIGNALDECK_DAEMON, forwards an allowlist of headers, attaches the
// server-only SIGNALDECK_API_TOKEN when set), so the daemon port is never
// exposed and there is one localhost to open. tickstream (:8321) and
// trader-hud (:8787) stay as background data sources the daemon consumes —
// not user-facing.

// Stage 2 nav consolidation: 24 flat routes became 6 hubs with nested
// sub-tab routes. Every old URL redirects (307, query string preserved —
// e.g. /institutions?manager=CIK) to its hub sub-tab so old bookmarks,
// briefing links and the alerts bell keep working. Hub indexes redirect to
// their default sub-tab so /markets etc. never 404. next.config redirects
// run before the filesystem AND are honored on client-side <Link> navs.
const HUB_REDIRECTS: { source: string; destination: string }[] = [
  // 2026-07-19 nav consolidation (9 tabs → 5): HOME absorbs TODAY, WATCHLIST
  // absorbs DECK + COMPARE, LAB absorbs DESK + LIVE. Old URLs land on the new
  // homes so bookmarks, the alerts bell and briefing links keep working.
  { source: "/today", destination: "/" },
  { source: "/deck", destination: "/watchlist" },
  { source: "/compare", destination: "/watchlist/compare" },
  { source: "/desk", destination: "/lab/desk" },
  { source: "/desk/overview", destination: "/lab/desk" },
  { source: "/live", destination: "/lab/live" },
  // 2026-07-18 hub merge: MARKETS + SIGNALS → one MARKET hub; research tabs
  // → LAB.
  //
  // 2026-08-02: the merge is now COMPLETE in the source tree too. Until then
  // /market/* pages were re-export stubs of the real components under
  // src/app/markets/*, so both trees existed and "which one do I edit?" had no
  // answer. The components now live at their canonical routes and src/app/
  // markets is gone — these entries are pure bookmark compatibility, and every
  // in-app link points at the destination directly (no chained 307s).
  { source: "/markets/screener", destination: "/market/overview" },
  { source: "/markets/trends", destination: "/market/trends" },
  { source: "/markets/macro", destination: "/market/macro" },
  { source: "/markets/regimes", destination: "/market/macro" },
  { source: "/markets/memory", destination: "/lab/memory" },
  { source: "/markets/graph", destination: "/lab/graph" },
  { source: "/signals/predictions", destination: "/market/signals" },
  { source: "/signals/regimes", destination: "/market/regimes" },
  { source: "/signals/alerts", destination: "/market/activity" },
  { source: "/signals/unusual", destination: "/market/unusual" },
  { source: "/signals/forecasts", destination: "/lab/forecasts" },
  { source: "/signals/confluence", destination: "/lab/confluence" },
  { source: "/signals/insights", destination: "/lab/insights" },
  { source: "/signals/debate", destination: "/lab/debate" },
  // MARKETS (flat legacy URLs — point at the FINAL destination, not at
  // /markets/*, which would chain a second 307 through the block above)
  { source: "/screener", destination: "/market/overview" },
  { source: "/trends", destination: "/market/trends" },
  { source: "/regime", destination: "/market/macro" },
  { source: "/macro", destination: "/market/macro" },
  // SIGNALS
  { source: "/predict", destination: "/signals/predictions" },
  { source: "/forecast", destination: "/signals/forecasts" },
  { source: "/insights", destination: "/signals/insights" },
  { source: "/alerts", destination: "/signals/alerts" },
  // INTEL
  { source: "/news", destination: "/intel/news" },
  { source: "/filings", destination: "/intel/filings" },
  { source: "/insiders", destination: "/intel/insiders" },
  { source: "/institutions", destination: "/intel/institutions" },
  { source: "/congress", destination: "/intel/congress" },
  // LAB
  { source: "/backtest", destination: "/lab/backtest" },
  { source: "/signal-backtest", destination: "/lab/signal-backtest" },
  { source: "/risk", destination: "/lab/risk" },
  { source: "/portfolio", destination: "/lab/portfolio" },
  { source: "/paper", destination: "/lab/paper" },
  { source: "/track-record", destination: "/lab/track-record" },
  { source: "/honesty", destination: "/lab/honesty" },
  { source: "/quality", destination: "/lab/system/quality" },
  { source: "/agents", destination: "/lab/system/agents" },
  { source: "/ai", destination: "/lab/system/ai" },
  // Hub indexes → default sub-tab
  { source: "/markets", destination: "/market/overview" },
  { source: "/signals", destination: "/signals/predictions" },
  { source: "/intel", destination: "/intel/news" },
  { source: "/lab", destination: "/lab/backtest" },
  // Stage 5: /lab/system is now a real page (quality + agents + AI mounted
  // together), so its old redirect-to-quality is gone.
];

// Applied to every route. CSP still allows 'unsafe-inline' (Next.js inline
// runtime scripts need it); the tightening path is a nonce-based CSP via
// middleware once those are eliminated. 'unsafe-eval' is dev-only (webpack/
// turbopack eval sourcemaps + HMR) — production builds never eval.
// connect-src is 'self' only: 'self' already covers same-origin ws/wss
// upgrades (incl. dev HMR), and bare ws:/wss: scheme sources would allow a
// WebSocket to ANY host — an exfiltration channel the app never uses.
const SCRIPT_SRC =
  process.env.NODE_ENV === "development"
    ? "'self' 'unsafe-inline' 'unsafe-eval'"
    : "'self' 'unsafe-inline'";
const SECURITY_HEADERS = [
  {
    key: "Content-Security-Policy",
    value:
      `default-src 'self'; script-src ${SCRIPT_SRC}; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`,
  },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "X-Frame-Options", value: "DENY" },
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=()",
  },
];

const nextConfig: NextConfig = {
  async headers() {
    return [{ source: "/(.*)", headers: SECURITY_HEADERS }];
  },
  async redirects() {
    // permanent:false → 307 so browsers don't cache the mapping forever
    // while the IA is still evolving.
    return HUB_REDIRECTS.map((r) => ({ ...r, permanent: false }));
  },
};

export default nextConfig;
