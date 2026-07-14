"use client";

// LIVE tab data hook — answers "is the whole pipeline live?" in one place.
// Parallel-fetches the TradingView webhook status, its recent raw feed, the
// worker fleet, daemon health and dataset accounting, then polls on the fast
// tier through the managed pollMs() loop (pauses while the tab is hidden,
// backs off on consecutive failures, re-fires on the app-wide freshness
// Retry). Each source is settled independently so a single failing endpoint
// KEEPS the last-good value for the others — the page shows a "connection
// lost" strip, never a blank. loading is true only until the first pass lands.

import { useEffect, useState } from "react";
import {
  api,
  pollMs,
  POLL_FAST,
  tvSignals,
  tvStatus,
  type DataStats,
  type TVSignal,
  type TvStatus,
  type WorkerRun,
} from "@/lib/api";

/** /api/health payload (the shape api.health() resolves to). */
export interface Health {
  version: string;
  uptimeS: number;
  alpaca: boolean;
}

export interface LiveState {
  status: TvStatus | null;
  signals: TVSignal[] | null;
  workers: WorkerRun[] | null;
  health: Health | null;
  stats: DataStats | null;
  /** Last fetch error — with data present it means "stale but retrying". */
  error: string | null;
  loading: boolean;
  refresh: () => void;
}

export default function useLive(): LiveState {
  const [status, setStatus] = useState<TvStatus | null>(null);
  const [signals, setSignals] = useState<TVSignal[] | null>(null);
  const [workers, setWorkers] = useState<WorkerRun[] | null>(null);
  const [health, setHealth] = useState<Health | null>(null);
  const [stats, setStats] = useState<DataStats | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      const [s, sig, w, h, d] = await Promise.allSettled([
        tvStatus(),
        tvSignals(30),
        api.agents(),
        api.health(),
        api.dataStats(),
      ]);
      if (!alive) return;
      // First rejection (if any) surfaces as the page's error; a fulfilled
      // source always overwrites its own state, a rejected one keeps last-good.
      let firstErr: string | null = null;
      const note = (r: PromiseSettledResult<unknown>) => {
        if (r.status === "rejected" && firstErr === null) {
          firstErr = r.reason instanceof Error ? r.reason.message : String(r.reason);
        }
      };
      if (s.status === "fulfilled") setStatus(s.value);
      else note(s);
      if (sig.status === "fulfilled") setSignals(sig.value);
      else note(sig);
      if (w.status === "fulfilled") setWorkers(w.value);
      else note(w);
      if (h.status === "fulfilled") setHealth(h.value);
      else note(h);
      if (d.status === "fulfilled") setStats(d.value);
      else note(d);
      setError(firstErr);
      setLoading(false);
    };
    load();
    const stop = pollMs(load, POLL_FAST);
    return () => {
      alive = false;
      stop();
    };
  }, [tick]);

  return {
    status,
    signals,
    workers,
    health,
    stats,
    error,
    loading,
    refresh: () => {
      setError(null);
      setTick((t) => t + 1);
    },
  };
}
