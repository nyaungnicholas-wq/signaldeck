"use client";

// Portfolio page state hook — the state/fetching half of the old monolithic
// /lab/portfolio page (pure refactor; behavior identical). Positions +
// watchlist ride the DEFAULT tier (they mark to the latest close, not the
// tape); correlation is a heavier query, so it loads once and refreshes on
// demand.

import { useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  pollMs,
  POLL_DEFAULT,
  type CorrelationResponse,
  type PortfolioResponse,
  type WatchRow,
} from "@/lib/api";

export interface PortfolioState {
  pf: PortfolioResponse | null;
  pfErr: string | null;
  watch: WatchRow[] | null;
  retry: () => void;
  /** Immediate re-fetch after a mutation (log / close) — no wait for the poll. */
  forcePoll: () => void;
  corr: CorrelationResponse | null;
  corrErr: string | null;
  corrRefreshing: boolean;
  loadCorr: () => void;
}

export default function usePortfolio(): PortfolioState {
  const [pf, setPf] = useState<PortfolioResponse | null>(null);
  const [pfErr, setPfErr] = useState<string | null>(null);
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  const [corr, setCorr] = useState<CorrelationResponse | null>(null);
  const [corrErr, setCorrErr] = useState<string | null>(null);
  const [corrRefreshing, setCorrRefreshing] = useState(false);

  // Unmount guard for the imperative loadCorr()/forcePoll() below — same job
  // as the `alive` flags in the effects, but for user-action requests.
  const aliveRef = useRef(true);
  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
    };
  }, []);

  // Default-tier poll: portfolio + watchlist (watchlist feeds the picker).
  useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .portfolio()
        .then((d) => {
          if (!alive) return;
          setPf(d);
          setPfErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setPfErr(e instanceof Error ? e.message : String(e));
        });
      api
        .watchlist()
        .then((w) => alive && setWatch(w))
        .catch(() => {
          /* picker just stays empty; portfolio error covers the outage */
        });
    };
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  // Correlation: load once, refresh on demand (heavier query, not polled).
  // The initial mount load skips the "refreshing" flag (the panel already
  // shows its skeleton while corr === null), so the effect only talks to the
  // external API and sets state in callbacks.
  useEffect(() => {
    let alive = true;
    api
      .correlation()
      .then((c) => {
        if (!alive) return;
        setCorr(c);
        setCorrErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setCorrErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, []);

  const loadCorr = useMemo(
    () => () => {
      setCorrRefreshing(true);
      return api
        .correlation()
        .then((c) => {
          if (!aliveRef.current) return;
          setCorr(c);
          setCorrErr(null);
        })
        .catch((e: unknown) => {
          if (!aliveRef.current) return;
          setCorrErr(e instanceof Error ? e.message : String(e));
        })
        .finally(() => {
          if (aliveRef.current) setCorrRefreshing(false);
        });
    },
    [],
  );

  const forcePoll = useMemo(
    () => () => {
      api
        .portfolio()
        .then((d) => {
          if (!aliveRef.current) return;
          setPf(d);
          setPfErr(null);
        })
        .catch((e: unknown) => {
          if (aliveRef.current) setPfErr(e instanceof Error ? e.message : String(e));
        });
    },
    [],
  );

  return {
    pf,
    pfErr,
    watch,
    retry: () => {
      setPfErr(null);
      setRetryTick((t) => t + 1);
    },
    forcePoll,
    corr,
    corrErr,
    corrRefreshing,
    loadCorr,
  };
}
