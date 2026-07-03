import type { NextConfig } from "next";

// Single-URL setup: the browser only ever talks to the web app (:8323).
// Every /api/* request is proxied server-side to the data daemon (:8322), so
// the daemon port is never exposed and there is one localhost to open.
// tickstream (:8321) and trader-hud (:8787) stay as background data sources
// the daemon consumes — not user-facing.
const DAEMON = process.env.SIGNALDECK_DAEMON ?? "http://127.0.0.1:8322";

const nextConfig: NextConfig = {
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${DAEMON}/api/:path*` }];
  },
};

export default nextConfig;
