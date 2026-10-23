// The daemon's licence withholding (HTTP 451), as the web app reads it.
//
// On a published deployment only the operator may receive content derived from
// Alpaca market data or from Kadoa's congressional compilation. Everyone else
// gets this answer, by contract with daemon/internal/api:
//
//   451, application/json,
//   {"error":"withheld","reason":"licence","source":"alpaca"|"kadoa"|...,"notice":"<plain sentence>"}
//
// A 451 is not an outage and not a sign-in problem: the daemon answered, and it
// will answer the same way on every retry until the rights question is settled.
// So it gets its own typed result, never "temporarily unavailable", never
// "unreachable", never the raw body. Plain TS (no JSX) so node --test runs it.

export type Withheld = { source: string; notice: string };

export const WITHHELD_STATUS = 451;

/** Where a reader goes for the reason. /terms owns the data-sources section. */
export const WITHHELD_HREF = "/terms#data-sources";

/** The contract's notices, used when a 451 arrives without one of its own. */
export const WITHHELD_NOTICES: Readonly<Record<string, string>> = {
  alpaca:
    "Withheld: SignalDeck's prices, forecasts and statistics are derived from Alpaca market data, which is licensed for personal use only. They are shown only to the operator until Alpaca gives written permission.",
  kadoa:
    "Withheld: the congressional trading rows were compiled by Kadoa, whose terms do not permit sharing them without written permission. Collection has stopped and the stored rows are shown only to the operator.",
};

const FALLBACK_NOTICE =
  "Withheld: this content comes from a data source whose terms do not permit SignalDeck to share it. It is shown only to the operator until written permission is given.";

const PREFIX = "Withheld:";

const str = (v: unknown): string | undefined => (typeof v === "string" && v.trim() ? v.trim() : undefined);

/**
 * The withholding a response carries, or null when it carries none.
 *
 * ANY 451 is a withholding, even one whose body is missing or is the older
 * operator-facing licence sentence (it names an env var to flip and must not
 * reach a reader): the status is the contract, the body only words it. The
 * notice always starts "Withheld:", which is how withheldFromMessage knows it
 * again after a caller has reduced the error to its message string.
 */
export function parseWithheld(status: number, body: unknown): Withheld | null {
  if (status !== WITHHELD_STATUS) return null;
  const b = typeof body === "object" && body !== null && !Array.isArray(body) ? (body as Record<string, unknown>) : {};
  const source = str(b.source)?.toLowerCase() ?? "unknown";
  const given = b.error === "withheld" ? str(b.notice) : undefined;
  const notice = given ?? WITHHELD_NOTICES[source] ?? FALLBACK_NOTICE;
  return { source, notice: notice.startsWith(PREFIX) ? notice : `${PREFIX} ${notice}` };
}

/**
 * Most pages keep only `e.message` from a failed read and hand that string to
 * ErrorState. api.ts makes a withholding's message its notice, so this turns
 * the string back into the typed result instead of every caller learning a new
 * error shape.
 */
export function withheldFromMessage(message: string | null | undefined): Withheld | null {
  const m = message?.trim();
  if (!m || !m.startsWith(PREFIX)) return null;
  const known = Object.keys(WITHHELD_NOTICES).find((k) => WITHHELD_NOTICES[k] === m);
  const source = known ?? (/\bKadoa\b/.test(m) ? "kadoa" : /\bAlpaca\b/.test(m) ? "alpaca" : "unknown");
  return { source, notice: m };
}
