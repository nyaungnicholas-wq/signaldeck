"use client";

// FRESHNESS BADGE (freshness wave): tiny "updated Xs ago" chip driven by the
// app-wide freshness bus (lib/freshness). Turns amber past 90s without a
// successful fetch — or while offline — and says "stale" in plain words.
// Renders nothing before the first success (no fabricated age).

import { useAppFreshness } from "@/lib/freshness";

const STALE_AFTER_S = 90;

function ageLabel(s: number): string {
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h`;
}

export default function FreshnessBadge({ className = "" }: { className?: string }) {
  const { staleSeconds, offline } = useAppFreshness();
  if (staleSeconds === null) return null;
  const stale = offline || staleSeconds > STALE_AFTER_S;

  return (
    <span
      className={`chip inline-flex items-center gap-1.5 whitespace-nowrap ${className}`}
      style={stale ? { color: "var(--warn)", borderColor: "var(--warn)" } : undefined}
    >
      <svg
        width="12"
        height="12"
        viewBox="0 0 16 16"
        fill="none"
        aria-hidden="true"
        className="shrink-0"
      >
        <circle cx="8" cy="8" r="6.25" stroke="currentColor" strokeWidth="1.5" />
        <path
          d="M8 4.5V8l2.5 1.5"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <span className="tnum">
        updated {ageLabel(staleSeconds)} ago
        {stale && " — stale"}
      </span>
      {/* The visible chip re-renders every second. Making THAT a live region
          would read "updated 1s ago, updated 2s ago…" forever, which is worse
          than silence. A screen reader gets only the transition that changes
          what you should believe: the data stopped arriving. Going healthy
          again clears the region silently — there is nothing to say.

          Silent while offline: OfflineBanner is already a live region saying
          exactly that, and announcing it twice is worse than announcing it
          once. */}
      <span className="sr-only" role="status">
        {stale && !offline ? "These numbers have stopped updating." : ""}
      </span>
    </span>
  );
}
