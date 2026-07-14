"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  POLL_DEFAULT,
  type BreakoutRow,
  type Market,
  type RankedRow,
  type RegimeChange,
  type RegimeState,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import HelpTip from "@/components/HelpTip";
import RegimeMap from "@/components/regime/RegimeMap";
import RegimeChanges from "@/components/regime/RegimeChanges";
import RankingTable from "@/components/regime/RankingTable";
import BreakoutFeed from "@/components/regime/BreakoutFeed";
import Gauge from "@/components/viz/Gauge";
import { regimeColor, regimeKind } from "@/components/regime/regime";
import PagePurpose from "@/components/PagePurpose";
import ProOnly from "@/components/ProOnly";

// This page describes what IS. It polls three trend-detection endpoints and
// renders them side by side. Regimes/rankings are computed from stored bars
// with no lookahead — they are a description, not a prediction. Regimes move
// on worker cadence, so the default polling tier is plenty.

interface RegimeData {
  states: RegimeState[];
  changes: RegimeChange[];
  ranking: RankedRow[];
  breakouts: BreakoutRow[];
}

export default function RegimePage() {
  const [data, setData] = useState<RegimeData | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [fetchedAt, setFetchedAt] = useState(0);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      Promise.all([api.regime(), api.ranking(), api.breakouts()])
        .then(([regime, ranking, breakouts]) => {
          if (!alive) return;
          setData({
            states: regime.states ?? [],
            changes: regime.changes ?? [],
            ranking: ranking ?? [],
            breakouts: breakouts ?? [],
          });
          setErr(null);
          setFetchedAt(Math.floor(Date.now() / 1000));
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

  const loading = data === null && err === null;
  const hardError = data === null && err !== null;

  // Build symbol → market from the regime states so the changes/breakouts
  // panels (whose rows carry no market) can still link accurately.
  const marketBySymbol = useMemo(() => {
    const m: Record<string, Market> = {};
    for (const s of data?.states ?? []) m[s.symbol] = s.market;
    for (const r of data?.ranking ?? []) if (!m[r.symbol]) m[r.symbol] = r.market;
    return m;
  }, [data]);

  const counts = useMemo(() => {
    return {
      symbols: data?.states.length ?? 0,
      changes: data?.changes.length ?? 0,
      ranked: data?.ranking.length ?? 0,
      events: data?.breakouts.length ?? 0,
    };
  }, [data]);

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-1">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">REGIME</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          creating &amp; detecting trends
        </span>
        {data !== null && (
          <div className="flex flex-wrap items-center gap-2">
            <span className="chip tnum">{counts.symbols} classified</span>
            <span className="chip tnum">{counts.ranked} ranked</span>
            <span className="chip tnum">{counts.events} events</span>
          </div>
        )}
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {err !== null && data !== null && (
            <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
              poll failed — showing last data
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
        id="markets-regimes"
        text="What mode is each market in right now — trending, choppy, or stressed? Strategies that work in one regime fail in another; this page names the regime first."
      />

      {/* hard error (nothing to show) */}
      {hardError && (
        <ErrorState
          message={err ?? "could not load regime data"}
          hint="is the daemon running? start it with signaldeckd and this page will pick it up."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {/* loading */}
      {loading && <Skeleton lines={4} label="loading regimes" />}

      {data !== null && (
        <>
          {/* Stage 5: regime-mix dial — share of classified symbols currently
              in an uptrend, plus per-regime counts. Descriptive (stored bars,
              no lookahead); the caption carries the honest n. */}
          <section className="panel">
            <div className="panel-h">
              REGIME MIX
              <HelpTip label="What is a regime?">
                A regime is the mode a symbol&apos;s market is currently in — uptrend, downtrend,
                range, or squeeze — detected from stored bars with no lookahead. Strategies that
                work in one regime fail in another, so the regime is named first. It describes
                what IS, not what will be.
              </HelpTip>
            </div>
            <div className="flex flex-wrap items-center gap-x-8 gap-y-4 px-4 py-4">
              {(() => {
                const total = data.states.length;
                const byKind = new Map<string, number>();
                for (const s of data.states) {
                  const k = regimeKind(s.label);
                  byKind.set(k, (byKind.get(k) ?? 0) + 1);
                }
                const up = byKind.get("uptrend") ?? 0;
                return (
                  <>
                    <Gauge
                      label="UPTREND SHARE"
                      value={total > 0 ? (up / total) * 100 : 0}
                      min={0}
                      max={100}
                      hasData={total > 0}
                      caption={
                        total > 0
                          ? `${up} of ${total} classified symbols in an uptrend regime — descriptive, no lookahead`
                          : "nothing classified yet — the regime worker fills this in from stored bars"
                      }
                      format={(v) => `${v.toFixed(0)}%`}
                      zones={[
                        { from: 0, to: 35, color: "var(--ask)" },
                        { from: 35, to: 55, color: "var(--dim)" },
                        { from: 55, to: 100, color: "var(--bid)" },
                      ]}
                    />
                    <div className="flex flex-wrap items-center gap-2">
                      {(["uptrend", "squeeze", "downtrend", "range", "other"] as const)
                        .filter((k) => (byKind.get(k) ?? 0) > 0)
                        .map((k) => (
                          <span
                            key={k}
                            className="chip tnum"
                            style={{ color: regimeColor(k), borderColor: regimeColor(k) }}
                          >
                            {k} {byKind.get(k)}
                          </span>
                        ))}
                    </div>
                  </>
                );
              })()}
            </div>
          </section>

          <RegimeMap states={data.states} />

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <RegimeChanges changes={data.changes} marketBySymbol={marketBySymbol} />
            <BreakoutFeed breakouts={data.breakouts} marketBySymbol={marketBySymbol} />
          </div>

          {/* raw cross-sectional score table — methodology detail, folded in
              simple mode (never deleted); pro mode renders it directly */}
          <ProOnly summary="Show the relative-strength ranking table">
            <RankingTable rows={data.ranking} />
          </ProOnly>

          {/* honesty note */}
          <div
            className="panel px-4 py-3 text-[0.75rem] leading-relaxed"
            style={{ color: "var(--faint)" }}
          >
            Regimes and rankings are computed from stored bars with no lookahead; they describe
            what <span style={{ color: "var(--dim)" }}>IS</span>, not a guarantee of what&apos;s
            next.
          </div>
        </>
      )}
    </div>
  );
}
