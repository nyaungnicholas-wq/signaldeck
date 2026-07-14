"use client";

// PRESSURE panel — the no-black-box promise. A gauge per scored horizon,
// then the full component decomposition table for the selected horizon.

import type { Horizon, Score } from "@/lib/api";
import { fmtScore, fmtTs } from "@/lib/format";
import ScoreGauge from "@/components/ScoreGauge";
import HorizonChips from "./HorizonChips";
import EmptyState from "@/components/EmptyState";

function fmtVal(v: number): string {
  if (!isFinite(v)) return "—";
  const a = Math.abs(v);
  if (a >= 1000) return v.toLocaleString("en-US", { maximumFractionDigits: 1 });
  if (a >= 1) return v.toFixed(2);
  if (a === 0) return "0";
  return v.toPrecision(3);
}

export default function PressurePanel({
  scores,
  horizon,
  onHorizon,
}: {
  scores: Partial<Record<Horizon, Score>>;
  horizon: Horizon;
  onHorizon: (h: Horizon) => void;
}) {
  const present = (Object.keys(scores) as Horizon[]).filter((h) => scores[h]);
  const sel = scores[horizon];

  return (
    <section className="panel">
      <div className="panel-h">
        <span>PRESSURE</span>
        <span className="ml-auto">
          <HorizonChips value={horizon} onChange={onHorizon} available={present.length ? present : undefined} />
        </span>
      </div>
      <div className="flex flex-col gap-4 p-4">
        {present.length === 0 ? (
          <EmptyState
            message="No scores yet"
            detail="The scorer needs a few bars of history first."
          />
        ) : (
          <>
            <div className="flex flex-col gap-3">
              {present.map((h) => (
                <ScoreGauge key={h} score={scores[h]!.score} label={`${h} pressure`} />
              ))}
            </div>

            {sel ? (
              <div>
                <div className="mb-1 flex items-baseline justify-between text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                  <span>COMPONENT DECOMPOSITION · {horizon}</span>
                  <span className="tnum">as of {fmtTs(sel.ts)}</span>
                </div>
                {sel.components.length === 0 ? (
                  <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    no component breakdown stored for this score.
                  </p>
                ) : (
                  <div className="table-wrap">
                    <table className="w-full text-[0.75rem] tnum">
                      <thead>
                        <tr className="text-left text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                          <th className="py-1.5 pr-3 font-medium" title="Signal component (e.g. momentum, imbalance)">
                            COMPONENT
                          </th>
                          <th className="py-1.5 pr-3 text-right font-medium" title="Raw measured value">
                            VALUE
                          </th>
                          <th className="py-1.5 pr-3 text-right font-medium" title="Normalized value, scaled to −1…+1">
                            NORM
                          </th>
                          <th className="py-1.5 pr-3 text-right font-medium" title="Weight of this component in the total score">
                            WEIGHT
                          </th>
                          <th className="py-1.5 pr-3 text-right font-medium" title="Contribution to the total score (norm × weight)">
                            CONTRIB
                          </th>
                          <th className="py-1.5 font-medium">NOTE</th>
                        </tr>
                      </thead>
                      <tbody>
                        {sel.components.map((c, i) => (
                          <tr key={`${c.name}-${i}`} className="border-t" style={{ borderColor: "var(--border)" }}>
                            <td className="py-1.5 pr-3" style={{ color: "var(--text)" }}>{c.name}</td>
                            <td className="py-1.5 pr-3 text-right" style={{ color: "var(--dim)" }}>{fmtVal(c.value)}</td>
                            <td className="py-1.5 pr-3 text-right" style={{ color: "var(--dim)" }}>{fmtVal(c.norm)}</td>
                            <td className="py-1.5 pr-3 text-right" style={{ color: "var(--dim)" }}>{fmtVal(c.weight)}</td>
                            <td
                              className="py-1.5 pr-3 text-right"
                              style={{ color: c.contrib > 0 ? "var(--bid)" : c.contrib < 0 ? "var(--ask)" : "var(--dim)" }}
                            >
                              {fmtScore(c.contrib)}
                            </td>
                            <td className="py-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>{c.note || "—"}</td>
                          </tr>
                        ))}
                        <tr className="border-t" style={{ borderColor: "var(--border)" }}>
                          <td className="py-1.5 pr-3 text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                            TOTAL
                          </td>
                          <td colSpan={3} />
                          <td
                            className="py-1.5 pr-3 text-right font-semibold"
                            style={{ color: sel.score > 0 ? "var(--bid)" : sel.score < 0 ? "var(--ask)" : "var(--dim)" }}
                          >
                            {fmtScore(sel.score)}
                          </td>
                          <td />
                        </tr>
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            ) : (
              <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                no {horizon} score yet — pick another horizon above.
              </p>
            )}
          </>
        )}
      </div>
    </section>
  );
}
