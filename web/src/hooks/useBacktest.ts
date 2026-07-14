"use client";

// Backtest page state hook — the state/fetching half of the old monolithic
// /lab/backtest page (pure refactor; behavior identical). The watchlist feeds
// the symbol picker on the SLOW tier (the picker universe moves slowly), with
// the signed-out fallback to the public screener; run() posts the plain-English
// rule and splits the daemon's helpful parse/history feedback (soft) from a
// hard outage.

import { useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  pollMs,
  POLL_SLOW,
  screenerRows,
  type BacktestResponse,
  type Market,
  type WatchRow,
} from "@/lib/api";

/** A hard error is the daemon being unreachable; a soft error is a strategy the
 * parser couldn't handle or a symbol without enough history. Soft errors are the
 * API's helpful, expected feedback — shown in --warn, never as a crash. */
function isSoftError(msg: string): boolean {
  const m = msg.toLowerCase();
  return (
    m.includes("supported forms") ||
    m.includes("could not parse") ||
    m.includes("not enough history") ||
    m.includes("unknown symbol")
  );
}

export interface BacktestState {
  // symbol universe (for the picker chips)
  watch: WatchRow[] | null;
  watchErr: string | null;
  retryWatch: () => void;
  // form state
  text: string;
  setText: (t: string) => void;
  symbol: string | null;
  market: Market;
  pick: (symbol: string, market: Market) => void;
  selectedRow: WatchRow | null;
  // run state
  run: () => void;
  running: boolean;
  canRun: boolean;
  resp: BacktestResponse | null;
  ranFor: { symbol: string; text: string } | null;
  softErr: string | null;
  hardErr: string | null;
}

export default function useBacktest(): BacktestState {
  // symbol universe (for the picker chips)
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);

  // form state
  const [text, setText] = useState("");
  const [symbol, setSymbol] = useState<string | null>(null);
  const [market, setMarket] = useState<Market>("crypto");

  // run state
  const [running, setRunning] = useState(false);
  const [resp, setResp] = useState<BacktestResponse | null>(null);
  const [ranFor, setRanFor] = useState<{ symbol: string; text: string } | null>(
    null,
  );
  const [softErr, setSoftErr] = useState<string | null>(null);
  const [hardErr, setHardErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // True once any watchlist load has populated the picker. The poll closure
  // below outlives many renders, so reading `watch` there would stay frozen
  // at its mount value (null) and re-run the first-load sync every poll.
  const hasLoadedRef = useRef(false);

  // Load the watchlist once for the symbol picker; poll (slow tier) so
  // newly-subscribed symbols appear. The picker never mutates the running
  // result.
  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .watchlist()
        .then((r) => {
          if (!alive) return;
          const firstLoad = !hasLoadedRef.current;
          hasLoadedRef.current = true;
          setWatch(r);
          setWatchErr(null);
          setSymbol((cur) => {
            if (cur && r.some((row) => row.symbol === cur)) return cur;
            return r.length ? r[0].symbol : null;
          });
          setMarket((curMkt) => {
            // keep market in sync with the auto-selected symbol on first load
            const first = r[0];
            return first && firstLoad ? first.market : curMkt;
          });
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          // Stage 5: signed out the watchlist 401s — fall back to the public
          // universe screener (top 24 by |1d score|) so the picker works.
          if (msg.includes("401")) {
            screenerRows()
              .then((all) => {
                if (!alive) return;
                hasLoadedRef.current = true;
                const top = [...all]
                  .sort(
                    (a, b) =>
                      Math.abs(b.scores?.["1d"]?.score ?? 0) -
                      Math.abs(a.scores?.["1d"]?.score ?? 0),
                  )
                  .slice(0, 24);
                setWatch(top);
                setWatchErr(null);
                setSymbol((cur) => {
                  if (cur && top.some((row) => row.symbol === cur)) return cur;
                  return top.length ? top[0].symbol : null;
                });
              })
              .catch((e2: unknown) => {
                if (!alive) return;
                setWatchErr(e2 instanceof Error ? e2.message : String(e2));
              });
            return;
          }
          setWatchErr(msg);
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const selectedRow = useMemo(
    () => watch?.find((r) => r.symbol === symbol && r.market === market) ?? null,
    [watch, symbol, market],
  );

  const run = () => {
    const t = text.trim();
    if (!symbol || !t || running) return;
    setRunning(true);
    setSoftErr(null);
    setHardErr(null);
    api
      .backtest(symbol, market, t)
      .then((r) => {
        setResp(r);
        setRanFor({ symbol, text: t });
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        if (isSoftError(msg)) {
          setSoftErr(msg);
        } else {
          setHardErr(msg);
        }
        setResp(null);
        setRanFor(null);
      })
      .finally(() => setRunning(false));
  };

  const canRun = Boolean(symbol) && text.trim().length > 0 && !running;

  return {
    watch,
    watchErr,
    retryWatch: () => {
      setWatchErr(null);
      setRetryTick((t) => t + 1);
    },
    text,
    setText,
    symbol,
    market,
    pick: (s, m) => {
      setSymbol(s);
      setMarket(m);
    },
    selectedRow,
    run,
    running,
    canRun,
    resp,
    ranFor,
    softErr,
    hardErr,
  };
}
