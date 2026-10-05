"use client";

import { PageHero, Reveal } from "@/components/ui/Kit";
import QualityPage from "./quality/page";
import AgentsPage from "./agents/page";
import AIPage from "./ai/page";

export default function SystemAllPage() {
  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="System Health"
        subtitle="One look at the machine: data quality, the background worker fleet, and the AI surface — same honesty gates as the focused tabs."
      />
      <Reveal>
        <section aria-label="data quality" className="panel reveal-item" style={{ "--i": 0 } as React.CSSProperties}>
          <QualityPage />
        </section>
        <section aria-label="agent fleet" className="panel reveal-item" style={{ "--i": 1 } as React.CSSProperties}>
          <AgentsPage />
        </section>
        <section aria-label="AI surface" className="panel reveal-item" style={{ "--i": 2 } as React.CSSProperties}>
          <AIPage />
        </section>
      </Reveal>
    </div>
  );
}
