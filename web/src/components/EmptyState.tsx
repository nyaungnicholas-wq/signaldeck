/** Shared "no data yet" panel — quiet, not an error. */
export default function EmptyState({
  message,
  detail,
  className = "",
}: {
  message: string;
  detail?: string;
  className?: string;
}) {
  return (
    <div className={`panel px-4 py-6 text-[0.8rem] ${className}`} style={{ color: "var(--dim)" }}>
      {message}
      {detail ? (
        <div className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {detail}
        </div>
      ) : null}
    </div>
  );
}
