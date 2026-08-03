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
  const loading = !current && !err;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="HONESTY"
        subtitle="Were past scores any good? Score buckets graded against the returns that actually followed."
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
                    ? `${(current.rawN ?? 0).toLocaleString("en-US")} raw minute-cadence rows collapse to ${(current.independentN ?? current.n).toLocaleString("en-US")} independent (symbol, UTC-day) resolutions — pooling rows that resolve against the same move would overstate confidence.`
                    : "One observation per (symbol, UTC-day) resolution — pooling rows that resolve against the same move would overstate confidence."}
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
              label="Best Quintile"
              value={current.buckets?.[4]?.meanFwd ?? 0}
              decimals={2}
              suffix="%"
              glow="up"
              i={2}
            />
            <StatTile
              label="Worst Quintile"
              value={current.buckets?.[0]?.meanFwd ?? 0}
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
