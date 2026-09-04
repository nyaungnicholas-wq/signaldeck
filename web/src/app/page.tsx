import fs from "node:fs/promises";
import path from "node:path";
import Link from "next/link";
import WaitlistForm from "@/components/landing/WaitlistForm";

/**
 * The front door.
 *
 * Until now `/` was the authenticated dashboard, so an anonymous visitor was
 * bounced to a sign-in form reading "SignalDeck is a private workspace" -- on
 * a deployment whose entire argument is that anyone should be able to check
 * its claims. The dashboard moved to /dashboard; this is what the public sees.
 *
 * Two rules govern everything below.
 *
 * 1. LEAD WITH THE FAILURES. The retired model, the refusal state and the
 *    no-skill verdicts are the product, not an embarrassment to bury under a
 *    feature list. Anyone can publish a forecast; almost nobody publishes the
 *    grade saying it did not work.
 *
 * 2. NEVER PRINT A NUMBER THIS FILE INVENTED. Every figure is read out of
 *    data/accuracy_registry.json at request time. If the registry is
 *    unreadable the page says so and shows nothing, exactly the way
 *    /api/accuracy is fail-closed. Hardcoding accuracy here is how three
 *    different stale figures ended up in circulation once already.
 */
export const dynamic = "force-dynamic";

export const metadata = {
  title: "SignalDeck - a market instrument that grades itself in public",
  description:
    "Volatility and loss estimates for US equities. Every claim is pre-registered before the outcome and graded in public, including the ones that failed.",
};

type Row = {
  predictor?: string;
  band?: string;
  live_n?: number | null;
  live_acc?: number | null;
  null_acc?: number | null;
  skill?: number | null;
  verdict?: string | null;
  retire?: boolean | null;
  distinct_days?: number | null;
};

type Registry = {
  status?: string;
  generated?: string;
  refusal_reason?: string;
  rows?: Row[];
  stale_last_registry?: { graded_at?: string; rows?: Row[] };
};

async function loadRegistry(): Promise<Registry | null> {
  // next start runs from web/; the repo-root fallback covers ad-hoc invocations
  // and the container, where /app/data is the volume.
  for (const p of [
    path.resolve(process.cwd(), "..", "data", "accuracy_registry.json"),
    path.resolve(process.cwd(), "data", "accuracy_registry.json"),
  ]) {
    try {
      return JSON.parse(await fs.readFile(p, "utf8")) as Registry;
    } catch {
      /* try the next location */
    }
  }
  return null;
}

const pct = (x: number | null | undefined, dec = 1) =>
  x == null ? "—" : `${(x * 100).toFixed(dec)}%`;

const pp = (x: number | null | undefined) =>
  x == null ? "—" : `${x >= 0 ? "+" : "−"}${Math.abs(x * 100).toFixed(1)}pp`;

/** Stored verdict strings carry a mojibake byte where an em dash belongs. */
const cleanVerdict = (v?: string | null) =>
  (v ?? "").replace(/�/g, "—").replace(/\s+/g, " ").trim();

const REFUSALS: ReadonlyArray<readonly [string, string]> = [
  [
    "It will not tell you what to buy.",
    "There is no order path, no portfolio, no execution and no position sizing anywhere on this site. It is a measuring instrument, not a broker and not an adviser.",
  ],
  [
    "It will not publish a number it cannot stand behind.",
    "When the grading window is degenerate the tables are removed rather than reprinted. If the banner above is red, you are looking at exactly that.",
  ],
  [
    "It will not claim a forecasting edge it has not measured.",
    "Every predictor this platform has tested for price direction has failed against its own null, and those results stay published at full size.",
  ],
  [
    "It will not move the goalposts.",
    "Claims are hash-chained before the outcome exists. Editing one appends a visible amendment; it cannot quietly replace the original.",
  ],
];

const CHECKS = [
  {
    href: "/accuracy",
    title: "The grades",
    body: "Every predictor with its sample size, its baseline, its interval and its verdict. Failures sorted first.",
  },
  {
    href: "/proof",
    title: "The receipts",
    body: "Recompute the hash chain in your browser. If one stored prediction had been edited, the chain breaks and the page says so.",
  },
  {
    href: "/volatility",
    title: "The risk estimates",
    body: "How volatile a stock is about to get, and the live record of how that estimate has actually scored against two simple rules.",
  },
  {
    href: "/glossary",
    title: "The terms",
    body: "Plain-English definitions for every statistical term used here, with no assumed background.",
  },
] as const;

