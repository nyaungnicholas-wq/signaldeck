"use client";

// /lab/risk — thin composition page. State lives in useRisk(); the builder
// and result sections live in src/components/risk/ (pure refactor of the old
// monolithic page). Request-driven: nothing polls, the report recomputes only
// when the user runs it.

import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import PortfolioBuilder from "@/components/risk/PortfolioBuilder";
import RiskResults from "@/components/risk/RiskResults";
import useRisk, { DEFAULT_NOTIONAL } from "@/hooks/useRisk";

function fmtUSD(v: number): string {
  if (!isFinite(v)) return "—";
  return v.toLocaleString("en-US", { maximumFractionDigits: 0 });
}

export default function RiskPage() {
  const r = useRisk();

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">RISK</h1>
        <span className="chip">RiskLens</span>
        <span className="chip tnum">
          {r.validHoldings.length} holding{r.validHoldings.length === 1 ? "" : "s"}
        </span>
        <span
          className="chip tnum"
          style={{ color: r.totalWeight > 0 ? "var(--dim)" : "var(--faint)" }}
        >
          weights total {r.totalWeight.toFixed(0)}%
          {r.totalWeight > 0 && r.totalWeight !== 100 ? " · normalized" : ""}
        </span>
        {r.report !== null && (
          <span className="chip tnum">
            {(r.report.Confidence * 100).toFixed(0)}% confidence · 1-day
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-risk"
        text="How risky is a given mix of holdings, and what actually diversifies it? Computed from stored history — the past, which is not a guarantee."
      />

      {/* builder */}
      <PortfolioBuilder
        rows={r.rows}
        setRow={r.setRow}
        addRow={r.addRow}
        removeRow={r.removeRow}
        picker={r.picker}
        watch={r.watch}
        watchErr={r.watchErr}
        addFromWatchlist={r.addFromWatchlist}
        equalWeightWatchlist={r.equalWeightWatchlist}
        notional={r.notional}
        setNotional={r.setNotional}
        run={r.run}
        running={r.running}
        canRun={r.canRun}
      />

      {/* run error */}
      {r.runErr !== null && (
        <ErrorState
          message={r.runErr}
          hint="is the daemon running? start it with signaldeckd, then retry."
          retry={r.run}
        />
      )}

      {/* initial / empty state */}
      {r.report === null && r.runErr === null && (
        <section className="panel">
          <div className="px-4 py-10 text-center text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {r.validHoldings.length === 0 ? (
              <>
                add at least one holding — a symbol and a weight — then{" "}
                <span style={{ color: "var(--dim)" }}>investigate risk</span> to compute 1-day
                Value-at-Risk, the drivers behind it, and a set of stress scenarios.
              </>
            ) : (
              <>
                {r.validHoldings.length} holding{r.validHoldings.length === 1 ? "" : "s"} ready —
                press <span style={{ color: "var(--dim)" }}>investigate risk</span> to price 1-day
                VaR, risk drivers, and stress scenarios against{" "}
                <span className="tnum">${fmtUSD(r.notional > 0 ? r.notional : DEFAULT_NOTIONAL)}</span>{" "}
                notional.
              </>
            )}
          </div>
        </section>
      )}

      {/* results */}
      {r.report !== null && (
        <RiskResults
          report={r.report}
          summary={r.summary}
          ranNotional={r.ranNotional}
          drivers={r.drivers}
          maxPct={r.maxPct}
        />
      )}
    </div>
  );
}
