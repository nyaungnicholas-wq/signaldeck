"use client";

// STAGE 3 — guided story flow. Major pages read top-to-bottom as one story:
//   1 · the verdict / big picture → 2 · why → 3 · the details.
// <StorySection> is a numbered plain-English section wrapper. When
// `collapsible` (the DETAILS tier), the SIMPLE view-mode starts it COLLAPSED
// behind a "show the numbers" affordance — raw tables are demoted, never
// deleted — while PRO mode starts expanded. A user's manual toggle always
// wins over the mode default. Hydration-safe: first paint is always SIMPLE
// (collapsed), the same pattern as every other view-mode consumer.

import { useState } from "react";
import { useViewMode } from "@/components/Plain";

export default function StorySection({
  n,
  title,
  sub,
  collapsible = false,
  showLabel = "show the numbers",
  hideLabel = "hide the numbers",
  children,
}: {
  n: number;
  title: string;
  /** Plain-English one-liner after the title (dim). */
  sub?: string;
  /** DETAILS tier: SIMPLE mode starts collapsed, PRO starts expanded. */
  collapsible?: boolean;
  showLabel?: string;
  hideLabel?: string;
  children: React.ReactNode;
}) {
  const mode = useViewMode();
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = !collapsible || (userOpen ?? mode === "pro");

  return (
    <section aria-label={title} className="flex flex-col gap-3">
      <div className="mt-1 flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="m-0 text-[0.8rem] font-extrabold tracking-[0.18em]">
          <span aria-hidden="true" style={{ color: "var(--accent)" }}>
            {n} ·{" "}
          </span>
          {title}
        </h2>
        {sub && (
          <span className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
            {sub}
          </span>
        )}
        {collapsible && (
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setUserOpen(!open)}
            className="chip ml-auto min-h-[36px] cursor-pointer px-3 text-[0.7rem] transition-colors duration-150 hover:text-[var(--text)]"
            style={open ? undefined : { color: "var(--accent)", borderColor: "var(--accent)" }}
          >
            {open ? hideLabel : `${showLabel} ↓`}
          </button>
        )}
      </div>
      {open && children}
      {!open && (
        <p className="m-0 text-[0.7rem]" style={{ color: "var(--faint)" }}>
          detail panels are collapsed in simple view — nothing is hidden, only folded.
        </p>
      )}
    </section>
  );
}
