"use client";

// SHORTS (Stage 5 — FINRA Reg SHO): daily short sale VOLUME from FINRA's
// free, registration-less Consolidated NMS files, universe-scoped to tracked
// symbols. HONESTY, prominently and verbatim from the API: this ratio is NOT
// short interest — it includes market-maker activity, and a high ratio is
// NOT directly bearish (the classic retail misread). The sparkline is drawn
// DIRECTION-NEUTRAL (accent, not green/red) for the same reason: a rising
// ratio is not "good" or "bad".

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  shortsExtremes,
  shortsSymbol,
  type ShortsExtreme,
  type ShortsExtremesResponse,
  type ShortsSymbolResponse,
  type ShortVolumePoint,
} from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";

const POLL_MS = 300_000; // files land once per trading day — poll slowly

function fmtVol(v: number): string {
  if (!isFinite(v)) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

function fmtPct(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

/** Direction-NEUTRAL ratio sparkline: accent stroke on purpose — trend color
 *  would imply a high/rising ratio is directional, which the caveat denies. */
function RatioSpark({ values }: { values: number[] }) {
  const w = 110;
  const h = 26;
  const pts = (values ?? []).filter((v) => Number.isFinite(v));
  if (pts.length < 5) {
    return (
      <svg width={w} height={h} role="img" aria-label="not enough days for a trend yet (needs 5+)">
        <title>not enough stored days yet (needs 5+) — the worker ingests one file per trading day</title>
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
    <svg width={w} height={h} role="img" aria-label={`ratio ${fmtPct(pts[0])} → ${fmtPct(pts[pts.length - 1])} over ${pts.length} days (descriptive, not directional)`}>
      <polyline points={path} fill="none" stroke="var(--accent)" strokeWidth="1.5" />
    </svg>
  );
}

export default function ShortsPage() {
  const { symbol } = useIntelSymbol();
  const [extremes, setExtremes] = useState<ShortsExtremesResponse | null>(null);
  const [series, setSeries] = useState<ShortsSymbolResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () => {
      if (symbol) {
        shortsSymbol(symbol, 30)
          .then((r) => {
            if (!alive) return;
            setSeries(r);
            setErr(null);
          })
          .catch((e: unknown) => {
            if (!alive) return;
            setSeries(null);
            setErr(e instanceof Error ? e.message : String(e));
          });
      } else {
        shortsExtremes(20)
          .then((r) => {
            if (!alive) return;
            setExtremes(r);
            setErr(null);
          })
          .catch((e: unknown) => {
            if (!alive) return;
            setErr(e instanceof Error ? e.message : String(e));
          });
      }
    };
    load();
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol, retryTick]);

  const data = symbol ? series : extremes;
  const loading = data === null && err === null;
  const hardError = data === null && err !== null;
  const caveat = data?.caveat ?? "";
  const note = data?.note ?? "";

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">SHORTS</h1>
        {/* THE caveat — the whole point of the honesty doctrine here. */}
        <span className="chip" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>
          NOT short interest — volume ratio only
        </span>
        {err !== null && data !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {loading && <Skeleton lines={6} label="loading short sale volume" />}
      {hardError && (
        <ErrorState
          message={err ?? "short volume data unavailable"}
          hint={
            symbol
              ? "Only tracked stocks have Reg SHO rows (ingestion is universe-scoped) — clear the shared symbol filter or check the daemon."
              : "Is the daemon running? The finra-shorts worker ingests FINRA's daily file after ~6:30pm ET."
          }
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {data !== null && (
        <section className="panel">
          <div className="panel-h flex-wrap gap-2">
            {symbol ? `DAILY SHORT SALE VOLUME — ${symbol}` : "HIGHEST SHORT VOLUME RATIOS (LATEST DAY)"}
            {symbol ? (
              <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                {symbol} — from the shared intel filter
              </span>
            ) : (
              extremes?.day && <span className="chip tnum">{extremes.day}</span>
            )}
            {symbol && series?.latestZ != null && (
              <span className="chip tnum" title={series.zNote}>
                latest z {series.latestZ.toFixed(2)} (descriptive)
              </span>
            )}
          </div>

          {/* Verbatim caveat + provenance — always visible, never abbreviated. */}
          <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {caveat}. {note}
            {!symbol && extremes ? ` ${extremes.floorNote} (minTotalVol ${fmtVol(extremes.minTotalVol)} shares).` : ""}
          </p>

          {symbol ? (
            !series?.series || series.series.length === 0 ? (
              <EmptyState
                className="m-4"
                message={`No Reg SHO rows stored for ${symbol}`}
                detail="Rows exist only for tracked stocks and accrue one trading day at a time (first run backfills ~30 trading days). Crypto has no Reg SHO data."
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-[0.8rem]">
                  <thead>
                    <tr className="text-left" style={{ color: "var(--dim)" }}>
                      <th className="px-4 py-2 font-normal">DAY</th>
                      <th className="px-4 py-2 font-normal">RATIO</th>
                      <th className="px-4 py-2 font-normal">SHORT VOL</th>
                      <th className="px-4 py-2 font-normal">EXEMPT</th>
                      <th className="px-4 py-2 font-normal">TOTAL VOL</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...series.series].reverse().map((p: ShortVolumePoint) => (
                      <tr key={p.day} style={{ borderTop: "1px solid var(--border)" }}>
                        <td className="px-4 py-2 tnum">{p.day}</td>
                        <td className="px-4 py-2 tnum">{fmtPct(p.shortPct)}</td>
                        <td className="px-4 py-2 tnum">{fmtVol(p.shortVol)}</td>
                        <td className="px-4 py-2 tnum">{fmtVol(p.shortExempt)}</td>
                        <td className="px-4 py-2 tnum">{fmtVol(p.totalVol)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          ) : !extremes?.extremes || extremes.extremes.length === 0 ? (
            <EmptyState
              className="m-4"
              message="No Reg SHO data stored yet"
              detail={extremes?.emptyNote ?? "The finra-shorts worker ingests FINRA's free daily file after ~6:30pm ET and backfills ~30 trading days on first run."}
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-[0.8rem]">
                <thead>
                  <tr className="text-left" style={{ color: "var(--dim)" }}>
                    <th className="px-4 py-2 font-normal">SYMBOL</th>
                    <th className="px-4 py-2 font-normal">DAY</th>
                    <th className="px-4 py-2 font-normal">RATIO</th>
                    <th className="px-4 py-2 font-normal">LAST 30D</th>
                    <th className="px-4 py-2 font-normal">SHORT VOL</th>
                    <th className="px-4 py-2 font-normal">TOTAL VOL</th>
                  </tr>
                </thead>
                <tbody>
                  {extremes.extremes.map((e: ShortsExtreme) => (
                    <tr key={e.symbolId} style={{ borderTop: "1px solid var(--border)" }}>
                      <td className="px-4 py-2">
                        <Link
                          href={`/s/stocks/${encodeURIComponent(e.symbol)}`}
                          className="font-bold"
                          style={{ color: "var(--accent)" }}
                        >
                          {e.symbol}
                        </Link>
                      </td>
                      <td className="px-4 py-2 tnum">{e.day}</td>
                      <td className="px-4 py-2 tnum">{fmtPct(e.shortPct)}</td>
                      <td className="px-4 py-2">
                        <RatioSpark values={e.spark} />
                      </td>
                      <td className="px-4 py-2 tnum">{fmtVol(e.shortVol)}</td>
                      <td className="px-4 py-2 tnum">{fmtVol(e.totalVol)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}
    </div>
  );
}
