"use client";

// MICROSTRUCTURE (crypto only) — live book snapshot: bid/ask/mid/wmid,
// order-book imbalance mini-gauge, spread and apply latency, plus a
// 5-minute imbalance sparkline polled from /api/snaps.

import { useEffect, useState } from "react";
import { api, pollMs, POLL_LIVE, type Market, type Snap } from "@/lib/api";
import { useSnapStream } from "@/hooks/useSnapStream";
import { ago, fmtPrice, fmtScore } from "@/lib/format";
import Spark from "@/components/Spark";

function fmtLat(ns: number): string {
  if (!isFinite(ns) || ns <= 0) return "—";
  if (ns < 1_000) return `${ns.toFixed(0)}ns`;
  if (ns < 1_000_000) return `${(ns / 1_000).toFixed(1)}µs`;
  return `${(ns / 1_000_000).toFixed(1)}ms`;
}

function Cell({
  label,
  value,
  color,
  title,
}: {
  label: string;
  value: string;
  color?: string;
  title?: string;
}) {
  return (
    <div>
      <div className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
        <span title={title}>{label}</span>
      </div>
      <div className="tnum text-[0.85rem]" style={{ color: color ?? "var(--text)" }}>{value}</div>
    </div>
  );
}

export default function MicroPanel({
  symbol,
  market,
  snap,
}: {
  symbol: string;
  market: Market;
  snap: Snap;
}) {
  const [snaps, setSnaps] = useState<Snap[]>([]);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .snaps(symbol, market, 300)
        .then((s) => {
          if (alive) setSnaps(s ?? []);
        })
        .catch(() => {
          if (alive) setSnaps([]);
        });
    load();
    // POLL_LIVE backfills the 5-min series and stays as the fallback if the
    // SSE stream is unavailable; the stream (below) drives per-second updates.
    const stop = pollMs(load, POLL_LIVE);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market]);

  // Live push: the newest snapshot the instant the daemon emits it (1 Hz+).
  const { snap: live } = useSnapStream(symbol, market);
  const s = live ?? snap;

  // Advance the imbalance sparkline at the stream cadence by appending each
  // freshly-pushed snap to the tail (deduped by ts, capped at 10 min).
  // Guarded adjustment during render — no effect round-trip per pushed snap.
  const [prevLive, setPrevLive] = useState<Snap | null>(null);
  if (live && live !== prevLive) {
    setPrevLive(live);
    setSnaps((prev) => {
      if (prev.length && prev[prev.length - 1].ts >= live.ts) return prev;
      const next = [...prev, live];
      return next.length > 600 ? next.slice(next.length - 600) : next;
    });
  }

  const imbPct = Math.max(0, Math.min(100, 50 + s.imb * 50));

  return (
    <section className="panel">
      <div className="panel-h">
        <span>MICROSTRUCTURE</span>
        <span className="ml-auto tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ago(s.ts)}
        </span>
      </div>
      <div className="flex flex-col gap-4 p-4">
        <div className="grid grid-cols-3 gap-x-4 gap-y-3 sm:grid-cols-6">
          <Cell label="BID" title="Best bid price" value={fmtPrice(s.bid)} color="var(--bid)" />
          <Cell label="ASK" title="Best ask price" value={fmtPrice(s.ask)} color="var(--ask)" />
          <Cell label="MID" title="Midpoint between bid and ask" value={fmtPrice(s.mid)} />
          <Cell label="WMID" title="Weighted mid price (size-weighted midpoint)" value={fmtPrice(s.wmid)} />
          <Cell label="SPREAD" title="Ask minus bid" value={fmtPrice(s.spread)} />
          <Cell
            label="APPLY LAT"
            title="Latency to apply a book update to the consolidated order book"
            value={fmtLat(s.applyLatNs)}
          />
        </div>

        <div>
          <div className="mb-1 flex items-baseline justify-between text-[0.75rem]">
            <span
              style={{ color: "var(--dim)" }}
              title="Order-book imbalance: −1 = all size on the ask side, +1 = all size on the bid side"
            >
              book imbalance
            </span>
            <span className="tnum" style={{ color: "var(--text)" }}>
              {fmtScore(s.imb)} · {s.imb >= 0.15 ? "bid-heavy" : s.imb <= -0.15 ? "ask-heavy" : "balanced"}
            </span>
          </div>
          <div
            role="meter"
            aria-valuemin={-1}
            aria-valuemax={1}
            aria-valuenow={Number(s.imb.toFixed(3))}
            aria-label="order book imbalance"
            className="relative rounded-full border"
            style={{
              height: 8,
              borderColor: "var(--border)",
              background: "linear-gradient(90deg, var(--ask-dim), var(--panel2) 50%, var(--bid-dim))",
            }}
          >
            <div className="absolute top-[-3px] bottom-[-3px] w-px" style={{ left: "50%", background: "var(--faint)" }} />
            <div
              className="absolute top-[-2px] bottom-[-2px] w-1 rounded-sm transition-[left] duration-200"
              style={{
                left: `calc(${imbPct}% - 2px)`,
                background: "var(--accent)",
                boxShadow: "0 0 8px rgba(251,191,36,.6)",
              }}
            />
          </div>
        </div>

        <div>
          <div className="mb-1 text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
            IMBALANCE · LAST 5 MIN
          </div>
          {snaps.length >= 2 ? (
            <Spark values={snaps.map((s) => s.imb)} width={280} height={36} />
          ) : (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              collecting snapshots…
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
