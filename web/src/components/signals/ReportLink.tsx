"use client";

// The uniform "report →" chip every signal row carries: one click opens the
// per-signal detail report (/signals/report) pre-aimed at the row's symbol and
// signal kind. Kept tiny and identical everywhere so users learn it once.

import Link from "next/link";

export default function ReportLink({
  symbol,
  market,
  kind,
}: {
  symbol: string;
  market: string;
  kind: string;
}) {
  return (
    <Link
      href={`/signals/report/${market}/${encodeURIComponent(symbol)}?kind=${encodeURIComponent(kind)}`}
      className="chip shrink-0 px-2 py-[1px] text-[0.7rem] tracking-wider hover:text-[var(--accent)]"
      style={{ color: "var(--dim)" }}
      title={`open the detail report for this ${kind} signal`}
      onClick={(e) => e.stopPropagation()}
    >
      report →
    </Link>
  );
}
