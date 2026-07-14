"use client";

import Link from "next/link";
import { useMemo } from "react";
import type { RankedRow } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import EmptyState from "@/components/EmptyState";

function retColor(v: number): string {
  if (!Number.isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

/** Clamp a 0..100 score to a bar width. */
function scorePct(v: number): number {
  if (!Number.isFinite(v)) return 0;
  return Math.max(0, Math.min(100, v));
}

function RankRow({ r }: { r: RankedRow }) {
  const pct = scorePct(r.score);
  return (
    <tr className="border-t transition-colors duration-150 hover:bg-[var(--panel2)]" style={{ borderColor: "var(--border)" }}>
      <td className="tnum px-3 py-2 text-right" style={{ color: "var(--faint)" }}>
        {Number.isFinite(r.rank) ? r.rank : "—"}
      </td>
      <td className="px-3 py-2">
        <Link
          href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
          className="mono cursor-pointer text-[0.75rem] font-bold transition-colors duration-150 hover:text-[var(--accent)]"
        >
          {r.symbol}
          <span className="ml-1.5 text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
            {r.market}
          </span>
        </Link>
      </td>
      <td className="px-3 py-2">
        <div className="flex items-center gap-2">
          <div
            role="meter"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(pct)}
            aria-label={`${r.symbol} relative strength score`}
            className="h-1.5 min-w-[40px] flex-1 overflow-hidden rounded-full"
            style={{ background: "var(--panel2)" }}
          >
            <div
              className="h-full transition-[width] duration-300"
              style={{ width: `${pct}%`, background: "var(--accent)" }}
            />
          </div>
          <span className="tnum w-9 shrink-0 text-right text-[0.75rem]" style={{ color: "var(--dim)" }}>
            {Number.isFinite(r.score) ? r.score.toFixed(0) : "—"}
          </span>
        </div>
      </td>
      <td className="tnum px-3 py-2 text-right text-[0.75rem]" style={{ color: retColor(r.ret1m) }}>
        {Number.isFinite(r.ret1m) ? fmtPct(r.ret1m) : "—"}
      </td>
      <td className="tnum px-3 py-2 text-right text-[0.75rem]" style={{ color: retColor(r.ret3m) }}>
        {Number.isFinite(r.ret3m) ? fmtPct(r.ret3m) : "—"}
      </td>
    </tr>
  );
}

/** RELATIVE STRENGTH RANKING — who is strongest vs the field right now. */
export default function RankingTable({ rows }: { rows: RankedRow[] }) {
  const sorted = useMemo(() => {
    return [...rows].sort((a, b) => {
      const ra = Number.isFinite(a.rank) ? a.rank : Infinity;
      const rb = Number.isFinite(b.rank) ? b.rank : Infinity;
      if (ra !== rb) return ra - rb;
      return (b.score || 0) - (a.score || 0);
    });
  }, [rows]);

  return (
    <section className="panel">
      <div className="panel-h">
        RELATIVE STRENGTH RANKING
        <span className="tnum" style={{ color: "var(--faint)" }}>
          who is strongest vs the field right now
        </span>
      </div>
      {sorted.length === 0 ? (
        <EmptyState
          className="border-0"
          message="No ranking yet"
          detail="It fills in once symbols have enough return history to rank."
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[380px] text-[0.75rem]">
            <thead>
              <tr className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                <th className="px-3 py-2 text-right font-medium" title="rank within the field">#</th>
                <th className="px-3 py-2 text-left font-medium">SYMBOL</th>
                <th className="px-3 py-2 text-left font-medium" title="relative strength score, 0–100">SCORE · 0–100</th>
                <th className="px-3 py-2 text-right font-medium" title="return over the last month">1M</th>
                <th className="px-3 py-2 text-right font-medium" title="return over the last three months">3M</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((r) => (
                <RankRow key={`${r.market}:${r.symbol}`} r={r} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
