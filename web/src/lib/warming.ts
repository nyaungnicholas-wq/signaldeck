// The daemon's 503 {"error":"warming"} answer, handled in one place: get() in
// api.ts waits it out through untilWarm, and api.ts re-exports both names.
// No imports on purpose, so node --test loads this file as it is
// (warming.test.mjs); api.ts itself cannot be loaded that way.

/** The fields of api.ts's ApiError that a warming answer is recognised by. */
export type WarmingError = Error & { status: number; code?: string; retryAfterMs?: number };

/** 503 {"error":"warming"}: after a restart a cache this read is served from
 *  has no value yet and the daemon is building it. The daemon answered, so this
 *  is not an outage; ask again after Retry-After. */
export function isWarming(e: unknown): e is WarmingError {
  if (!(e instanceof Error)) return false;
  const w = e as Partial<WarmingError>;
  return w.status === 503 && w.code === "warming";
}

export interface UntilWarmOptions {
  /** Runs before each wait, so a page can say "warming up" instead of loading. */
  onWarming?: () => void;
  /** False once the caller has gone: no further request is sent for it. */
  alive?: () => boolean;
  /** Total wait, default 3 min: the slowest cold build measured is 129 s. */
  capMs?: number;
  /** Injected by tests; defaults to setTimeout. */
  sleep?: (ms: number) => Promise<void>;
}

/** Re-runs fn while the daemon says it is warming, honouring Retry-After, for at
 *  most capMs. Anything else, the cap, or a caller that has gone throws the
 *  last error. */
export async function untilWarm<T>(fn: () => Promise<T>, opts: UntilWarmOptions = {}): Promise<T> {
  const {
    onWarming,
    alive = () => true,
    capMs = 180_000,
    sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms)),
  } = opts;
  const deadline = Date.now() + capMs;
  for (;;) {
    try {
      return await fn();
    } catch (e) {
      const wait = isWarming(e) ? Math.min(e.retryAfterMs ?? 30_000, deadline - Date.now()) : 0;
      if (wait <= 0 || !alive()) throw e;
      onWarming?.();
      await sleep(wait);
      // The caller may have gone during the sleep: send nothing more for it.
      if (!alive()) throw e;
    }
  }
}
