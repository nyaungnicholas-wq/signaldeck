"use client";

/**
 * A sortable table column header, done so a keyboard can use it.
 *
 * Three tables here sorted by putting `onClick` straight on the `<th>`. A
 * table header is not focusable, so sorting was unreachable without a mouse,
 * and with no `aria-sort` a screen reader could not tell the table was
 * sortable or which way it was ordered.
 *
 * The split matters: `aria-sort` belongs on the `<th>` because it describes
 * the COLUMN's state, while the pressable thing has to be a real `<button>`
 * inside it. Putting either on the other element is what the broken versions
 * were doing.
 */

import type { ReactElement } from "react";

export type SortDirection = "asc" | "desc";

function SortIcon({ active, dir }: { active: boolean; dir: SortDirection }): ReactElement {
  return (
    <svg
      aria-hidden="true"
      width="8"
      height="8"
      viewBox="0 0 8 8"
      className="ml-1 inline-block"
      style={{ color: active ? "var(--accent)" : "var(--faint)", opacity: active ? 1 : 0.4 }}
    >
      <polygon points={dir === "asc" ? "4,1 7,6 1,6" : "4,7 7,2 1,2"} fill="currentColor" />
    </svg>
  );
}

export default function SortHeader({
  label,
  active,
  dir,
  onSort,
  align = "left",
  className = "",
}: {
  label: string;
  /** Is the table currently sorted by THIS column? */
  active: boolean;
  dir: SortDirection;
  onSort: () => void;
  align?: "left" | "right";
  /** Extra classes for the <th>, for tables with their own cell padding. */
  className?: string;
}): ReactElement {
  return (
    <th
      aria-sort={active ? (dir === "asc" ? "ascending" : "descending") : "none"}
      className={`${align === "right" ? "text-right" : "text-left"} ${className}`}
    >
      <button
        type="button"
        onClick={onSort}
        // The <th> already announces the sort state; the button only needs to
        // say what pressing it does, or screen readers hear it twice.
        aria-label={`Sort by ${label.toLowerCase()}`}
        className="inline-flex min-h-[40px] cursor-pointer items-center font-normal uppercase tracking-wider transition-colors duration-150 hover:text-[var(--text)]"
        style={active ? { color: "var(--accent)" } : undefined}
      >
        {label}
        <SortIcon active={active} dir={dir} />
      </button>
    </th>
  );
}
