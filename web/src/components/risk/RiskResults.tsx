"use client";

// Result sections for /lab/risk — plain-English headline, VaR cells, risk
// drivers and stress scenarios, extracted from the old monolithic page (pure
// refactor; behavior identical). Every VaR figure keeps its visible
// plain-English hint and the parametric-VaR caveat stays visible in both
// modes; the annual-vol explanation moved from a hover-only header title to
// a click/keyboard HelpTip.

import { type RiskReport } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";

// $ amount from a loss percent, always shown as the dollar magnitude lost.
function lossUSD(pct: number, notional: number): number {
  return (Math.abs(pct) / 100) * notional;
}

function fmtUSD(v: number): string {
  if (!isFinite(v)) return "—";
  return v.toLocaleString("en-US", { maximumFractionDigits: 0 });
}

/** One VaR figure: percent + dollar translation, framed as a loss. */
function VaRCell({
  label,
  labelTitle,
  pct,
  notional,
  hint,
  faded = false,
}: {
  label: string;
  labelTitle?: string;
  pct: number;
  notional: number;
  hint: string;
  faded?: boolean;
}) {
  const has = isFinite(pct);
  return (
    <div
      className="flex flex-col gap-1 px-4 py-4"
      style={{ borderRight: "1px solid var(--border)" }}
    >
      <div className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }} title={labelTitle}>
        {label}
      </div>
      <div
        className="tnum text-[1.5rem] font-bold leading-none"
        style={{ color: has ? "var(--bad)" : "var(--faint)", opacity: faded ? 0.75 : 1 }}
      >
        {has ? `−${Math.abs(pct).toFixed(2)}%` : "—"}
      </div>
      <div className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
        {has ? `−$${fmtUSD(lossUSD(pct, notional))}` : "—"}
      </div>
      <div className="mt-1 text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
        {hint}
      </div>
    </div>
  );
}

