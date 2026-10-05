"use client";

// ADDITIVE LEDGER — the waterfall from the 50% coin-flip baseline to the
// calibrated P(up). Each ledger entry is one signed pp contribution (green
// pushes right/up, red pushes left/down); the calibration adjustment is the
// LAST entry by API construction. The attribution method renders VERBATIM
// with an exact/proportional badge, and the sum line reconciles the entries
// to the target — if they don't add up, the page says so instead of hiding it.

import type { CompositeLedger, CompositeLedgerEntry } from "@/lib/api";
import { useViewMode } from "@/components/Plain";
import { fmtPp, legName } from "./compositeUi";

type WalkRow = CompositeLedgerEntry & { from: number; to: number };

export default function AdditiveLedger({
  ledger,
  calProb,
}: {
  ledger: CompositeLedger;
  calProb: number;
}) {
  const mode = useViewMode();
  const entries = ledger.entries ?? [];

  // Cumulative walk in pp from the 50% baseline (0 on this axis).
  const rows = entries.reduce<WalkRow[]>((acc, e) => {
    const from = acc.length ? acc[acc.length - 1].to : 0;
    acc.push({ ...e, from, to: from + e.contribPp });
    return acc;
  }, []);
  const lo = Math.min(0, ledger.targetPp, ...rows.map((r) => Math.min(r.from, r.to)));
  const hi = Math.max(0, ledger.targetPp, ...rows.map((r) => Math.max(r.from, r.to)));
  const span = hi - lo || 1;
  const x = (v: number) => ((v - lo) / span) * 100;

  const sumsMatch = Math.abs(ledger.sumPp - ledger.targetPp) < 0.05;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        {mode === "simple" ? "HOW THE NUMBER ADDS UP" : "ADDITIVE EDGE LEDGER (pp FROM 50%)"}
        {/* attribution honesty badge — exact decomposition vs proportional */}
        <span
          className="chip"
          title={
            ledger.exact
              ? "entries sum exactly to the target edge"
              : "blend weights are not persisted on the prediction row — contributions are attributed proportionally"
          }
          style={
            ledger.exact
              ? { color: "var(--ok)", borderColor: "var(--ok)" }
              : { color: "var(--warn)", borderColor: "var(--warn)" }
          }
        >
          {ledger.exact ? "exact" : "proportional"}
        </span>
        <span className="chip tnum ml-auto" title="the calibrated probability the ledger lands on">
          50% → {(calProb * 100).toFixed(1)}%
        </span>
      </div>

      {rows.length === 0 ? (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          No ledger entries stored on this verdict — the method line below says why.
        </p>
      ) : (
        <div className="flex flex-col gap-1.5 px-4 py-3">
          {/* baseline label row */}
          <div className="flex items-center gap-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            <span className="w-32 shrink-0 sm:w-40">leg</span>
            <div className="relative h-4 flex-1">
              <span
                className="absolute top-0 -translate-x-1/2 whitespace-nowrap"
                style={{ left: `${x(0)}%` }}
              >
                50% baseline
              </span>
            </div>
            <span className="w-16 shrink-0 text-right">contrib</span>
            {mode === "pro" && <span className="w-12 shrink-0 text-right">leg P</span>}
          </div>

          {rows.map((r, i) => {
            const pos = r.contribPp >= 0;
            const color = pos ? "var(--bid)" : "var(--ask)";
            const left = x(Math.min(r.from, r.to));
            const width = Math.max(0.5, Math.abs(x(r.to) - x(r.from)));
            const isCal = r.leg === "calibration";
            return (
              <div key={`${r.leg}-${i}`} className="flex items-center gap-2">
                <span
                  className="w-32 shrink-0 truncate text-[0.75rem] sm:w-40"
                  style={{ color: isCal ? "var(--accent)" : "var(--dim)" }}
                  title={r.leg}
                >
                  {legName(r.leg, mode)}
                </span>
                <div
                  className="relative h-4 flex-1 overflow-hidden rounded-sm"
                  style={{ background: "color-mix(in srgb, var(--faint) 10%, transparent)" }}
                  role="img"
                  aria-label={`${r.leg} contributes ${fmtPp(r.contribPp)} (running total ${fmtPp(r.to)})`}
                >
                  {/* the 50% baseline tick, on every row */}
                  <div
                    aria-hidden="true"
                    className="absolute top-0 h-full"
                    style={{ left: `${x(0)}%`, width: 1, background: "var(--faint)" }}
                  />
                  <div
                    className="absolute top-0.5 bottom-0.5 rounded-sm"
                    style={{ left: `${left}%`, width: `${width}%`, background: color }}
                  />
                </div>
                <span className="tnum w-16 shrink-0 text-right text-[0.75rem]" style={{ color }}>
                  {fmtPp(r.contribPp)}
                </span>
                {mode === "pro" && (
                  <span
                    className="tnum w-12 shrink-0 text-right text-[0.75rem]"
                    style={{ color: "var(--faint)" }}
                    title={r.prob != null ? "this leg's own P(up) input" : undefined}
                  >
                    {r.prob != null ? `${(r.prob * 100).toFixed(0)}%` : ""}
                  </span>
                )}
              </div>
            );
          })}

          {/* landing row: where the walk ends — the calibrated P(up) */}
          <div className="flex items-center gap-2">
            <span
              className="w-32 shrink-0 text-[0.75rem] font-bold sm:w-40"
              style={{ color: "var(--text)" }}
            >
              {mode === "simple" ? "= calibrated chance" : "= calProb"}
            </span>
            <div className="relative h-4 flex-1">
              <div
                aria-hidden="true"
                className="absolute top-0 h-full"
                style={{ left: `${x(0)}%`, width: 1, background: "var(--faint)" }}
              />
              <div
                className="absolute top-0 h-full rounded-sm"
                style={{ left: `${x(ledger.targetPp)}%`, width: 2, background: "var(--accent)" }}
              />
            </div>
            <span className="tnum w-16 shrink-0 text-right text-[0.75rem] font-bold">
              {(calProb * 100).toFixed(1)}%
            </span>
            {mode === "pro" && <span className="w-12 shrink-0" />}
          </div>

          {/* the reconciliation line — the ledger must add up, visibly */}
          <p
            className="tnum mt-1 text-[0.75rem]"
            style={{ color: sumsMatch ? "var(--faint)" : "var(--warn)" }}
          >
            sums to {fmtPp(ledger.sumPp)} (target {fmtPp(ledger.targetPp)})
            {sumsMatch ? "" : " — entries do not reconcile exactly; see the method line"}
          </p>
        </div>
      )}

      {/* attribution method — verbatim, always visible */}
      <p
        className="px-4 py-2.5 text-[0.75rem] leading-relaxed"
        style={{ color: "var(--faint)", borderTop: "1px solid var(--border)" }}
      >
        {ledger.method}
      </p>
    </section>
  );
}
