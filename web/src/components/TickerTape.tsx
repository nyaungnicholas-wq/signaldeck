"use client";

// TICKER TAPE (Signal8 wave, Stage 4): slim auto-scrolling strip at the top
// of the home page — index ETFs (SPY/QQQ/DIA/IWM), sector ETFs (XLK/XLF/XLE/
// XLV), BTC, and VIX. One /api/tape call, polled every 60s.
//
// HONESTY: prices are STORED DAILY CLOSES refreshed on worker cadence — not
// live quotes — and the strip's title tooltip + VIX chip say so (VIX is the
// FRED VIXCLS daily close, ~1 trading day behind). A member with no bars yet
// shows an em-dash, never a fabricated number.
//
// Motion: the track duplicates its content and translates -50% in a loop
// (pause on hover/focus). Under prefers-reduced-motion the global CSS kills
// the animation, so the media query below also hides the duplicate copy and
// falls back to plain horizontal scrolling.
//
// FAILURE (freshness wave): the strip never silently unmounts. Before the
// FIRST successful load it shows a slim skeleton; once data has loaded, a
// failed poll keeps the last-known tape frozen + desaturated with an inline
// "stale Xm · retry" affordance wired to the shared freshness bus.

import Link from "next/link";
import { useEffect, useState } from "react";
import { tape, type TapeItem, type TapeResponse } from "@/lib/api";
import { onRetry, requestRetry } from "@/lib/freshness";
import { fmtPct, fmtPrice } from "@/lib/format";

function itemHref(it: TapeItem): string {
  if (it.kind === "vix") return "/markets/macro";
  const market = it.market ?? "stocks";
  return `/s/${market}/${encodeURIComponent(it.symbol)}`;
}

