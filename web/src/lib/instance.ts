// Which price source this site's daemon shows publicly, asked from a SERVER
// component. "" = none (the default instance: price-derived content is
// operator-only). "iexhist" = the public instance fed by IEX's historical feed.
//
// The server pages were written for the default instance, where every figure
// is withheld from visitors. On the IEX instance that wording is wrong: the
// forecasts are public there and its record is new. Pages ask this once and
// pick the wording that is true for the instance they are running on.

const DAEMON = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";

export async function getPriceSource(): Promise<string> {
  try {
    const res = await fetch(`${DAEMON}/api/health`, {
      cache: "no-store",
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(3_000),
    });
    if (!res.ok) return "";
    const body = (await res.json()) as { priceSource?: unknown };
    return typeof body.priceSource === "string" ? body.priceSource : "";
  } catch {
    // An unreachable daemon is treated as the default instance: the page then
    // shows its usual unreachable state, never public-instance wording.
    return "";
  }
}

export const IEX_SOURCE = "iexhist";
