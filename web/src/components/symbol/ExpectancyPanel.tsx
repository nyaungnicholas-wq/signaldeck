"use client";

// WHAT USUALLY HAPPENS NEXT — measured historical tendencies per market
// state, with sample sizes. The row matching the symbol's current state
// is highlighted. Explicitly NOT a forecast.

import type { Expectancy, Horizon } from "@/lib/api";
import { fmtPct, humanizeState } from "@/lib/format";
import HorizonChips from "./HorizonChips";

export default function ExpectancyPanel({
  expectancy,
  stateKeys,
  horizon,
  onHorizon,
}: {
  expectancy: Partial<Record<Horizon, Expectancy[]>>;
  stateKeys: Partial<Record<Horizon, string>>;
  horizon: Horizon;
  onHorizon: (h: Horizon) => void;
}) {
  const rows = [...(expectancy[horizon] ?? [])].sort((a, b) => b.n - a.n);
  const currentKey = stateKeys[horizon] ?? "";
  const available = (Object.keys(expectancy) as Horizon[]).filter(
    (h) => (expectancy[h] ?? []).length > 0
  );

  return (
    <section className="panel">
      <div className="panel-h">
        <span>WHAT USUALLY HAPPENS NEXT</span>
        <span className="ml-auto">
          <HorizonChips value={horizon} onChange={onHorizon} available={available.length ? available : undefined} />
        </span>
      </div>
      <div className="flex flex-col gap-3 p-4">
        <p className="text-[0.78rem]" style={{ color: "var(--text)" }}>
          <span style={{ color: "var(--dim)" }}>current state: </span>
          {humanizeState(stateKeys["1d"] ?? "")}
        </p>

        {rows.length === 0 ? (
          <p className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
            no {horizon} expectancy yet — tendencies appear once enough
            history has been observed for each state.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-[0.8rem] tnum">
              <thead>
                <tr className="text-left text-[0.64rem] tracking-wide" style={{ color: "var(--faint)" }}>
                  <th className="py-1.5 pr-3 font-medium">STATE</th>
                  <th className="py-1.5 pr-3 text-right font-medium">N</th>
                  <th className="py-1.5 pr-3 text-right font-medium">HIT RATE</th>
                  <th className="py-1.5 pr-3 text-right font-medium">MEAN FWD</th>
                  <th className="py-1.5 text-right font-medium">MEDIAN FWD</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r, i) => {
                  const isNow = !!currentKey && r.stateKey === currentKey;
                  return (
                    <tr
                      key={`${r.stateKey}-${i}`}
                      className="border-t"
                      style={{
                        borderColor: "var(--border)",
                        background: isNow ? "rgba(251,191,36,0.06)" : undefined,
                      }}
                    >
                      <td className="py-1.5 pr-3" style={{ color: isNow ? "var(--text)" : "var(--dim)" }}>
                        {humanizeState(r.stateKey)}
                        {isNow && (
                          <span
                            className="ml-2 rounded border px-1.5 py-0.5 text-[0.6rem] tracking-wider"
                            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
                          >
                            NOW
                          </span>
                        )}
                      </td>
                      <td className="py-1.5 pr-3 text-right" style={{ color: "var(--dim)" }}>{r.n}</td>
                      <td className="py-1.5 pr-3 text-right" style={{ color: "var(--text)" }}>
                        {fmtPct(r.hitRate * 100, false)}
                      </td>
                      <td
                        className="py-1.5 pr-3 text-right"
                        style={{ color: r.meanFwd > 0 ? "var(--bid)" : r.meanFwd < 0 ? "var(--ask)" : "var(--dim)" }}
                      >
                        {fmtPct(r.meanFwd * 100)}
                      </td>
                      <td
                        className="py-1.5 text-right"
                        style={{ color: r.medianFwd > 0 ? "var(--bid)" : r.medianFwd < 0 ? "var(--ask)" : "var(--dim)" }}
                      >
                        {fmtPct(r.medianFwd * 100)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        <p className="text-[0.66rem]" style={{ color: "var(--faint)" }}>
          Measured historical tendencies with sample sizes — not forecasts.
        </p>
      </div>
    </section>
  );
}
