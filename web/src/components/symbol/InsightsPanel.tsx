"use client";

// INSIGHTS — the daemon's written observations for this symbol.

import type { Insight } from "@/lib/api";
import { ago } from "@/lib/format";
import EmptyState from "@/components/EmptyState";

export default function InsightsPanel({ insights }: { insights?: Insight[] }) {
  const items = insights ?? [];
  return (
    <section className="panel">
      <div className="panel-h">
        <span>INSIGHTS</span>
        <span className="ml-auto tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {items.length}
        </span>
      </div>
      <div className="p-4">
        {items.length === 0 ? (
          <EmptyState
            message="No insights written for this symbol yet"
            detail="The insight-writer agent adds plain-English reads as scores and states change."
          />
        ) : (
          <ul className="flex flex-col gap-3">
            {items.map((it) => (
              <li key={it.id} className="border-b pb-3 last:border-b-0 last:pb-0" style={{ borderColor: "var(--border)" }}>
                <div className="flex items-baseline justify-between gap-3">
                  <span className="text-[0.8rem] font-bold" style={{ color: "var(--text)" }}>
                    {it.headline}
                  </span>
                  <span className="tnum shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {ago(it.ts)}
                  </span>
                </div>
                {it.body && (
                  <p className="mt-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                    {it.body}
                  </p>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
