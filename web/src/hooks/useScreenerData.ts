"use client";

// Screener data loop, extracted from the page split. Whole-universe PUBLIC
// read (Stage 5) — the screener is the universe table, not the per-user
// watchlist, so it renders logged out too. Rank + regime enrichers fail soft:
// their columns show "—" instead of taking the whole table down.

import { useCallback, useEffect, useState } from "react";
import {
  api,
  pollMs,
  POLL_DEFAULT,
  screenerRows,
  type RankedRow,
  type RegimeState,
  type WatchRow,
} from "@/lib/api";

export function useScreenerData() {
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [ranking, setRanking] = useState<RankedRow[] | null>(null);
  const [regimes, setRegimes] = useState<RegimeState[] | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () => {
      screenerRows()
        .then((r) => {
          if (!alive) return;
          setRows(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
      api
        .ranking()
        .then((r) => alive && setRanking(r ?? []))
        .catch(() => {});
      api
        .regime()
        .then((r) => alive && setRegimes(r.states ?? []))
        .catch(() => {});
    };
    load();
    // Screener rows move on worker cadence — the default tier is plenty.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const retry = useCallback(() => {
    setErr(null);
    setRetryTick((t) => t + 1);
  }, []);

  return { rows, err, ranking, regimes, retry };
}
