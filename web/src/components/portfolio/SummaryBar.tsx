"use client";

// Positions summary strip — extracted verbatim from the old monolithic
// /lab/portfolio page (pure refactor; behavior identical).

import { type PortfolioResponse } from "@/lib/api";
import { fmtPct, fmtPrice, scoreColor } from "@/lib/format";

export default function SummaryBar({ stat }: { stat: PortfolioResponse["stat"] }) {
  const cells: { label: string; value: React.ReactNode }[] = [
    {
      label: "gross value",
      value: <span className="tnum">{fmtPrice(stat.GrossValue)}</span>,
    },
    {
      label: "total P&L",
      value: (
        <span className="tnum" style={{ color: scoreColor(stat.TotalPnLPct) }}>
          {fmtPct(stat.TotalPnLPct)}
          <span className="ml-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            {stat.TotalPnLAbs >= 0 ? "+" : "−"}
            {fmtPrice(Math.abs(stat.TotalPnLAbs))}
          </span>
        </span>
      ),
    },
    {
      label: "winners / losers",
      value: (
        <span className="tnum">
          <span style={{ color: "var(--bid)" }}>{stat.Winners}</span>
          <span style={{ color: "var(--faint)" }}> / </span>
          <span style={{ color: "var(--ask)" }}>{stat.Losers}</span>
        </span>
      ),
    },
    {
      label: "best",
      value: stat.Best ? (
        <span style={{ color: "var(--bid)" }}>{stat.Best}</span>
      ) : (
        <span style={{ color: "var(--faint)" }}>—</span>
      ),
    },
    {
      label: "worst",
      value: stat.Worst ? (
        <span style={{ color: "var(--ask)" }}>{stat.Worst}</span>
      ) : (
        <span style={{ color: "var(--faint)" }}>—</span>
      ),
    },
  ];
  return (
    <section className="panel">
      <div className="panel-h">POSITIONS SUMMARY</div>
      <div className="grid grid-cols-2 gap-px sm:grid-cols-3 lg:grid-cols-5" style={{ background: "var(--border)" }}>
        {cells.map((c) => (
          <div key={c.label} className="px-4 py-3" style={{ background: "var(--panel)" }}>
            <div
              className="text-[0.75rem] tracking-wide"
              style={{ color: "var(--faint)" }}
              title={c.label === "total P&L" ? "total profit and loss across logged positions" : undefined}
            >
              {c.label}
            </div>
            <div className="mt-1 text-[0.9rem] font-semibold">{c.value}</div>
          </div>
        ))}
      </div>
    </section>
  );
}
