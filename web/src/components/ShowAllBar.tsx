"use client";

// Why this exists: both the market screener and the insights feed cap a long
// list at a default, never a hard limit. A trimmed list that does not admit it
// is trimmed is just a wrong list, so the control and its wording must be
// identical in both places.

import type { ReactElement } from "react";

export default function ShowAllBar({
  shown,
  total,
  limit,
  expanded,
  onToggle,
  noun,
}: {
  shown: number;
  total: number;
  limit: number | null;
  expanded: boolean;
  onToggle: (next: boolean) => void;
  noun: string;
}): ReactElement | null {
  if (limit === null || (!expanded && total <= limit)) {
    return null;
  }

  if (!expanded) {
    return (
      <div
        className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t px-4 py-3"
        style={{ borderColor: "var(--border)" }}
      >
        <button
          type="button"
          className="chip min-h-[40px] cursor-pointer px-3 text-[0.8rem] transition-colors duration-150 hover:text-[var(--text)]"
          style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
          onClick={() => onToggle(true)}
        >
          Show all {total} {noun}
        </button>
        {/* "in the current order" rather than "by your current sort" — this
            is shared with a time-ordered feed where the reader chose no sort,
            and the sentence has to stay true in both places. */}
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          Showing the first {shown} in the current order. {total - shown} more are out of view,
          not out of the data.
        </span>
      </div>
    );
  }

  return (
    <div
      className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t px-4 py-3"
      style={{ borderColor: "var(--border)" }}
    >
      <button
        type="button"
        className="chip min-h-[40px] cursor-pointer px-3 text-[0.8rem] transition-colors duration-150 hover:text-[var(--text)]"
        style={{ color: "var(--dim)" }}
        onClick={() => onToggle(false)}
      >
        Back to the top {limit}
      </button>
      <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        Showing all {total} {noun}.
      </span>
    </div>
  );
}