function Section({
  eyebrow,
  title,
  children,
}: {
  eyebrow: string;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <div
          className="mono text-[0.7rem] uppercase tracking-[0.18em]"
          style={{ color: "var(--accent)" }}
        >
          {eyebrow}
        </div>
        <h2 className="m-0 text-[1.35rem] font-bold leading-tight sm:text-[1.6rem]">{title}</h2>
      </div>
      {children}
    </section>
  );
}

export default async function Landing() {
  const reg = await loadRegistry();
  const refused = reg ? reg.status !== "OK" : true;
  // When publication is refused the live rows are withheld and the last
  // successful grade is nested. Reading it is honest ONLY because the banner
  // states plainly that it is the last successful grade, not a current one.
  const rows: Row[] = (reg?.rows?.length ? reg.rows : reg?.stale_last_registry?.rows) ?? [];
  const gradedAt = reg?.stale_last_registry?.graded_at ?? reg?.generated ?? null;

  const headline = rows.filter((r) => r.band === "all" && r.live_n != null).slice(0, 4);
  const failed = rows.find((r) => r.retire === true);

  return (
    <div className="flex flex-col gap-12 pb-16">
      <header className="flex flex-col gap-5 pt-6">
        <div
          className="mono text-[0.7rem] uppercase tracking-[0.2em]"
          style={{ color: "var(--accent)" }}
        >
          Open research instrument
        </div>
        <h1 className="m-0 max-w-[20ch] text-[2rem] font-extrabold leading-[1.08] sm:text-[2.8rem]">
          A market instrument that grades itself in public.
        </h1>
        <p className="m-0 max-w-[62ch] text-[1rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          SignalDeck estimates how volatile a stock is about to get, and how much you could
          lose on a bad day. Every claim is written down <em>before</em> the outcome is known,
          hash-chained so it cannot be edited afterwards, and then graded against what
          actually happened.
        </p>
        <p className="m-0 max-w-[62ch] text-[1rem] font-semibold leading-relaxed">
          Including the claims that failed. Especially those.
        </p>
        <div className="flex flex-wrap gap-3 pt-1">
          <Link
            href="/accuracy"
            className="chip"
            style={{ borderColor: "var(--accent)", color: "var(--accent)", padding: "0.6rem 1rem" }}
          >
            See the grades
          </Link>
          <Link href="/volatility" className="chip" style={{ padding: "0.6rem 1rem" }}>
            The risk estimates
          </Link>
          <Link href="/proof" className="chip" style={{ padding: "0.6rem 1rem" }}>
            Verify the chain
          </Link>
        </div>
      </header>

      <Section eyebrow="The live record" title="What this platform has measured about itself">
        {reg == null ? (
          <div
            className="panel px-4 py-3 text-sm"
            style={{ borderColor: "var(--border-strong)", color: "var(--dim)" }}
          >
            The accuracy registry is not readable on this deployment, so no figures are shown.
            That is deliberate: this page never prints a number it cannot source.
          </div>
        ) : (
          <>
            {refused && (
              <div
                data-testid="landing-refusal"
                className="rounded border border-red-500/60 bg-red-500/10 px-4 py-3 text-sm text-red-300"
              >
                <div className="font-bold uppercase tracking-wide">Publication refused</div>
                <div className="mt-1 opacity-90">
                  The grader is currently withholding figures. The table below is the{" "}
                  <strong>last successful grade</strong>
                  {gradedAt ? ` (${gradedAt})` : ""}, not a current one. A number graded by code
                  that refused to run today is not a live number.
                </div>
              </div>
            )}

            {failed && (
              <div className="panel flex flex-col gap-2 px-4 py-4">
                <div
                  className="mono text-[0.7rem] uppercase tracking-[0.15em]"
                  style={{ color: "var(--bad)" }}
                >
                  Retired by its own rule
                </div>
                <div className="text-[1.05rem] font-semibold">{failed.predictor}</div>
                <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
                  <span className="tnum text-[1.9rem] font-extrabold" style={{ color: "var(--bad)" }}>
                    {pp(failed.skill)}
                  </span>
                  <span className="text-sm" style={{ color: "var(--dim)" }}>
                    <span className="tnum">{pct(failed.live_acc)}</span> correct against a{" "}
                    <span className="tnum">{pct(failed.null_acc)}</span> baseline, over{" "}
                    <span className="tnum">{failed.live_n?.toLocaleString()}</span> forecasts on{" "}
                    <span className="tnum">{failed.distinct_days}</span> days
                  </span>
                </div>
                <p
                  className="m-0 max-w-[70ch] text-sm leading-relaxed"
                  style={{ color: "var(--dim)" }}
                >
                  This model predicted direction worse than guessing the majority class. A
                  pre-registered rule detected that and stopped it publishing automatically,
                  without anyone having to decide to be honest that day. Retirement here does
                  not lapse, and a later good week does not reverse it.
                </p>
              </div>
            )}

            {headline.length > 0 && (
              <div className="table-wrap">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left" style={{ color: "var(--faint)" }}>
                      <th className="py-2 pr-4 font-medium">Predictor</th>
                      <th className="py-2 pr-4 text-right font-medium">n</th>
                      <th className="py-2 pr-4 text-right font-medium">Accuracy</th>
                      <th className="py-2 pr-4 text-right font-medium">Baseline</th>
                      <th className="py-2 pr-4 text-right font-medium">Skill</th>
                      <th className="py-2 font-medium">Verdict</th>
                    </tr>
                  </thead>
                  <tbody>
                    {headline.map((r) => (
                      <tr key={r.predictor} style={{ borderTop: "1px solid var(--border)" }}>
                        <td className="py-2 pr-4">{r.predictor}</td>
                        <td className="tnum py-2 pr-4 text-right">{r.live_n?.toLocaleString()}</td>
                        <td className="tnum py-2 pr-4 text-right">{pct(r.live_acc)}</td>
                        <td className="tnum py-2 pr-4 text-right" style={{ color: "var(--dim)" }}>
                          {pct(r.null_acc)}
                        </td>
                        <td
                          className="tnum py-2 pr-4 text-right font-semibold"
                          style={{ color: (r.skill ?? 0) < 0 ? "var(--bad)" : "var(--dim)" }}
                        >
                          {pp(r.skill)}
                        </td>
                        <td className="py-2 text-[0.8rem]" style={{ color: "var(--dim)" }}>
                          {cleanVerdict(r.verdict)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <p
              className="m-0 max-w-[70ch] text-[0.8rem] leading-relaxed"
              style={{ color: "var(--faint)" }}
            >
              &ldquo;Baseline&rdquo; is what you would score by always calling the majority
              direction. Beating it is the only thing that counts as skill, and on this record
              nothing has. Full methodology and every row:{" "}
              <Link href="/accuracy" style={{ color: "var(--accent)" }}>
                /accuracy
              </Link>
              .
            </p>
          </>
        )}
      </Section>

      <Section eyebrow="Constraints" title="What this refuses to do">
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {REFUSALS.map(([head, body]) => (
            <li key={head} className="panel flex flex-col gap-1 px-4 py-3">
              <div className="font-semibold">{head}</div>
              <div className="text-sm leading-relaxed" style={{ color: "var(--dim)" }}>
                {body}
              </div>
            </li>
          ))}
        </ul>
      </Section>

      <Section eyebrow="Verification" title="How you can check all of this yourself">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {CHECKS.map((c) => (
            <Link
              key={c.href}
              href={c.href}
              className="panel flex flex-col gap-2 px-4 py-4 no-underline"
              style={{ color: "inherit" }}
            >
              <div className="font-semibold" style={{ color: "var(--accent)" }}>
                {c.title}
              </div>
              <div className="text-sm leading-relaxed" style={{ color: "var(--dim)" }}>
                {c.body}
              </div>
            </Link>
          ))}
        </div>
      </Section>

      <Section eyebrow="Stay in touch" title="Get told when the risk forecasts go live">
        <p className="m-0 max-w-[62ch] text-sm leading-relaxed" style={{ color: "var(--dim)" }}>
          Two new estimates are being pre-registered: how volatile a stock is about to get, and
          how much you could lose on a bad day. Both will be graded in public from the day they
          start, with the verdict published either way. One email when that record is worth
          reading. Nothing else, ever.
        </p>
        <WaitlistForm />
      </Section>

      <footer
        className="border-t pt-6 text-[0.78rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        SignalDeck measures and stores; it does not advise. Nothing here is financial advice or
        a recommendation to trade. Figures are derived analytics computed from licensed market
        data, which is never redistributed.
      </footer>
    </div>
  );
}
