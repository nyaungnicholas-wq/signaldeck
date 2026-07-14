"use client";

// One labeled stat cell for the lab result grids (backtest, signal backtest,
// paper). Extracted from the three identical per-page copies. `help` renders
// an accessible <HelpTip> beside the label — the replacement for the old
// hover-only [title] tooltip; `hint` stays the always-visible caption line.

import HelpTip from "@/components/HelpTip";

export default function Metric({
  label,
  value,
  color,
  hint,
  help,
}: {
  label: string;
  value: string;
  color?: string;
  hint?: string;
  help?: React.ReactNode;
}) {
  return (
    <div
      className="flex flex-col gap-1 px-4 py-3"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <span
        className="flex items-center gap-1 text-[0.75rem] tracking-wide"
        style={{ color: "var(--faint)" }}
      >
        {label}
        {help && <HelpTip label={`What does ${label.toLowerCase()} mean?`}>{help}</HelpTip>}
      </span>
      <span
        className="tnum text-[0.95rem] font-bold"
        style={{ color: color ?? "var(--text)" }}
      >
        {value}
      </span>
      {hint && (
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {hint}
        </span>
      )}
    </div>
  );
}
