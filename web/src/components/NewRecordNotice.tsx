import Link from "next/link";

// Shown on the grades and volatility-record pages of the public (IEX-fed)
// instance. That instance started a NEW record on IEX data; nothing on it has
// been graded yet, and the older research record was measured on licensed data
// that cannot be shown here. Saying exactly that is the whole job: an empty
// table or a "refused" banner would read as a fault.
export default function NewRecordNotice({ title, what }: { title: string; what: string }) {
  return (
    <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5" data-testid="new-record-notice">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.4rem] font-extrabold tracking-tight">{title}</h1>
        <p className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {what}
        </p>
      </header>
      <section className="panel px-5 py-5 text-[0.85rem] leading-relaxed">
        <p className="m-0 font-semibold">No grades yet: this public record is new.</p>
        <p className="mt-2" style={{ color: "var(--dim)" }}>
          The forecasts on this site are built from IEX exchange data, and that record has only just
          started. A forecast is graded once its window has passed, so the first results for
          one-month forecasts appear about a month after the record began. Until then there is
          nothing to show here, and nothing is being hidden.
        </p>
        <p className="mt-2" style={{ color: "var(--dim)" }}>
          SignalDeck also keeps an older research record measured on licensed market data. Its
          figures are not shown on this site because that licence does not allow it.
        </p>
        <p className="mt-3">
          <Link href="/forecasts" className="hover:underline" style={{ color: "var(--accent)" }}>
            See the current forecasts
          </Link>
        </p>
      </section>
    </div>
  );
}
