"use client";

/** A horizontal 0..100 meter bar with a colored fill and accessible role.
    `pct` is clamped to [0,100]; `color` is a CSS color/var for the fill. */
export default function Bar({
  pct,
  color,
  label,
  height = "0.9rem",
}: {
  pct: number;
  color: string;
  label: string;
  height?: string;
}) {
  const clamped = Math.max(0, Math.min(100, isFinite(pct) ? pct : 0));
  return (
    <div
      role="meter"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(clamped)}
      aria-label={label}
      className="w-full overflow-hidden rounded-full border"
      style={{ height, borderColor: "var(--border)", background: "var(--panel2)" }}
    >
      <div
        className="h-full transition-[width] duration-300"
        style={{ width: `${clamped}%`, background: color }}
      />
    </div>
  );
}
