import { Fragment } from "react";
import Link from "next/link";
import WaitlistForm from "@/components/landing/WaitlistForm";
import RefusalNotice from "@/components/RefusalNotice";

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
 * 2. ONE PUBLICATION DECISION. Every figure on this page comes from the
 *    daemon's GET /api/accuracy at request time -- the same verdict path
 *    /accuracy renders and the generated documents follow. This page used to
 *    read the registry JSON file straight off disk, which is how it came
 *    to print a red "publication refused" banner ABOVE a table of the refused
 *    figures (2026-09-07): the file on disk had lost its status field to an
 *    evening regrade, and the page had its own private idea of what "refused"
 *    meant. It has none now. Refused means no numbers; the daemon says which.
 */
export const dynamic = "force-dynamic";

export const metadata = {
  title: "SignalDeck - a market instrument that grades itself in public",
  description:
    "Volatility and loss estimates for US equities. Every claim is pre-registered before the outcome and graded in public, including the ones that failed.",
};

const DAEMON = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";

type Row = {
  predictor: string;
  horizon: string;
  variant: string;
  family: string;
  publication_status: string;
  retired: boolean;
  retirement_sticky: boolean;
  retirement_source?: string;
  reasons: string[];
  evidence_refs: string[];
  live_n: number;
  live_acc: number | null;
  null_acc: number | null;
  skill: number | null;
  effective_n: number | null;
  distinct_days: number | null;
  ci_method?: string;
  note?: string;
};

type Live =
  | { kind: "ok"; gradedAt: string; rows: Row[] }
  | { kind: "refused"; status: string; reason: string; gradedAt?: string; refusedSince?: string }
  | { kind: "private" }
  | { kind: "unreachable"; detail: string };

type Envelope = {
  status?: string;
  reason?: string;
  graded_at?: string;
  refused_since?: string;
  rows?: Row[];
  error?: string;
};

async function loadLive(): Promise<Live> {
  let res: Response;
  try {
    res = await fetch(`${DAEMON}/api/accuracy`, {
      cache: "no-store",
      headers: { Accept: "application/json" },
      // Bounded: a hung daemon must not pin this server render forever.
      signal: AbortSignal.timeout(15_000),
    });
  } catch (e) {
    return { kind: "unreachable", detail: e instanceof Error ? e.message : String(e) };
  }
  if (res.status === 401 || res.status === 403) return { kind: "private" };
  let body: Envelope | null = null;
  try {
    body = (await res.json()) as Envelope;
  } catch {
    body = null;
  }
  if (res.ok && body?.status === "OK") {
    return { kind: "ok", gradedAt: body.graded_at ?? "", rows: body.rows ?? [] };
  }
  if (body?.status) {
    return {
      kind: "refused",
      status: body.status,
      reason: body.reason ?? "the daemon gave no reason",
      gradedAt: body.graded_at,
      refusedSince: body.refused_since,
    };
  }
  return { kind: "unreachable", detail: `HTTP ${res.status}` };
}

const pct = (x: number | null | undefined, dec = 1) =>
  x == null ? "—" : `${(x * 100).toFixed(dec)}%`;

const pp = (x: number | null | undefined) =>
  x == null ? "—" : `${x >= 0 ? "+" : "−"}${Math.abs(x * 100).toFixed(1)}pp`;

const rowLabel = (r: Row) =>
  r.predictor + (r.horizon ? ` (${r.horizon}${r.variant ? `, ${r.variant}` : ""})` : "");

const CONDEMNED = new Set(["FAILED", "RETIRED"]);

const statusTone = (s: string) =>
  CONDEMNED.has(s) ? "var(--bad)" : s === "OK" ? "var(--ok)" : "var(--warn)";

