"use client";

import { PageHero, Reveal, StatTile } from "@/components/ui/Kit";
import StrategyComposer from "@/components/backtest/StrategyComposer";
import BacktestResults from "@/components/backtest/BacktestResults";
import useBacktest from "@/hooks/useBacktest";

export default function BacktestPage() {
  const bt = useBacktest();

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="BACKTEST"
        subtitle="Simulate a chosen rule on historical bars with next-bar fills and no lookahead — history, not a promise."
        right={
          bt.ranFor && (
            <span className="mono text-sm" style={{ color: "var(--dim)" }}>
              {bt.ranFor.symbol} · {bt.market}
            </span>
          )
        }
      />

      {bt.ranFor && bt.resp?.result && (
        <Reveal>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatTile
              label="Total Return"
              value={bt.resp.result.TotalReturn}
              decimals={2}
              suffix="%"
              glow={bt.resp.result.TotalReturn >= 0 ? "up" : "down"}
              i={0}
            />
            <StatTile
              label="Win Rate"
              value={bt.resp.result.WinRate}
              decimals={2}
              suffix="%"
              glow="accent"
              i={1}
            />
            <StatTile
              label="Trades"
              value={bt.resp.result.NumTrades}
              glow="hud"
              i={2}
            />
            <StatTile
              label="Max Drawdown"
              value={bt.resp.result.MaxDrawdown}
              decimals={2}
              suffix="%"
              glow="down"
              i={3}
            />
          </div>
        </Reveal>
      )}

      <Reveal>
        <div className="panel">
          <div className="panel-h">Strategy Composer</div>
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
        </div>
      </Reveal>

      {bt.resp && bt.resp.result && bt.ranFor && (
        <Reveal>
          <div className="panel hud-panel">
            <div className="panel-h">Backtest Results</div>
            <BacktestResults resp={bt.resp} ranFor={bt.ranFor} />
          </div>
        </Reveal>
      )}

      <Reveal>
        <div className="panel">
          <div className="panel-h">Methodology</div>
          <div className="px-4 py-4 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            Signals compute on each bar using only prior data and fill at the NEXT bar&rsquo;s open. Costs are charged per trade. No survivorship beyond what is stored. The number is honest, not flattering.
          </div>
        </div>
      </Reveal>
    </div>
  );
}
