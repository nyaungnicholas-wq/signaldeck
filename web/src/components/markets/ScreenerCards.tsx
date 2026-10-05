"use client";

// Stage 4 (tables→charts): CARDS view — the same filtered rows as a grid of
// verdict cards: symbol + price + day %, the 30d sparkline, and the Stage-2
// VerdictCard (REAL calibrated 1d prob or the honest NO READ YET, tier badge
// always visible). Nothing the table shows is lost — switch views any time.
// Extracted verbatim from the screener page in the page split.

import Link from "next/link";
import { fmtPct, fmtPrice, scoreColor } from "@/lib/format";
import VerdictCard from "@/components/VerdictCard";
import Sparkline from "@/components/viz/Sparkline";
import type { Derived } from "@/components/markets/screenerModel";

export default function ScreenerCards({ filtered }: { filtered: Derived[] }) {
  return (
    <div className="grid grid-cols-1 gap-2 px-4 py-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {filtered.map((d) => {
        const r = d.row;
        return (
          <div
            key={`${r.market}:${r.symbol}`}
            className="flex flex-col gap-2 rounded-lg border bg-[var(--panel2)] p-3 transition-colors duration-150 hover:bg-[var(--panel)]"
            style={{ borderColor: "var(--border)" }}
          >
            <div className="flex items-baseline justify-between gap-2">
              <Link
                href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                className="mono min-w-0 cursor-pointer truncate font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                title={r.name || r.symbol}
              >
                {r.symbol}
                <span className="ml-1.5 text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
                  {r.market}
                </span>
              </Link>
              <span className="tnum shrink-0 text-[0.75rem]">
                {fmtPrice(r.lastClose)}{" "}
                <span style={{ color: scoreColor(r.dayChangePct) }} title="change since previous stored close">
                  {fmtPct(r.dayChangePct)}
                </span>
              </span>
            </div>
            {/* same stored daily closes as the table's TREND 30D column */}
            <Sparkline closes={r.spark} width={190} height={34} area={false} />
            <VerdictCard
              size="sm"
              symbol={r.symbol}
              market={r.market}
              calProb={r.calProb1d ?? null}
              nUsed={r.nUsed1d ?? 0}
              tier={r.tier1d ?? ""}
              tierProgress={{ nSamples: r.nSamples1d ?? 0, threshold: r.tierThreshold ?? 0 }}
            />
          </div>
        );
      })}
    </div>
  );
}