function changeColor(v: number): string {
  if (!Number.isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

function staleLabel(lastOkMs: number): string {
  const s = Math.max(0, Math.floor((Date.now() - lastOkMs) / 1000));
  if (s < 60) return `stale ${s}s`;
  if (s < 3600) return `stale ${Math.floor(s / 60)}m`;
  return `stale ${Math.floor(s / 3600)}h`;
}

function TapeEntry({ it }: { it: TapeItem }) {
  return (
    <Link
      href={itemHref(it)}
      title={`${it.label}${it.note ? ` — ${it.note}` : ""}`}
      className="flex shrink-0 cursor-pointer items-baseline gap-1.5 px-3 py-1.5 text-[0.75rem] transition-colors duration-150 hover:text-[var(--accent)]"
    >
      <span className="mono font-bold tracking-wide">{it.kind === "vix" ? "VIX" : it.symbol}</span>
      {it.hasData ? (
        <>
          <span className="tnum" style={{ color: "var(--dim)" }}>
            {fmtPrice(it.price)}
          </span>
          <span className="tnum" style={{ color: changeColor(it.dayChangePct) }}>
            {fmtPct(it.dayChangePct)}
          </span>
        </>
      ) : (
        <span style={{ color: "var(--faint)" }} title="no bars stored yet — universe seeding">
          —
        </span>
      )}
      {it.kind === "vix" && (
        <span className="text-[0.75rem] tracking-wider" style={{ color: "var(--faint)" }}>
          FRED·1d lag
        </span>
      )}
    </Link>
  );
}

export default function TickerTape({
  // Stage 4 (dashboard rebuild): when the parent already holds the tape from
  // the ONE /api/dashboard roundup it passes items/note here and this strip
  // does NOT fetch on its own (no duplicate /api/tape call). Omit the prop
  // (undefined) for the original self-fetching behavior.
  items: itemsProp,
  note: noteProp,
}: {
  items?: TapeItem[] | null;
  note?: string;
} = {}) {
  const [resp, setResp] = useState<TapeResponse | null>(null);
  const [lastOkMs, setLastOkMs] = useState<number | null>(null);
  // Consecutive failed polls. Bumping (not a boolean) re-renders every failed
  // cycle so the "stale Xm" age below stays current without an extra timer.
  const [failCount, setFailCount] = useState(0);
  const driven = itemsProp !== undefined;

  useEffect(() => {
    if (driven) return; // parent-driven: no self-fetch, no poll
    let alive = true;
    const load = () =>
      tape()
        .then((r) => {
          if (!alive) return;
          setResp(r);
          setLastOkMs(Date.now());
          setFailCount(0);
        })
        .catch(() => alive && setFailCount((n) => n + 1)); // keep last-known tape; stale chip renders below
    load();
    const t = setInterval(load, 60_000);
    const offRetry = onRetry(load); // shared Retry affordances re-fire the fetch immediately
    return () => {
      alive = false;
      clearInterval(t);
      offRetry();
    };
  }, [driven]);

  const items = driven ? itemsProp : (resp?.items ?? null);
  const note = driven ? noteProp : resp?.note;
  const failed = !driven && failCount > 0;

  if (items == null) {
    if (failed) {
      // First load failed — say so instead of vanishing.
      return (
        <section aria-label="market ticker tape" className="panel">
          <div
            role="status"
            className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1 text-[0.75rem]"
          >
            <span style={{ color: "var(--bad)" }}>tape unavailable</span>
            <span style={{ color: "var(--dim)" }}>daemon unreachable — retrying every 60s</span>
            <button
              type="button"
              onClick={() => requestRetry()}
              className="chip min-h-[40px] cursor-pointer border-[var(--accent)] px-4 text-[var(--accent)] transition-colors duration-150 hover:text-[var(--text)]"
            >
              retry
            </button>
          </div>
        </section>
      );
    }
    // Not loaded yet: slim shimmer matching the strip height (first load only).
    return (
      <section aria-label="market ticker tape" className="panel">
        <div role="status" aria-label="loading market tape" className="px-3 py-2">
          <div className="skeleton-bar h-4 rounded" />
          <span className="sr-only">loading market tape…</span>
        </div>
      </section>
    );
  }

  if (items.length === 0) return null; // loaded, genuinely empty → no strip (honest quiet)

  const track = items.map((it) => <TapeEntry key={`${it.kind}:${it.symbol}`} it={it} />);

  return (
    <section aria-label="market ticker tape" className="panel" title={note}>
      <style>{`
        .tape-wrap { overflow: hidden; }
        .tape-track {
          display: flex;
          width: max-content;
          animation: tape-scroll 55s linear infinite;
        }
        .tape-track:hover, .tape-track:focus-within { animation-play-state: paused; }
        @keyframes tape-scroll {
          from { transform: translateX(0); }
          to { transform: translateX(-50%); }
        }
        @media (prefers-reduced-motion: reduce) {
          .tape-wrap { overflow-x: auto; -webkit-overflow-scrolling: touch; }
          .tape-copy-b { display: none; }
        }
        /* Stale (poll failing): freeze the marquee, drop the loop copy, allow
           plain horizontal scrolling — same fallback as reduced motion. */
        .tape-stale { overflow-x: auto; -webkit-overflow-scrolling: touch; }
        .tape-stale .tape-track { animation: none; }
        .tape-stale .tape-copy-b { display: none; }
      `}</style>
      <div className="flex items-center">
        <div
          className={`tape-wrap min-w-0 flex-1${failed ? " tape-stale" : ""}`}
          // Desaturate rather than fade: greyed reads "not live" without
          // dropping text below AA contrast.
          style={failed ? { filter: "grayscale(1)" } : undefined}
        >
          <div className="tape-track">
            <div className="flex" style={{ borderRight: "1px solid var(--border)" }}>
              {track}
            </div>
            {/* duplicate copy for the seamless loop — hidden from AT + reduced motion */}
            <div className="tape-copy-b flex" aria-hidden="true">
              {track}
            </div>
          </div>
        </div>
        {failed && lastOkMs !== null && (
          <div
            role="status"
            className="flex shrink-0 items-center gap-2 self-stretch border-l px-3 py-1 text-[0.75rem]"
            style={{ borderColor: "var(--border)" }}
          >
            <span className="tnum whitespace-nowrap" style={{ color: "var(--warn)" }}>
              {staleLabel(lastOkMs)}
            </span>
            <button
              type="button"
              onClick={() => requestRetry()}
              className="chip min-h-[40px] cursor-pointer border-[var(--accent)] px-3 text-[var(--accent)] transition-colors duration-150 hover:text-[var(--text)]"
            >
              retry
            </button>
          </div>
        )}
      </div>
    </section>
  );
}
