"use client";

// OFFLINE BANNER (freshness wave): full-width strip pinned to the top of the
// viewport, visible ONLY while the app is offline — navigator.onLine false or
// 3+ consecutive API failures (see lib/freshness). Renders nothing when
// online, and position:fixed means showing/hiding never shifts page layout.

import { useAppFreshness } from "@/lib/freshness";

// Minute granularity on purpose: this text lives inside a role="status" live
// region (aria-atomic by default), so every text change is re-announced in
// full by screen readers. A per-second countdown would announce the whole
// banner once a second for the first minute of an outage.
function ageLabel(s: number): string {
  if (s < 60) return "under a minute";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h`;
}

export default function OfflineBanner() {
  const { offline, staleSeconds, retry } = useAppFreshness();
  if (!offline) return null;

  return (
    <div
      role="status"
      className="fixed inset-x-0 top-0 z-50 flex flex-wrap items-center justify-center gap-x-3 gap-y-2 border-b px-4 py-2 text-[0.75rem]"
      style={{ background: "var(--panel3)", borderColor: "var(--warn)", boxShadow: "var(--shadow-2)" }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 16 16"
        fill="none"
        aria-hidden="true"
        className="shrink-0"
        style={{ color: "var(--warn)" }}
      >
        <circle cx="8" cy="8" r="6.25" stroke="currentColor" strokeWidth="1.5" />
        <line x1="3.9" y1="12.1" x2="12.1" y2="3.9" stroke="currentColor" strokeWidth="1.5" />
      </svg>
      <span style={{ color: "var(--text)" }}>
        offline — showing last loaded data
        {staleSeconds !== null && (
          <span className="tnum" style={{ color: "var(--dim)" }}>
            {" "}
            · updated {ageLabel(staleSeconds)} ago
          </span>
        )}
      </span>
      <button
        type="button"
        onClick={retry}
        className="chip min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:text-[var(--text)]"
        style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
      >
        Retry
      </button>
    </div>
  );
}
