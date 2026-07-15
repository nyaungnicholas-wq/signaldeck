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
  // MARKETS
  { source: "/screener", destination: "/markets/screener" },
  { source: "/trends", destination: "/markets/trends" },
  { source: "/regime", destination: "/markets/regimes" },
  { source: "/macro", destination: "/markets/macro" },
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
  { source: "/markets", destination: "/markets/screener" },
  { source: "/signals", destination: "/signals/predictions" },
  { source: "/intel", destination: "/intel/news" },
  { source: "/lab", destination: "/lab/backtest" },
  { source: "/desk", destination: "/desk/overview" },
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
