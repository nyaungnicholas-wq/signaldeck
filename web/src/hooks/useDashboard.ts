"use client";

// Dashboard page data hook — the fetching half of the old monolithic
// src/app/page.tsx (pure refactor; behavior identical). ONE /api/dashboard
// roundup on the POLL_DEFAULT tier (the server caches shared sections 60s
// and says so) + the existing session-scoped unread-alerts poll once we know
// a session exists. Both loops run through the managed pollMs() helper, so
// they pause while the tab is hidden, back off on consecutive failures and
// re-fire on the app-wide freshness Retry. A dashboard() failure KEEPS the
// last-good payload — the page shows a "refresh failed" chip, never a blank.

import { useEffect, useState } from "react";
import {
  api,
  dashboard,
  pollMs,
  POLL_DEFAULT,
  type AlertRow,
  type DashboardResponse,
  type Market,
  isAuthError,
  ApiError,
} from "@/lib/api";

export interface DashboardState {
  dash: DashboardResponse | null;
  /** Last fetch error — with dash !== null it means "stale but retrying". */
  error: string | null;
  /** HTTP status behind `error`; 401 = signed out, 0 = daemon never answered. */
  errorStatus: number;
  /** Session-scoped unread alerts; null while loading or logged out. */
  alerts: AlertRow[] | null;
  /** Featured chart symbol — locked in once so polls never yank the chart. */
  featured: { symbol: string; market: Market } | null;
  loggedIn: boolean;
  /** Re-run the roundup now (watchlist changed, error retry button). */
  refresh: () => void;
}

export default function useDashboard(): DashboardState {
  const [dash, setDash] = useState<DashboardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  /** HTTP status behind `error` (0 = never reached the daemon). Lets the page
   *  tell "signed out" (401) from "daemon down" instead of guessing. */
  const [errorStatus, setErrorStatus] = useState(0);
  const [tick, setTick] = useState(0);
  const [alerts, setAlerts] = useState<AlertRow[] | null>(null);
  const [featured, setFeatured] = useState<{ symbol: string; market: Market } | null>(null);

  // ONE roundup — immediate load, then the managed POLL_DEFAULT loop.
  useEffect(() => {
    let alive = true;
    const load = () =>
      dashboard()
        .then((d) => {
          if (!alive) return;
          setDash(d);
          setError(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setError(e instanceof Error ? e.message : String(e));
          setErrorStatus(e instanceof ApiError ? e.status : 0);
        });
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [tick]);

  // Featured symbol: first watchlist symbol, else top mover, else SPY.
  // Locked in from the first payload — adjusted during render (guarded), so
  // later polls never yank the chart and no effect fires an extra pass.
  if (dash && featured === null) {
    const wl = dash.watchlist?.sparks?.[0];
    const mover = dash.movers.gainers?.[0];
    if (wl) setFeatured({ symbol: wl.symbol, market: wl.market });
    else if (mover) setFeatured({ symbol: mover.symbol, market: "stocks" });
    else setFeatured({ symbol: "SPY", market: "stocks" });
  }

  const loggedIn = dash !== null && dash.watchlist !== null;

  // Clear session alerts the moment the session goes away (guarded render
  // adjustment — logging back in starts from the loading state again).
  if (!loggedIn && alerts !== null) setAlerts(null);

  // Session-scoped alerts — only once we know a session exists.
  useEffect(() => {
    if (!loggedIn) return;
    let alive = true;
    const load = () =>
      api
        .alerts(true, 8)
        .then((a) => {
          if (alive) setAlerts(a);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          // Only a 401 means the session is gone; transient/network errors
          // keep the last-known alerts — the freshness UI surfaces outages.
          if (isAuthError(e)) setAlerts(null);
        });
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [loggedIn]);

  return {
    dash,
    error,
    errorStatus,
    alerts,
    featured,
    loggedIn,
    refresh: () => {
      setError(null);
      setTick((t) => t + 1);
    },
  };
}
