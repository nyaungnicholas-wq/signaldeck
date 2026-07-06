"use client";

// SHORT VOLUME panel (Stage 5 — FINRA Reg SHO): one stock's daily short sale
// VOLUME ratio from FINRA's free Consolidated NMS files, with a 30-day
// direction-NEUTRAL sparkline (accent, not green/red — a rising ratio is not
// directional) and the labeled descriptive z. Stocks only.
//
// HONESTY, verbatim from the API and rendered in full: this is NOT short
// interest — it includes market-maker activity, and a high ratio is NOT
// directly bearish. That misread is the classic retail trap; the caveat is
// the panel's most important row.

import { useEffect, useState } from "react";
import { shortsSymbol, type ShortsSymbolResponse } from "@/lib/api";

const POLL_MS = 5 * 60_000; // one file per trading day — poll slowly

function fmtVol(v: number): string {
  if (!isFinite(v)) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

/** Neutral-color sparkline (see header comment for why not green/red). */
function NeutralSpark({ values }: { values: number[] }) {
  const w = 140;
  const h = 30;
  const pts = (values ?? []).filter((v) => Number.isFinite(v));
  if (pts.length < 5) {
    return (
      <svg width={w} height={h} role="img" aria-label="not enough stored days for a trend yet (needs 5+)">
        <title>not enough stored days yet (needs 5+) — one file per trading day</title>
        <line x1={2} y1={h / 2} x2={w - 2} y2={h / 2} stroke="var(--border)" strokeDasharray="2 4" />
      </svg>
    );
  }
  const min = Math.min(...pts);
  const max = Math.max(...pts);
  const span = max - min || 1;
  const path = pts
    .map((v, i) => {
      const x = 2 + (i / (pts.length - 1)) * (w - 4);
      const y = h - 3 - ((v - min) / span) * (h - 6);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg width={w} height={h} role="img" aria-label={`short volume ratio over ${pts.length} days (descriptive, not directional)`}>
      <polyline points={path} fill="none" stroke="var(--accent)" strokeWidth="1.5" />
    </svg>
  );
}

export default function ShortVolumePanel({ symbol }: { symbol: string }) {
  const [data, setData] = useState<ShortsSymbolResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      shortsSymbol(symbol, 30)
        .then((r) => {
          if (!alive) return;
          setData(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol]);

  const series = data?.series ?? [];
  const latest = series.length > 0 ? series[series.length - 1] : null;
  const ratios = series.map((p) => p.shortPct);

  return (
    <section className="panel" aria-label={`FINRA Reg SHO daily short sale volume for ${symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        SHORT VOLUME · {symbol}
        <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
          FINRA Reg SHO daily (free) · posts ~6pm ET
        </span>
      </div>

      {err !== null && data === null && (
        <p className="px-4 py-3 text-[0.78rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {data === null && err === null && (
        <p className="px-4 py-3 text-[0.78rem]" style={{ color: "var(--faint)" }}>
          loading Reg SHO daily short volume…
        </p>
      )}

      {data !== null && latest === null && (
        <p className="px-4 py-4 text-[0.78rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          No Reg SHO rows stored yet — the finra-shorts worker ingests one free FINRA
          file per trading day (first run backfills ~30 trading days). Honest absence,
          not an error.
        </p>
      )}

      {data !== null && latest !== null && (
        <div className="flex flex-wrap items-center gap-4 px-4 py-3">
          <div>
            <div className="text-[0.7rem]" style={{ color: "var(--dim)" }}>
              RATIO · {latest.day}
            </div>
            <div className="tnum text-lg font-bold">{(latest.shortPct * 100).toFixed(1)}%</div>
          </div>
          <div>
            <div className="text-[0.7rem]" style={{ color: "var(--dim)" }}>
              SHORT / TOTAL
            </div>
            <div className="tnum text-[0.85rem]">
              {fmtVol(latest.shortVol)} / {fmtVol(latest.totalVol)}
            </div>
          </div>
          <div>
            <div className="text-[0.7rem]" style={{ color: "var(--dim)" }}>
              LAST {series.length}D
            </div>
            <NeutralSpark values={ratios} />
          </div>
          {data.latestZ != null && (
            <div title={data.zNote}>
              <div className="text-[0.7rem]" style={{ color: "var(--dim)" }}>
                Z (DESCRIPTIVE)
              </div>
              <div className="tnum text-[0.85rem]">{data.latestZ.toFixed(2)}</div>
            </div>
          )}
        </div>
      )}

      {/* THE caveat — verbatim, always rendered, even while empty/loading. */}
      <p className="px-4 pb-3 text-[0.72rem] leading-relaxed" style={{ color: "var(--warn)" }}>
        {data?.caveat ??
          "short sale volume ratio (Reg SHO daily) — NOT short interest; includes market-maker activity; a high ratio is NOT directly bearish"}
      </p>
    </section>
  );
}
