"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, type Trends, type TrendsMover } from "@/lib/api";
import { ago, fmtPct, fmtScore, scoreColor } from "@/lib/format";
import ScoreGauge from "@/components/ScoreGauge";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import Gauge from "@/components/viz/Gauge";

function MoverRow({ m }: { m: TrendsMover }) {
  return (
    <li
      className="flex items-center gap-3 px-4 py-2.5 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <Link
        href={`/s/${m.market}/${encodeURIComponent(m.symbol)}`}
        className="w-24 shrink-0 cursor-pointer text-[0.8rem] font-bold transition-colors duration-150 hover:text-[var(--accent)]"
      >
        {m.symbol}
        <span className="ml-1.5 text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
          {m.market}
        </span>
      </Link>
      <div className="min-w-0 flex-1">
        <ScoreGauge score={m.score} compact label={`${m.symbol} pressure score`} />
      </div>
      <span
        className="tnum w-14 shrink-0 text-right text-[0.8rem]"
        style={{ color: scoreColor(m.score) }}
      >
        {fmtScore(m.score)}
      </span>
      <span
        className="tnum w-16 shrink-0 text-right text-[0.8rem]"
        style={{ color: scoreColor(m.dayChangePct) }}
      >
        {fmtPct(m.dayChangePct)}
      </span>
    </li>
  );
}

function MoversPanel({ title, movers }: { title: string; movers: TrendsMover[] }) {
  return (
    <section className="panel">
      <div className="panel-h">{title}</div>
      {movers.length === 0 ? (
        <EmptyState
          message="No scored symbols yet"
          detail="Scores appear once enough bars are recorded."
        />
      ) : (
        <ul>
          {movers.map((m) => (
            <MoverRow key={`${m.market}:${m.symbol}`} m={m} />
          ))}
        </ul>
      )}
    </section>
  );
}

export default function TrendsPage() {
  const [trends, setTrends] = useState<Trends | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .trends()
        .then((t) => {
          if (!alive) return;
          setTrends(t);
          setErr(null);
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
  }, [retryTick]);

  const { top, bottom } = useMemo(() => {
    const movers = trends?.movers ?? [];
    const sorted = [...movers]
      .filter((m) => isFinite(m.score))
      .sort((a, b) => b.score - a.score);
    return {
      top: sorted.slice(0, 5),
      bottom: [...sorted].reverse().slice(0, 5),
    };
  }, [trends]);

  const loading = trends === null && err === null;
  const hardError = trends === null && err !== null;

  const scored = trends?.scored ?? 0;
  const positive = Math.min(trends?.positive1d ?? 0, scored);
  const negative = Math.max(0, scored - positive);
  const posPct = scored > 0 ? (positive / scored) * 100 : 0;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">TRENDS</h1>
        {trends !== null && (
          <>
            <span className="chip tnum">tracked {trends.tracked}</span>
            <span className="chip tnum">scored {trends.scored}</span>
            <span className="chip tnum">
              <span style={{ color: positive > negative ? "var(--bid)" : undefined }}>
                {positive} of {scored} positive
              </span>{" "}
              on 1d
            </span>
          </>
        )}
        {err !== null && trends !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {loading && <Skeleton lines={4} label="loading trends" />}

      {hardError && (
        <ErrorState
          message={err ?? "request failed"}
          hint="Is the daemon running? Start it with signaldeckd and this page will recover."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {trends !== null && (
        <>
          {/* breadth bar */}
          <section className="panel">
            <div className="panel-h">
              1D BREADTH
              <span className="tnum" style={{ color: "var(--faint)" }}>
                share of scored symbols with positive 1d pressure
              </span>
            </div>
            <div className="flex flex-wrap items-center gap-x-8 gap-y-4 px-4 py-4">
              {/* Stage 5: breadth dial — same number as the split bar, at a
                  glance; the caption carries the honest n. */}
              <Gauge
                label="1D BREADTH"
                value={posPct}
                min={0}
                max={100}
                hasData={scored > 0}
                caption={
                  scored > 0
                    ? `${positive} of ${scored} scored symbols positive on 1d — descriptive, from stored scores`
                    : "nothing scored yet — the scorer agent fills this in as bar history accrues"
                }
                format={(v) => `${v.toFixed(0)}%`}
                zones={[
                  { from: 0, to: 45, color: "var(--ask)" },
                  { from: 45, to: 55, color: "var(--dim)" },
                  { from: 55, to: 100, color: "var(--bid)" },
                ]}
              />
              <div className="min-w-56 flex-1">
              {scored === 0 ? (
                <div className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  nothing scored yet — the scorer agent fills this in as bar history accrues.
                </div>
              ) : (
                <>
                  <div
                    role="meter"
                    aria-valuemin={0}
                    aria-valuemax={scored}
                    aria-valuenow={positive}
                    aria-label={`market breadth: ${positive} of ${scored} scored symbols positive on 1d`}
                    className="flex h-4 overflow-hidden rounded-full border"
                    style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
                  >
                    <div
                      className="transition-[width] duration-300"
                      style={{ width: `${posPct}%`, background: "var(--bid)" }}
                    />
                    <div
                      className="transition-[width] duration-300"
                      style={{ width: `${100 - posPct}%`, background: "var(--ask)" }}
                    />
                  </div>
                  <div className="tnum mt-2 flex justify-between text-[0.78rem]">
                    <span style={{ color: "var(--bid)" }}>
                      {positive} positive ({posPct.toFixed(0)}%)
                    </span>
                    <span style={{ color: "var(--ask)" }}>
                      {negative} flat / negative ({(100 - posPct).toFixed(0)}%)
                    </span>
                  </div>
                </>
              )}
              </div>
            </div>
          </section>

          {/* movers */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <MoversPanel title="TOP MOVERS — HIGHEST SCORE" movers={top} />
            <MoversPanel title="BOTTOM MOVERS — LOWEST SCORE" movers={bottom} />
          </div>

          {/* market insight */}
          <section className="panel">
            <div className="panel-h">
              MARKET INSIGHT
              {trends.marketInsight && (
                <span className="tnum" style={{ color: "var(--faint)" }}>
                  {ago(trends.marketInsight.ts)}
                </span>
              )}
            </div>
            {trends.marketInsight ? (
              <div className="px-4 py-4">
                <h2 className="text-[0.85rem] font-bold" style={{ color: "var(--text)" }}>
                  {trends.marketInsight.headline}
                </h2>
                <p
                  className="mt-2 whitespace-pre-wrap text-[0.78rem] leading-relaxed"
                  style={{ color: "var(--dim)" }}
                >
                  {trends.marketInsight.body}
                </p>
              </div>
            ) : (
              <EmptyState
                message="No market brief yet"
                detail="The insight-writer agent produces one every 15 minutes."
              />
            )}
          </section>
        </>
      )}
    </div>
  );
}
