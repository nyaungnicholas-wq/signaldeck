"use client";

import { useEffect, useState } from "react";
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
import HelpTip from "@/components/HelpTip";
import CellBar from "@/components/viz/CellBar";
import { PageHero, StatTile } from "@/components/ui/Kit";

function retColor(pct: number): string {
  if (!isFinite(pct)) return "var(--dim)";
  if (pct > 0.05) return "var(--bid)";
  if (pct < -0.05) return "var(--ask)";
  return "var(--dim)";
}
function retDir(pct: number): string {
  if (!isFinite(pct)) return "n/a";
  if (pct > 0.05) return "up";
  if (pct < -0.05) return "down";
  return "flat";
}

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
      <table className="v4-table w-full text-left text-[0.75rem]">
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

  const hr = result?.HitRate ?? 0;
  const hrPct = Math.round(hr * 100);
  const hits = Math.round(hr * analogs.length);
  const balanced = Math.abs(hr - 0.5) <= 0.15;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="MARKET MEMORY"
        subtitle="The past days whose market state most resembles today — and the return that followed them."
        right={
          data !== null && (
            <div className="flex items-center gap-2">
              <span className="mono chip">{proxy}</span>
              {!gated && (
                <>
                  <span className="tnum chip">{analogs.length} analogs</span>
                  <span className="tnum chip">{fwd}d forward</span>
                </>
              )}
              {err !== null && (
                <span className="chip" style={{ color: "var(--ask)", borderColor: "var(--ask)" }}>
                  poll failed — showing last data
                </span>
              )}
            </div>
          )
        }
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
          <section className="hud-panel">
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
                sub={today ? `${proxy} closed ${retDir(today.ret1 * 100)} on the day` : undefined}
                i={0}
              />
              <StatTile
                label="5-DAY REALIZED VOL"
                value={today ? fmtPct(today.vol5 * 100, false) : "—"}
                sub="daily σ of recent returns"
                i={1}
              />
              <StatTile
                label="20-DAY MOMENTUM"
                value={today ? fmtPct(today.mom20 * 100) : "—"}
                sub={today ? `${retDir(today.mom20 * 100)} over ~20 sessions` : undefined}
                i={2}
              />
            </div>
          </section>

          {result && analogs.length > 0 ? (
            <>
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
                    sub={`analogs went ${retDir(result.MeanFwd)} on average`}
                    i={0}
                  />
                  <StatTile
                    label={`MEDIAN FORWARD (${fwd}D)`}
                    value={fmtPct(result.MedianFwd)}
                    sub={`the typical analog went ${retDir(result.MedianFwd)}`}
                    i={1}
                  />
                  <StatTile
                    label="HIT RATE"
                    value={`${hrPct}%`}
                    sub={`${hits} of ${analogs.length} rose over ${fwd}d`}
                    i={2}
                  />
                  <StatTile
                    label="ANALOGS"
                    value={String(analogs.length)}
                    sub={`nearest of ${result.N} days on record`}
                    i={3}
                  />
                </div>
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
                        style={{ color: hr > 0.5 ? "var(--bid)" : "var(--ask)" }}
                      >
                        {hrPct}%
                      </span>{" "}
                      rose over the next {fwd} days. That is a tendency in similar past days — still
                      descriptive, not a forecast for this one.
                    </>
                  )}
                </div>
              </section>

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
