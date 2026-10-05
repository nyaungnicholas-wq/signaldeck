"use client";

// Forecast track-record strip — extracted from the old monolithic page.tsx. The
// track-record gate progress, the costed paper P&L and the ledger-integrity
// chip, each pulled from the REAL /api/track-record payload and each keeping
// its honest framing (gated = grade withheld with its actual reason, paper = simulation upper
// bound). The load-bearing caveats that used to hide behind hover titles are
// click/keyboard HelpTips now. Nothing here is ever fabricated: fetch failure
// renders an honest note.

import Link from "next/link";
import { fmtPct } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import HelpTip from "@/components/HelpTip";
import useDashboardProof from "@/hooks/useDashboardProof";
import RefusalNotice from "@/components/RefusalNotice";
import HypotheticalNote from "@/components/HypotheticalNote";
import { ledgerHeadline } from "@/lib/ledgerHeadline";

export default function ProofStrip() {
  const { tr, err } = useDashboardProof();

  if (err && !tr) {
    return (
      <section className="panel px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        proof data unavailable ({err}) — the track record lives at{" "}
        <Link
          href="/lab/track-record"
          className="mono cursor-pointer text-[var(--dim)] underline transition-colors duration-150 hover:text-[var(--accent)]"
        >
          /lab/track-record
        </Link>{" "}
        once the daemon is reachable.
      </section>
    );
  }
  if (!tr) {
    return (
      <section className="panel p-3">
        <Skeleton lines={2} label="loading track-record proof" />
      </section>
    );
  }

  const threshold = tr.gate?.threshold ?? tr.minIndependentN;
  const paper = tr.paper;
  // The /proof headline's verdict, not `intact` alone: a regenerated chain is
  // consistent too, and only a failing signed anchor tells them apart.
  const ledger = tr.ledger ? ledgerHeadline(tr.ledger) : null;
  return (
    <section className="panel" aria-label="forecast track record">
      <div className="flex flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3">
        {/* gate progress — the honest scoreboard state */}
        <div className="flex min-w-[220px] flex-1 flex-col gap-1">
          {tr.gated ? (
            <>
              <span className="text-[0.75rem] font-semibold" style={{ color: "var(--warn)" }}>
                Grade withheld —{" "}
                <span className="tnum">
                  {tr.independentN.toLocaleString("en-US")}
                </span>{" "}
                deduplicated symbol-days
              </span>
              {tr.independentN < threshold && <div
                className="h-1.5 w-full overflow-hidden rounded"
                style={{ background: "var(--border)" }}
                role="progressbar"
                aria-valuemin={0}
                aria-valuemax={threshold}
                aria-valuenow={Math.min(tr.independentN, threshold)}
                aria-label="deduplicated symbol-day resolutions toward the significance gate"
              >
                <div
                  className="h-full rounded"
                  style={{
                    width: `${Math.min(100, (tr.independentN / Math.max(1, threshold)) * 100)}%`,
                    background: "var(--warn)",
                  }}
                />
              </div>}
              {tr.distinctDays != null && tr.minDistinctDays != null && (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  {tr.distinctDays} distinct market days; {tr.minDistinctDays} required.
                  {" "}Observation minimum: {threshold}.
                </span>
              )}
              <RefusalNotice compact tone="warn" title="Why the grade is withheld" testId="proofstrip-refusal" reason={tr.note || "The record has not cleared every evidence check. See the full track record for details."} />
            </>
          ) : (
            (() => {
              const beats = tr.winRate != null && tr.naiveBaseline != null && tr.winRate > tr.naiveBaseline;
              const winRateDisplay = tr.winRate != null ? `${(tr.winRate * 100).toFixed(1)}%` : "—";
              const naiveBaselineDisplay = tr.naiveBaseline != null ? `${(tr.naiveBaseline * 100).toFixed(1)}%` : null;
              const edgeDisplay = tr.edgeVsNaive != null ? `${tr.edgeVsNaive >= 0 ? "+" : ""}${(tr.edgeVsNaive * 100).toFixed(1)}pp` : null;
              const hasPositiveEdge = tr.edgeVsNaive != null && tr.edgeVsNaive > 0;
              return (
                <>
                  <span className="text-[0.75rem] font-semibold" style={{ color: beats ? "var(--ok)" : "var(--warn)" }}>
                    measured: right{" "}
                    <span className="tnum">
                      {winRateDisplay}
                    </span>{" "}
                    of the time
                  </span>
                  {tr.naiveBaseline != null && (
                    <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      {" "}
                      vs {naiveBaselineDisplay} for the majority-direction baseline
                      {tr.edgeVsNaive != null && (
                        <>
                          {" "}
                          <span className="tnum">
                            {edgeDisplay}
                          </span>
                          {!hasPositiveEdge ? " — does not beat the naive guess" : ""}
                        </>
                      )}
                    </span>
                  )}
                  <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    over {tr.independentN.toLocaleString("en-US")} deduplicated (symbol, trading day) resolutions · 1d horizon
                  </span>
                </>
              );
            })()
          )}
        </div>

        {/* paper P&L — always labeled a simulation. Operator-only since
            2026-10-05 (daemon publicTrackRecord): absent, the block is not
            drawn at all rather than read as a figure that failed to load. */}
        {paper && (
        <>
        <div className="flex flex-col gap-0.5">
          <span
            className="inline-flex items-center gap-1 text-[0.75rem] tracking-wider"
            style={{ color: "var(--faint)" }}
          >
            PAPER P&amp;L (SIMULATED, COSTED)
            <HelpTip label="What paper P&L means">
              A simulated book on stored data with next-bar fills and per-side costs — an upper
              bound on what the signals could have earned, not a brokerage account and not a
              promise of future returns.
            </HelpTip>
          </span>
          {/* `available` does not imply totalReturn was computed (it is optional on
              the wire). The old `?? 0` rendered a missing figure as "0.00%" in the
              GREEN branch — a flat-performance claim invented from absence. Match
              the winRate treatment a few lines up: absent reads as an em-dash. */}
          {paper?.available && paper.totalReturn != null ? (
            <span
              className="tnum text-[0.85rem] font-semibold"
              style={{ color: paper.totalReturn >= 0 ? "var(--bid)" : "var(--ask)" }}
            >
              {fmtPct(paper.totalReturn * 100)}
            </span>
          ) : paper?.available ? (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              —
            </span>
          ) : (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {paper?.note || "Paper performance unavailable"}
            </span>
          )}
        </div>

        {/* The paper figure is hypothetical performance; say so in the regulator's words, not only in a tip. */}
        <HypotheticalNote className="basis-full" />
        </>
        )}

        {/* ledger integrity — the record grades the record that was made */}
        {tr.ledger && ledger && (
          <span className="inline-flex items-center gap-1">
            <span
              className="chip"
              style={
                ledger.tone === "ok"
                  ? { color: "var(--ok)", borderColor: "var(--ok)" }
                  : { color: "var(--bad)", borderColor: "var(--bad)" }
              }
            >
              {ledger.tone === "ok" ? "ledger intact" : ledger.text}
              <span className="tnum ml-1.5 font-normal" style={{ color: "var(--faint)" }}>
                {tr.ledger.count.toLocaleString("en-US")}
              </span>
            </span>
            <HelpTip label="What ledger intact means">
              Every flagship prediction is hash-chained append-only — “intact” means the stored rows
              are internally consistent: editing, deleting or reordering any one of them breaks a full
              recomputation of the chain. It does not prove when they were written — deleting every row and re-appending
              a fabricated chain also verifies intact.
            </HelpTip>
          </span>
        )}

        <Link
          href="/lab/track-record"
          className="chip ml-auto inline-flex min-h-[36px] cursor-pointer items-center transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
        >
          Investigate the full track record →
        </Link>
      </div>
    </section>
  );
}
