"use client";

// CellBar (Stage 4 of the "tables → charts" pass) — an inline magnitude bar
// that sits BEHIND a number inside a table cell, so a column of %s / ratios /
// dollar values reads at a glance without replacing the exact figure.
//
// HONESTY: the bar is a magnitude aid only. The caller decides the scale
// (absolute 0..1 for true fractions, or column-relative for open-ended
// values) and says so in `title`. A null/non-finite fraction renders an
// empty track — never a fake fill. Color is the caller's job too, so a
// direction-NEUTRAL quantity (e.g. the FINRA short-volume ratio) can stay
// neutral instead of implying good/bad.

export default function CellBar({
  frac,
  label,
  color = "var(--accent)",
  title,
  width = 84,
  align = "right",
}: {
  /** Fill fraction 0..1 (already normalized by the caller); null = no fill. */
  frac: number | null | undefined;
  /** The exact formatted value — always rendered, never replaced. */
  label: string;
  /** Bar color (CSS value). Pick a neutral for non-directional quantities. */
  color?: string;
  /** Tooltip explaining what the bar's scale means (absolute vs relative). */
  title?: string;
  /** Track width in px (fits inside table cells). */
  width?: number;
  align?: "left" | "right";
}) {
  const pct =
    frac == null || !Number.isFinite(frac) ? null : Math.max(0, Math.min(1, frac)) * 100;
  return (
    <span
      className="relative inline-block align-middle"
      style={{ width }}
      title={title}
      role="img"
      aria-label={
        pct === null ? `${label} (no magnitude bar — no read)` : `${label} — bar fill ${pct.toFixed(0)}%`
      }
    >
      {/* the track */}
      <span
        aria-hidden="true"
        className="absolute inset-y-[3px] left-0 w-full rounded-sm"
        style={{ background: "color-mix(in srgb, var(--faint) 12%, transparent)" }}
      />
      {/* the fill — behind the text so the number stays legible */}
      {pct !== null && (
        <span
          aria-hidden="true"
          className="absolute inset-y-[3px] left-0 rounded-sm"
          style={{
            width: `${pct}%`,
            background: `color-mix(in srgb, ${color} 30%, transparent)`,
          }}
        />
      )}
      <span
        className={`tnum relative z-[1] block px-1 ${align === "right" ? "text-right" : "text-left"}`}
      >
        {label}
      </span>
    </span>
  );
}
