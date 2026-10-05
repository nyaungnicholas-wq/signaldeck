"use client";

// Result sections for /lab/backtest — THE READ, the performance grid and the
// equity curve, extracted from the old monolithic page (pure refactor).
// SIMPLE mode folds the metric grid behind a ProOnly disclosure; the
// plain-English read, every honesty chip (incl. "backtested — not a live
// track record") and the equity curve stay visible in BOTH modes.

import { type BacktestResponse } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import ProOnly from "@/components/ProOnly";
import EquityCurve from "@/components/backtest/EquityCurve";
import Metric from "@/components/backtest/Metric";

/** The daemon serializes the full Strategy struct; the contract type only
 * declares Name, so widen locally to read the honest round-trip cost. */
type StrategyMeta = { Name: string; CostBps?: number };

function fmtBps(bps: number): string {
  if (!isFinite(bps)) return "—";
  return Number.isInteger(bps) ? `${bps} bps` : `${bps.toFixed(1)} bps`;
}

export default function BacktestResults({
  resp,
  ranFor,
}: {
  resp: BacktestResponse;
  ranFor: { symbol: string; text: string };
}) {
  const result = resp.result;
  const strat = (resp.strategy ?? null) as StrategyMeta | null;
  const costBps = strat?.CostBps;
  const lowConfidence = result.NumTrades > 0 && result.NumTrades < 10;
  const noTrades = result.NumTrades === 0;

  return (
    <>
      {/* headline read: the Explain string */}
      <section className="panel">
        <div className="panel-h">
          THE READ
          <span className="tnum" style={{ color: "var(--faint)" }}>
            {ranFor.symbol} · {(strat?.Name ?? ranFor.text).slice(0, 64)}
          </span>
        </div>
        <div className="px-4 py-4">
          <p
            className="text-[0.85rem] leading-relaxed"
            style={{ color: "var(--text)" }}
          >
            {resp.explain}
          </p>
          {/* honesty chips */}
          <div className="mt-3 flex flex-wrap gap-1.5">
            <span className="chip">next-bar fills, no lookahead</span>
            <span className="chip tnum">
              round-trip cost{" "}
              {costBps !== undefined ? fmtBps(costBps) : "—"} / side
            </span>
            <span className="chip tnum">
              {result.NumTrades} trade{result.NumTrades === 1 ? "" : "s"}
            </span>
            <span
              className="chip"
              style={{
                color:
                  result.VsBuyHold > 0
                    ? "var(--bid)"
                    : result.VsBuyHold < 0
                      ? "var(--ask)"
                      : "var(--dim)",
                borderColor:
                  result.VsBuyHold > 0
                    ? "var(--bid)"
                    : result.VsBuyHold < 0
                      ? "var(--ask)"
                      : "var(--border)",
              }}
            >
              {result.VsBuyHold > 0
                ? "beat buy & hold"
                : result.VsBuyHold < 0
                  ? "lagged buy & hold"
                  : "matched buy & hold"}
            </span>
            {lowConfidence && (
              <span
                className="chip"
                style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              >
                few trades — low confidence
              </span>
            )}
            {noTrades && (
              <span
                className="chip"
                style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              >
                no trades fired — nothing to conclude
              </span>
            )}
            <span
              className="chip"
              style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              title="This is a backtest over historical bars, not a live forward track record."
            >
              backtested / in-sample — not a live track record (n=
              {result.NumTrades})
            </span>
          </div>
        </div>
      </section>

      {/* metrics grid — technical gauges; SIMPLE mode folds them */}
      <ProOnly summary="Show performance metrics">
        <section className="panel">
          <div className="panel-h">
            PERFORMANCE
            <span className="tnum" style={{ color: "var(--faint)" }}>
              returns as fractions of starting equity (1.00)
            </span>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4">
            <Metric
              label="VS BUY & HOLD"
              value={fmtPct(result.VsBuyHold * 100)}
              color={
                result.VsBuyHold > 0
                  ? "var(--bid)"
                  : result.VsBuyHold < 0
                    ? "var(--ask)"
                    : "var(--dim)"
              }
              hint="strategy return minus passive holding, after costs"
            />
            <Metric
              label="TOTAL RETURN"
              value={fmtPct(result.TotalReturn * 100)}
              color={result.TotalReturn >= 0 ? "var(--bid)" : "var(--ask)"}
            />
            <Metric
              label="CAGR"
              help="Compound annual growth rate — only shown when the tested span is ~1yr+ and there are enough trades to annualize honestly."
              value={
                result.CAGRReported === false
                  ? "n/a"
                  : fmtPct(result.CAGR * 100)
              }
              color={
                result.CAGRReported === false
                  ? "var(--dim)"
                  : result.CAGR >= 0
                    ? "var(--bid)"
                    : "var(--ask)"
              }
              hint={
                result.CAGRReported === false
                  ? `span too short/thin to annualize${
                      result.SpanYears !== undefined
                        ? ` (~${result.SpanYears.toFixed(2)}yr, ${result.NumTrades} trades)`
                        : ""
                    }`
                  : "compound annual growth rate"
              }
            />
            <Metric
              label="MAX DRAWDOWN"
              help="Largest peak-to-trough decline in equity over the tested span."
              value={fmtPct(-result.MaxDrawdown * 100)}
              color="var(--ask)"
              hint="worst peak-to-trough"
            />
            <Metric
              label="SHARPE"
              help="Sharpe ratio (risk-adjusted return). Annualized by the bar interval inferred from the data, not a hardcoded 252."
              value={
                isFinite(result.Sharpe) ? result.Sharpe.toFixed(2) : "—"
              }
              hint={
                result.BarsPerYear !== undefined
                  ? `rf=0, annualized ~${Math.round(result.BarsPerYear)} bars/yr`
                  : "rf=0, annualized; read as relative"
              }
            />
            <Metric
              label="NUM TRADES"
              value={String(result.NumTrades)}
              color={lowConfidence || noTrades ? "var(--warn)" : "var(--text)"}
              hint={
                noTrades
                  ? "nothing fired"
                  : lowConfidence
                    ? "low confidence"
                    : undefined
              }
            />
            <Metric
              label="WIN RATE"
              help="Share of closed trades that were net-positive. Suppressed with fewer than 2 closed trades — one trade is not a win rate."
              value={
                result.WinRateMeaningful === false
                  ? "n/a"
                  : fmtPct(result.WinRate * 100, false)
              }
              color={
                result.WinRateMeaningful === false ? "var(--dim)" : undefined
              }
              hint={
                result.WinRateMeaningful === false
                  ? `too few closed trades (${result.ClosedTrades ?? 0})`
                  : "of closed trades"
              }
            />
            <Metric
              label="EXPOSURE"
              value={fmtPct(result.ExposurePct * 100, false)}
              hint="share of bars in the market"
            />
          </div>
        </section>
      </ProOnly>

      {/* equity curve */}
      <section className="panel">
        <div className="panel-h">
          EQUITY CURVE
          <span className="tnum" style={{ color: "var(--faint)" }}>
            growth of 1.00 · dashed line = break-even
          </span>
        </div>
        <EquityCurve equity={result.Equity} ts={resp.ts} />
      </section>
    </>
  );
}
