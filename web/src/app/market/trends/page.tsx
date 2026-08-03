"use client";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, POLL_DEFAULT, type Trends, type TrendsMover } from "@/lib/api";
import { ago, fmtPct, scoreColor } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";
import PatternsExplorer from "@/components/markets/PatternsExplorer";
import { Reveal, StatTile, PageHero, DeltaBadge, MiniBar } from "@/components/ui/Kit";

function MoverCard({ m, maxChange, i }: { m: TrendsMover; maxChange: number; i: number }) {
  const dir = m.dayChangePct > 0 ? "up" : m.dayChangePct < 0 ? "down" : "";
  const colorVar = dir === "up" ? "--bid" : dir === "down" ? "--ask" : "--dim";
  return (
    <Link
      href={`/s/${m.market}/${encodeURIComponent(m.symbol)}`}
      className="panel reveal-item block p-4 transition-colors duration-150 hover:border-[var(--accent)] cursor-pointer"
      style={{ "--i": Math.min(12, i) } as React.CSSProperties}
    >
      <div className="flex justify-between items-start mb-2">
        <span className="mono text-sm font-bold">{m.symbol}</span>
        <DeltaBadge value={m.dayChangePct} />
      </div>
      <div className="flex items-end justify-between gap-2">
        <div className="flex flex-col gap-1 min-w-0">
          <span className="text-xs truncate" style={{ color: "var(--dim)" }}>
            {m.market}
          </span>
          <span
            className="tnum text-2xl font-bold num-hero"
            style={{ color: `var(${colorVar})` }}
          >
            {fmtPct(m.dayChangePct)}
          </span>
        </div>
        <MiniBar
          value={Math.abs(m.dayChangePct)}
          max={maxChange || 1}
          color={`var(${colorVar})`}
          i={i}
          height={8}
        />
      </div>
      <div className="mt-2 text-xs" style={{ color: "var(--faint)" }}>
        Score:{" "}
        <span className="tnum" style={{ color: scoreColor(m.score) }}>
          {m.score.toFixed(2)}
        </span>
      </div>
    </Link>
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
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const { top, bottom, topGainer, topLoser, upMovers, downMovers } = useMemo(() => {
    const movers = trends?.movers ?? [];
    const sorted = [...movers]
      .filter((m) => isFinite(m.score))
      .sort((a, b) => b.dayChangePct - a.dayChangePct);

    const top = sorted.slice(0, 12);
    const bottom = [...sorted].reverse().slice(0, 12);

    const topGainer = sorted[0] || null;
    const topLoser = sorted[sorted.length - 1] || null;

    const upMovers = movers.filter((m) => m.dayChangePct > 0).length;
    const downMovers = movers.filter((m) => m.dayChangePct < 0).length;

    return { top, bottom, topGainer, topLoser, upMovers, downMovers };
  }, [trends]);

  const maxTopChange = useMemo(() => {
    const changes = top.map((m) => Math.abs(m.dayChangePct));
    return Math.max(...changes, 1);
  }, [top]);

  const maxBottomChange = useMemo(() => {
    const changes = bottom.map((m) => Math.abs(m.dayChangePct));
    return Math.max(...changes, 1);
  }, [bottom]);

  const loading = trends === null && err === null;
  const hardError = trends === null && err !== null;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Trends"
        subtitle="Who is moving and how persistently — every mover with its recent shape, not just a number."
        right={
          trends !== null && (
            <div className="flex flex-wrap gap-2">
              <span className="chip tnum">Tracked {trends.tracked}</span>
              <span className="chip tnum">Scored {trends.scored}</span>
            </div>
          )
        }
      />

      {loading && <Skeleton lines={4} label="Loading trends..." />}
      {hardError && (
        <ErrorState
          message={err ?? "Request failed"}
          hint="Is the daemon running? Start it with signaldeckd and this page will recover."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {trends !== null && (
        <>
          <Reveal>
            <div className="grid grid-cols-2 sm:grid-cols-2 xl:grid-cols-4 gap-3">
              <StatTile
                label="Top Gainer"
                value={topGainer?.symbol || "—"}
                sub={topGainer ? `${topGainer.market} • ${fmtPct(topGainer.dayChangePct)}` : ""}
                glow="up"
                i={0}
              />
              <StatTile
                label="Top Loser"
                value={topLoser?.symbol || "—"}
                sub={topLoser ? `${topLoser.market} • ${fmtPct(topLoser.dayChangePct)}` : ""}
                glow="down"
                i={1}
              />
              <StatTile
                label="Up Movers"
                value={upMovers}
                sub="symbols with positive 1d change"
                glow="hud"
                i={2}
              />
              <StatTile
                label="Down Movers"
                value={downMovers}
                sub="symbols with negative 1d change"
                glow="accent"
                i={3}
              />
            </div>
          </Reveal>

          <section className="panel">
            <div className="panel-h flex justify-between">
              <span>
                Market Breadth
                <HelpTip label="What is market breadth?">
                  The ratio of moving symbols that are up versus down, showing overall market direction.
                </HelpTip>
              </span>
              <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {upMovers + downMovers} moving symbols
              </span>
            </div>
            <div className="px-4 py-3">
              <div
                role="meter"
                aria-valuemin={0}
                aria-valuemax={upMovers + downMovers}
                aria-valuenow={upMovers}
                aria-label={`Market breadth: ${upMovers} up, ${downMovers} down`}
                className="flex h-3 overflow-hidden rounded-full"
                style={{ background: "rgba(255,255,255,0.06)" }}
              >
                <div
                  className="bar-animate transition-[width] duration-300"
                  style={{
                    width: `${((upMovers / (upMovers + downMovers || 1)) * 100)}%`,
                    background: "var(--bid)",
                  }}
                />
                <div
                  className="bar-animate transition-[width] duration-300"
                  style={{
                    width: `${((downMovers / (upMovers + downMovers || 1)) * 100)}%`,
                    background: "var(--ask)",
                  }}
                />
              </div>
              <div className="flex justify-between mt-2 text-[0.75rem]">
                <span className="tnum" style={{ color: "var(--bid)" }}>
                  {upMovers} up
                </span>
                <span className="tnum" style={{ color: "var(--ask)" }}>
                  {downMovers} down
                </span>
              </div>
            </div>
          </section>

          <Reveal>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {top.map((m, i) => (
                <MoverCard key={`${m.market}:${m.symbol}`} m={m} maxChange={maxTopChange} i={i} />
              ))}
            </div>
          </Reveal>

          <Reveal>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {bottom.map((m, i) => (
                <MoverCard key={`${m.market}:${m.symbol}`} m={m} maxChange={maxBottomChange} i={i} />
              ))}
            </div>
          </Reveal>

          <PatternsExplorer />

          <p className="px-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            <Link
              href="/market/overview"
              className="cursor-pointer transition-colors duration-150 hover:text-[var(--accent)]"
              style={{ color: "var(--dim)" }}
            >
              Compare the full universe on the screener →
            </Link>
          </p>

          <section className="panel">
            <div className="panel-h flex justify-between">
              <span>Market Insight</span>
              {trends.marketInsight && (
                <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  {ago(trends.marketInsight.ts)}
                </span>
              )}
            </div>
            {trends.marketInsight ? (
              <div className="px-4 py-4">
                <h2 className="text-sm font-bold" style={{ color: "var(--text)" }}>
                  {trends.marketInsight.headline}
                </h2>
                <p
                  className="mt-2 whitespace-pre-wrap text-[0.75rem] leading-relaxed"
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
