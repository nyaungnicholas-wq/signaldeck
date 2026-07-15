"use client";

// HIGH-CONVICTION OPPORTUNITIES — the ranked shortlist from the recommendation
// engine. Each row is a real, clickable pick that re-aims the recommendation
// column to its right: symbol, the color-coded decision, the 1–10 conviction
// score, a confidence-band chip (low amber / moderate neutral / high green),
// and the modeled expected return when the engine has one. The engine's own
// caveat rides the footnote. Presentational only — the page owns the fetch so
// its first row can seed the default selection.

import ErrorState from "@/components/ErrorState";
import Skeleton from "@/components/Skeleton";

export interface TopRow {
  symbol: string;
  market: string;
  decision: string;
  score: number;
  confidenceLabel: string;
  band: "low" | "moderate" | "high" | string;
  edge: number;
  expectedReturnPct: number;
  hasExpectedReturn: boolean;
}

/** Decision → semantic color: buy-side green, sell-side red, watch amber,
 *  hold/unknown neutral. */
function decisionColor(d: string): string {
  const s = (d || "").toLowerCase();
  if (s === "buy" || s === "accumulate") return "var(--ok)";
  if (s === "reduce" || s === "avoid") return "var(--bad)";
  if (s === "watch") return "var(--warn)";
  return "var(--dim)";
}

/** Confidence band → chip color: high=green, low=amber, moderate=neutral. */
function bandColor(band: string): string {
  if (band === "high") return "var(--ok)";
  if (band === "low") return "var(--warn)";
  return "var(--faint)";
}

export default function OpportunityList({
  rows,
  note,
  err,
  activeKey,
  onSelect,
}: {
  rows: TopRow[] | null;
  note: string;
  err: string | null;
  activeKey: string;
  onSelect: (symbol: string, market: string) => void;
}) {
  const list = Array.isArray(rows) ? rows : null;

  return (
    <section className="panel self-start">
      <div className="panel-h">HIGH-CONVICTION OPPORTUNITIES</div>

      {err && list === null ? (
        <ErrorState message={err} className="border-0" />
      ) : list === null ? (
        <div className="px-4 py-4">
          <Skeleton lines={6} label="loading opportunities" className="border-0 p-0" />
        </div>
      ) : list.length === 0 ? (
        <p className="px-4 py-4 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          No ranked opportunities yet — picks appear here as the engine scores the universe.
        </p>
      ) : (
        <ul className="flex flex-col">
          {list.map((r) => {
            const key = `${r.symbol}|${r.market}`;
            const active = key === activeKey;
            return (
              <li key={key}>
                <button
                  type="button"
                  onClick={() => onSelect(r.symbol, r.market)}
                  aria-current={active ? "true" : undefined}
                  className="flex w-full cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-left transition-colors duration-150 hover:bg-[rgba(255,255,255,0.02)]"
                  style={{
                    borderTop: "1px solid var(--border)",
                    background: active ? "var(--panel3)" : "transparent",
                    boxShadow: active ? "inset 2px 0 0 var(--accent)" : undefined,
                  }}
                >
                  <span className="mono font-bold" style={{ color: "var(--text)" }}>
                    {r.symbol}
                  </span>
                  <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {r.market}
                  </span>
                  <span className="text-[0.75rem] font-bold" style={{ color: decisionColor(r.decision) }}>
                    {r.decision}
                  </span>
                  <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--dim)" }}>
                    {Number.isFinite(r.score) ? r.score.toFixed(1) : "—"}
                    <span style={{ color: "var(--faint)" }}>/10</span>
                  </span>
                  <span
                    className="chip"
                    style={{ color: bandColor(r.band), borderColor: bandColor(r.band) }}
                    title={r.confidenceLabel || "confidence band"}
                  >
                    {r.band}
                  </span>
                  {r.hasExpectedReturn ? (
                    <span
                      className="tnum text-[0.75rem]"
                      style={{ color: r.expectedReturnPct >= 0 ? "var(--ok)" : "var(--bad)" }}
                    >
                      {r.expectedReturnPct >= 0 ? "+" : ""}
                      {Number.isFinite(r.expectedReturnPct) ? r.expectedReturnPct.toFixed(1) : "—"}%
                    </span>
                  ) : null}
                </button>
              </li>
            );
          })}
        </ul>
      )}

      {note ? (
        <p
          className="px-4 py-2.5 text-[0.75rem] leading-relaxed"
          style={{ color: "var(--faint)", borderTop: "1px solid var(--border)" }}
        >
          {note}
        </p>
      ) : null}
    </section>
  );
}
