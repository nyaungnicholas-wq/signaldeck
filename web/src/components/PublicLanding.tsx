import Link from "next/link";

// The front page of the public (IEX-fed) instance. The default landing page
// describes a record whose figures are withheld and invites sign-up; neither
// is true here. This one says what a visitor can see, where the data comes
// from, and what has not been measured yet. No accuracy claim of any kind.
const POINTS: { title: string; body: string }[] = [
  {
    title: "What you are looking at",
    body: "For each covered stock, a reading of whether its volatility, its price trend and its trading activity are running higher or lower than usual, and expected to stay that way over the coming weeks. Each reading comes from a fixed rule applied to recent prices and volumes.",
  },
  {
    title: "Where the data comes from",
    body: "IEX, a US stock exchange that publishes its own trading data for free. That means prices and volumes here are trades on IEX only, a small share of the whole market, and they are from the previous trading day, not live.",
  },
  {
    title: "What has not been measured",
    body: "How often these readings turn out right. This public record is new, so grades are few at first. The Grades page fills in by itself as each forecast window passes, and shows the result next to a simple baseline whether it is good or bad.",
  },
];

export default function PublicLanding() {
  return (
    <div className="mx-auto flex w-full max-w-[1000px] flex-col gap-8 pb-12 pt-6" data-testid="public-landing">
      <header className="flex flex-col gap-4">
        <div className="mono text-[0.7rem] uppercase tracking-[0.2em]" style={{ color: "var(--accent)" }}>
          Open research instrument
        </div>
        <h1 className="m-0 max-w-[24ch] text-[1.9rem] font-extrabold leading-tight sm:text-[2.4rem]">
          Stock forecasts with the record kept in the open.
        </h1>
        <p className="m-0 max-w-[60ch] text-[1rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          SignalDeck stores each one-month forecast, together with a simple baseline to beat, before
          the outcome is known, and keeps every one, including the ones that turn out wrong. No
          account is needed to read it. Descriptive, not advice.
        </p>
        <div className="flex flex-wrap items-center gap-3 pt-1">
          <Link href="/forecasts" className="cta-primary">
            See the forecasts
          </Link>
          <Link href="/accuracy" className="cta-secondary">
            How it will be graded
          </Link>
          <Link href="/glossary" className="cta-secondary">
            Plain-English terms
          </Link>
        </div>
      </header>

      <section className="grid gap-3 sm:grid-cols-3" aria-label="about this site">
        {POINTS.map((p) => (
          <div key={p.title} className="panel flex flex-col gap-2 px-5 py-4">
            <div className="text-[0.85rem] font-semibold">{p.title}</div>
            <p className="m-0 text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
              {p.body}
            </p>
          </div>
        ))}
      </section>

      <section className="panel px-5 py-4 text-[0.8rem] leading-relaxed" aria-label="data source">
        <a
          href="https://www.iex.io/legal/hist-data-terms"
          target="_blank"
          rel="noreferrer"
          className="font-semibold hover:underline"
        >
          Data provided for free by IEX.
        </a>{" "}
        <span style={{ color: "var(--dim)" }}>
          SignalDeck also keeps an older research record measured on licensed market data. Its
          figures are not shown on this site because that licence does not allow it.
        </span>
      </section>
    </div>
  );
}
