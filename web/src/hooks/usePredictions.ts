"use client";

// Data hooks for the PREDICT page (signals/predictions). The page composes
// UI; these own the state machines and the polling loops via the managed
// pollMs form (hidden-tab pause, failure backoff, freshness retry). Moved
// verbatim from the old monolithic page — behavior identical.

import { useEffect, useState } from "react";
import {
  api,
  pollMs,
  POLL_DEFAULT,
  POLL_SLOW,
  screenerRows,
  type Calibration,
  type Composite,
  type Market,
  type WatchRow,
  isAuthError,
} from "@/lib/api";

export type CalHorizon = "1d" | "1w";

export interface Picked {
  symbol: string;
  market: Market;
}

/** Watchlist for the symbol picker. Logged-out fallback — the watchlist is
 *  session-scoped (401 when signed out), so the picker falls back to the
 *  strongest-scored symbols from the PUBLIC universe screener instead of
 *  erroring the whole page. */
export function usePredictionWatchlist(retryTick: number) {
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);
  const [picked, setPicked] = useState<Picked | null>(null);
  const [anonPicker, setAnonPicker] = useState(false);

  useEffect(() => {
    let alive = true;
    const applyList = (w: WatchRow[]) => {
      setWatch(w);
      setWatchErr(null);
      // Default to the first symbol once, without clobbering the user's pick.
      setPicked((cur) => {
        if (cur && w.some((r) => r.symbol === cur.symbol && r.market === cur.market)) return cur;
        const first = w[0];
        return first ? { symbol: first.symbol, market: first.market } : cur;
      });
    };
    const load = () =>
      api
        .watchlist()
        .then((w) => {
          if (!alive) return;
          setAnonPicker(false);
          applyList(w);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (!isAuthError(e)) {
            setWatchErr(msg);
            return;
          }
          // Signed out → top 24 universe symbols by |1d score| (public read).
          screenerRows()
            .then((rows) => {
              if (!alive) return;
              const top = [...rows]
                .sort(
                  (a, b) =>
                    Math.abs(b.scores?.["1d"]?.score ?? 0) - Math.abs(a.scores?.["1d"]?.score ?? 0),
                )
                .slice(0, 24);
              setAnonPicker(true);
              applyList(top);
            })
            .catch((e2: unknown) => {
              if (!alive) return;
              setWatchErr(e2 instanceof Error ? e2.message : String(e2));
            });
        });
    load();
    // POLL_DEFAULT tier — managed loop (hidden-tab pause, failure backoff).
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  return { watch, watchErr, setWatchErr, picked, setPicked, anonPicker };
}

/** Composite SignalScore for the picked symbol. The payload is keyed by the
 *  symbol it was fetched for, so switching symbols never flashes a stale
 *  verdict (mismatched keys read as loading — no synchronous state reset
 *  inside the effect needed). */
export function useComposite(picked: Picked | null, retryTick: number) {
  const [compState, setCompState] = useState<{
    key: string;
    data: Composite | null;
    err: string | null;
    at: number;
  }>({ key: "", data: null, err: null, at: 0 });

  useEffect(() => {
    if (!picked) return;
    const key = `${picked.market}:${picked.symbol}`;
    let alive = true;
    const load = () =>
      api
        .composite(picked.symbol, picked.market)
        .then((c) => {
          if (!alive) return;
          setCompState({ key, data: c, err: null, at: Math.floor(Date.now() / 1000) });
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          setCompState((s) => ({ key, data: null, err: msg, at: s.key === key ? s.at : 0 }));
        });
    load();
    // POLL_DEFAULT tier — the scorer runs on stored predictions, not ticks.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [picked, retryTick]);

  const pickKey = picked ? `${picked.market}:${picked.symbol}` : "";
  const comp = compState.key === pickKey ? compState.data : null;
  const compErr = compState.key === pickKey ? compState.err : null;
  const compAt = compState.key === pickKey ? compState.at : 0;

  return {
    comp,
    compErr,
    compAt,
    resetComp: () => setCompState({ key: "", data: null, err: null, at: 0 }),
  };
}

/** Past-signal history: calibration of resolved outcomes for one horizon.
 *  Only a payload tagged with the selected horizon is trusted. */
export function useCalibration(horizon: CalHorizon, retryTick: number) {
  const [cal, setCal] = useState<Calibration | null>(null);
  const [calErr, setCalErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .calibration(horizon)
        .then((c) => {
          if (!alive) return;
          setCal(c);
          setCalErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setCalErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // POLL_SLOW tier — outcomes resolve as horizons (1d/1w) elapse.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [horizon, retryTick]);

  const current = cal && cal.horizon === horizon ? cal : null;
  return {
    cal: current,
    calLoading: cal === null && calErr === null,
    calErr: cal === null && calErr !== null ? calErr : null,
    clearCalErr: () => setCalErr(null),
  };
}
