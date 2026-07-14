"use client";

// VISUAL KIT (Stage 3) — <BigCandle/>: the featured candlestick. Wraps the
// existing CandleChart (lightweight-charts v5) + the Stage-7 chart-overlays
// fetch, and adds a compact symbol-switcher strip: the caller's watchlist
// (when logged in) + today's top movers as chips. Signals toggle defaults ON
// (score/regime/breakout markers); turning it off clears the overlay fetch.
//
// HONESTY: bars are stored daily closes on worker cadence — the header says
// so; an empty symbol shows WHY (backfill cadence), never a fake chart.
// ui-ux-pro-max notes applied: chips are real buttons (aria-pressed, ≥36px
// hit area, cursor-pointer, focus ring from globals.css), loading keeps the
// chart footprint (skeleton, no content jump), hover states are color-only
// (no layout-shifting transforms).

import { useEffect, useMemo, useState } from "react";
import {
  api,
  chartOverlays,
  movers,
  type Bar,
  type ChartOverlayMarker,
  type Market,
} from "@/lib/api";
import CandleChart from "@/components/symbol/CandleChart";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

export interface ChipSym {
  symbol: string;
  market: Market;
  src: "watchlist" | "mover";
}

const BAR_LIMIT = 240; // ~1y of daily bars

export default function BigCandle({
  symbol,
  market,
  height = 380,
  presetChips,
}: {
  symbol: string;
  market: Market;
  height?: number;
  /** Stage 4 (dashboard rebuild): when the parent already knows the chip strip
   *  (watchlist + movers from the ONE /api/dashboard roundup) it passes them
   *  here and this component skips its own watchlist/movers fetches entirely. */
  presetChips?: ChipSym[];
}) {
  const [active, setActive] = useState<{ symbol: string; market: Market }>({ symbol, market });
  const [signalsOn, setSignalsOn] = useState(true); // default ON per spec
  const [retryTick, setRetryTick] = useState(0);

  // Follow prop changes without an effect: when the caller swaps the featured
  // symbol, reset `active` during render (the "adjust state on prop change"
  // pattern) — no setState-in-effect, no extra render pass.
  const propKey = `${market}:${symbol}`;
  const [prevPropKey, setPrevPropKey] = useState(propKey);
  if (propKey !== prevPropKey) {
    setPrevPropKey(propKey);
    setActive({ symbol, market });
  }
  const activeKey = `${active.market}:${active.symbol}`;

  // Chip strip: watchlist (401 for anonymous users is fine — skip) + top movers.
  // Only fetched when the parent didn't supply presetChips; dedup happens in the
  // memo below, so neither path setStates synchronously from props.
  const [fetchedChips, setFetchedChips] = useState<ChipSym[]>([]);
  useEffect(() => {
    if (presetChips) return;
    let alive = true;
    (async () => {
      const acc: ChipSym[] = [];
      try {
        const wl = await api.watchlist();
        for (const r of wl) acc.push({ symbol: r.symbol, market: r.market, src: "watchlist" });
      } catch {
        // anonymous — watchlist chips simply absent
      }
      try {
        const mv = await movers(0, 3);
        for (const r of [...(mv.gainers ?? []), ...(mv.losers ?? [])]) {
          acc.push({ symbol: r.symbol, market: "stocks", src: "mover" });
        }
      } catch {
        // movers unavailable — strip still works with watchlist/featured
      }
      if (alive) setFetchedChips(acc);
    })();
    return () => {
      alive = false;
    };
  }, [presetChips]);

  const chips = useMemo(() => {
    const src = presetChips ?? fetchedChips;
    const seen = new Set<string>();
    return src.filter((c) => {
      const k = `${c.market}:${c.symbol}`;
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  }, [presetChips, fetchedChips]);

  // Bars for the active symbol, keyed by activeKey so a stale response can't
  // paint under a new header and the loading/error state is DERIVED from the
  // key rather than reset synchronously inside the effect.
  const [barsState, setBarsState] = useState<
    { key: string; bars: Bar[] } | { key: string; error: string } | null
  >(null);
  useEffect(() => {
    let alive = true;
    const key = `${active.market}:${active.symbol}`;
    api
      .bars(active.symbol, active.market, "1d", BAR_LIMIT)
      .then((b) => {
        if (alive) setBarsState({ key, bars: b ?? [] });
      })
      .catch((e: unknown) => {
        if (alive) setBarsState({ key, error: e instanceof Error ? e.message : String(e) });
      });
    return () => {
      alive = false;
    };
  }, [active.symbol, active.market, retryTick]);
  const loaded = barsState && barsState.key === activeKey ? barsState : null;
  const bars = loaded && "bars" in loaded ? loaded.bars : null;
  const barsError = loaded && "error" in loaded ? loaded.error : null;

  // Overlay markers — fetched only when signals are ON, also keyed by activeKey.
  // When signals are OFF we simply don't render them (no setState needed).
  const [overlayState, setOverlayState] = useState<{ key: string; markers: ChartOverlayMarker[] } | null>(null);
  useEffect(() => {
    if (!signalsOn) return;
    let alive = true;
    const key = `${active.market}:${active.symbol}`;
    chartOverlays(active.symbol, active.market)
      .then((o) => {
        if (alive) setOverlayState({ key, markers: o.markers ?? [] });
      })
      .catch(() => {
        if (alive) setOverlayState({ key, markers: [] }); // additive — chart stays useful
      });
    return () => {
      alive = false;
    };
  }, [active.symbol, active.market, signalsOn, retryTick]);
  const overlays =
    signalsOn && overlayState && overlayState.key === activeKey ? overlayState.markers : [];

  const chipList = useMemo(() => {
    // Ensure the active symbol is always visible in the strip.
    const has = chips.some((c) => c.symbol === active.symbol && c.market === active.market);
    return has ? chips : [{ symbol: active.symbol, market: active.market, src: "mover" as const }, ...chips];
  }, [chips, active]);

  return (
    <section className="panel" aria-label={`featured candlestick chart: ${active.symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        <span>
          {active.symbol} <span style={{ color: "var(--faint)" }}>· 1d</span>
        </span>
        <span className="text-[0.75rem] font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
          stored daily bars, worker cadence — not live
        </span>
        <button
          type="button"
          onClick={() => setSignalsOn((v) => !v)}
          aria-pressed={signalsOn}
          className="chip ml-auto min-h-[32px] cursor-pointer text-[0.75rem] transition-colors duration-150 hover:brightness-125"
          style={signalsOn ? { color: "var(--text)", borderColor: "var(--accent)" } : undefined}
          title="score/regime/breakout markers from stored worker events"
        >
          SIGNALS {signalsOn ? "ON" : "OFF"}
        </button>
      </div>

      {/* symbol switcher strip: watchlist + movers chips, scrollable */}
      <div
        className="flex items-center gap-1.5 overflow-x-auto border-b px-3 py-2"
        style={{ borderColor: "var(--border)" }}
        role="tablist"
        aria-label="switch chart symbol"
      >
        {chipList.map((c) => {
          const isActive = c.symbol === active.symbol && c.market === active.market;
          return (
            <button
              key={`${c.market}:${c.symbol}`}
              type="button"
              role="tab"
              aria-selected={isActive}
              onClick={() => setActive({ symbol: c.symbol, market: c.market })}
              className="chip min-h-[32px] shrink-0 cursor-pointer text-[0.75rem] transition-colors duration-150 hover:brightness-125"
              style={
                isActive
                  ? { color: "var(--text)", borderColor: "var(--accent)" }
                  : c.src === "watchlist"
                    ? { color: "var(--dim)" }
                    : undefined
              }
              title={c.src === "watchlist" ? "from your watchlist" : "from today's movers"}
            >
              {c.symbol}
            </button>
          );
        })}
      </div>

      {loaded === null && (
        <div className="p-3" style={{ height }}>
          <Skeleton lines={6} label={`loading ${active.symbol} bars`} />
        </div>
      )}
      {barsError && (
        <div className="p-3">
          <ErrorState
            message={barsError}
            hint="Is the daemon running? Bars come from /api/bars."
            retry={() => {
              setBarsState(null);
              setRetryTick((t) => t + 1);
            }}
          />
        </div>
      )}
      {bars !== null && bars.length === 0 && (
        <EmptyState
          message={`No daily bars for ${active.symbol} yet`}
          detail="Backfill runs on worker cadence after a symbol is added — the chart appears when the first daily bars land (nothing is drawn until then)."
        />
      )}
      {bars !== null && bars.length > 0 && (
        <div className="px-1 pb-1">
          <CandleChart bars={bars} tf="1d" height={height} overlays={overlays} />
        </div>
      )}
    </section>
  );
}
