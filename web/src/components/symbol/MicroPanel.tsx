"use client";

// MICROSTRUCTURE (crypto only) — live book snapshot: bid/ask/mid/wmid,
// order-book imbalance mini-gauge, spread and apply latency, plus a
// 5-minute imbalance sparkline polled from /api/snaps.

import { useEffect, useState } from "react";
import { api, pollMs, type Market, type Snap } from "@/lib/api";
import { ago, fmtPrice, fmtScore } from "@/lib/format";
import Spark from "@/components/Spark";

function fmtLat(ns: number): string {
  if (!isFinite(ns) || ns <= 0) return "—";
  if (ns < 1_000) return `${ns.toFixed(0)}ns`;
  if (ns < 1_000_000) return `${(ns / 1_000).toFixed(1)}µs`;
  return `${(ns / 1_000_000).toFixed(1)}ms`;
}

function Cell({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div>
      <div className="text-[0.62rem] tracking-wide" style={{ color: "var(--faint)" }}>{label}</div>
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
        .then((s) => alive && setSnaps(s ?? []))
        .catch(() => alive && setSnaps([]));
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol, market]);

  const imbPct = Math.max(0, Math.min(100, 50 + snap.imb * 50));

  return (
    <section className="panel">
      <div className="panel-h">
        <span>MICROSTRUCTURE</span>
        <span className="ml-auto tnum text-[0.66rem]" style={{ color: "var(--faint)" }}>
          {ago(snap.ts)}
        </span>
      </div>
      <div className="flex flex-col gap-4 p-4">
        <div className="grid grid-cols-3 gap-x-4 gap-y-3 sm:grid-cols-6">
          <Cell label="BID" value={fmtPrice(snap.bid)} color="var(--bid)" />
          <Cell label="ASK" value={fmtPrice(snap.ask)} color="var(--ask)" />
          <Cell label="MID" value={fmtPrice(snap.mid)} />
          <Cell label="WMID" value={fmtPrice(snap.wmid)} />
          <Cell label="SPREAD" value={fmtPrice(snap.spread)} />
          <Cell label="APPLY LAT" value={fmtLat(snap.applyLatNs)} />
        </div>

        <div>
          <div className="mb-1 flex items-baseline justify-between text-[0.7rem]">
            <span style={{ color: "var(--dim)" }}>book imbalance</span>
            <span className="tnum" style={{ color: "var(--text)" }}>
              {fmtScore(snap.imb)} · {snap.imb >= 0.15 ? "bid-heavy" : snap.imb <= -0.15 ? "ask-heavy" : "balanced"}
            </span>
          </div>
          <div
            role="meter"
            aria-valuemin={-1}
            aria-valuemax={1}
            aria-valuenow={Number(snap.imb.toFixed(3))}
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
          <div className="mb-1 text-[0.62rem] tracking-wide" style={{ color: "var(--faint)" }}>
            IMBALANCE · LAST 5 MIN
          </div>
          {snaps.length >= 2 ? (
            <Spark values={snaps.map((s) => s.imb)} width={280} height={36} />
          ) : (
            <p className="text-[0.7rem]" style={{ color: "var(--faint)" }}>
              collecting snapshots…
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
