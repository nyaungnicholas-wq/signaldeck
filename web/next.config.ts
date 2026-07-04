import type { NextConfig } from "next";

// Single-URL setup: the browser only ever talks to the web app (:8323).
// Every /api/* request is proxied server-side to the data daemon (:8322), so
// the daemon port is never exposed and there is one localhost to open.
// tickstream (:8321) and trader-hud (:8787) stay as background data sources
// the daemon consumes — not user-facing.
const DAEMON = process.env.SIGNALDECK_DAEMON ?? "http://127.0.0.1:8322";

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
  // Stage 5: /lab/system is now a real page (quality + agents + AI mounted
  // together), so its old redirect-to-quality is gone.
];

const nextConfig: NextConfig = {
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${DAEMON}/api/:path*` }];
  },
  async redirects() {
    // permanent:false → 307 so browsers don't cache the mapping forever
    // while the IA is still evolving.
    return HUB_REDIRECTS.map((r) => ({ ...r, permanent: false }));
  },
};

export default nextConfig;
