"use client";

// Correlation panel — extracted from the old monolithic /lab/portfolio page.
// The plain-English most/least-correlated sentences render first (visible in
// both modes); the raw matrix folds behind a ProOnly disclosure in SIMPLE
// mode. The diversification explanation moved from a hover-only title to a
// click/keyboard HelpTip.

import { type CorrelationResponse } from "@/lib/api";
import { scoreColor } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";

// ── correlation cell color ──────────────────────────────────────────────
// +correlation (move together) → red (--ask); −correlation (diversifying) →
// green (--bid). Diagonal (self, r=1) is muted so the eye reads the off-axis.
function corrCell(r: number): { bg: string; fg: string } {
  if (!isFinite(r)) return { bg: "var(--panel2)", fg: "var(--faint)" };
  const a = Math.min(1, Math.abs(r));
  if (r >= 0) return { bg: `rgba(248,113,113,${(0.12 + a * 0.5).toFixed(3)})`, fg: "var(--text)" };
  return { bg: `rgba(52,211,153,${(0.12 + a * 0.5).toFixed(3)})`, fg: "var(--text)" };
}

function fmtR(r: number): string {
  if (!isFinite(r)) return "—";
  return `${r >= 0 ? "" : "−"}${Math.abs(r).toFixed(2)}`;
}

function plainCorr(pair: [string, string], r: number, high: boolean): string {
  if (!pair || !pair[0] || !pair[1] || !isFinite(r)) return "";
  const [a, b] = pair;
  if (high) {
    return `Most correlated: ${a} & ${b} at ${fmtR(r)} — they move together, little diversification.`;
  }
  return `Least correlated: ${a} & ${b} at ${fmtR(r)} — the strongest diversifier in the set.`;
}

export default function CorrelationPanel({
  data,
  err,
  loading,
  onRefresh,
  refreshing,
}: {
  data: CorrelationResponse | null;
  err: string | null;
  loading: boolean;
  onRefresh: () => void;
  refreshing: boolean;
}) {
  const symbols = data?.symbols ?? [];
  const matrix = data?.matrix ?? [];
  const enough = symbols.length >= 2 && matrix.length === symbols.length;

  return (
    <section className="panel">
      <div className="panel-h">
        CORRELATION — WHAT ACTUALLY DIVERSIFIES
        {data && enough && (
          <span className="flex items-center gap-1.5">
            <span className="tnum" style={{ color: "var(--dim)" }}>
              diversification{" "}
              <span style={{ color: scoreColor(data.diversification * 2 - 1) }}>
                {data.diversification.toFixed(2)}
              </span>
              <span style={{ color: "var(--faint)" }}> / 1</span>
            </span>
            <HelpTip label="How to read the diversification score">
              0 means every symbol moves in lockstep (no diversification);
              1 means the holdings move independently of each other.
            </HelpTip>
          </span>
        )}
        <button
          type="button"
          onClick={onRefresh}
          disabled={refreshing}
          className="ml-auto min-h-[36px] cursor-pointer rounded-lg border px-3 py-1 text-[0.75rem] tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
          style={{ borderColor: "var(--border)", color: "var(--dim)", background: "var(--panel2)" }}
        >
          {refreshing ? "refreshing…" : "refresh"}
        </button>
      </div>

      {loading && (
        <div className="p-4">
          <Skeleton lines={4} label="loading correlation matrix" className="border-0 p-0" />
        </div>
      )}

      {err && !data && (
        <ErrorState className="m-4" message={err} retry={onRefresh} />
      )}

      {data && !enough && (
        <EmptyState
          className="m-4"
          message="Not enough symbols with overlapping history to correlate."
          detail="Subscribe to at least two symbols, let daily bars accrue, then refresh."
        />
      )}

      {data && enough && (
        <>
          {/* plain-English read first — visible in both modes */}
          <div className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            <p>{plainCorr(data.mostPair, data.mostR, true)}</p>
            <p className="mt-1">{plainCorr(data.leastPair, data.leastR, false)}</p>
          </div>

          {/* the raw matrix is PRO detail — SIMPLE mode folds it */}
          <div className="border-t px-4 py-3" style={{ borderColor: "var(--border)" }}>
            <ProOnly summary="Show the correlation matrix">
              <div className="overflow-x-auto">
                <table className="tnum border-separate" style={{ borderSpacing: 2 }}>
                  <thead>
                    <tr>
                      <th className="px-2 py-1" aria-hidden="true" />
                      {symbols.map((s) => (
                        <th
                          key={s}
                          scope="col"
                          className="px-1.5 py-1 text-[0.75rem] font-medium"
                          style={{ color: "var(--faint)" }}
                        >
                          {s}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {symbols.map((rowSym, i) => (
                      <tr key={rowSym}>
                        <th
                          scope="row"
                          className="pr-2 text-right text-[0.75rem] font-medium whitespace-nowrap"
                          style={{ color: "var(--faint)" }}
                        >
                          {rowSym}
                        </th>
                        {symbols.map((colSym, j) => {
                          const r = matrix[i]?.[j];
                          const diag = i === j;
                          const c = diag ? { bg: "var(--panel2)", fg: "var(--faint)" } : corrCell(r);
                          return (
                            <td
                              key={colSym}
                              title={`${rowSym} × ${colSym}: ${fmtR(r)}`}
                              className="h-9 w-11 rounded text-center text-[0.75rem]"
                              style={{ background: c.bg, color: c.fg }}
                            >
                              {diag ? "—" : fmtR(r)}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div
                className="mt-2 flex flex-wrap items-center gap-x-5 gap-y-1 text-[0.75rem]"
                style={{ color: "var(--faint)" }}
              >
                <span className="flex items-center gap-1.5">
                  <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: "rgba(248,113,113,.55)" }} />
                  move together (+)
                </span>
                <span className="flex items-center gap-1.5">
                  <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: "rgba(52,211,153,.55)" }} />
                  move opposite (−) · diversifying
                </span>
              </div>
            </ProOnly>
          </div>
        </>
      )}
    </section>
  );
}
