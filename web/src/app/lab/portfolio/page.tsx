"use client";

import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import LogForm from "@/components/portfolio/LogForm";
import PositionsTable from "@/components/portfolio/PositionsTable";
import CorrelationPanel from "@/components/portfolio/CorrelationPanel";
import usePortfolio from "@/hooks/usePortfolio";
import { PageHero, StatTile } from "@/components/ui/Kit";

export default function PortfolioPage() {
  const { pf, pfErr, watch, retry, forcePoll, corr, corrErr, corrRefreshing, loadCorr } = usePortfolio();
  const positions = pf?.positions ?? [];
  const open = positions.filter((p) => p.open);
  const loading = pf === null && pfErr === null;
  const hardError = pf === null && pfErr !== null;
  const corrLoading = corr === null && corrErr === null;

  const stat = pf?.stat;
  const totalPnL = stat?.TotalPnLAbs ?? 0;
  // These two tiles used to read `stat.GrossValue` and `stat.Winners` straight
  // out of the response — GrossValue is sum(|qty| * lastPrice) in dollars and
  // Winners is an integer count, so "Avg Score" showed 42000.00 and "Win Rate"
  // showed 3.0% for three winning positions. <SummaryBar> renders the same two
  // fields correctly, forty lines down the same page, which is how a $42,000
  // gross could sit under a hero tile labelled Avg Score.
  const scored = positions.map((p) => p.scoreAtEntry).filter((s): s is number => typeof s === "number");
  const avgScore = scored.length > 0 ? scored.reduce((a, b) => a + b, 0) / scored.length : null;
  // Winners and Losers count positions with STRICTLY positive / strictly
  // negative P&L, marked to the latest close (internal/portfolio/portfolio.go:
  // 68-69, 268-273) — open and closed alike. Break-even and unpriceable
  // positions fall into neither, so the denominator is "positions currently
  // showing a non-zero P&L", and a denominator of 0 means nothing is measurable
  // yet — which is not a 0% win rate. Marked-to-market, so it moves with the
  // tape; the `sub` says so rather than letting the label imply realised trades.
  const graded = (stat?.Winners ?? 0) + (stat?.Losers ?? 0);
  const winRate = graded > 0 ? ((stat?.Winners ?? 0) / graded) * 100 : null;
  const totalPositions = positions.length;
  const openPositions = open.length;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Portfolio Honesty Loop"
        subtitle="Grade your discretionary trades against what actually happened — the same honesty loop the model runs on itself."
      />

      {loading && <Skeleton lines={5} label="loading portfolio" />}

      {hardError && (
        <ErrorState
          message={
            /401/.test(pfErr ?? "")
              ? "Sign in to track your reads"
              : (pfErr ?? "could not load portfolio")
          }
          hint={
            /401/.test(pfErr ?? "")
              ? "The portfolio is scoped to your account (your entries, your scores-at-entry) — log in and this page will load."
              : undefined
          }
          retry={retry}
        />
      )}

      {pf !== null && (
        <>
          {positions.length > 0 && (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <StatTile
                label="Total PnL"
                value={totalPnL}
                decimals={2}
                prefix="$"
                delta={stat?.TotalPnLPct}
                glow={totalPnL > 0 ? "up" : totalPnL < 0 ? "down" : undefined}
                i={0}
              />
              <StatTile
                label="Avg Score"
                value={avgScore}
                sub="at entry"
                decimals={2}
                glow="accent"
                i={1}
              />
              <StatTile
                label="Win Rate"
                value={winRate}
                sub={graded > 0 ? `${stat?.Winners ?? 0} of ${graded} marked to last close` : undefined}
                decimals={1}
                suffix="%"
                glow="hud"
                i={2}
              />
              <StatTile
                label="Positions"
                value={totalPositions}
                sub={`${openPositions} open`}
                i={3}
              />
            </div>
          )}

          <div className="panel hud-panel">
            <div className="panel-h">Log a Read</div>
            <LogForm watch={watch} onLogged={forcePoll} />
          </div>

          <section className="panel">
            <div className="panel-h">
              POSITIONS
              <span className="tnum" style={{ color: "var(--faint)" }}>
                your logged reads, marked to the latest close
              </span>
            </div>
            {positions.length === 0 ? (
              <EmptyState
                className="m-4"
                message="No positions logged yet."
                detail="Pick a symbol above and log a read — entry price and the pressure score are captured at the latest close, so you can grade the call later."
              />
            ) : (
              <PositionsTable positions={positions} onClosed={forcePoll} />
            )}
          </section>
        </>
      )}

      <CorrelationPanel
        data={corr}
        err={corrErr}
        loading={corrLoading}
        onRefresh={loadCorr}
        refreshing={corrRefreshing}
      />
    </div>
  );
}
