/** Shared loading placeholder — shimmering panel rows in the terminal palette.
 *  Use `lines` for row count; `label` is announced to screen readers. */
export default function Skeleton({
  lines = 3,
  label = "loading",
  className = "",
}: {
  lines?: number;
  label?: string;
  className?: string;
}) {
  return (
    <div
      role="status"
      aria-label={label}
      className={`panel flex flex-col gap-3 p-4 ${className}`}
    >
      {Array.from({ length: lines }).map((_, i) => (
        <div
          key={i}
          className="skeleton-bar h-4 rounded"
          style={{ width: `${100 - ((i * 17) % 45)}%` }}
        />
      ))}
      <span className="sr-only">{label}…</span>
    </div>
  );
}
