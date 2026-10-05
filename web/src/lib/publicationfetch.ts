// What the accuracy page's server-side fetch of /api/accuracy actually said.
//
// A refusal the grader issued and a daemon that never answered are different
// facts. A fetch that threw (timeout, connection refused) or a reply that is not
// one of the daemon's own verdicts (a proxy's 502/504, a 429, an unknown status)
// is NOT a refusal: rendering it as REFUSED_STALE told visitors the grader had
// withheld the figures when the daemon was merely slow (2026-10-02: fifteen 6 s
// timeouts in half an hour). It is not publishable either: only a 2xx carrying
// status OK is.
//
// Pure functions, no I/O, so node --test can exercise them.

export type Unavailable = { kind: "unavailable"; cause: "timeout" | "unreachable" | "http"; detail: string };

export type PublicationFetch =
  | { kind: "ok"; body: Record<string, unknown> }
  | { kind: "private" }
  | { kind: "refused"; status: string; reason?: string; gradedAt?: string; refusedSince?: string }
  | Unavailable;

// Every refusal daemon/internal/api/accuracy.go emits. Anything else is not a
// verdict this page can name.
const REFUSALS = new Set(["REFUSED", "REFUSED_STALE", "REFUSED_UNAVAILABLE"]);

const str = (v: unknown): string | undefined => (typeof v === "string" ? v : undefined);

export function classifyPublicationFetch(
  res: { ok: boolean; status: number } | null,
  body: unknown,
  error?: unknown,
): PublicationFetch {
  if (res === null) {
    const name = error instanceof Error ? error.name : "";
    return {
      kind: "unavailable",
      cause: name === "TimeoutError" || name === "AbortError" ? "timeout" : "unreachable",
      detail: error instanceof Error ? error.message : String(error ?? "no response"),
    };
  }
  if (res.status === 401 || res.status === 403) return { kind: "private" };
  const env =
    typeof body === "object" && body !== null && !Array.isArray(body) ? (body as Record<string, unknown>) : null;
  const status = env ? str(env.status) : undefined;
  if (env && res.ok && status === "OK") return { kind: "ok", body: env };
  if (env && status && REFUSALS.has(status)) {
    return {
      kind: "refused",
      status,
      reason: str(env.reason),
      gradedAt: str(env.graded_at),
      refusedSince: str(env.refused_since),
    };
  }
  return { kind: "unavailable", cause: "http", detail: `HTTP ${res.status}${status ? ` status ${status}` : ""}` };
}

// The banner sentence, worded by cause. It must never name the grader as the
// one withholding, and must avoid the words the refusal summariser maps to "the
// grading service could not be read" (src/lib/refusal.ts).
export function unavailableReason(u: Unavailable): string {
  const tail = "so no figures are shown. This is not a refusal by the grader and says nothing about the record. Retry in a minute.";
  switch (u.cause) {
    case "timeout":
      return `The accuracy service did not answer in time, ${tail}`;
    case "http":
      return `The accuracy service is temporarily unavailable, ${tail}`;
    default:
      return `The accuracy service could not be reached, ${tail}`;
  }
}
