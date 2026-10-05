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
            {/* The daemon emits these as FRACTIONS (backtest.go:92-98:
                TotalReturn = equity-1, MaxDrawdown a positive fraction, WinRate
                = wins/closedTrades), so they must be scaled by 100 before a "%"
                suffix. Until 2026-08-11 these four tiles rendered the raw
                fraction: a +37% backtest read "0.37%" and a 62% win rate read
                "0.62%" — while <BacktestResults> sixty lines below rendered the
                identical fields correctly. Kept deliberately in the same form
                as that component so the two cannot drift again. */}
            <StatTile
              label="Total Return"
              value={bt.resp.result.TotalReturn * 100}
              decimals={2}
              suffix="%"
              glow={bt.resp.result.TotalReturn >= 0 ? "up" : "down"}
              i={0}
            />
            <StatTile
              label="Win Rate"
              // null (em-dash), never a number, when the daemon says the rate is
              // not meaningful — it is undefined below 2 closed trades, and one
              // closed trade rendering "100%" is a measurement nobody made.
              value={
                bt.resp.result.WinRateMeaningful === false
                  ? null
                  : bt.resp.result.WinRate * 100
              }
              decimals={2}
              suffix={bt.resp.result.WinRateMeaningful === false ? "" : "%"}
              sub={
                bt.resp.result.WinRateMeaningful === false
                  ? "needs 2+ closed trades"
                  : undefined
              }
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
              // Negated to match BacktestResults: the daemon reports drawdown as
              // a POSITIVE fraction, and a drawdown shown as a positive number
              // under a red "down" glow reads as a gain.
              value={-bt.resp.result.MaxDrawdown * 100}
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
