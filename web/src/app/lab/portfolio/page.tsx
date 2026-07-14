"use client";

// /portfolio — a discretionary trade log graded against what actually happened,
// plus a correlation heatmap so you can see what actually diversifies.
//
// Honesty framing is the product: every logged read carries the pressure score
// captured at entry, so the same out-of-sample honesty loop the model runs on
// itself is applied to YOUR calls. Thin composition page: state/fetching lives
// in usePortfolio(); sections live in src/components/portfolio/ (pure refactor
// of the old monolithic page).

import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import SummaryBar from "@/components/portfolio/SummaryBar";
import LogForm from "@/components/portfolio/LogForm";
import PositionsTable from "@/components/portfolio/PositionsTable";
import CorrelationPanel from "@/components/portfolio/CorrelationPanel";
import usePortfolio from "@/hooks/usePortfolio";

export default function PortfolioPage() {
  const { pf, pfErr, watch, retry, forcePoll, corr, corrErr, corrRefreshing, loadCorr } =
    usePortfolio();

  const positions = pf?.positions ?? [];
  const open = positions.filter((p) => p.open);
  const loading = pf === null && pfErr === null;
  const hardError = pf === null && pfErr !== null;
  const corrLoading = corr === null && corrErr === null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">PORTFOLIO</h1>
        {pf !== null && (
          <>
            <span className="chip tnum">{positions.length} logged</span>
            <span className="chip tnum">{open.length} open</span>
          </>
        )}
        {pfErr !== null && pf !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-portfolio"
        text="Are YOUR calls any good? Log discretionary trades and get graded against what actually happened — the same honesty loop the model runs on itself."
      />

      {/* honesty note — the reason this page exists */}
      <p className="px-1 text-[0.75rem] italic leading-relaxed" style={{ color: "var(--faint)" }}>
        This grades YOUR discretionary reads against what actually happened — the same honesty
        loop as the model, applied to you. The score beside each entry is the pressure reading at
        the moment you logged it; watch whether it was right.
      </p>

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
          {positions.length > 0 && <SummaryBar stat={pf.stat} />}

          <LogForm watch={watch} onLogged={forcePoll} />

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
