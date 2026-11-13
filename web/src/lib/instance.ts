// Which price source this site's daemon shows publicly, asked from a SERVER
// component. "" = none (the default instance: price-derived content is
// operator-only). "iexhist" = the public instance fed by IEX's historical feed.
//
// The server pages were written for the default instance, where every figure
// is withheld from visitors. On the IEX instance that wording is wrong: the
// forecasts are public there and its record is new. Pages ask this once and
// pick the wording that is true for the instance they are running on.

const DAEMON = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";

// The web process is TOLD which instance it serves (SIGNALDECK_WEB_PRICE_SOURCE,
// set by whatever starts the public web). Unset means the default instance and
// costs nothing: no daemon call, so these pages gain no latency and cannot fall
// back to the wrong wording because a health check timed out. When it is set,
// the daemon is asked as well. If it ANSWERS with a different source, this web
// is pointed at the wrong daemon and shows the default (withheld) wording,
// never public wording. If it does not answer at all, the told value stands:
// the public pages chosen by it need no daemon data, and falling back to the
// default wording during a restart would tell visitors the data is withheld.
export async function getPriceSource(): Promise<string> {
  const told = (process.env.SIGNALDECK_WEB_PRICE_SOURCE || "").trim().toLowerCase();
  if (!told) return "";
  const asked = await askDaemon();
  if (asked === null) return told;
  return asked === told ? told : "";
}

async function askDaemon(): Promise<string | null> {
  try {
    const res = await fetch(`${DAEMON}/api/health`, {
      cache: "no-store",
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(3_000),
    });
    if (!res.ok) return null;
    const body = (await res.json()) as { priceSource?: unknown };
    return typeof body.priceSource === "string" ? body.priceSource : "";
  } catch {
    return null; // unreachable: the caller decides
  }
}

export const IEX_SOURCE = "iexhist";
