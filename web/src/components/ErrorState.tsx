"use client";

/** Shared fetch-failure panel: message, daemon hint, and an optional Retry button. */
export default function ErrorState({
  message,
  retry,
  hint = "Is the daemon running? Start signaldeckd and this page recovers on its own.",
  className = "",
}: {
  message: string;
  retry?: () => void;
  hint?: string | null;
  className?: string;
}) {
  return (
    <div
      role="alert"
      className={`panel flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-4 text-[0.75rem] leading-relaxed ${className}`}
    >
      <div className="min-w-0 flex-1">
        <span style={{ color: "var(--bad)" }}>{message}</span>
        {hint ? (
          <div className="mt-1 text-[0.75rem]" style={{ color: "var(--dim)" }}>
            {hint}
          </div>
        ) : null}
      </div>
      {retry ? (
        <button
          type="button"
          onClick={retry}
          className="chip min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:text-[var(--text)]"
          style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          Retry
        </button>
      ) : null}
    </div>
  );
}
