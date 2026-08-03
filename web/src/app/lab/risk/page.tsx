"use client";

import { Reveal, PageHero, StatTile, AnimatedNumber, DeltaBadge, MiniBar, Gauge, Spark } from "@/components/ui/Kit";
import ErrorState from "@/components/ErrorState";
import PortfolioBuilder from "@/components/risk/PortfolioBuilder";
import RiskResults from "@/components/risk/RiskResults";
import useRisk, { DEFAULT_NOTIONAL } from "@/hooks/useRisk";

function fmtUSD(v: number): string {
  if (!isFinite(v)) return "—";
  return v.toLocaleString("en-US", { maximumFractionDigits: 0 });
}

export default function RiskPage() {
  const r = useRisk();

  const heroStats = [
    { label: "Holdings", value: r.validHoldings.length, sub: "positions in portfolio" },
    { label: "Total Weight", value: r.totalWeight, decimals: 0, suffix: "%", sub: r.totalWeight > 0 && r.totalWeight !== 100 ? "normalized" : "raw" },
    { label: "Confidence", value: r.report?.Confidence ? r.report.Confidence * 100 : 0, decimals: 0, suffix: "%", sub: "model confidence level" },
    { label: "Notional", value: r.notional > 0 ? r.notional : DEFAULT_NOTIONAL, prefix: "$", sub: "portfolio value" }
  ];

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="RiskLens"
        subtitle="Compute 1-day Value-at-Risk, risk drivers, and stress scenarios for a custom portfolio."
        live={false}
      />

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {heroStats.map((s, i) => (
          <StatTile
            key={i}
            label={s.label}
            value={s.value}
            decimals={s.decimals}
            prefix={s.prefix}
            suffix={s.suffix}
            sub={s.sub}
            i={i}
          />
        ))}
      </div>

      <div className="panel">
        <div className="panel-h">Portfolio Builder</div>
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
      </div>

      {r.runErr !== null && (
        <ErrorState
          message={r.runErr}
          hint="is the daemon running? start it with signaldeckd, then retry."
          retry={r.run}
        />
      )}

      {r.report === null && r.runErr === null && (
        <section className="panel">
          <div className="px-4 py-10 text-center text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {r.validHoldings.length === 0 ? (
              <>
                Add at least one holding — a symbol and a weight — then{" "}
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
