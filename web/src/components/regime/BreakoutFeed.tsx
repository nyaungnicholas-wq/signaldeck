"use client";

import Link from "next/link";
import { useMemo } from "react";
import type { BreakoutRow, Market } from "@/lib/api";
import { ago } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import { breakoutColor, humanizeKind, strengthPct } from "./regime";

/** Fallback market inference from symbol shape (BTC/USD → crypto, AAPL → stocks). */
function inferMarket(symbol: string): Market {
  return symbol.includes("/") ? "crypto" : "stocks";
}

function BreakoutRowItem({
  b,
  marketFor,
}: {
  b: BreakoutRow;
  marketFor: (sym: string) => Market;
}) {
  const color = breakoutColor(b.kind);
  const hasSym = !!b.symbol;
  const pct = strengthPct(b.strength);
  return (
    <li
      className="flex flex-wrap items-center gap-x-2.5 gap-y-1 px-4 py-2.5 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <span
        className="chip shrink-0"
        style={{ color, borderColor: color }}
        aria-label={`event: ${humanizeKind(b.kind)}`}
      >
        {humanizeKind(b.kind) || "event"}
      </span>
      {hasSym ? (
        <Link
          href={`/s/${marketFor(b.symbol)}/${encodeURIComponent(b.symbol)}`}
          className="shrink-0 cursor-pointer text-[0.8rem] font-bold transition-colors duration-150 hover:text-[var(--accent)]"
        >
          {b.symbol}
        </Link>
      ) : (
        <span className="shrink-0 text-[0.78rem]" style={{ color: "var(--faint)" }}>
          market-wide
        </span>
      )}
      <span className="min-w-0 flex-1 truncate text-[0.75rem]" style={{ color: "var(--dim)" }} title={b.detail}>
        {b.detail || "—"}
      </span>
      {Number.isFinite(b.strength) && b.strength > 0 && (
        <span
          className="tnum shrink-0 text-[0.75rem]"
          style={{ color: "var(--faint)" }}
          title="event strength"
        >
          {pct.toFixed(0)}%
        </span>
      )}
      <span className="tnum shrink-0 text-[0.78rem]" style={{ color: "var(--faint)" }}>
        {ago(b.ts)}
      </span>
    </li>
  );
}

/** BREAKOUTS & CORRELATION BREAKS — trend-creation events + decoupling pairs. */
export default function BreakoutFeed({
  breakouts,
  marketBySymbol,
}: {
  breakouts: BreakoutRow[];
  marketBySymbol: Record<string, Market>;
}) {
  const sorted = useMemo(
    () => [...breakouts].sort((a, b) => b.ts - a.ts),
    [breakouts],
  );

  const marketFor = (sym: string): Market => marketBySymbol[sym] ?? inferMarket(sym);

  return (
    <section className="panel">
      <div className="panel-h">
        BREAKOUTS & CORRELATION BREAKS
        <span className="tnum" style={{ color: "var(--faint)" }}>
          newest first
        </span>
      </div>
      {sorted.length === 0 ? (
        <EmptyState
          className="border-0"
          message="No breakouts or correlation breaks recorded yet"
          detail="Events log here as bars stream in."
        />
      ) : (
        <ul>
          {sorted.map((b, i) => (
            <BreakoutRowItem key={`${b.symbol || "mkt"}:${b.ts}:${i}`} b={b} marketFor={marketFor} />
          ))}
        </ul>
      )}
      <div
        className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        trend-creation events + pairs that just decoupled.
      </div>
    </section>
  );
}