export default function RiskResults({
  report,
  summary,
  ranNotional,
  drivers,
  maxPct,
}: {
  report: RiskReport;
  summary: string;
  ranNotional: number;
  drivers: RiskReport["Contributions"];
  maxPct: number;
}) {
  return (
    <>
      {/* plain-English headline */}
      {summary && (
        <section className="panel">
          <div className="px-5 py-5">
            <p
              className="text-[1.05rem] font-medium leading-relaxed"
              style={{ color: "var(--text)" }}
            >
              {summary}
            </p>
            <p className="mt-2 text-[0.75rem] tnum" style={{ color: "var(--faint)" }}>
              priced against ${fmtUSD(ranNotional)} notional ·{" "}
              {(report.Confidence * 100).toFixed(0)}% confidence · 1-day horizon
            </p>
          </div>
        </section>
      )}

      {/* VaR table */}
      <section className="panel">
        <div className="panel-h">
          VALUE AT RISK
          <span className="tnum" style={{ color: "var(--faint)" }}>
            worst plausible 1-day loss ·{" "}
            {(report.Confidence * 100).toFixed(0)}% confidence
          </span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3">
          <VaRCell
            label="HISTORICAL VaR (95%)"
            labelTitle="Value at risk — the worst plausible 1-day loss at 95% confidence, from actual return history"
            pct={report.HistVaRPct}
            notional={ranNotional}
            hint="1 day in 20, losses have been at least this bad — measured from actual return history."
          />
          <VaRCell
            label="HISTORICAL CVaR (95%)"
            labelTitle="Conditional value at risk — the average loss on the worst 1-in-20 days"
            pct={report.HistCVaRPct}
            notional={ranNotional}
            hint="the average loss on those worst 1-in-20 days — how deep the tail actually runs."
          />
          <VaRCell
            label="PARAMETRIC VaR (95%)"
            labelTitle="Value at risk estimated from a normal distribution of returns"
            pct={report.ParamVaRPct}
            notional={ranNotional}
            hint="the normal-curve estimate — cleaner, but blind to fat tails."
            faded
          />
        </div>
        <div
          className="px-4 py-3 text-[0.75rem] leading-relaxed"
          style={{ borderTop: "1px solid var(--border)", color: "var(--faint)" }}
        >
          <span style={{ color: "var(--warn)" }}>note:</span> parametric VaR assumes normal
          returns — real tails are fatter, so it tends to understate risk. Historical VaR is
          the more honest number.
        </div>
      </section>

      {/* risk drivers */}
      <section className="panel">
        <div className="panel-h">
          RISK DRIVERS
          <span className="tnum" style={{ color: "var(--faint)" }}>
            where the portfolio&apos;s risk actually comes from
          </span>
        </div>
        {drivers.length === 0 ? (
          <EmptyState
            className="border-0"
            message="No risk contributions returned"
            detail="The daemon may lack return history for these symbols — let bars accrue and run again."
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-[0.75rem]">
              <thead>
                <tr style={{ borderBottom: "1px solid var(--border)" }}>
                  <th
                    scope="col"
                    className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    SYMBOL
                  </th>
                  <th
                    scope="col"
                    className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    SHARE OF RISK
                  </th>
                  <th
                    scope="col"
                    className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    WEIGHT
                  </th>
                  <th
                    scope="col"
                    className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    <span className="inline-flex items-center gap-1">
                      ANNUAL VOL
                      <HelpTip label="What is annual vol?">
                        Annualized volatility — how much this symbol swings in a
                        typical year, measured from stored return history.
                      </HelpTip>
                    </span>
                  </th>
                </tr>
              </thead>
              <tbody className="tnum">
                {drivers.map((c) => {
                  const share = isFinite(c.PctOfRisk) ? c.PctOfRisk : 0;
                  const barPct = maxPct > 0 ? (share / maxPct) * 100 : 0;
                  return (
                    <tr
                      key={c.Symbol}
                      className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ borderBottom: "1px solid var(--border)" }}
                    >
                      <td className="px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                        {c.Symbol}
                      </td>
                      <td className="px-3 py-2">
                        <div className="flex items-center gap-2">
                          <div
                            className="h-2 flex-1 overflow-hidden rounded-full"
                            style={{ background: "var(--panel2)", minWidth: 80 }}
                            role="meter"
                            aria-valuemin={0}
                            aria-valuemax={100}
                            aria-valuenow={Number((share * 100).toFixed(1))}
                            aria-label={`${c.Symbol} share of portfolio risk`}
                          >
                            <div
                              className="h-full transition-[width] duration-300"
                              style={{ width: `${barPct}%`, background: "var(--accent)" }}
                            />
                          </div>
                          <span
                            className="w-12 shrink-0 text-right"
                            style={{ color: "var(--text)" }}
                          >
                            {(share * 100).toFixed(1)}%
                          </span>
                        </div>
                      </td>
                      <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                        {(c.Weight * 100).toFixed(1)}%
                      </td>
                      <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                        {isFinite(c.Vol) ? `${(c.Vol * 100).toFixed(1)}%` : "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* stress scenarios */}
      <section className="panel">
        <div className="panel-h">
          STRESS SCENARIOS
          <span className="tnum" style={{ color: "var(--faint)" }}>
            what this book would have done in known shocks
          </span>
        </div>
        {(report.Scenarios ?? []).length === 0 ? (
          <EmptyState
            className="border-0"
            message="No stress scenarios returned for this portfolio"
            detail="Scenario replays need enough shared history across the holdings."
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-[0.75rem]">
              <thead>
                <tr style={{ borderBottom: "1px solid var(--border)" }}>
                  <th
                    scope="col"
                    className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    SCENARIO
                  </th>
                  <th
                    scope="col"
                    className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                    title="Profit and loss"
                  >
                    P&amp;L
                  </th>
                  <th
                    scope="col"
                    className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    ON ${fmtUSD(ranNotional)}
                  </th>
                  <th
                    scope="col"
                    className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                    style={{ color: "var(--faint)" }}
                  >
                    DETAIL
                  </th>
                </tr>
              </thead>
              <tbody className="tnum">
                {report.Scenarios.map((s, i) => {
                  const has = isFinite(s.PnLPct);
                  const color = !has
                    ? "var(--faint)"
                    : s.PnLPct > 0
                      ? "var(--bid)"
                      : s.PnLPct < 0
                        ? "var(--ask)"
                        : "var(--dim)";
                  const usd = has ? (s.PnLPct / 100) * ranNotional : NaN;
                  return (
                    <tr
                      key={`${s.Name}:${i}`}
                      className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ borderBottom: "1px solid var(--border)" }}
                    >
                      <td className="px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                        {s.Name}
                      </td>
                      <td className="px-3 py-2 text-right" style={{ color }}>
                        {has ? fmtPct(s.PnLPct) : "—"}
                      </td>
                      <td className="px-3 py-2 text-right" style={{ color }}>
                        {has
                          ? `${usd >= 0 ? "+" : "−"}$${fmtUSD(Math.abs(usd))}`
                          : "—"}
                      </td>
                      <td className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--dim)" }}>
                        {s.Detail || "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
