"use client";

import { useEffect, useState } from "react";
import { api, pollMs, POLL_SLOW, type SentCorrPayload, type SentCorrResult } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import ProOnly from "@/components/ProOnly";
import { PageHero, StatTile, Reveal, Spark, MiniBar, DeltaBadge } from "@/components/ui/Kit";

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
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  if (err) return <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />;
  if (!data) return <Skeleton lines={8} />;

  const studies = [...data.studies].sort((a, b) => a.result.horizon - b.result.horizon);
  const coverageStats = [
    { label: "Headlines Scored", value: data.coverage.newsRowsTotal, delta: undefined, spark: undefined },
    { label: "Expressed Polarity", value: data.coverage.newsRowsPolar, delta: undefined, spark: undefined },
    { label: "Symbol-Days Aligned", value: data.coverage.rows, delta: undefined, spark: undefined },
  ];

  return (
    <div className="page-enter space-y-4">
      <PageHero 
        title="SENTIMENT ANALYSIS" 
        subtitle="Does news text predict forward returns, or just describe them? This is the raw vs. partial correlation of headline sentiment." 
        right={fetchedAt > 0 && <span className="mono text-sm" style={{ color: 'var(--hud)' }}>Updated {ago(fetchedAt)}</span>}
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {coverageStats.map((stat, i) => (
          <StatTile key={stat.label} label={stat.label} value={stat.value} decimals={0} i={i} sub={stat.label === 'Expressed Polarity' ? 'Factual headlines excluded, not zero.' : undefined} />
        ))}
        <StatTile 
          label="Archive Span" 
          value={data.coverage.newsFirstDay ? `${data.coverage.newsFirstDay} → ${data.coverage.newsLastDay}` : "—"} 
          i={3} 
        />
      </div>

      <section className="panel p-4">
        <h2 className="panel-h mb-3">Data Coverage</h2>
        <p className="text-sm leading-relaxed" style={{ color: 'var(--dim)' }}>
          Sentiment is keyed to the first session on which each headline was already public, not the headline&rsquo;s own calendar day.
        </p>
        {data.alignment && data.alignment.caughtUp === false && (
          <div className="mt-3 p-3 rounded border text-sm leading-relaxed" style={{ borderColor: 'var(--line)', color: 'var(--dim)' }}>
            <b style={{ color: 'var(--accent)' }}>The study sees less than the archive holds.</b> Headlines are stored back to {data.alignment.archiveFirstDay || "—"}, but only sessions from {data.alignment.alignedFirstDay || "—"} onward have an aligned sentiment feature. The backwards backfill {data.alignment.backfillReachedDay ? `has reached ${data.alignment.backfillReachedDay}` : "has not run a pass yet"}. Every verdict below is measured on the aligned span only.
          </div>
        )}
      </section>

      {studies.length === 0 && (
        <section className="panel p-4 text-sm" style={{ color: 'var(--dim)' }}>
          The study has not run yet. The sentiment-corr-runner works on a 12-hour cadence and stores one verdict per horizon.
        </section>
      )}

      <Reveal className="space-y-4">
        {studies.map((s) => (
          <StudyCard key={`${s.feature}-${s.result.horizon}`} result={s.result} ranAt={s.ranAt} />
        ))}
      </Reveal>

      <ProOnly>
        <section className="panel p-4">
          <h2 className="panel-h mb-3">Methodology</h2>
          <dl className="grid gap-3 text-sm">
            <dt className="font-bold tracking-wide" style={{ color: 'var(--dim)' }}>How it is measured</dt>
            <dd className="leading-relaxed">{data.methodology}</dd>
            <dt className="font-bold tracking-wide" style={{ color: 'var(--dim)' }}>Why partial, not raw</dt>
            <dd className="leading-relaxed">{data.whyPartialNotRaw}</dd>
            <dt className="font-bold tracking-wide" style={{ color: 'var(--dim)' }}>Why the interval is clustered</dt>
            <dd className="leading-relaxed">{data.whyClusteredCI}</dd>
            <dt className="font-bold tracking-wide" style={{ color: 'var(--dim)' }}>How headlines are scored</dt>
            <dd className="leading-relaxed">{data.scoring}</dd>
            <dt className="font-bold tracking-wide" style={{ color: 'var(--dim)' }}>When a verdict is withheld</dt>
            <dd className="leading-relaxed">{data.gates}</dd>
            <dt className="font-bold tracking-wide" style={{ color: 'var(--dim)' }}>What was expected</dt>
            <dd className="leading-relaxed">{data.expectation}</dd>
          </dl>
        </section>
      </ProOnly>

      <div className="panel p-4 text-sm leading-relaxed" style={{ color: 'var(--dim)' }}>
        {data.caveat}
      </div>
    </div>
  );
}

