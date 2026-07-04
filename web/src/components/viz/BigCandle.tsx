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
  const [bars, setBars] = useState<Bar[] | null>(null);
  const [overlays, setOverlays] = useState<ChartOverlayMarker[]>([]);
  const [signalsOn, setSignalsOn] = useState(true); // default ON per spec
  const [chips, setChips] = useState<ChipSym[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // Follow prop changes (e.g. the page swaps the featured symbol).
  useEffect(() => setActive({ symbol, market }), [symbol, market]);

  // Chip strip: watchlist (401 for anonymous users is fine — just skip) +
  // top movers. Loaded once; deduped, watchlist first. Skipped entirely when
  // the parent supplies presetChips (single-roundup dashboards).
  useEffect(() => {
    if (presetChips) {
      const seen = new Set<string>();
      setChips(
        presetChips.filter((c) => {
          const k = `${c.market}:${c.symbol}`;
          if (seen.has(k)) return false;
          seen.add(k);
          return true;
        })
      );
      return;
    }
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
      if (!alive) return;
      const seen = new Set<string>();
      setChips(
        acc.filter((c) => {
          const k = `${c.market}:${c.symbol}`;
          if (seen.has(k)) return false;
          seen.add(k);
          return true;
        })
      );
    })();
    return () => {
      alive = false;
    };
  }, [presetChips]);

  // Bars + (when signals ON) overlay markers for the active symbol.
  useEffect(() => {
    let alive = true;
    setBars(null);
    setError(null);
    api
      .bars(active.symbol, active.market, "1d", BAR_LIMIT)
      .then((b) => {
        if (alive) setBars(b ?? []);
      })
      .catch((e: unknown) => {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [active, retryTick]);

  useEffect(() => {
    let alive = true;
    if (!signalsOn) {
      setOverlays([]);
      return;
    }
    chartOverlays(active.symbol, active.market)
      .then((o) => {
        if (alive) setOverlays(o.markers ?? []);
      })
      .catch(() => {
        if (alive) setOverlays([]); // overlays are additive — chart stays useful
      });
    return () => {
      alive = false;
    };
  }, [active, signalsOn, retryTick]);

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
        <span className="text-[0.65rem] font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
          stored daily bars, worker cadence — not live
        </span>
        <button
          type="button"
          onClick={() => setSignalsOn((v) => !v)}
          aria-pressed={signalsOn}
          className="chip ml-auto min-h-[32px] cursor-pointer text-[0.65rem] transition-colors duration-150 hover:brightness-125"
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
              className="chip min-h-[32px] shrink-0 cursor-pointer text-[0.68rem] transition-colors duration-150 hover:brightness-125"
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

      {bars === null && !error && (
        <div className="p-3" style={{ height }}>
          <Skeleton lines={6} label={`loading ${active.symbol} bars`} />
        </div>
      )}
      {bars === null && error && (
        <div className="p-3">
          <ErrorState
            message={error}
            hint="Is the daemon running? Bars come from /api/bars."
            retry={() => {
              setError(null);
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
          <CandleChart bars={bars} tf="1d" height={height} overlays={signalsOn ? overlays : []} />
        </div>
      )}
    </section>
  );
}
