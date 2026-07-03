"use client";

import Link from "next/link";
import { useMemo } from "react";
import type { Market, RegimeChange } from "@/lib/api";
import { ago } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import { regimeColor } from "./regime";

/** Fallback market inference from symbol shape (BTC/USD → crypto, AAPL → stocks),
    used only when the states map doesn't carry the symbol. */
function inferMarket(symbol: string): Market {
  return symbol.includes("/") ? "crypto" : "stocks";
}

function ChangeRow({ c, marketFor }: { c: RegimeChange; marketFor: (sym: string) => Market }) {
  const fromColor = regimeColor(c.from);
  const toColor = regimeColor(c.to);
  return (
    <li
      className="flex flex-wrap items-center gap-x-2 gap-y-1 px-4 py-2.5 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <Link
        href={`/s/${marketFor(c.symbol)}/${encodeURIComponent(c.symbol)}`}
        className="w-20 shrink-0 cursor-pointer text-[0.8rem] font-bold transition-colors duration-150 hover:text-[var(--accent)]"
      >
        {c.symbol}
      </Link>
      <span className="tnum text-[0.78rem]" style={{ color: fromColor }}>
        {c.from || "—"}
      </span>
      <span aria-hidden="true" style={{ color: "var(--faint)" }}>
        →
      </span>
      <span className="tnum text-[0.78rem] font-bold" style={{ color: toColor }}>
        {c.to || "—"}
      </span>
      <span className="tnum ml-auto shrink-0 text-[0.78rem]" style={{ color: "var(--faint)" }}>
        {ago(c.ts)}
      </span>
    </li>
  );
}

/** REGIME CHANGES — the transition log; the change is the signal.
    `marketBySymbol` lets us build accurate symbol links (changes rows don't
    carry a market of their own); we fall back to shape inference. */
export default function RegimeChanges({
  changes,
  marketBySymbol,
}: {
  changes: RegimeChange[];
  marketBySymbol: Record<string, Market>;
}) {
  const sorted = useMemo(
    () => [...changes].sort((a, b) => b.ts - a.ts),
    [changes],
  );

  const marketFor = (sym: string): Market => marketBySymbol[sym] ?? inferMarket(sym);

  return (
    <section className="panel">
      <div className="panel-h">
        REGIME CHANGES
        <span className="tnum" style={{ color: "var(--faint)" }}>
          transitions, newest first
        </span>
      </div>
      {sorted.length === 0 ? (
        <EmptyState
          className="border-0"
          message="No regime changes recorded yet"
          detail="Transitions log here as the data grows."
        />
      ) : (
        <ul>
          {sorted.map((c, i) => (
            <ChangeRow key={`${c.symbol}:${c.ts}:${i}`} c={c} marketFor={marketFor} />
          ))}
        </ul>
      )}
      <div
        className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        The transition is the signal — a regime change often precedes the move.
      </div>
    </section>
  );
}
