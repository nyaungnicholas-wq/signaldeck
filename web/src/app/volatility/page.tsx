import Link from "next/link";

/**
 * The live record of the HAR volatility forecast, for a reader with no
 * statistics background.
 *
 * The hard part of this page is not showing numbers, it is REFUSING to. The
 * forecast has only just started accruing live outcomes, so for a long while
 * the honest answer to "is it any good" is "not enough evidence yet", and that
 * has to be the loudest thing on the page rather than a footnote under a
 * table of impressive-looking figures.
 *
 * So: when the daemon says a horizon is INSUFFICIENT, this renders NO losses
 * and NO comparisons at all. Not a dash, not a zero, not a greyed-out number.
 * Absent. A zero loss reads as a perfect forecast, and a greyed-out number
 * still gets screenshotted.
 */
export const dynamic = "force-dynamic";

// THE WORD "LOSS" ON THIS PAGE MEANS QLIKE, THE STATISTICAL LOSS FUNCTION the
// forecast is scored under — forecast error, lower is better. The old metadata
// promised "how much you could lose on a bad day", which on a finance site
// every reader takes as a drawdown or value-at-risk estimate in their own
// money. There is no such feature: /api/vol-forecast/record returns meanQlike,
// vsEwma, vsRandomWalk and a verdict, and carries no quantile, VaR or drawdown
// field anywhere. Two different meanings of one word, and the marketing one was
// winning on the page title.
//
// It is also not a per-symbol tool. It is the graded record of ONE
// pre-registered forecast against two baselines, and both horizons currently
// read INSUFFICIENT.
export const metadata = {
  title: "Volatility forecast: the live record",
  description:
    "How one pre-registered volatility forecast has scored against two simple baselines, under the loss function registered before any of it was measured. A record of forecast error, not a per-symbol risk estimate.",
};

type Horizon = {
  horizon: number;
  n: number;
  distinctDays: number;
  ungradable: number;
  sufficient: boolean;
  verdict: string;
  verdictReason: string;
  meanQlike: { har: number | null; rw: number | null; ewma: number | null };
  vsEwma: number | null;
  vsRandomWalk: number | null;
  lowerIsBetter: boolean;
};

type Record = {
  asOf: number;
  evidence: string;
  minDistinctDays: number;
  horizons: Horizon[];
  caveat: string;
};

async function loadRecord(): Promise<Record | null> {
  const daemon = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";
  try {
    // Bounded: a hung (not refused) daemon must not pin this server render forever.
    const res = await fetch(`${daemon}/api/vol-forecast/record`, { cache: "no-store", signal: AbortSignal.timeout(15_000) });
    if (!res.ok) return null;
    return (await res.json()) as Record;
  } catch {
    return null;
  }
}

const sessions = (h: number) => (h === 1 ? "1 session ahead" : `${h} sessions ahead`);

function Progress({ have, need }: { have: number; need: number }) {
  const pct = Math.min(100, need > 0 ? (have / need) * 100 : 0);
  return (
    <div className="flex flex-col gap-1">
      <div
        className="h-2 w-full overflow-hidden rounded"
        style={{ background: "var(--panel2)", border: "1px solid var(--border)" }}
      >
        <div
          className="h-full"
          style={{ width: `${pct}%`, background: "var(--accent)" }}
          role="presentation"
        />
      </div>
      <div className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {have.toLocaleString()} of {need.toLocaleString()} trading days
      </div>
    </div>
  );
}

