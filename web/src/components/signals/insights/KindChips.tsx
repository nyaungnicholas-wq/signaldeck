"use client";

// Kind filter chips for the INSIGHTS feed. Counts come from the latest
// polled window (last N insights). Selecting a real kind re-queries the
// daemon server-side (?kind=, json_extract on data.kind) — rows are never
// faked by hiding them client-side. Two pseudo-keys are local-only by
// nature: ALL (the unfiltered feed) and UNLABELED (blobs with no kind tag,
// which the server cannot address).

export const ALL_KINDS = "__all";
export const UNLABELED = "__unlabeled";

export interface KindOption {
  key: string; // ALL_KINDS | UNLABELED | a data.kind value
  label: string;
  count: number;
  title?: string;
}

export default function KindChips({
  options,
  active,
  onSelect,
}: {
  options: KindOption[];
  active: string;
  onSelect: (key: string) => void;
}) {
  return (
    <div
      className="flex flex-wrap items-center gap-1.5"
      role="group"
      aria-label="Filter insights by kind"
    >
      {options.map((o) => {
        const isActive = active === o.key;
        return (
          <button
            key={o.key}
            type="button"
            aria-pressed={isActive}
            title={o.title}
            onClick={() => onSelect(o.key)}
            className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:bg-[var(--panel3)] hover:text-[var(--text)]"
            style={{
              color: isActive ? "var(--accent)" : undefined,
              borderColor: isActive ? "var(--accent)" : undefined,
            }}
          >
            {o.label} <span className="tnum">{o.count}</span>
          </button>
        );
      })}
    </div>
  );
}
