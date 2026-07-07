"use client";

import { useEffect, useState } from "react";
import { api, HORIZONS, pollMs, type Honesty, type Horizon } from "@/lib/api";
import { ago } from "@/lib/format";
import HeroStats from "@/components/honesty/HeroStats";
import QuintileTable from "@/components/honesty/QuintileTable";
import ScatterPlot from "@/components/honesty/ScatterPlot";
import Explainer from "@/components/honesty/Explainer";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";

/** HONESTY — grades persisted scores against what the market actually did. */
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
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [horizon, retryTick]);

  // Only trust data that belongs to the selected horizon (payload is tagged),
  // so switching chips never shows a stale mix.
  const current = data && data.horizon === horizon ? data : null;
  const loading = !current && !err;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">HONESTY</h1>
        <span className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
          were the scores any good?
        </span>
        <div role="group" aria-label="Outcome horizon" className="flex items-center gap-1">
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
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {err && current && (
            <span className="chip" style={{ color: "var(--warn)" }}>
              poll failed — showing last data
            </span>
          )}
          {/* Phase 0 labeling: not a live track record until the gate clears. */}
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
            <span
              className="chip tnum"
              title={
                (current.rawN ?? current.independentN ?? current.n) !==
                (current.independentN ?? current.n)
                  ? `${(current.rawN ?? 0).toLocaleString("en-US")} raw minute-cadence rows collapse to ${(current.independentN ?? current.n).toLocaleString("en-US")} independent symbol-days`
                  : "independent (symbol, UTC-day) resolutions"
              }
            >
              {(current.independentN ?? current.n ?? 0).toLocaleString("en-US")} independent
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
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-honesty"
        text="Were past scores any good? Score buckets graded against the returns that actually followed — the page that argues against the product when the data says so."
      />

      {/* error state */}
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

      {/* loading state (no data for this horizon yet) */}
      {loading && <Skeleton lines={5} label="loading honesty report" />}

      {current && (current.n ?? 0) === 0 && (
        <EmptyState
          message={`No resolved scores for the ${horizon} horizon yet.`}
          detail="Scores need time to mature before they can be graded — check back after the horizon has elapsed."
        />
      )}

      {current && (current.n ?? 0) > 0 && (
        <>
          <HeroStats data={current} horizon={horizon} />
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <QuintileTable buckets={current.buckets ?? []} />
            <ScatterPlot points={current.points ?? []} />
          </div>
        </>
      )}

      <Explainer horizon={horizon} />
    </div>
  );
}
