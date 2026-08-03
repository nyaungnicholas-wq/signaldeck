"use client";

import { useEffect, useState, useRef, useCallback, useMemo } from "react";
import Link from "next/link";
import { api, type WatchRow, screenerRows } from "@/lib/api";
import { fmtPrice } from "@/lib/format";
import { Reveal, Spark, StatTile, PageHero, DeltaBadge } from "@/components/ui/Kit";

export default function WatchlistPage() {
  const [watchlist, setWatchlist] = useState<WatchRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [undo, setUndo] = useState<{
    symbol: string;
    market: "crypto" | "stocks";
    timer: ReturnType<typeof setTimeout>;
  } | null>(null);

  // The pending-undo timer lives in a ref as well as in state. The unmount
  // cleanup below needs the CURRENT timer, but `undo` was never a dependency of
  // that effect, so the cleanup closed over the initial null and cleared
  // nothing — unmounting mid-undo left a 5s timer that then called setState on
  // a dead component. A ref is always current without making the effect re-run
  // (and re-fetch) on every undo.
  const undoTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const refetch = useCallback(() => {
    api.watchlist().then(setWatchlist).catch((e) => setError(String(e)));
  }, []);

  useEffect(() => {
    refetch();
    return () => { if (undoTimer.current) clearTimeout(undoTimer.current); };
  }, [refetch]);

  const remove = useCallback(
    (symbol: string, market: "crypto" | "stocks") => {
      const timer = setTimeout(() => {
        undoTimer.current = null;
        setUndo(null);
        refetch();
      }, 5000);
      undoTimer.current = timer;
      setUndo({ symbol, market, timer });
      setWatchlist((prev) =>
        prev ? prev.filter((r) => !(r.symbol === symbol && r.market === market)) : prev
      );
    },
    [refetch]
  );

  const undoRemove = useCallback(() => {
    if (!undo) return;
    clearTimeout(undo.timer);
    undoTimer.current = null;
    api.subscribe(undo.symbol, undo.market).then(refetch).catch(refetch);
    setUndo(null);
  }, [undo, refetch]);

  const stats = useMemo(() => {
    if (!watchlist || watchlist.length === 0)
      return { count: 0, best: null, worst: null };
    const sorted = [...watchlist].sort(
      (a, b) => (b.dayChangePct ?? 0) - (a.dayChangePct ?? 0)
    );
    return {
      count: watchlist.length,
      best: sorted[0],
      worst: sorted[sorted.length - 1],
    };
  }, [watchlist]);

  const removeHandler = (symbol: string, market: "crypto" | "stocks") => {
    api.unsubscribe(symbol, market).catch(() => {});
  };

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Watchlist"
        live
        subtitle="Your symbols, your radar — add anything from the universe and every signal tracks it for you."
        right={<AddSymbol refetch={refetch} />}
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <StatTile
          label="Symbols Tracked"
          value={stats.count}
          glow="hud"
          i={0}
        />
        {stats.best && (
          <StatTile
            label="Best 24h"
            value={stats.best.symbol}
            sub={fmtPrice(stats.best.lastClose ?? 0)}
            delta={stats.best.dayChangePct ?? 0}
            glow="up"
            i={1}
          />
        )}
        {stats.worst && (
          <StatTile
            label="Worst 24h"
            value={stats.worst.symbol}
            sub={fmtPrice(stats.worst.lastClose ?? 0)}
            delta={stats.worst.dayChangePct ?? 0}
            glow="down"
            i={2}
          />
        )}
      </div>

      {error && (
        <div className="panel p-4" style={{ color: "var(--ask)" }}>
          {error}
        </div>
      )}

      {!watchlist ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="panel h-32 animate-pulse" />
          ))}
        </div>
      ) : watchlist.length === 0 ? (
        <div className="hud-panel p-8 text-center" style={{ color: "var(--dim)" }}>
          <p className="mb-2 text-lg font-medium">Nothing on your watchlist yet</p>
          <p className="text-sm">
            Search above to add a symbol — each one becomes a tracked card here, with its
            signals, alerts and news.
          </p>
          <p className="mt-1 text-sm" style={{ color: "var(--faint)" }}>
            Not sure where to start? Try SPY or NVDA.
          </p>
        </div>
      ) : (
        <Reveal>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {watchlist.map((row, i) => (
              <WatchlistCard
                key={`${row.market}-${row.symbol}`}
                row={row}
                i={i}
                onRemove={() => remove(row.symbol, row.market)}
                removeHandler={removeHandler}
                isUndo={undo?.symbol === row.symbol && undo?.market === row.market}
                onUndo={undoRemove}
              />
            ))}
          </div>
        </Reveal>
      )}
    </div>
  );
}

