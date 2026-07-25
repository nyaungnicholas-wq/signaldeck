"use client";

// SENTIMENT — the platform's first test of a NON-price signal.
//
// The page is built around one distinction, because everything else here is
// decoration if it gets lost: the RAW correlation between headline sentiment and
// forward returns is contaminated (headlines are written about moves that already
// happened), and the PARTIAL correlation — after removing same-session and
// trailing price movement — is the number that would mean something. The layout
// puts the partial IC in the hero, shows the raw one beside it as a comparison,
// and labels the gap between them for what it is.
//
// Withheld metrics render as "withheld", never as 0.0000. A null here means the
// daemon could not support a number on this sample, which is a different
// statement from "the effect is zero" and the UI must not collapse the two.

import { useEffect, useState } from "react";
import { api, pollMs, POLL_SLOW, type SentCorrPayload, type SentCorrResult } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";

const pct = (v: number) => `${(v * 100).toFixed(2)}%`;
const ic = (v: number) => (v >= 0 ? "+" : "") + v.toFixed(4);

export default function SentimentPage() {
  const [data, setData] = useState<SentCorrPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [fetchedAt, setFetchedAt] = useState(0);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .sentimentCorrelation()
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
          setFetchedAt(Math.floor(Date.now() / 1000));
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // The study runs twice a day; polling faster would only re-render the same
    // numbers.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  if (err) return <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />;
  if (!data) return <Skeleton lines={8} />;

  const studies = [...data.studies].sort((a, b) => a.result.horizon - b.result.horizon);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">SENTIMENT</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          does news text predict returns, or just describe them?
        </span>
        {fetchedAt > 0 && (
          <span className="ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
            updated {ago(fetchedAt)}
          </span>
        )}
      </div>

      <PagePurpose
        id="lab-sentiment"
        text={
          "Every other signal on this platform is derived from price, which is why they all run into the same accuracy ceiling. " +
          "News text is genuinely different input, so it gets its own test. The catch is that headlines are written ABOUT moves " +
          "that already happened, so the raw correlation is not evidence of anything. The number that matters is the partial " +
          "correlation, measured after same-session and trailing price movement are removed. EXPERIMENTAL — nothing here feeds " +
          "any prediction, score or alert."
        }
      />

      {/* Coverage first: whether the data can answer the question at all. */}
      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2 className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]" style={{ color: "var(--faint)" }}>
          DATA COVERAGE
        </h2>
        <div className="grid grid-cols-2 gap-3 text-[0.8rem] sm:grid-cols-4">
          <Stat label="headlines scored" value={data.coverage.newsRowsTotal.toLocaleString()} />
          <Stat
            label="expressed polarity"
            value={`${data.coverage.newsRowsPolar.toLocaleString()}`}
            hint="A factual headline has NO sentiment — it is excluded, not counted as zero."
          />
          <Stat label="symbol-days aligned" value={data.coverage.rows.toLocaleString()} />
          <Stat
            label="archive span"
            value={
              data.coverage.newsFirstDay
                ? `${data.coverage.newsFirstDay} → ${data.coverage.newsLastDay}`
                : "—"
            }
          />
        </div>
        <p className="mt-2 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Sentiment is keyed to the first session on which each headline was already public, not to
          the headline&apos;s own calendar day — most financial news publishes outside market hours,
          and keying it to its own day would let the study act on information before it existed.
        </p>

        {/* The archive can run years deeper than the study. It did, silently,
            for as long as feature-building only covered a trailing window. */}
        {data.alignment && data.alignment.caughtUp === false && (
          <p
            className="mt-2 rounded border p-2 text-[0.72rem] leading-relaxed"
            style={{ borderColor: "var(--line)" }}
          >
            <b>The study sees less than the archive holds.</b> Headlines are stored back to{" "}
            {data.alignment.archiveFirstDay || "—"}, but only sessions from{" "}
            {data.alignment.alignedFirstDay || "—"} onward have an aligned sentiment feature. The
            backwards backfill{" "}
            {data.alignment.backfillReachedDay
              ? `has reached ${data.alignment.backfillReachedDay}`
              : "has not run a pass yet"}
            . Every verdict below is measured on the aligned span only — read it as a result about
            one era, not about the archive.
          </p>
        )}
      </section>

      {studies.length === 0 && (
        <section
          className="rounded border p-3 text-[0.8rem]"
          style={{ borderColor: "var(--line)", background: "var(--panel)" }}
        >
          The study has not run yet. The sentiment-corr-runner works on a 12-hour cadence and stores
          one verdict per horizon.
        </section>
      )}

      {studies.map((s) => (
        <StudyCard key={`${s.feature}-${s.result.horizon}`} result={s.result} ranAt={s.ranAt} />
      ))}

      <ProOnly>
        <section
          className="rounded border p-3"
          style={{ borderColor: "var(--line)", background: "var(--panel)" }}
        >
          <h2
            className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
            style={{ color: "var(--faint)" }}
          >
            METHOD
          </h2>
          <dl className="flex flex-col gap-2 text-[0.75rem] leading-relaxed">
            <Note term="How it is measured" text={data.methodology} />
            <Note term="Why partial, not raw" text={data.whyPartialNotRaw} />
            <Note term="Why the interval is clustered" text={data.whyClusteredCI} />
            <Note term="How headlines are scored" text={data.scoring} />
            <Note term="When a verdict is withheld" text={data.gates} />
            <Note term="What was expected" text={data.expectation} />
          </dl>
        </section>
      </ProOnly>

      <p
        className="rounded border p-3 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--line)", color: "var(--faint)" }}
      >
        {data.caveat}
      </p>
    </div>
  );
}

