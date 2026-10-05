"use client";

// MULTI-AGENT PANEL — the nine analyst roles that each reason INDEPENDENTLY over
// the same real stored data and reach their own stance. Showing all nine (not a
// single blended verdict) is the honesty: disagreement is visible, and a
// unanimous panel reads differently from a split one. Each row is a role, its
// stance (bullish green / bearish red / neutral faint), and its plain-English
// view verbatim.

import type { RecoAgent } from "@/components/desk/RecommendationCard";

/** Stance → color: bullish green, bearish red, neutral faint. */
function stanceColor(stance: string): string {
  if (stance === "bullish") return "var(--ok)";
  if (stance === "bearish") return "var(--bad)";
  return "var(--faint)";
}

export default function AgentPanel({ agents }: { agents: RecoAgent[] }) {
  const list = Array.isArray(agents) ? agents : [];

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        MULTI-AGENT PANEL
        <span className="normal-case" style={{ color: "var(--faint)", letterSpacing: "normal" }}>
          — {list.length} analysts
        </span>
      </div>

      {list.length === 0 ? (
        <p className="px-4 py-4 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          No analyst views for this symbol yet.
        </p>
      ) : (
        <div className="flex flex-col">
          {list.map((a, i) => (
            <div
              key={`${a.role}-${i}`}
              className="flex flex-col gap-1 px-4 py-2.5"
              style={{ borderTop: i === 0 ? undefined : "1px solid var(--border)" }}
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[0.82rem] font-bold" style={{ color: "var(--text)" }}>
                  {a.role}
                </span>
                <span
                  className="chip ml-auto font-medium"
                  style={{ color: stanceColor(a.stance), borderColor: stanceColor(a.stance) }}
                >
                  {a.stance}
                </span>
              </div>
              <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                {a.view}
              </p>
            </div>
          ))}
        </div>
      )}

      <p
        className="px-4 py-2.5 text-[0.75rem] leading-relaxed"
        style={{ color: "var(--faint)", borderTop: "1px solid var(--border)" }}
      >
        Each analyst reasons independently over real stored data — the panel shows every view, agreement
        and disagreement alike, never a single blended answer.
      </p>
    </section>
  );
}
