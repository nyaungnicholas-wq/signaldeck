"use client";

// The forecasts-page explainer, extended for the model race: one honest
// paragraph per model. All three pass the SAME bar — walk-forward, no
// lookahead, and a probability is only shown as signal when the model beats
// the base rate out-of-sample. Wording mirrors the daemon's package docs
// (internal/forecast, internal/gbm, internal/meanrev).

import { useViewMode } from "@/components/Plain";

export default function ModelExplainer() {
  const mode = useViewMode();
  const n = (plain: string, pro: string) => (mode === "simple" ? plain : pro);

  return (
    <section className="panel">
      <div className="panel-h">HOW EACH MODEL EARNS THE RIGHT TO A NUMBER</div>
      <div className="flex flex-col gap-3 px-4 py-4 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        <p className="m-0">
          <span style={{ color: "var(--text)" }}>{n("Straight-line model", "Walk-forward logistic")}</span> — a
          linear model trained walk-forward on stored bars with{" "}
          <span style={{ color: "var(--text)" }}>no lookahead</span>: every prediction is graded only
          against bars that came strictly after its training window. It earns the right to show a
          probability by beating the base rate out-of-sample; when it can&apos;t, the number is grayed
          and labeled noise. Refreshes hourly (<span style={{ color: "var(--dim)" }}>forecast-trainer</span> agent).
        </p>
        <p className="m-0">
          <span style={{ color: "var(--text)" }}>{n("Pattern-finder (trees)", "GBM (gradient-boosted trees)")}</span> — the
          non-linear sibling: from-scratch gradient-boosted decision trees built to catch structure a
          straight line can&apos;t (interactions, thresholds). It is deterministic (no randomness — same
          data, same model) and is graded by the exact same walk-forward report card (accuracy, Brier,
          AUC, lift); on a pure-noise target it honestly reports ~zero lift and gets dropped. Its labels
          are raw close-to-close direction with <span style={{ color: "var(--text)" }}>no transaction costs</span> —
          a P(up) above 50% is a directional lean, not a tradeable edge.
        </p>
        <p className="m-0">
          <span style={{ color: "var(--text)" }}>{n("Snap-back model", "Mean-reversion (costed)")}</span> — the
          contrarian counterweight: it fades the momentum blend&apos;s lean (reflecting its raw P(up)
          about 50%), because extremes are slightly more likely to pull back than continue. Since a
          contrarian trade fights the prevailing move, it is graded{" "}
          <span style={{ color: "var(--text)" }}>stricter than the others</span>: walk-forward AND net of a
          round-trip cost — a call only counts as a win when the realized move covers the cost. It only
          ships when that costed edge is positive.
        </p>
        <p className="m-0" style={{ color: "var(--faint)" }}>
          One shared rule: an ungraded or edgeless model is dropped, never down-weighted — a missing
          number here is the truth, not a bug.
        </p>
      </div>
    </section>
  );
}
