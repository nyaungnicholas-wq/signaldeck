"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  type BreakoutRow,
  type Market,
  type RankedRow,
  type RegimeChange,
  type RegimeState,
} from "@/lib/api";
import { ago } from "@/lib/format";
import RegimeMap from "@/components/regime/RegimeMap";
import RegimeChanges from "@/components/regime/RegimeChanges";
import RankingTable from "@/components/regime/RankingTable";
import BreakoutFeed from "@/components/regime/BreakoutFeed";

// This page describes what IS. It polls three trend-detection endpoints and
// renders them side by side. Regimes/rankings are computed from stored bars
// with no lookahead — they are a description, not a prediction.
const POLL_MS = 10_000;

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
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

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
        <span className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
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

      {/* hard error (nothing to show) */}
      {hardError && (
        <div className="panel px-4 py-8 text-center text-[0.75rem]">
          <div style={{ color: "var(--bad)" }}>{err}</div>
          <div className="mt-2" style={{ color: "var(--faint)" }}>
            is the daemon running? start it with{" "}
            <span style={{ color: "var(--dim)" }}>signaldeckd</span> and this page will pick it up.
          </div>
        </div>
      )}

      {/* loading */}
      {loading && (
        <div className="panel px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading…
        </div>
      )}

      {data !== null && (
        <>
          <RegimeMap states={data.states} />

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <RegimeChanges changes={data.changes} marketBySymbol={marketBySymbol} />
            <BreakoutFeed breakouts={data.breakouts} marketBySymbol={marketBySymbol} />
          </div>

          <RankingTable rows={data.ranking} />

          {/* honesty note */}
          <div
            className="panel px-4 py-3 text-[0.7rem] leading-relaxed"
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
