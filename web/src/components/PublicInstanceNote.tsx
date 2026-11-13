import { getPriceSource, IEX_SOURCE } from "@/lib/instance";

// A note at the top of /terms and /privacy on the public (IEX-fed) instance
// only. Those pages describe the main service: Alpaca-derived content withheld,
// member accounts, email. On the public instance the price data is IEX's and
// there are no accounts, so the page says which parts apply here. It adds
// facts; it does not rewrite the legal text underneath.
export default async function PublicInstanceNote({ page }: { page: "terms" | "privacy" }) {
  if ((await getPriceSource()) !== IEX_SOURCE) return null;
  return (
    <div
      role="note"
      data-testid="public-instance-note"
      className="panel px-4 py-3 text-[0.8rem] leading-relaxed"
      style={{ borderColor: "var(--accent)" }}
    >
      <p className="m-0 font-semibold">About this site</p>
      {page === "terms" ? (
        <>
          <p className="mt-2">
            The forecasts shown on this site are built only from IEX Exchange historical data.{" "}
            <a
              href="https://www.iex.io/legal/hist-data-terms"
              target="_blank"
              rel="noreferrer"
              className="font-semibold hover:underline"
            >
              Data provided for free by IEX.
            </a>{" "}
            Prices and volumes are trades on IEX only, not the whole market, and are from the
            previous trading day. IEX does not endorse this site or its forecasts.
          </p>
          <p className="mt-2" style={{ color: "var(--dim)" }}>
            No Alpaca market data, and nothing derived from it, is shown on this site. The sections
            below about content withheld under the Alpaca licence and about member accounts describe
            SignalDeck&rsquo;s separate research service, not this site. Everything here is
            descriptive market analysis, not financial advice.
          </p>
        </>
      ) : (
        <p className="mt-2" style={{ color: "var(--dim)" }}>
          This site has no accounts and no sign-up, so it asks for no name, email or password, and
          the sections below about account data, email and member features do not apply here. The
          parts about your IP address, the companies that put the site on the internet, and how to
          contact us still apply.
        </p>
      )}
    </div>
  );
}
