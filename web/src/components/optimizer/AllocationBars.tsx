"use client";

// Horizontal allocation bars for one optimized portfolio: one row per symbol,
// sorted by weight DESCENDING, bar width ∝ weight (the literal fraction of the
// book). Zero-weight holdings sink to the bottom and dim out so the eye reads
// what actually got allocated. The weight is printed as a % on the right —
// never color-alone — and mirrored into each bar's aria-label for screen
// readers, so a concentrated result (e.g. min-variance → 100% SPY) still reads.
//
// A three-column CSS grid on the container (ticker · bar · %) keeps every row's
// columns aligned and sizes the ticker column to the widest symbol, so long
// crypto pairs like BTC/USD don't shove the bars out of line.

import { Fragment } from "react";

// Below 0.05% a weight rounds to "0.0%"; treat it as zero for dimming/ordering.
const EPS = 5e-4;

function fmtWeight(w: number): string {
  if (!isFinite(w)) return "—";
  return `${(w * 100).toFixed(1)}%`;
}

export default function AllocationBars({
  symbols,
  weights,
  accent = "var(--accent)",
}: {
  symbols: string[];
  weights: number[];
  /** Bar fill color — defaults to the amber accent; both cards use it. */
  accent?: string;
}) {
  const rows = symbols
    .map((symbol, i) => ({ symbol, weight: isFinite(weights[i]) ? weights[i] : 0 }))
    .sort((a, b) => b.weight - a.weight);

  if (rows.length === 0) {
    return (
      <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        no weights to show
      </p>
    );
  }

  return (
    <div
      role="group"
      aria-label="allocation by symbol"
      className="grid items-center gap-x-3 gap-y-2"
      style={{ gridTemplateColumns: "minmax(3rem, max-content) 1fr minmax(3.5rem, max-content)" }}
    >
      {rows.map((r) => {
        const zero = r.weight < EPS;
        const pct = Math.max(0, Math.min(100, r.weight * 100));
        const label = fmtWeight(r.weight);
        return (
          <Fragment key={r.symbol}>
            <span
              className="mono whitespace-nowrap text-[0.8125rem]"
              style={{ color: zero ? "var(--faint)" : "var(--text)", opacity: zero ? 0.7 : 1 }}
            >
              {r.symbol}
            </span>
            <div
              role="meter"
              aria-valuenow={Number(pct.toFixed(1))}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={`${r.symbol}: ${label} of portfolio`}
              className="relative h-2.5 overflow-hidden rounded-full"
              style={{ background: "var(--panel2)", border: "1px solid var(--border)" }}
            >
              <div
                className="h-full rounded-full transition-[width] duration-300"
                style={{ width: `${pct}%`, background: zero ? "transparent" : accent }}
              />
            </div>
            <span
              className="tnum whitespace-nowrap text-right text-[0.8125rem]"
              style={{ color: zero ? "var(--faint)" : "var(--text)" }}
            >
              {label}
            </span>
          </Fragment>
        );
      })}
    </div>
  );
}
