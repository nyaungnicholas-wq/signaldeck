export type AccuracyStatus =
  | "OK"
  | "INSUFFICIENT"
  | "FAILED"
  | "RETIRED"
  | "REFUSED"
  | "REFUSED_STALE"
  | "NO_BASELINE"
  | "QUARANTINED"
  | "PRIVATE";

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
    REFUSED:
      "refused: the publication gate will not stand behind figures over this window",
    REFUSED_STALE: "refused: the grader has not produced a fresh result",
    NO_BASELINE: "no comparable baseline; accuracy alone is not evidence",
    QUARANTINED: "quarantined: excluded from every benchmark denominator",
    PRIVATE: "private on this deployment: sign in to read the record (an access setting, not a statistical refusal)",
  };

  const colorMap: Record<AccuracyStatus, string> = {
    OK: "border-emerald-500/60 bg-emerald-500/10 text-emerald-300",
    INSUFFICIENT: "border-amber-500/60 bg-amber-500/10 text-amber-300",
    FAILED: "border-red-500/60 bg-red-500/10 text-red-300",
    RETIRED: "border-red-500/60 bg-red-500/10 text-red-300",
    REFUSED: "border-red-500/60 bg-red-500/10 text-red-300",
    REFUSED_STALE: "border-red-500/60 bg-red-500/10 text-red-300",
    NO_BASELINE: "border-zinc-500/60 bg-zinc-500/10 text-zinc-300",
    QUARANTINED: "border-zinc-500/60 bg-zinc-500/10 text-zinc-300",
    PRIVATE: "border-amber-500/60 bg-amber-500/10 text-amber-300",
  };

  // The status arrives OFF THE WIRE, so TypeScript can never narrow it and a
  // value the daemon emits but this union omits falls through to undefined --
  // which is exactly what happened to "REFUSED". It rendered as
  // `class="rounded border px-4 py-3 text-sm undefined "`: no red border, no
  // background, and an empty description, on the one state this page exists
  // to shout. The daemon emits REFUSED from five sites in api/accuracy.go,
  // more than emit REFUSED_STALE.
  //
  // The fallbacks below are not belt-and-braces, they are the fix: an
  // unrecognised status must read as REFUSED, because for an honesty surface
  // "we do not know what this means" and "do not trust this" are the same
  // answer. Fail closed, loudly.
  const tone = colorMap[status] ?? "border-red-500/60 bg-red-500/10 text-red-300";
  const explanation =
    gloss[status] ?? "unrecognised publication status - treat this as refused";

  return (
    <div
      data-testid="accuracy-status-banner"
      data-status={status}
      className={`rounded border px-4 py-3 text-sm ${tone} ${className}`}
    >
      <div className="font-bold uppercase tracking-wide">{status}</div>
      <div className="mt-1 opacity-90">{explanation}</div>
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