const REFUSALS: ReadonlyArray<readonly [string, string]> = [
  [
    "It will not tell you what to buy.",
    "There is no order path, no portfolio, no execution and no position sizing anywhere on this site. It is a measuring instrument, not a broker and not an adviser.",
  ],
  [
    "It will not publish a number it cannot stand behind.",
    "When the grading window is degenerate, the tables are removed rather than reprinted. If the notice above is red, you are looking at exactly that.",
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

function StillTrue() {
  return (
    <div className="flex flex-col gap-2 pt-1">
      <div className="text-[0.8rem] font-semibold">What is still true while figures are withheld</div>
      <ul className="m-0 flex list-disc flex-col gap-1 pl-5 text-[0.8rem] leading-relaxed">
        <li>
          The flagship directional model was retired on 2026-07-24 by a pre-registered rule, and
          retirement does not lapse.{" "}
          <Link href="/accuracy" style={{ color: "var(--accent)" }}>
            The registry
          </Link>
        </li>
        <li>
          Every claim is hash-chained before its outcome exists; the chain recomputes on the
          receipts page.{" "}
          <Link href="/proof" style={{ color: "var(--accent)" }}>
            The receipts
          </Link>
        </li>
        <li>
          The volatility record keeps accruing and publishes its verdict either way.{" "}
          <Link href="/volatility" style={{ color: "var(--accent)" }}>
            The risk estimates
          </Link>
        </li>
      </ul>
      <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
        A refusal over collapsed cross-sections does not clear by waiting: the graded window is
        anchored to the survivorship epoch and does not roll forward, so those days stay in it.
        Nothing has been deleted; the withheld grade stays in the registry as a dated record.
      </div>
    </div>
  );
}

function LiveRecord({ live }: { live: Live }) {
  if (live.kind === "refused") {
    return (
      <RefusalNotice
        status={live.status}
        reason={live.reason}
        gradedAt={live.gradedAt}
        refusedSince={live.refusedSince}
        testId="landing-refusal"
      >
        <StillTrue />
      </RefusalNotice>
    );
  }
  if (live.kind === "private") {
    return (
      <div className="panel flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
        <span style={{ color: "var(--dim)" }}>
          The accuracy record is private on this deployment — sign in to read it. This is an
          access setting, not a statistical refusal.
        </span>
        <Link href="/login" className="chip">
          Sign in
        </Link>
      </div>
    );
  }
  if (live.kind === "unreachable") {
    return (
      <div className="panel flex flex-col gap-1 px-4 py-3 text-sm" style={{ color: "var(--dim)" }}>
        <span>
          The grading service is unreachable right now, so no figures are shown. That is
          deliberate: this page never prints a number it cannot source.
        </span>
        <span className="mono text-[0.72rem]" style={{ color: "var(--faint)" }}>
          {live.detail}
        </span>
      </div>
    );
  }

  const rows = [...live.rows]
    .sort(
      (a, b) =>
        Number(CONDEMNED.has(b.publication_status)) - Number(CONDEMNED.has(a.publication_status)),
    )
    .slice(0, 8);
  const retired = live.rows.find((r) => r.retired);

  return (
    <>
      {retired && (
        <div className="panel flex flex-col gap-2 px-4 py-4">
          <div
            className="mono text-[0.7rem] uppercase tracking-[0.15em]"
            style={{ color: "var(--bad)" }}
          >
            Retired by its own rule
          </div>
          <div className="text-[1.05rem] font-semibold">{rowLabel(retired)}</div>
          <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
            <span className="tnum text-[1.9rem] font-extrabold" style={{ color: "var(--bad)" }}>
              {pp(retired.skill)}
            </span>
            <span className="text-sm" style={{ color: "var(--dim)" }}>
              <span className="tnum">{pct(retired.live_acc)}</span> correct against a{" "}
              <span className="tnum">{pct(retired.null_acc)}</span> baseline, over{" "}
              <span className="tnum">{retired.live_n.toLocaleString()}</span> forecasts on{" "}
              <span className="tnum">{retired.distinct_days ?? "—"}</span> days
            </span>
          </div>
          <p className="m-0 max-w-[70ch] text-sm leading-relaxed" style={{ color: "var(--dim)" }}>
            This model predicted direction worse than guessing the majority class. A
            pre-registered rule detected that and stopped it publishing automatically, without
            anyone having to decide to be honest that day. Retirement here does not lapse, and a
            later good week does not reverse it.
          </p>
        </div>
      )}

      {rows.length > 0 && (
        <div className="table-wrap">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left" style={{ color: "var(--faint)" }}>
                <th className="py-2 pr-4 font-medium">Predictor</th>
                <th className="py-2 pr-4 font-medium">Status</th>
                <th className="py-2 pr-4 text-right font-medium">n</th>
                <th className="py-2 pr-4 text-right font-medium">Effective n</th>
                <th className="py-2 pr-4 text-right font-medium">Days</th>
                <th className="py-2 pr-4 text-right font-medium">Accuracy</th>
                <th className="py-2 pr-4 text-right font-medium">Baseline</th>
                <th className="py-2 text-right font-medium">Skill</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => {
                const key = `${r.predictor}|${r.horizon}|${r.variant}`;
                return (
                  <Fragment key={key}>
                    <tr style={{ borderTop: "1px solid var(--border)" }}>
                      <td className="py-2 pr-4">{rowLabel(r)}</td>
                      <td className="py-2 pr-4">
                        <span
                          className="chip mono"
                          style={{
                            color: statusTone(r.publication_status),
                            borderColor: statusTone(r.publication_status),
                          }}
                        >
                          {r.publication_status}
                        </span>
                      </td>
                      <td className="tnum py-2 pr-4 text-right">{r.live_n.toLocaleString()}</td>
                      <td className="tnum py-2 pr-4 text-right">
                        {r.effective_n == null ? "—" : Math.round(r.effective_n).toLocaleString()}
                      </td>
                      <td className="tnum py-2 pr-4 text-right">{r.distinct_days ?? "—"}</td>
                      <td className="tnum py-2 pr-4 text-right">{pct(r.live_acc)}</td>
                      <td className="tnum py-2 pr-4 text-right" style={{ color: "var(--dim)" }}>
                        {pct(r.null_acc)}
                      </td>
                      <td
                        className="tnum py-2 text-right font-semibold"
                        style={{ color: (r.skill ?? 0) < 0 ? "var(--bad)" : "var(--dim)" }}
                      >
                        {pp(r.skill)}
                      </td>
                    </tr>
                    {r.reasons.length > 0 && (
                      <tr>
                        <td
                          colSpan={8}
                          className="pb-2 pr-4 text-[0.75rem] leading-relaxed"
                          style={{ color: "var(--dim)" }}
                        >
                          {r.reasons.join(" · ")}
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <p className="m-0 max-w-[70ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        &ldquo;Baseline&rdquo; is what you would score by always calling the majority direction.
        Beating it is the only thing that counts as skill, and on this record nothing has. Status
        is the daemon&rsquo;s publication verdict for the grade of{" "}
        <span className="tnum">{live.gradedAt || "unknown"}</span>: what the record supports, not
        a stored sentence. Full methodology and every row:{" "}
        <Link href="/accuracy" style={{ color: "var(--accent)" }}>
          /accuracy
        </Link>
        .
      </p>
    </>
  );
}

export default async function Landing() {
  const live = await loadLive();

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
        <LiveRecord live={live} />
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
    </div>
  );
}
