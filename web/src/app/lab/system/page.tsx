"use client";

// LAB → SYSTEM (Stage 5) — the "one look at the machine" page: data QUALITY,
// the AGENTS worker fleet, and the AI surface mounted TOGETHER (user
// decision: "System sub-tab = quality + agents + AI chat mounted together").
// Each section is the SAME component as its focused sub-tab (single source
// of truth — no duplicated fetching logic to drift), so every honesty
// note/gate renders identically here; the AI chat keeps its own auth gate.
// The QUALITY / AGENTS / AI tabs above remain as focused deep links.

import QualityPage from "./quality/page";
import AgentsPage from "./agents/page";
import AIPage from "./ai/page";

export default function SystemAllPage() {
  return (
    <div className="flex flex-col gap-8">
      <section aria-label="data quality">
        <QualityPage />
      </section>
      <section aria-label="agent fleet">
        <AgentsPage />
      </section>
      <section aria-label="AI surface">
        <AIPage />
      </section>
    </div>
  );
}
