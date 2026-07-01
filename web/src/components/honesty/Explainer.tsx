"use client";

import { api, type Horizon } from "@/lib/api";

/** Static prose: what this page measures and why it can be trusted. */
export default function Explainer({ horizon }: { horizon: Horizon }) {
  return (
    <section className="panel">
      <div className="panel-h">HOW HONESTY WORKS</div>
      <div
        className="space-y-2 px-4 py-3 text-[0.75rem] leading-relaxed"
        style={{ color: "var(--dim)" }}
      >
        <p>
          Every Pressure Score is persisted at write time, before anyone knows what happens next.
          When the score&apos;s horizon elapses (1h, 1d or 1w), the outcome-resolver worker records
          what the market actually did over that window and pins the forward return to the original
          score. Nothing is backfilled, re-scored or cherry-picked — a wrong call stays wrong in the
          record.
        </p>
        <p>
          <span style={{ color: "var(--text)" }}>IC</span> is the Pearson correlation between score
          and forward return across all resolved pairs: 0 means the scores carried no information,
          +1 would mean they perfectly ranked what came next. The quintile table asks the same
          question bucket by bucket — if mean forward return climbs from strong-sell to strong-buy,
          the scores rank outcomes.
        </p>
        <p>
          <a
            href={api.exportUrl("outcomes", `horizon=${horizon}`)}
            className="cursor-pointer underline decoration-dotted underline-offset-2 transition-colors duration-150 hover:decoration-solid"
            style={{ color: "var(--accent)" }}
            download
          >
            download the raw outcomes CSV ({horizon})
          </a>{" "}
          and check the math yourself.
        </p>
      </div>
    </section>
  );
}
