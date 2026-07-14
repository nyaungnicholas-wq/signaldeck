"use client";

// /lab/backtest — thin composition page. State/fetching lives in
// useBacktest(); the composer and result sections live in
// src/components/backtest/ (pure refactor of the old monolithic page).

import PagePurpose from "@/components/PagePurpose";
import StrategyComposer from "@/components/backtest/StrategyComposer";
import BacktestResults from "@/components/backtest/BacktestResults";
import useBacktest from "@/hooks/useBacktest";

export default function BacktestPage() {
  const bt = useBacktest();

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">BACKTEST</h1>
        <span className="chip">CopilotQuant</span>
        <span className="chip">next-bar fills · no lookahead</span>
        {bt.ranFor && (
          <span className="chip tnum">
            {bt.ranFor.symbol} · {bt.market}
          </span>
        )}
        {bt.watchErr !== null && bt.watch !== null && (
          <span
            className="chip"
            style={{ color: "var(--bad)", borderColor: "var(--bad)" }}
          >
            watchlist poll failed
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-backtest"
        text="What would a chosen rule have done in the past? A simulation on stored bars with next-bar fills and no lookahead — history, not a promise."
      />

      {/* strategy composer */}
      <StrategyComposer
        text={bt.text}
        setText={bt.setText}
        watch={bt.watch}
        watchErr={bt.watchErr}
        retryWatch={bt.retryWatch}
        symbol={bt.symbol}
        market={bt.market}
        pick={bt.pick}
        selectedRow={bt.selectedRow}
        run={bt.run}
        running={bt.running}
        canRun={bt.canRun}
        softErr={bt.softErr}
        hardErr={bt.hardErr}
      />

      {/* results — runtime guard on result matches the old page even though
          the contract type declares it non-null */}
      {bt.resp && bt.resp.result && bt.ranFor && (
        <BacktestResults resp={bt.resp} ranFor={bt.ranFor} />
      )}

      {/* static explainer — always visible so the method is never hidden */}
      <section className="panel">
        <div className="panel-h">HOW THIS BACKTEST STAYS HONEST</div>
        <div
          className="px-4 py-4 text-[0.75rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          Signals compute on each bar using only prior data and fill at the NEXT
          bar&apos;s open. Costs are charged per trade. No survivorship beyond
          what is stored. The number is honest, not flattering.
        </div>
      </section>
    </div>
  );
}
