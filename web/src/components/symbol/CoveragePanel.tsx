"use client";

// COVERAGE — how much history is stored per timeframe, plus CSV exports.

import { api, type Market } from "@/lib/api";
import { fmtDate } from "@/lib/format";
import EmptyState from "@/components/EmptyState";

const TF_ORDER = ["1m", "1h", "1d"];

export default function CoveragePanel({
  symbol,
  market,
  coverage,
}: {
  symbol: string;
  market: Market;
  coverage: Record<string, { bars: number; from: number; to: number }>;
}) {
  const rank = (tf: string) => {
    const i = TF_ORDER.indexOf(tf);
    return i === -1 ? 99 : i;
  };
  const tfs = Object.keys(coverage).sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
  const q = `symbol=${encodeURIComponent(symbol)}&market=${market}`;

  return (
    <section className="panel">
      <div className="panel-h">
        <span>COVERAGE &amp; EXPORT</span>
      </div>
      <div className="flex flex-wrap items-center gap-2 p-4">
        {tfs.length === 0 ? (
          <EmptyState
            message="No bars stored yet"
            detail="Backfill runs shortly after subscribing."
            className="w-full"
          />
        ) : (
          tfs.map((tf) => {
            const c = coverage[tf];
            return (
              <span key={tf} className="chip tnum">
                <span style={{ color: "var(--text)" }}>{tf}</span>
                {" · "}
                {c.bars.toLocaleString("en-US")} bars
                {" · "}
                {fmtDate(c.from)} → {fmtDate(c.to)}
              </span>
            );
          })
        )}
        <span className="ml-auto flex flex-wrap items-center gap-2">
          {tfs.map((tf) => (
            <a
              key={tf}
              href={api.exportUrl("bars", `${q}&tf=${tf}`)}
              download
              className="chip inline-flex min-h-[40px] cursor-pointer items-center transition-colors duration-150 hover:border-[var(--accent)]"
              style={{ color: "var(--accent)" }}
              title={`Download stored ${tf} bars as CSV`}
            >
              bars {tf} .csv
            </a>
          ))}
          <a
            href={api.exportUrl("scores", q)}
            download
            className="chip inline-flex min-h-[40px] cursor-pointer items-center transition-colors duration-150 hover:border-[var(--accent)]"
            style={{ color: "var(--accent)" }}
            title="Download score history as CSV"
          >
            scores .csv
          </a>
        </span>
      </div>
    </section>
  );
}