export default async function RiskPage() {
  const rec = await loadRecord();

  return (
    <div className="flex flex-col gap-10 pb-16 pt-6">
      <header className="flex flex-col gap-4">
        <div
          className="mono text-[0.7rem] uppercase tracking-[0.2em]"
          style={{ color: "var(--accent)" }}
        >
          Volatility record
        </div>
        <h1 className="m-0 max-w-[22ch] text-[1.9rem] font-extrabold leading-tight sm:text-[2.4rem]">
          How much is this likely to move?
        </h1>
        <p className="m-0 max-w-[65ch] text-[0.95rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          Volatility is how much a price swings about, and unlike the direction of a price it
          is genuinely forecastable: calm weeks tend to be followed by calm weeks and violent
          ones by violent ones. This page reports our estimate of it, and — the part that
          matters — how that estimate has actually scored against two deliberately simple
          rules that anyone could run instead.
        </p>
        <p className="m-0 max-w-[65ch] text-[0.95rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          The two rules are &ldquo;tomorrow will be like today&rdquo; and RiskMetrics, an
          industry-standard weighted average. Beating them is the only thing that would count
          as skill. <strong>Lower loss is better.</strong>
        </p>
      </header>

      {rec == null ? (
        <div
          className="panel px-4 py-3 text-sm"
          style={{ borderColor: "var(--border-strong)", color: "var(--dim)" }}
        >
          The live record is not readable right now, so no figures are shown. That is
          deliberate: this page never prints a number it could not source.
        </div>
      ) : (
        <>
          {/* The caveat is rendered verbatim, at the top, before any number, and is
              never collapsed. A caveat that appears only when something is broken
              teaches readers that its absence means "safe to trust". */}
          <div
            className="rounded border px-4 py-3 text-[0.85rem] leading-relaxed"
            style={{
              borderColor: "var(--warn)",
              background: "color-mix(in srgb, var(--warn) 10%, transparent)",
              color: "var(--warn)",
            }}
            data-testid="risk-caveat"
          >
            {rec.caveat}
          </div>

          <div className="flex flex-col gap-4">
            {rec.horizons.map((h) => {
              const insufficient = !h.sufficient;
              return (
                <section
                  key={h.horizon}
                  className="panel flex flex-col gap-4 px-4 py-4"
                  data-testid={`risk-horizon-${h.horizon}`}
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <h2 className="m-0 text-[1.1rem] font-semibold">{sessions(h.horizon)}</h2>
                    <span
                      className="mono rounded border px-2 py-1 text-[0.7rem] font-bold uppercase tracking-[0.12em]"
                      style={{
                        borderColor: insufficient ? "var(--warn)" : "var(--ok)",
                        color: insufficient ? "var(--warn)" : "var(--ok)",
                        background: insufficient
                          ? "color-mix(in srgb, var(--warn) 12%, transparent)"
                          : "color-mix(in srgb, var(--ok) 12%, transparent)",
                      }}
                    >
                      {h.verdict}
                    </span>
                  </div>

                  <p
                    className="m-0 max-w-[70ch] text-[0.85rem] leading-relaxed"
                    style={{ color: "var(--dim)" }}
                  >
                    {h.verdictReason}
                  </p>

                  <Progress have={h.distinctDays} need={rec.minDistinctDays} />

                  <div className="flex flex-wrap gap-x-8 gap-y-2 text-[0.78rem]">
                    <span style={{ color: "var(--faint)" }}>
                      resolved forecasts{" "}
                      <span className="tnum" style={{ color: "var(--dim)" }}>
                        {h.n.toLocaleString()}
                      </span>
                    </span>
                    <span style={{ color: "var(--faint)" }}>
                      ungradable{" "}
                      <span className="tnum" style={{ color: "var(--dim)" }}>
                        {h.ungradable.toLocaleString()}
                      </span>
                    </span>
                  </div>

                  {/* Rendered ONLY when the daemon says the evidence is sufficient.
                      Below the floor there is nothing here at all -- see the file
                      header for why a dash or a zero would be worse than nothing. */}
                  {h.sufficient && h.meanQlike.har != null && (
                    <div className="flex flex-col gap-3 border-t pt-3" style={{ borderColor: "var(--border)" }}>
                      <div className="table-wrap">
                        <table className="w-full text-sm">
                          <thead>
                            <tr className="text-left" style={{ color: "var(--faint)" }}>
                              <th className="py-1 pr-4 font-medium">Method</th>
                              <th className="py-1 text-right font-medium">Mean loss</th>
                            </tr>
                          </thead>
                          <tbody>
                            {[
                              ["Our forecast", h.meanQlike.har],
                              ["RiskMetrics EWMA", h.meanQlike.ewma],
                              ["Tomorrow is like today", h.meanQlike.rw],
                            ].map(([label, v]) => (
                              <tr key={String(label)} style={{ borderTop: "1px solid var(--border)" }}>
                                <td className="py-1 pr-4">{label}</td>
                                <td className="tnum py-1 text-right">
                                  {typeof v === "number" ? v.toFixed(4) : "—"}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                      <p className="m-0 text-[0.78rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                        Difference against RiskMetrics:{" "}
                        <span className="tnum" style={{ color: "var(--text)" }}>
                          {h.vsEwma != null ? h.vsEwma.toFixed(4) : "—"}
                        </span>
                        . A <strong>negative</strong> number means our forecast had the lower
                        loss. It is a record, not a verdict.
                      </p>
                    </div>
                  )}
                </section>
              );
            })}
          </div>

          <p className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            A verdict about skill can only come from the pre-registered grader, never from
            this page. The claim, the nulls, the loss function and the minimum evidence were
            all written down and hash-chained before any of these forecasts existed, so the
            goalposts cannot move once the numbers arrive. You can read that registration on{" "}
            <Link href="/proof" style={{ color: "var(--accent)" }}>
              the receipts page
            </Link>
            .
          </p>

          <footer
            className="border-t pt-5 text-[0.78rem] leading-relaxed"
            style={{ borderColor: "var(--border)", color: "var(--faint)" }}
          >
            SignalDeck measures and stores; it does not advise. Nothing here is financial
            advice or a recommendation to trade, and a volatility estimate says nothing about
            which direction a price will go.
          </footer>
        </>
      )}
    </div>
  );
}
