import type { Metadata } from "next";
import { PLAIN, type MetricKey } from "@/lib/plain";

// Bare page name — the root layout appends " — SignalDeck" via its template.
export const metadata: Metadata = { title: "Glossary" };

// The glossary is rendered from the same PLAIN dictionary that feeds every
// tooltip on the site.  This page cannot drift from those definitions because
// both are built from PLAIN[key].read(null) at render time.
const ORDER: readonly MetricKey[] = [
  "cal_prob",
  "regime",
  "pressure",
  "vix",
  "zscore",
  "win_rate",
  "base_rate",
  "lift",
  "ic",
  "brier",
  "reliability",
  "quintile_spread",
  "sample_gate",
];

export default function GlossaryPage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">
        Glossary
      </h1>
      <p
        className="text-[0.85rem] leading-relaxed max-w-prose"
        style={{ color: "var(--dim)" }}
      >
        Every number SignalDeck shows you, in plain English first and the
        precise definition underneath. If a term on any page is unclear, it is
        defined here.
      </p>

      <dl className="panel flex flex-col">
        {ORDER.map((key, i) => {
          const def = PLAIN[key];
          const reading = def.read(null);
          const showProLabel = def.label.simple !== def.label.pro;
          return (
            <div
              key={key}
              className={`px-4 py-3 sm:px-5 ${
                i > 0 ? "border-t" : ""
              }`}
              style={{ borderColor: "var(--border)" }}
            >
              <dt className="flex items-baseline gap-2 text-[0.85rem] font-bold" style={{ color: "var(--text)" }}>
                <span>{def.label.simple}</span>
                {showProLabel && (
                  <span className="chip mono text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {def.label.pro}
                  </span>
                )}
              </dt>
              <dd
                className="m-0 text-[0.85rem] leading-relaxed"
                style={{ color: "var(--dim)" }}
              >
                {reading.detail}
              </dd>
            </div>
          );
        })}
      </dl>

      <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        These definitions are generated from the same dictionary the tooltips
        use, so they cannot drift apart.
      </p>
    </div>
  );
}