function StudyCard({ result: r, ranAt }: { result: SentCorrResult; ranAt: number }) {
  const significant = r.partialICLo != null && r.partialICHi != null && ((r.partialICLo > 0 && r.partialICHi > 0) || (r.partialICLo < 0 && r.partialICHi < 0));
  const isGated = r.gated;
  const partialIC = r.partialIC ?? 0;
  const rawIC = r.rawIC ?? 0;
  const icSparkData = [partialIC, rawIC].filter(v => !isNaN(v));
  const quintileFwdReturns = r.quintiles.map(q => q.meanFwd);
  const maxFwd = Math.max(...quintileFwdReturns, 0.01);

  return (
    <div className="panel reveal-item" style={{ "--i": Math.min(r.horizon, 12) } as React.CSSProperties}>
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <h2 className="panel-h">{r.horizon}-SESSION FORWARD RETURN</h2>
        {isGated ? (
          <span className="rounded px-2 py-0.5 text-xs font-bold" style={{ background: 'var(--panel3)', color: 'var(--dim)' }}>NO VERDICT</span>
        ) : (
          <span className="rounded px-2 py-0.5 text-xs font-bold" style={{ background: significant ? 'color-mix(in srgb, var(--bid) 10%, transparent)' : 'var(--panel3)', color: significant ? 'var(--bid)' : 'var(--dim)' }}>
            {significant ? "INTERVAL EXCLUDES ZERO" : "NO MEASURABLE EFFECT"}
          </span>
        )}
        <span className="ml-auto mono text-xs" style={{ color: 'var(--dim)' }}>
          {r.obs.toLocaleString()} obs · {r.symbols} symbols · {r.months} months · {ranAt > 0 && `ran ${ago(ranAt)}`}
        </span>
      </div>

      {isGated ? (
        <p className="text-sm leading-relaxed">{r.gateReason}</p>
      ) : (
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4 mb-4">
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Partial IC (THE Number)</div>
              <div className="text-xl font-bold tnum mt-1">{r.partialIC == null ? "withheld" : ic(r.partialIC)}</div>
              <div className="mt-1"><Spark data={icSparkData} width={100} height={28} /></div>
            </div>
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>CI ({r.ciLevel})</div>
              <div className="text-sm tnum mt-1">{r.partialICLo == null || r.partialICHi == null ? "withheld" : `[${ic(r.partialICLo)}, ${ic(r.partialICHi)}]`}</div>
            </div>
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Raw IC (Contaminated)</div>
              <div className="text-sm tnum mt-1">{r.rawIC == null ? "withheld" : ic(r.rawIC)}</div>
            </div>
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Price Explained Share</div>
              <div className="text-sm tnum mt-1">
                {r.priceExplainedShare == null ? "—" : r.priceExplainedShare >= 0 ? pct(r.priceExplainedShare) : "None"}
              </div>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4 mb-4">
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Sign Hit Rate</div>
              <div className="text-sm tnum mt-1">{r.hitRate == null ? "—" : pct(r.hitRate)}</div>
            </div>
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Base Rate (Best Constant Call)</div>
              <div className="text-sm tnum mt-1">{r.baseRate == null ? "—" : pct(r.baseRate)}</div>
            </div>
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Edge vs Constant</div>
              <div className="text-sm tnum mt-1">{r.edgeVsConstant == null ? "—" : pct(r.edgeVsConstant)}</div>
              {r.edgeVsConstant != null && <DeltaBadge value={r.edgeVsConstant} className="mt-1" />}
            </div>
            <div className="panel p-3">
              <div className="text-xs tracking-wide" style={{ color: 'var(--dim)' }}>Aligned Spread, Net</div>
              <div className="text-sm tnum mt-1">{r.spreadAlignedNet == null ? "—" : pct(r.spreadAlignedNet)}</div>
            </div>
          </div>

          {r.quintiles.length > 0 && (
            <div className="mb-4 overflow-x-auto">
              <table className="w-full min-w-[24rem] text-xs v4-table">
                <thead>
                  <tr style={{ color: 'var(--dim)' }}>
                    <th className="text-left font-normal">Quintile</th>
                    <th className="text-right font-normal">N</th>
                    <th className="text-right font-normal">Mean Sentiment</th>
                    <th className="text-right font-normal">Mean Forward Return</th>
                    <th className="text-right font-normal w-32">Return Magnitude</th>
                  </tr>
                </thead>
                <tbody>
                  {r.quintiles.map((q) => (
                    <tr key={q.quintile}>
                      <td className="py-1 mono">Q{q.quintile}{q.quintile === 1 && " (neg)"}{q.quintile === 5 && " (pos)"}</td>
                      <td className="py-1 text-right tnum">{q.n.toLocaleString()}</td>
                      <td className="py-1 text-right tnum">{q.meanSent.toFixed(3)}</td>
                      <td className="py-1 text-right tnum">{pct(q.meanFwd)}</td>
                      <td className="py-1 text-right">
                        <MiniBar value={q.meanFwd} max={maxFwd} color={q.meanFwd >= 0 ? "var(--bid)" : "var(--ask)"} height={8} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {r.monotonic === false && (
                <p className="mt-1 text-xs" style={{ color: 'var(--dim)' }}>
                  Quintile means are NOT ordered — any spread rests on extreme buckets.
                </p>
              )}
            </div>
          )}

          {r.alignedSide && (
            <p className="mb-2 text-xs" style={{ color: 'var(--dim)' }}>Aligned book: {r.alignedSide}.</p>
          )}
        </>
      )}

      <p className="text-sm leading-relaxed border-t pt-3" style={{ borderColor: 'var(--line)', color: 'var(--dim)' }}>
        {r.verdict}
      </p>
    </div>
  );
}