/** One horizon's verdict. */
function StudyCard({ result: r, ranAt }: { result: SentCorrResult; ranAt: number }) {
  const significant =
    r.partialICLo != null &&
    r.partialICHi != null &&
    ((r.partialICLo > 0 && r.partialICHi > 0) || (r.partialICLo < 0 && r.partialICHi < 0));

  return (
    <section
      className="rounded border p-3"
      style={{ borderColor: "var(--line)", background: "var(--panel)" }}
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h2 className="text-[0.8rem] font-bold tracking-[0.12em]">
          {r.horizon}-SESSION FORWARD RETURN
        </h2>
        {r.gated ? (
          <span
            className="rounded px-1.5 py-0.5 text-[0.65rem] font-bold"
            style={{ background: "var(--line)", color: "var(--faint)" }}
          >
            NO VERDICT
          </span>
        ) : (
          <span
            className="rounded px-1.5 py-0.5 text-[0.65rem] font-bold"
            style={{
              background: significant ? "var(--warn-bg, var(--line))" : "var(--line)",
              color: significant ? "var(--warn, var(--fg))" : "var(--faint)",
            }}
          >
            {significant ? "INTERVAL EXCLUDES ZERO" : "NO MEASURABLE EFFECT"}
          </span>
        )}
        <span className="ml-auto text-[0.68rem]" style={{ color: "var(--faint)" }}>
          {r.obs.toLocaleString()} independent obs · {r.symbols} symbols · {r.months} months
          {ranAt > 0 && ` · ran ${ago(ranAt)}`}
        </span>
      </div>

      {r.gated ? (
        <p className="text-[0.8rem] leading-relaxed">{r.gateReason}</p>
      ) : (
        <>
          <div className="mb-3 grid grid-cols-2 gap-3 text-[0.8rem] sm:grid-cols-4">
            <Stat
              label="partial IC"
              value={r.partialIC == null ? "withheld" : ic(r.partialIC)}
              strong
              hint="Sentiment vs forward return, after removing same-session and trailing price moves. THE number."
            />
            <Stat
              label={r.ciLevel + " CI"}
              value={
                r.partialICLo == null || r.partialICHi == null
                  ? "withheld"
                  : `[${ic(r.partialICLo)}, ${ic(r.partialICHi)}]`
              }
              hint="Bootstrap resampling whole calendar months, then widened for the number of horizons tested together."
            />
            <Stat
              label="raw IC (contaminated)"
              value={r.rawIC == null ? "withheld" : ic(r.rawIC)}
              hint="Shown only for comparison: this is the number that reads price rather than text."
            />
            <Stat
              label="was price"
              value={
                r.priceExplainedShare == null
                  ? "—"
                  : r.priceExplainedShare >= 0
                    ? pct(r.priceExplainedShare)
                    : "none — controls strengthened it"
              }
              hint="Share of the raw correlation that turned out to be price contamination."
            />
          </div>

          <div className="mb-3 grid grid-cols-2 gap-3 text-[0.8rem] sm:grid-cols-4">
            <Stat label="sign hit rate" value={r.hitRate == null ? "—" : pct(r.hitRate)} />
            <Stat
              label="best constant call"
              value={r.baseRate == null ? "—" : pct(r.baseRate)}
              hint="The honest null: always guessing the majority direction. Not 50%."
            />
            <Stat
              label="edge vs that"
              value={r.edgeVsConstant == null ? "—" : pct(r.edgeVsConstant)}
              strong
            />
            <Stat
              label="aligned spread, net"
              value={r.spreadAlignedNet == null ? "—" : pct(r.spreadAlignedNet)}
              hint="Return of the quintile book pointing the SAME way as the measured IC, after costs."
            />
          </div>

          {r.quintiles.length > 0 && (
            <div className="mb-3 overflow-x-auto">
              <table className="w-full min-w-[24rem] text-[0.75rem]">
                <thead>
                  <tr style={{ color: "var(--faint)" }}>
                    <th className="text-left font-normal">sentiment quintile</th>
                    <th className="text-right font-normal">n</th>
                    <th className="text-right font-normal">mean sentiment</th>
                    <th className="text-right font-normal">mean forward return</th>
                  </tr>
                </thead>
                <tbody>
                  {r.quintiles.map((q) => (
                    <tr key={q.quintile} style={{ borderTop: "1px solid var(--line)" }}>
                      <td className="py-1">
                        Q{q.quintile}
                        {q.quintile === 1 && " (most negative)"}
                        {q.quintile === 5 && " (most positive)"}
                      </td>
                      <td className="py-1 text-right">{q.n.toLocaleString()}</td>
                      <td className="py-1 text-right">{q.meanSent.toFixed(3)}</td>
                      <td className="py-1 text-right">{pct(q.meanFwd)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {r.monotonic === false && (
                <p className="mt-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  Quintile means are NOT ordered — any spread here rests on the extreme buckets
                  rather than on a consistent relationship.
                </p>
              )}
            </div>
          )}

          {r.alignedSide && (
            <p className="mb-2 text-[0.72rem]" style={{ color: "var(--faint)" }}>
              Aligned book: {r.alignedSide}.
            </p>
          )}
        </>
      )}

      <p className="text-[0.78rem] leading-relaxed" style={{ borderTop: "1px solid var(--line)", paddingTop: "0.5rem" }}>
        {r.verdict}
      </p>
    </section>
  );
}

function Stat({
  label,
  value,
  hint,
  strong,
}: {
  label: string;
  value: string;
  hint?: string;
  strong?: boolean;
}) {
  return (
    <div>
      <div className="flex items-center gap-1 text-[0.65rem] tracking-wide" style={{ color: "var(--faint)" }}>
        <span>{label}</span>
        {hint && <HelpTip label={label}>{hint}</HelpTip>}
      </div>
      <div className={strong ? "text-[0.95rem] font-bold" : "text-[0.85rem]"}>{value}</div>
    </div>
  );
}

function Note({ term, text }: { term: string; text: string }) {
  return (
    <div>
      <dt className="text-[0.68rem] font-bold tracking-wide" style={{ color: "var(--faint)" }}>
        {term}
      </dt>
      <dd>{text}</dd>
    </div>
  );
}
