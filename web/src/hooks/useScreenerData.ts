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
  // A FAILED ranking fetch is not an empty ranking. Swallowing it left `ranking`
  // null, screenerModel read it as `ranking ?? []`, every row came back with
  // rank === null, and ScreenerTable rendered "—" under the tooltip "not in the
  // latest ranking pass" — a factual assertion that the pass RAN and excluded
  // the symbol. The rows path in this same hook already splits loading/error/
  // empty correctly, which is exactly why the enricher columns leaked.
  const [rankingFailed, setRankingFailed] = useState(false);

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
        .then((r) => {
          if (!alive) return;
          setRanking(r ?? []);
          setRankingFailed(false);
        })
        .catch(() => {
          if (alive) setRankingFailed(true);
        });
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

  return { rows, err, ranking, regimes, rankingFailed, retry };
}
