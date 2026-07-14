"use client";

import Link from "next/link";
import { type SectorAgg } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import Bar from "./Bar";

/** MeanRet1M color: green up, red down, dim flat. */
function retColor(v: number): string {
  if (!isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

/** MeanScore (0..100) bar color: strong green, weak red, mid dim. */
function scoreColor(v: number): string {
  if (!isFinite(v)) return "var(--dim)";
  if (v >= 55) return "var(--bid)";
  if (v <= 45) return "var(--ask)";
  return "var(--dim)";
}

/** Symbols carrying "/" (e.g. BTC/USD) are crypto pairs that have no stock
    detail page — render those as static chips, not links. */
function isLinkable(sym: string): boolean {
  return !sym.includes("/");
}

export default function SectorRow({ s }: { s: SectorAgg }) {
  const score = isFinite(s.MeanScore) ? s.MeanScore : 0;
  return (
    <li
      className="px-4 py-3 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 sm:flex-nowrap">
        <span
          className="w-full truncate text-sm font-bold sm:w-36 sm:shrink-0"
          title={s.Sector}
        >
          {s.Sector || "(unlabeled)"}
        </span>
        <div className="min-w-0 flex-1">
          <Bar
            pct={score}
            color={scoreColor(score)}
            label={`${s.Sector || "sector"} mean pressure score ${score.toFixed(0)} of 100`}
          />
        </div>
        <span
          className="tnum w-14 shrink-0 text-right text-[0.75rem]"
          style={{ color: scoreColor(score) }}
          title="Mean pressure score (0–100)"
          aria-label={`mean score ${score.toFixed(0)}`}
        >
          {score.toFixed(0)}
        </span>
        <span
          className="tnum w-16 shrink-0 text-right text-[0.75rem]"
          style={{ color: retColor(s.MeanRet1M) }}
          title="Mean 1-month return"
          aria-label={`mean 1-month return ${fmtPct(s.MeanRet1M)}`}
        >
          {fmtPct(s.MeanRet1M)}
        </span>
        <span
          className="tnum w-16 shrink-0 text-right text-[0.75rem]"
          style={{ color: "var(--faint)" }}
          title="Number of symbols in this sector"
          aria-label={`${s.N} symbols`}
        >
          {s.N} sym
        </span>
      </div>

      {s.Symbols && s.Symbols.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1.5 pl-0 sm:pl-[9.75rem]">
          {s.Symbols.map((sym) =>
            isLinkable(sym) ? (
              <Link
                key={sym}
                href={`/s/stocks/${encodeURIComponent(sym)}`}
                className="chip tnum cursor-pointer transition-colors duration-150 hover:text-[var(--text)] hover:border-[var(--accent)]"
              >
                {sym}
              </Link>
            ) : (
              <span key={sym} className="chip tnum" style={{ color: "var(--faint)" }}>
                {sym}
              </span>
            ),
          )}
        </div>
      )}
    </li>
  );
}
