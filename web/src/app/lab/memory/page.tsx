"use client";

// LAB › MEMORY — lived at /markets/memory until the 2026-08-02 merge folded
// the duplicate /markets tree away; the old URL still 307s here.
//
// MARKET MEMORY — the historical-analog surface. It asks a deliberately humble
// question: which past SPY days had a market STATE most like today's, and what
// return actually FOLLOWED them? The daemon (GET /api/market-memory) z-scores
// three features — 1d return, 5d realized vol, 20d momentum — finds the K
// nearest past days, and reports the mean/median forward return and hit rate
// across them. Everything here is descriptive of the past; the page never
// pretends the analogs are a forecast, and it says so out loud (and again
// whenever the hit rate lands near a coin flip).
//
// Self-contained like markets/trends: one "use client" page, local StatTile /
// AnalogsTable / GatedPanel helpers, shared design primitives only.

import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import {
  api,
  pollMs,
  POLL_SLOW,
  type Analog,
  type MarketMemoryResult,
} from "@/lib/api";
import { fmtPct, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import HelpTip from "@/components/HelpTip";
import CellBar from "@/components/viz/CellBar";

// Color/direction for an ALREADY-PERCENT return (e.g. +1.5 = +1.5%). The word
// carries the meaning so color is never the only channel; a hair either side of
// zero reads as flat rather than faking a lean out of rounding noise.
function retColor(pct: number): string {
  if (!isFinite(pct)) return "var(--dim)";
  if (pct > 0.05) return "var(--ok)";
  if (pct < -0.05) return "var(--bad)";
  return "var(--dim)";
}
function retDir(pct: number): string {
  if (!isFinite(pct)) return "n/a";
  if (pct > 0.05) return "up";
  if (pct < -0.05) return "down";
  return "flat";
}

/** Small mono stat tile — label, big number, optional caption and inline help. */
function StatTile({
  label,
  value,
  sub,
  color,
  help,
}: {
  label: string;
  value: string;
  sub?: string;
  color?: string;
  help?: ReactNode;
}) {
  return (
    <div
      className="rounded-lg border px-3 py-2.5"
      style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
    >
      <div className="flex items-center gap-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        <span>{label}</span>
        {help ? <HelpTip label={`about ${label.toLowerCase()}`}>{help}</HelpTip> : null}
      </div>
      <div className="tnum mt-1 text-xl font-bold" style={{ color: color ?? "var(--text)" }}>
        {value}
      </div>
      {sub ? (
        <div className="mt-0.5 text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
          {sub}
        </div>
      ) : null}
    </div>
  );
}

/** One row per analog, closest first. The Distance figure is exact; the bar
 *  behind it is a RELATIVE closeness aid (fuller = closer among the shown set),
 *  and the forward return is colored + signed. */
function AnalogsTable({ analogs, fwd }: { analogs: Analog[]; fwd: number }) {
  let min = Infinity;
  let max = 0;
  for (const a of analogs) {
    if (a.Distance < min) min = a.Distance;
    if (a.Distance > max) max = a.Distance;
  }
  const span = max - min;

  return (
    <div className="table-wrap">
      <table className="w-full text-left text-[0.75rem]">
        <thead>
          <tr className="uppercase tracking-wider" style={{ color: "var(--faint)" }}>
            <th className="px-4 py-2 font-medium">#</th>
            <th className="px-4 py-2 font-medium">Date</th>
            <th className="px-4 py-2 font-medium">
              <span className="inline-flex items-center gap-1">
                Distance
                <HelpTip label="what distance means">
                  Normalized (z-scored) euclidean distance between today&apos;s feature vector and
                  that past day&apos;s — smaller means more similar. The bar behind each number
                  shows closeness relative to the other analogs shown, not an absolute scale.
                </HelpTip>
              </span>
            </th>
            <th className="px-4 py-2 text-right font-medium">{fwd}-day forward</th>
          </tr>
        </thead>
        <tbody>
          {analogs.map((a, i) => {
            // closest → fullest bar; guard the all-equal case (span 0 → all full).
            const frac = span > 0 ? 1 - (a.Distance - min) / span : 1;
            return (
              <tr key={`${a.Ts}-${i}`} style={{ borderTop: "1px solid var(--border)" }}>
                <td className="tnum px-4 py-2" style={{ color: "var(--faint)" }}>
                  {i + 1}
                </td>
                <td className="tnum px-4 py-2" style={{ color: "var(--text)" }}>
                  {fmtDate(a.Ts)}
                </td>
                <td className="px-4 py-2">
                  <CellBar
                    frac={frac}
                    label={a.Distance.toFixed(2)}
                    color="var(--dim)"
                    align="left"
                    width={96}
                    title="z-scored distance in feature space — smaller is closer. Bar = closeness relative to the analogs shown."
                  />
                </td>
                <td
                  className="tnum px-4 py-2 text-right font-bold"
                  style={{ color: retColor(a.FwdReturn) }}
                >
                  {fmtPct(a.FwdReturn)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** Gated state: not enough usable history to match analogs. Shows the honest
 *  N/60 progress and the daemon's own note, verbatim. */
function GatedPanel({ data }: { data: MarketMemoryResult }) {
  const raw = data.result?.N ?? data.history;
  const n = raw ?? 0;
  const hasCount = raw !== undefined;
  const pct = Math.min(100, (n / 60) * 100);
  const note =
    data.result?.Note ?? data.note ?? "Not enough history yet to build analogs.";

  return (
    <section className="panel">
      <div className="panel-h">NOT ENOUGH HISTORY YET</div>
      <div className="px-4 py-5">
        {hasCount ? (
          <>
            <div className="tnum text-xl font-bold" style={{ color: "var(--warn)" }}>
              {n} / 60 days
            </div>
            <div
              role="meter"
              aria-valuemin={0}
              aria-valuemax={60}
              aria-valuenow={n}
              aria-label={`history progress: ${n} of 60 required days`}
              className="mt-2 h-3 overflow-hidden rounded-full border"
              style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
            >
              <div
                className="h-full transition-[width] duration-300"
                style={{ width: `${pct}%`, background: "var(--accent)" }}
              />
            </div>
          </>
        ) : null}
        <p className="mt-3 text-[0.75rem] italic leading-relaxed" style={{ color: "var(--dim)" }}>
          {note}
        </p>
        <p className="mt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Market Memory needs at least 60 past {data.proxy ?? "SPY"} days that already have a known{" "}
          {data.forwardHorizonDays ?? 20}-day forward outcome before it can match analogs. This
          fills in automatically as bar history accrues.
        </p>
      </div>
    </section>
  );
}

export default function MarketMemoryPage() {
  const [data, setData] = useState<MarketMemoryResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .marketMemory()
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // Analogs recompute from daily SPY bars — the state moves at most once a
    // day, so the slow reference tier is plenty (managed loop: hidden-tab
    // pause, failure backoff).
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const loading = data === null && err === null;
  const hardError = data === null && err !== null;

  const result = data?.result ?? null;
  const analogs = result?.Analogs ?? [];
  const gated = data ? data.gated === true || result?.Gated === true : false;
  const fwd = data?.forwardHorizonDays ?? 20;
  const proxy = data?.proxy ?? "SPY";
  const today = data?.today;

  // Hit-rate honesty: near 0.50 there is no directional edge — say so plainly
  // instead of dressing up a coin flip as a lean.
  const hr = result?.HitRate ?? 0;
  const hrPct = Math.round(hr * 100);
  const hits = Math.round(hr * analogs.length);
  const balanced = Math.abs(hr - 0.5) <= 0.15;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">MARKET MEMORY</h1>
        {data !== null && <span className="chip mono">{proxy}</span>}
        {data !== null && !gated && (
          <>
            <span className="chip tnum">{analogs.length} analogs</span>
            <span className="chip tnum">{fwd}d forward</span>
          </>
        )}
        {err !== null && data !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="markets-memory"
        text="The past days whose market state most resembles today — and the return that FOLLOWED them."
      />

      {loading && <Skeleton lines={5} label="loading market memory" />}

      {hardError && (
        <ErrorState
          message={err ?? "request failed"}
          hint="Is the daemon running? Start it with signaldeckd and this page will recover. SPY must be tracked — it is the market proxy."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {data !== null && gated && <GatedPanel data={data} />}

      {data !== null && !gated && (
        <>
          {/* TODAY — the market state we are matching against */}
          <section className="panel">
            <div className="panel-h">
              TODAY — {proxy} STATE
              <span
                className="text-[0.75rem] font-normal normal-case tracking-normal"
                style={{ color: "var(--faint)" }}
              >
                matched on {(data.features ?? ["1d return", "5d realized vol", "20d momentum"]).join(" · ")}
              </span>
            </div>
            <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-3">
              <StatTile
                label="1-DAY RETURN"
                value={today ? fmtPct(today.ret1 * 100) : "—"}
                color={today ? retColor(today.ret1 * 100) : undefined}
                sub={today ? `${proxy} closed ${retDir(today.ret1 * 100)} on the day` : undefined}
              />
              <StatTile
                label="5-DAY REALIZED VOL"
                value={today ? fmtPct(today.vol5 * 100, false) : "—"}
                sub="daily σ of recent returns"
                help="Sample standard deviation of the last few daily returns, shown as a daily percentage. Higher means a choppier tape. This is a magnitude, not a direction."
              />
              <StatTile
                label="20-DAY MOMENTUM"
                value={today ? fmtPct(today.mom20 * 100) : "—"}
                color={today ? retColor(today.mom20 * 100) : undefined}
                sub={today ? `${retDir(today.mom20 * 100)} over ~20 sessions` : undefined}
              />
            </div>
          </section>

          {result && analogs.length > 0 ? (
            <>
              {/* OUTLOOK — what came AFTER the analogs */}
              <section className="panel">
                <div className="panel-h">
                  OUTLOOK — WHAT FOLLOWED THE ANALOGS
                  <HelpTip label="how to read the outlook">
                    Across the {analogs.length} most similar past days, this is the return that came
                    over the NEXT {fwd} trading days — mean, median, and how often it was positive.
                    It describes what happened after similar states before; it is not a prediction
                    for today.
                  </HelpTip>
                </div>
                <div className="grid grid-cols-2 gap-3 p-4 lg:grid-cols-4">
                  <StatTile
                    label={`MEAN FORWARD (${fwd}D)`}
                    value={fmtPct(result.MeanFwd)}
                    color={retColor(result.MeanFwd)}
                    sub={`analogs went ${retDir(result.MeanFwd)} on average`}
                  />
                  <StatTile
                    label={`MEDIAN FORWARD (${fwd}D)`}
                    value={fmtPct(result.MedianFwd)}
                    color={retColor(result.MedianFwd)}
                    sub={`the typical analog went ${retDir(result.MedianFwd)}`}
                  />
                  <StatTile
                    label="HIT RATE"
                    value={`${hrPct}%`}
                    sub={`${hits} of ${analogs.length} rose over ${fwd}d`}
                  />
                  <StatTile
                    label="ANALOGS"
                    value={String(analogs.length)}
                    sub={`nearest of ${result.N} days on record`}
                  />
                </div>
                {/* honesty readout — a coin-flip hit rate is stated as "no signal" */}
                <div
                  className="border-t px-4 py-3 text-[0.75rem] leading-relaxed"
                  style={{ borderColor: "var(--border)", color: "var(--dim)" }}
                >
                  {balanced ? (
                    <>
                      <span className="font-bold" style={{ color: "var(--warn)" }}>
                        No strong signal.
                      </span>{" "}
                      {hrPct}% of these analogs rose over the next {fwd} days — close to a coin flip.
                      Read the mean and median above as scatter, not a directional lean.
                    </>
                  ) : (
                    <>
                      Of the {analogs.length} closest analogs,{" "}
                      <span
                        className="tnum font-bold"
                        style={{ color: hr > 0.5 ? "var(--ok)" : "var(--bad)" }}
                      >
                        {hrPct}%
                      </span>{" "}
                      rose over the next {fwd} days. That is a tendency in similar past days — still
                      descriptive, not a forecast for this one.
                    </>
                  )}
                </div>
              </section>

              {/* ANALOGS — the matched days themselves, closest first */}
              <section className="panel">
                <div className="panel-h">
                  ANALOGS — CLOSEST PAST {proxy} DAYS
                  <span
                    className="text-[0.75rem] font-normal normal-case tracking-normal"
                    style={{ color: "var(--faint)" }}
                  >
                    ranked by similarity to today
                  </span>
                </div>
                <AnalogsTable analogs={analogs} fwd={fwd} />
              </section>
            </>
          ) : (
            <EmptyState
              message="No analogs to show yet"
              detail="Today's state was captured, but no comparable past days were returned. This resolves as more bar history accrues."
            />
          )}

          {/* HONEST CAVEAT — always, verbatim from the daemon */}
          <section className="panel">
            <div className="panel-h">HONEST CAVEAT</div>
            <div className="px-4 py-4">
              <p className="text-[0.75rem] italic leading-relaxed" style={{ color: "var(--dim)" }}>
                {data.note ?? "Descriptive analogs, NOT a forecast. The future need not rhyme."}
              </p>
              {result?.Note ? (
                <p className="tnum mt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                  {result.Note}
                </p>
              ) : null}
            </div>
          </section>
        </>
      )}
    </div>
  );
}
