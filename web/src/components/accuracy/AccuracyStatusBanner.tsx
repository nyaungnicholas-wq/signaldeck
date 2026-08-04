export type AccuracyStatus = "OK" | "INSUFFICIENT" | "FAILED" | "RETIRED" | "REFUSED_STALE" | "NO_BASELINE" | "QUARANTINED";

/**
 * Renders an explicit publication status for accuracy results.
 * A refusal a reader cannot act on is barely better than silence.
 * This banner ensures status is loud, includes reasons, and
 * prevents silent failures like audit F-1 (2026-08-03).
 */
export function AccuracyStatusBanner({
  status,
  reasons = [],
  evidenceRefs = [],
  className = "",
}: {
  status: AccuracyStatus;
  reasons?: string[];
  evidenceRefs?: string[];
  className?: string;
}) {
  const gloss: Record<AccuracyStatus, string> = {
    OK: "published: the live record supports this row",
    INSUFFICIENT: "not enough independent evidence to publish an interval",
    FAILED: "the live record contradicts this row",
    RETIRED: "retired: the record has contradicted this model and retirement does not lapse",
    REFUSED_STALE: "refused: the grader has not produced a fresh result",
    NO_BASELINE: "no comparable baseline; accuracy alone is not evidence",
    QUARANTINED: "quarantined: excluded from every benchmark denominator",
  };

  const colorMap: Record<AccuracyStatus, string> = {
    OK: "border-emerald-500/60 bg-emerald-500/10 text-emerald-300",
    INSUFFICIENT: "border-amber-500/60 bg-amber-500/10 text-amber-300",
    FAILED: "border-red-500/60 bg-red-500/10 text-red-300",
    RETIRED: "border-red-500/60 bg-red-500/10 text-red-300",
    REFUSED_STALE: "border-red-500/60 bg-red-500/10 text-red-300",
    NO_BASELINE: "border-zinc-500/60 bg-zinc-500/10 text-zinc-300",
    QUARANTINED: "border-zinc-500/60 bg-zinc-500/10 text-zinc-300",
  };

  return (
    <div
      data-testid="accuracy-status-banner"
      data-status={status}
      className={`rounded border px-4 py-3 text-sm ${colorMap[status]} ${className}`}
    >
      <div className="font-bold uppercase tracking-wide">{status}</div>
      <div className="mt-1 opacity-90">{gloss[status]}</div>
      {reasons.length > 0 && (
        <ul className="mt-2 list-disc pl-4">
          {reasons.map((r, i) => (
            <li key={i} data-testid="accuracy-status-reason">
              {r}
            </li>
          ))}
        </ul>
      )}
      {evidenceRefs.length > 0 && (
        <div className="mt-2 font-mono text-xs opacity-70">
          evidence: {evidenceRefs.join(", ")}
        </div>
      )}
    </div>
  );
}