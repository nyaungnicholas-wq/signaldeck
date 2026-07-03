"use client";

import Link from "next/link";
import { useMemo } from "react";
import type { RegimeState } from "@/lib/api";
import { ago } from "@/lib/format";
import { regimeColor, regimeKind, regimeSortRank, strengthPct } from "./regime";

function RegimeTile({ s }: { s: RegimeState }) {
  const color = regimeColor(s.label);
  const kind = regimeKind(s.label);
  const pct = strengthPct(s.strength);
  return (
    <div
      className="flex flex-col gap-2 rounded-lg border p-3 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
    >
      <div className="flex items-center justify-between gap-2">
        <Link
          href={`/s/${s.market}/${encodeURIComponent(s.symbol)}`}
          className="cursor-pointer text-[0.82rem] font-bold transition-colors duration-150 hover:text-[var(--accent)]"
        >
          {s.symbol}
          <span className="ml-1.5 text-[0.6rem] font-normal" style={{ color: "var(--faint)" }}>
            {s.market}
          </span>
        </Link>
        <span
          className="chip shrink-0"
          style={{ color, borderColor: color }}
          aria-label={`regime: ${kind}`}
        >
          {s.label || "—"}
        </span>
      </div>

      {/* strength bar */}
      <div
        role="meter"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(pct)}
        aria-label={`${s.symbol} regime strength`}
        className="h-1.5 w-full overflow-hidden rounded-full"
        style={{ background: "var(--panel)" }}
        title={`strength ${pct.toFixed(0)}%`}
      >
        <div
          className="h-full transition-[width] duration-300"
          style={{ width: `${pct}%`, background: color }}
        />
      </div>

      <div className="flex items-center justify-between gap-2 text-[0.68rem]">
        <span className="min-w-0 flex-1 truncate" style={{ color: "var(--dim)" }} title={s.note}>
          {s.note || "no note"}
        </span>
        <span className="tnum shrink-0" style={{ color: "var(--faint)" }}>
          {ago(s.ts)}
        </span>
      </div>
    </div>
  );
}

/** REGIME MAP — every tracked symbol as a tile, grouped by regime. */
export default function RegimeMap({ states }: { states: RegimeState[] }) {
  const sorted = useMemo(() => {
    return [...states].sort((a, b) => {
      const r = regimeSortRank(a.label) - regimeSortRank(b.label);
      if (r !== 0) return r;
      const sa = Number.isFinite(a.strength) ? a.strength : -Infinity;
      const sb = Number.isFinite(b.strength) ? b.strength : -Infinity;
      if (sb !== sa) return sb - sa;
      return a.symbol.localeCompare(b.symbol);
    });
  }, [states]);

  return (
    <section className="panel">
      <div className="panel-h">
        REGIME MAP
        <span className="tnum" style={{ color: "var(--faint)" }}>
          what state each symbol is in right now
        </span>
      </div>
      {sorted.length === 0 ? (
        <div className="px-4 py-6 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          no regimes classified yet — states appear once enough bars are stored per symbol.
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-2 p-3 sm:grid-cols-2 lg:grid-cols-3">
          {sorted.map((s) => (
            <RegimeTile key={`${s.market}:${s.symbol}`} s={s} />
          ))}
        </div>
      )}
    </section>
  );
}
