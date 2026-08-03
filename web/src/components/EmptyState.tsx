import Link from "next/link";

/** Shared "no data yet" panel — quiet, not an error.
 *
 *  `action` is the difference between an empty state and a dead end: it says
 *  what to do next. Pass it wherever the emptiness is something the USER can
 *  resolve (no symbols tracked, no backtest run). Omit it when the emptiness is
 *  ours to fix or just has to be waited out — offering a button that cannot
 *  help is worse than offering none. */
export default function EmptyState({
  message,
  detail,
  action,
  className = "",
}: {
  message: string;
  detail?: string;
  action?: { label: string; href: string };
  className?: string;
}) {
  return (
    <div className={`panel px-4 py-6 text-[0.75rem] ${className}`} style={{ color: "var(--dim)" }}>
      {message}
      {detail ? (
        <div className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {detail}
        </div>
      ) : null}
      {action ? (
        <Link
          href={action.href}
          className="mt-3 inline-flex min-h-[40px] cursor-pointer items-center rounded-lg border px-3 text-[0.8rem] font-medium transition-colors duration-150 hover:bg-[var(--accent-dim)]"
          style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
        >
          {action.label}
        </Link>
      ) : null}
    </div>
  );
}
