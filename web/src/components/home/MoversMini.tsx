"use client";

// Top-movers mini-table (sidebar) — extracted from the old monolithic
// page.tsx. The data-provenance note that used to hide behind a hover title
// is now a click/keyboard HelpTip, and the footer CTA uses verb language.

import Link from "next/link";
import type { DashboardResponse } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import HelpTip from "@/components/HelpTip";
import { changeColor } from "@/components/home/helpers";

export default function MoversMini({ movers }: { movers: DashboardResponse["movers"] }) {
  const gainers = movers.gainers ?? [];
  const losers = movers.losers ?? [];
  const col = (title: string, rows: typeof gainers, bordered = false) => (
    <div
      className="min-w-0 flex-1"
      style={bordered ? { borderLeft: "1px solid var(--border)" } : undefined}
    >
      <div className="px-3 pb-1 pt-2 text-[0.75rem] tracking-[0.14em]" style={{ color: "var(--faint)" }}>
        {title}
      </div>
      <ul className="m-0 list-none p-0">
        {rows.map((m) => (
          <li key={m.symbol}>
            <Link
              href={`/s/stocks/${encodeURIComponent(m.symbol)}`}
              className="flex cursor-pointer items-baseline gap-2 px-3 py-1 text-[0.75rem] transition-colors duration-150 hover:text-[var(--accent)]"
            >
              <span className="mono truncate font-bold tracking-wide">{m.symbol}</span>
              <span className="tnum ml-auto" style={{ color: changeColor(m.changePct) }}>
                {fmtPct(m.changePct)}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
  return (
    <section className="panel" aria-label="top movers">
      <div className="panel-h">
        <span>TOP MOVERS</span>
        <HelpTip label="Where movers come from">{movers.note}</HelpTip>
      </div>
      {gainers.length === 0 && losers.length === 0 ? (
        <p className="px-3 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          No fresh universe bars yet — movers appear as the universe poller
          stores daily closes on its own cadence.
        </p>
      ) : (
        <>
          <div className="flex pb-2">
            {col("GAINERS", gainers)}
            {col("LOSERS", losers, true)}
          </div>
          <div className="border-t px-3 py-1.5" style={{ borderColor: "var(--border)" }}>
            <Link
              href="/markets/screener"
              className="inline-flex min-h-[36px] cursor-pointer items-center text-[0.75rem] tracking-wider text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
            >
              Compare the whole universe →
            </Link>
          </div>
        </>
      )}
    </section>
  );
}
