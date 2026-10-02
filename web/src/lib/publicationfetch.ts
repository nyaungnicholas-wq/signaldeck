// What the accuracy page's server-side fetch of /api/accuracy actually said.
//
// A refusal the grader issued and a daemon that never answered are different
// facts. A fetch that threw (timeout, connection refused) or a reply that is not
// the daemon's own envelope (a proxy's 502/504) is NOT a refusal: rendering it
// as REFUSED_STALE told visitors the grader had withheld the figures when the
// daemon was merely slow (2026-10-02: fifteen 6 s timeouts in half an hour).
//
// Pure function, no I/O, so node --test can exercise it.

export type PublicationFetch =
  | { kind: "ok"; body: Record<string, unknown> }
  | { kind: "private" }
  | { kind: "refused"; status: string; reason?: string; gradedAt?: string; refusedSince?: string }
  | { kind: "unavailable"; detail: string };

const str = (v: unknown): string | undefined => (typeof v === "string" ? v : undefined);

export function classifyPublicationFetch(
  res: { ok: boolean; status: number } | null,
  body: unknown,
  error?: unknown,
): PublicationFetch {
  if (res === null) {
    return { kind: "unavailable", detail: error instanceof Error ? error.message : String(error ?? "no response") };
  }
  if (res.status === 401 || res.status === 403) return { kind: "private" };
  const env =
    typeof body === "object" && body !== null && !Array.isArray(body) ? (body as Record<string, unknown>) : null;
  const status = env && typeof env.status === "string" && env.status !== "" ? env.status : undefined;
  if (env && res.ok && status === "OK") return { kind: "ok", body: env };
  if (env && status !== undefined) {
    return {
      kind: "refused",
      status,
      reason: str(env.reason),
      gradedAt: str(env.graded_at),
      refusedSince: str(env.refused_since),
    };
  }
  return { kind: "unavailable", detail: `HTTP ${res.status}` };
}
