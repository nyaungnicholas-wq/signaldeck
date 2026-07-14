"use client";

// AGREEMENT strip for the model race: when 2+ models have EARNED a graded
// out-of-sample edge (lift > 0), we say out loud whether they agree on
// direction. Disagreement is a signal too — it means the edge is weaker than
// any single number suggests. Models that failed the gate are shown as gray
// "noise" chips, never silently dropped: the reader always sees why a model
// isn't part of the read.

import { useViewMode } from "@/components/Plain";
import type { Horizon } from "@/lib/api";

export interface AgreementEntry {
  key: string;
  /** Technical model name (PRO mode). */
  name: string;
  /** Plain-English model name (SIMPLE mode). */
  plainName: string;
  prob: number;
  lift: number;
}

function pct0(v: number): string {
  return isFinite(v) ? `${Math.round(v * 100)}%` : "—";
}

export default function AgreementStrip({
  entries,
  horizon,
}: {
  /** All model lanes that HAVE stats for the selected horizon (gated or not). */
  entries: AgreementEntry[];
  horizon: Horizon;
}) {
  const mode = useViewMode();
  const label = (e: AgreementEntry) => (mode === "simple" ? e.plainName : e.name);

  const graded = entries.filter((e) => isFinite(e.lift) && e.lift > 0 && isFinite(e.prob));
  const gatedOut = entries.filter((e) => !(isFinite(e.lift) && e.lift > 0 && isFinite(e.prob)));

  // Fewer than two graded models — no agreement read exists, and we say so.
  if (graded.length < 2) {
    return (
      <section className="panel">
        <div className="panel-h">
          DO THE MODELS AGREE?
          <span className="chip tnum" style={{ color: "var(--faint)" }}>
            {horizon}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-2 px-4 py-3">
          <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
            no agreement read — it needs 2+ models with a graded out-of-sample edge;{" "}
            {graded.length === 1 ? "only 1 qualifies" : "none qualify"} on {horizon} today.
          </span>
          {graded.map((e) => (
            <span key={e.key} className="chip tnum" style={{ color: "var(--text)" }}>
              {label(e)} {pct0(e.prob)} {e.prob >= 0.5 ? "up" : "down"}
            </span>
          ))}
          {gatedOut.map((e) => (
            <span key={e.key} className="chip" style={{ color: "var(--faint)" }}>
              {label(e)} — noise (no OOS edge)
            </span>
          ))}
        </div>
      </section>
    );
  }

  const up = graded.filter((e) => e.prob >= 0.5);
  const down = graded.filter((e) => e.prob < 0.5);
  const agree = up.length === 0 || down.length === 0;
  const agreeUp = agree && down.length === 0;

  const part = (list: AgreementEntry[], dir: "up" | "down") =>
    list.map((e) => `${label(e)} ${pct0(e.prob)} ${dir}`).join(" · ");

  const verdictColor = agree ? (agreeUp ? "var(--bid)" : "var(--ask)") : "var(--warn)";
  const verdictBg = agree ? (agreeUp ? "var(--bid-dim)" : "var(--panel2)") : "var(--panel2)";

  return (
    <section className="panel">
      <div className="panel-h">
        DO THE MODELS AGREE?
        <span className="chip tnum" style={{ color: "var(--faint)" }}>
          {horizon}
        </span>
      </div>
      <div className="flex flex-col gap-2 px-4 py-3">
        <div
          className="rounded-lg border px-2.5 py-1.5 text-[0.75rem] leading-snug"
          style={{ borderColor: verdictColor, background: verdictBg, color: verdictColor }}
        >
          {agree ? (
            <>
              models agree on {horizon}: {part(graded, agreeUp ? "up" : "down")}
            </>
          ) : (
            <>
              models disagree on {horizon}: {part(up, "up")} vs {part(down, "down")}
            </>
          )}
        </div>
        <p className="m-0 text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
          {agree
            ? "independent models landing on the same side is worth more than any single number — but it is still a lean, not a certainty."
            : "disagreement is a signal too — the models see different structure in the same bars, so treat any single lean as weaker than its number suggests."}
        </p>
        {gatedOut.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {gatedOut.map((e) => (
              <span key={e.key} className="chip" style={{ color: "var(--faint)" }}>
                {label(e)} — noise (no OOS edge), excluded from the read
              </span>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
