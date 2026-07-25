"use client";

// TODAY'S READ — the decisive opening card the dashboard now leads with. It
// answers ONE question before anything else: is there a high-confidence,
// evidence-backed read right now? It shows the single best-evidenced calibrated
// prediction across your watchlist, or — the honest and deliberate default —
// "No qualified read today" when nothing clears the evidence gate.
//
// Why lead with this: a signals product that will SAY "no read today" reads as
// more credible than one that manufactures a daily pick. The pick here is
// gated three ways (a real calibrated probability · n ≥ the resolved-outcome
// gate · a lean past coin-flip), so when it does speak, the number is earned.
//
// Pure assembly over the existing /api/dashboard payload — no new endpoint.

import Link from "next/link";
import { useMemo } from "react";
import type { DashboardResponse, DashWatchSpark, Market } from "@/lib/api";
import VerdictCard from "@/components/VerdictCard";

function symbolHref(symbol: string, market?: Market): string {
  const m: Market = market ?? (symbol.includes("/") ? "crypto" : "stocks");
  return `/s/${m}/${encodeURIComponent(symbol)}`;
}

export interface BestRead {
  spark: DashWatchSpark;
  prob: number;
  n: number;
  tier: string;
}

/** The single best-evidenced 1d read on the watchlist, or null when none
 *  clears the gate. Exported pure so the choice stays testable. Ranking:
 *  most resolved outcomes first (the strongest evidence), then the biggest
 *  lean. Coin-flips (|p−0.5| < 0.05) are never "a read". */
export function pickBestRead(dash: DashboardResponse): BestRead | null {
  const sparks = dash.watchlist?.sparks ?? [];
  const minN = dash.gauges.confidence.minResolvedN || 30;
  const ranked = sparks
    .filter((s) => s.calProb1d != null && (s.nSamples1d ?? 0) >= minN)
    .map((s) => ({ s, prob: s.calProb1d as number, n: s.nSamples1d ?? 0 }))
    .map((c) => ({ ...c, lean: Math.abs(c.prob - 0.5) }))
    .filter((c) => c.lean >= 0.05)
    .sort((a, b) => b.n - a.n || b.lean - a.lean || a.s.symbol.localeCompare(b.s.symbol));
  const top = ranked[0];
  return top ? { spark: top.s, prob: top.prob, n: top.n, tier: top.s.tier1d ?? "" } : null;
}

export default function TodaysRead({ dash }: { dash: DashboardResponse }) {
  const best = useMemo(() => pickBestRead(dash), [dash]);
  const watchlistEmpty = (dash.watchlist?.sparks ?? []).length === 0;

  return (
    <section
      className="panel relative"
      style={{ borderColor: "color-mix(in srgb, var(--accent) 45%, var(--border))" }}
      aria-labelledby="todays-read-h"
    >
      <div className="panel-h">
        <span style={{ color: "var(--accent)" }}>TODAY&rsquo;S READ</span>
        <span className="chip px-2 py-[1px] text-[0.75rem]">
          {best ? "best-evidenced · 1d" : "evidence gate"}
        </span>
      </div>

      {best ? (
        <div className="flex flex-col gap-4 px-5 py-5 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex min-w-0 flex-col gap-2">
            <p id="todays-read-h" className="m-0 text-[1.05rem] leading-snug font-bold lg:text-[1.2rem]">
              {best.spark.symbol}: a{" "}
              <span style={{ color: "var(--accent)" }}>{Math.round(best.prob * 100)}%</span>{" "}
              calibrated chance of rising over the next day.
            </p>
            <p className="m-0 text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
              The strongest gated read on your watchlist — backed by{" "}
              <span className="tnum font-semibold">n={best.n}</span> resolved outcomes
              {best.tier ? ` · ${best.tier} model` : ""}. Backtested calibration graded
              against stored outcomes — descriptive, not a forecast.
            </p>
            <Link
              href={symbolHref(best.spark.symbol, best.spark.market)}
              className="mt-1 inline-flex min-h-[44px] w-fit cursor-pointer items-center gap-2 rounded-lg border px-4 text-[0.85rem] font-bold tracking-wide transition-colors duration-150 hover:bg-[var(--accent-dim)]"
              style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
            >
              Investigate {best.spark.symbol}
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </Link>
          </div>
          <div className="shrink-0 lg:w-[340px]">
            <VerdictCard
              symbol={best.spark.symbol}
              market={best.spark.market}
              calProb={best.prob}
              nUsed={best.n}
              tier={best.tier}
              sparkCloses={best.spark.closes ?? undefined}
              size="lg"
            />
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2 px-5 py-6">
          <p id="todays-read-h" className="m-0 text-[1.05rem] leading-snug font-bold lg:text-[1.2rem]">
            No qualified read today.
          </p>
          <p className="m-0 max-w-[65ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {watchlistEmpty
              ? "Add symbols to your watchlist and, once enough of their predictions resolve, the best-evidenced one will surface here."
              : `Nothing on your watchlist cleared the evidence gate — a read needs a calibrated probability with at least ${
                  dash.gauges.confidence.minResolvedN || 30
                } resolved outcomes behind it and a lean past a coin-flip. Most days there isn't a high-confidence read, and saying so is the point.`}
          </p>
          <Link
            href="/lab/track-record"
            className="mt-1 inline-flex w-fit cursor-pointer items-center gap-1.5 text-[0.75rem] font-semibold tracking-wide transition-colors duration-150"
            style={{ color: "var(--accent)" }}
          >
            see the track record that gates this →
          </Link>
        </div>
      )}
    </section>
  );
}
