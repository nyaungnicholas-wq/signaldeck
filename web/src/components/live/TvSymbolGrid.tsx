"use client";

// Per-streamed-symbol firing grid for the TradingView feed. One tile per
// streamed symbol: a green dot + "fired <age> ago (×count)" when the symbol has
// ever fired, a grey dot + "no signal yet" otherwise. Each dot carries a text
// label + aria-label — never colour alone. A symbol showing "no signal yet" is
// not a fault: crossing alerts fire sporadically.

import { ago } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import type { TvStatusSymbol } from "@/lib/api";

function SymbolTile({ s }: { s: TvStatusSymbol }) {
  const fired = s.lastFiredAt != null;
  const color = fired ? "var(--ok)" : "var(--faint)";
  const detail = fired ? `fired ${ago(s.lastFiredAt as number)} (×${s.count})` : "no signal yet";
  return (
    <div className="panel flex flex-col gap-1.5 p-3">
      <div className="flex items-center gap-2">
        <span
          role="img"
          aria-label={fired ? `${s.symbol} fired ${ago(s.lastFiredAt as number)}` : `${s.symbol} no signal yet`}
          className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
          style={{ background: color, boxShadow: fired ? "0 0 8px rgba(52,211,153,.5)" : undefined }}
        />
        <span className="mono text-sm font-bold" style={{ color: "var(--text)" }}>
          {s.symbol}
        </span>
        <span className="ml-auto chip uppercase tracking-wider" style={{ padding: "1px 7px" }}>
          {s.market}
        </span>
      </div>
      <span className="tnum text-[0.75rem]" style={{ color: fired ? "var(--dim)" : "var(--faint)" }}>
        {detail}
      </span>
    </div>
  );
}

export default function TvSymbolGrid({ symbols }: { symbols: TvStatusSymbol[] }) {
  if (symbols.length === 0) {
    return (
      <EmptyState
        message="No symbols are being streamed"
        detail="Streamed symbols (symbols.stream=1) light up here as TradingView alerts resolve to them."
      />
    );
  }
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
      {symbols.map((s) => (
        <SymbolTile key={`${s.market}:${s.symbol}`} s={s} />
      ))}
    </div>
  );
}
