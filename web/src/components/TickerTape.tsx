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

import Link from "next/link";
import { useEffect, useState } from "react";
import { tape, type TapeItem, type TapeResponse } from "@/lib/api";
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

function TapeEntry({ it }: { it: TapeItem }) {
  return (
    <Link
      href={itemHref(it)}
      title={`${it.label}${it.note ? ` — ${it.note}` : ""}`}
      className="flex shrink-0 cursor-pointer items-baseline gap-1.5 px-3 py-1.5 text-[0.75rem] transition-colors duration-150 hover:text-[var(--accent)]"
    >
      <span className="font-bold tracking-wide">{it.kind === "vix" ? "VIX" : it.symbol}</span>
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
        <span className="text-[0.62rem] tracking-wider" style={{ color: "var(--faint)" }}>
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
  const driven = itemsProp !== undefined;

  useEffect(() => {
    if (driven) return; // parent-driven: no self-fetch, no poll
    let alive = true;
    const load = () =>
      tape()
        .then((r) => alive && setResp(r))
        .catch(() => alive && setResp(null)); // silent: strip simply hides on error
    load();
    const t = setInterval(load, 60_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [driven]);

  const items = (driven ? itemsProp : resp?.items) ?? [];
  const note = driven ? noteProp : resp?.note;
  if (items.length === 0) return null; // no data / offline → no strip (honest quiet)

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
      `}</style>
      <div className="tape-wrap">
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
    </section>
  );
}