function WatchlistCard({
  row,
  i,
  onRemove,
  removeHandler,
  isUndo,
  onUndo,
}: {
  row: WatchRow;
  i: number;
  onRemove: () => void;
  removeHandler: (symbol: string, market: "crypto" | "stocks") => void;
  isUndo: boolean;
  onUndo: () => void;
}) {
  const [pending, setPending] = useState(false);

  const handleRemove = () => {
    setPending(true);
    removeHandler(row.symbol, row.market);
    onRemove();
    setPending(false);
  };

  const sym = encodeURIComponent(row.symbol);
  return (
    <article
      className="panel reveal-item relative p-4 flex flex-col gap-2"
      style={{ "--i": i } as React.CSSProperties}
    >
      {isUndo ? (
        <div className="flex items-center justify-center h-full gap-2">
          <span style={{ color: "var(--ask)" }}>removed</span>
          <button
            onClick={onUndo}
            className="chip px-3 py-1 text-[0.75rem] cursor-pointer hover:bg-[rgba(255,255,255,0.06)]"
            style={{ color: "var(--accent)" }}
          >
            undo
          </button>
        </div>
      ) : (
        <>
          <button
            onClick={handleRemove}
            disabled={pending}
            className="absolute top-2 right-2 w-6 h-6 flex items-center justify-center rounded-full hover:bg-[rgba(255,255,255,0.1)] cursor-pointer"
            aria-label={`Remove ${row.symbol}`}
          >
            <svg width="12" height="12" viewBox="0 0 12 12">
              <line x1="3" y1="3" x2="9" y2="9" stroke="var(--dim)" strokeWidth="1.5" strokeLinecap="round" />
              <line x1="9" y1="3" x2="3" y2="9" stroke="var(--dim)" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </button>

          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <Link
              href={`/s/${row.market}/${sym}`}
              className="mono cursor-pointer text-base font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
            >
              {row.symbol}
            </Link>
            <span className="min-w-0 truncate text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {row.name}
            </span>
            <span className="tnum ml-auto text-[0.85rem]">{fmtPrice(row.lastClose)}</span>
            {row.dayChangePct != null && <DeltaBadge value={row.dayChangePct} />}
          </div>

          {row.spark && (
            <div className="mt-1">
              <Spark data={row.spark} width={200} height={36} />
            </div>
          )}

          <div className="mt-auto pt-2">
            <Link
              href={`/signals/report/${row.market}/${sym}?kind=overview`}
              className="chip inline-flex items-center px-3 py-1 text-[0.75rem] cursor-pointer transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
            >
              Report
            </Link>
          </div>
        </>
      )}
    </article>
  );
}

function AddSymbol({ refetch }: { refetch: () => void }) {
  const [query, setQuery] = useState("");
  const [, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [screenerData, setScreenerData] = useState<Awaited<ReturnType<typeof screenerRows>> | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const loadScreener = async () => {
    if (screenerData) return;
    setLoading(true);
    try {
      const data = await screenerRows();
      setScreenerData(data);
    } catch {
      setError("Failed to load universe");
    } finally {
      setLoading(false);
    }
  };

  // Derived, not stored. This was an effect that called setMatches during the
  // effect body, which schedules a second render for every keystroke
  // (react-hooks/set-state-in-effect) and briefly paints a stale list. Matches
  // are a pure function of the query and the loaded universe, so compute them.
  const matches = useMemo(() => {
    if (!query || !screenerData) return [];
    const q = query.toLowerCase();
    return screenerData
      .filter(
        (r) =>
          r.symbol.toLowerCase().includes(q) ||
          r.name?.toLowerCase().includes(q)
      )
      .slice(0, 8);
  }, [query, screenerData]);

  const add = async (symbol: string, market: "crypto" | "stocks") => {
    setPending(symbol);
    setError(null);
    try {
      await api.subscribe(symbol, market);
      setQuery(""); // clearing the query empties `matches` — it is derived now
      refetch();
    } catch {
      setError("Failed to add symbol");
    } finally {
      setPending(null);
    }
  };

  return (
    <div ref={containerRef} className="relative">
      <input
        ref={inputRef}
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onFocus={loadScreener}
        placeholder="Add symbol..."
        className="panel w-64 px-3 py-2 text-sm mono focus:outline-none focus:ring-1 focus:ring-[var(--accent)]"
        style={{ backgroundColor: "var(--bg)" }}
      />
      {error && (
        <div className="absolute top-full mt-1 right-0 panel p-2 text-xs" style={{ color: "var(--ask)" }}>
          {error}
        </div>
      )}
      {matches.length > 0 && (
        <div className="absolute top-full mt-1 right-0 panel w-80 max-h-96 overflow-y-auto z-50">
          {matches.map((m) => (
            <div
              key={m.symbol}
              className="flex items-center justify-between px-3 py-2 border-b border-[rgba(255,255,255,0.06)] last:border-0 hover:bg-[rgba(255,255,255,0.06)]"
            >
              <div className="flex-1 min-w-0">
                <span className="mono text-sm font-medium">{m.symbol}</span>
                <span className="ml-2 text-[0.75rem] truncate" style={{ color: "var(--faint)" }}>
                  {m.name}
                </span>
              </div>
              {m.spark && (
                <div className="mx-2 flex-shrink-0">
                  <Spark data={m.spark} width={60} height={20} />
                </div>
              )}
              <button
                onClick={() => add(m.symbol, m.market)}
                disabled={pending === m.symbol}
                className="chip px-2 py-1 text-[0.75rem] cursor-pointer hover:bg-[rgba(255,255,255,0.1)] disabled:opacity-50"
              >
                {pending === m.symbol ? "..." : "Add"}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
