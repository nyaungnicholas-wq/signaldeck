"use client";

import { useEffect, useState } from "react";
import { api, HORIZONS, pollMs, POLL_SLOW, type Honesty, type Horizon } from "@/lib/api";
import { ago } from "@/lib/format";
import HeroStats from "@/components/honesty/HeroStats";
import QuintileTable from "@/components/honesty/QuintileTable";
import ScatterPlot from "@/components/honesty/ScatterPlot";
import Explainer from "@/components/honesty/Explainer";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";
import { PageHero, StatTile } from "@/components/ui/Kit";

export default function HonestyPage() {
  const [horizon, setHorizon] = useState<Horizon>("1d");
  const [data, setData] = useState<Honesty | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [fetchedAt, setFetchedAt] = useState(0);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .honesty(horizon)
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
  }, [horizon, retryTick]);

  const current = data && data.horizon === horizon ? data : null;

  // Best/worst bucket by MEASURED forward return, not by position.
  //
  // These tiles used to read buckets[4] and buckets[0]. That array is ordered by
  // SCORE bucket, so those indices assume the score-ordered quintiles are
  // monotone in forward return - which is the exact hypothesis this page exists
  // to test, and which the live data refutes (the ordering is currently
  // inverted). Hard-coding it asserted the conclusion instead of showing it.
  // Labelling each tile with the bucket it actually came from makes an
  // inversion visible rather than silently relabelling it "Best".
  const gradedBuckets = (current?.buckets ?? []).filter(
    (b) => (b?.n ?? 0) > 0 && Number.isFinite(b?.meanFwd),
  );
  const bestBucket = gradedBuckets.reduce<(typeof gradedBuckets)[number] | null>(
    (acc, b) => (acc == null || b.meanFwd > acc.meanFwd ? b : acc),
    null,
  );
  const worstBucket = gradedBuckets.reduce<(typeof gradedBuckets)[number] | null>(
    (acc, b) => (acc == null || b.meanFwd < acc.meanFwd ? b : acc),
    null,
  );
  const loading = !current && !err;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="HONESTY"
        subtitle="How wrong have we been? Every score bucket graded against the returns that actually followed. The weak buckets stay on the page."
        right={
          <div className="flex flex-wrap items-center gap-2">
            {err && current && (
              <span className="chip" style={{ color: "var(--warn)" }}>
                poll failed — showing last data
              </span>
            )}
            {current && current.live !== true && (
              <span
                className="chip"
                style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                title={
                  current.trackLabel ??
                  "These figures are graded on backtested / in-sample resolutions, not a live forward track record."
                }
              >
                backtested — not live
              </span>
            )}
            {current && (
              <span className="flex items-center gap-1.5">
                <span className="chip tnum">
                  {(current.independentN ?? current.n ?? 0).toLocaleString("en-US")} independent
                </span>
                <HelpTip label="What counts as independent?">
                  {(current.rawN ?? current.independentN ?? current.n) !==
                  (current.independentN ?? current.n)
                    ? `${(current.rawN ?? 0).toLocaleString("en-US")} raw minute-cadence rows collapse to ${(current.independentN ?? current.n).toLocaleString("en-US")} independent (symbol, trading day) resolutions — pooling rows that resolve against the same move would overstate confidence.`
                    : "One observation per (symbol, trading day) resolution — pooling rows that resolve against the same move would overstate confidence."}
                </HelpTip>
              </span>
            )}
            <span className="chip tnum">
              {loading ? (
                <span style={{ color: "var(--faint)" }}>loading…</span>
              ) : fetchedAt ? (
                `updated ${ago(fetchedAt)}`
              ) : (
                "—"
              )}
            </span>
          </div>
        }
      />
      <div className="flex items-center gap-1">
        {HORIZONS.map((h) => {
          const active = h === horizon;
          return (
            <button
              key={h}
              type="button"
              onClick={() => setHorizon(h)}
              aria-pressed={active}
              className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
              title={`Grade scores against realized ${h} returns`}
              style={
                active
                  ? { color: "var(--accent)", borderColor: "var(--accent)" }
                  : undefined
              }
            >
              {h}
            </button>
          );
        })}
      </div>

      {err && !current && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Start signaldeckd and this page will pick it up."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {loading && <Skeleton lines={5} label="loading honesty report" />}

      {current && (current.n ?? 0) === 0 && (
        <EmptyState
          message={`No resolved scores for the ${horizon} horizon yet.`}
          detail="Scores need time to mature before they can be graded — check back after the horizon has elapsed."
        />
      )}

      {current && (current.n ?? 0) > 0 && (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatTile
              label="Total Graded"
              value={current.n ?? 0}
              glow="hud"
              i={0}
            />
            <StatTile
              label="Independent"
              value={current.independentN ?? current.n ?? 0}
              glow="hud"
              i={1}
            />
            <StatTile
              label={bestBucket ? `Best bucket - ${bestBucket.label}` : "Best bucket"}
              // A short or empty buckets array rendered "0.00%" - a fabricated
              // forward return, on the page whose entire purpose is not
              // fabricating them.
              //
              // x100: meanFwd is a 0-1 FRACTION here and StatTile's suffix does
              // no conversion, so the raw value rendered "0.01%" while
              // QuintileTable rendered the SAME number as "+1.31%" a few hundred
              // pixels below. Same convention as QuintileTable and HeroStats.
              value={bestBucket ? bestBucket.meanFwd * 100 : undefined}
              decimals={2}
              suffix="%"
              glow="up"
              i={2}
            />
            <StatTile
              label={worstBucket ? `Worst bucket - ${worstBucket.label}` : "Worst bucket"}
              value={worstBucket ? worstBucket.meanFwd * 100 : undefined}
              decimals={2}
              suffix="%"
              glow="down"
              i={3}
            />
          </div>
          <div className="hud-panel">
            <div className="panel-h">HeroStats</div>
            <HeroStats data={current} horizon={horizon} />
          </div>
          <ProOnly summary="Show the quintile table & scatter">
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              <div className="panel">
                <div className="panel-h">Quintile Table</div>
                <QuintileTable buckets={current.buckets ?? []} />
              </div>
              <div className="panel">
                <div className="panel-h">Scatter Plot</div>
                <ScatterPlot points={current.points ?? []} />
              </div>
            </div>
          </ProOnly>
        </>
      )}
      <div className="panel">
        <div className="panel-h">Explainer</div>
        <Explainer horizon={horizon} />
      </div>
    </div>
  );
}
