"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, type Insight } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

type Filter = "all" | "market" | "symbol";

const FILTERS: { key: Filter; label: string }[] = [
  { key: "all", label: "all" },
  { key: "market", label: "market briefs" },
  { key: "symbol", label: "per-symbol" },
];

/** Infer the market from the symbol shape — insight rows don't carry it.
    Pairs like BTC/USD contain "/" → crypto; bare tickers → stocks. */
function inferMarket(symbol: string): "crypto" | "stocks" {
  return symbol.includes("/") ? "crypto" : "stocks";
}

function InsightCard({ ins }: { ins: Insight }) {
  const isMarket = ins.scope === "market";
  const symbol = ins.symbol ?? "";
  return (
    <article
      className="px-4 py-4 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <div className="flex flex-wrap items-center gap-2">
        {isMarket ? (
          <span
            className="chip"
            style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
          >
            market
          </span>
        ) : (
          <>
            <span className="chip">symbol</span>
            {symbol && (
              <Link
                href={`/s/${inferMarket(symbol)}/${encodeURIComponent(symbol)}`}
                className="cursor-pointer text-[0.78rem] font-bold transition-colors duration-150 hover:text-[var(--accent)]"
              >
                {symbol}
              </Link>
            )}
          </>
        )}
        <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ago(ins.ts)}
        </span>
      </div>
      <h2 className="mt-2 text-[0.86rem] font-bold" style={{ color: "var(--text)" }}>
        {ins.headline || "(untitled insight)"}
      </h2>
      {ins.body && (
        <p
          className="mt-1.5 whitespace-pre-wrap text-[0.78rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          {ins.body}
        </p>
      )}
    </article>
  );
}

export default function InsightsPage() {
  const [insights, setInsights] = useState<Insight[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .insights(100)
        .then((rows) => {
          if (!alive) return;
          setInsights(Array.isArray(rows) ? rows : []);
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

  const sorted = useMemo(
    () => [...(insights ?? [])].sort((a, b) => (b.ts ?? 0) - (a.ts ?? 0)),
    [insights],
  );

  const counts = useMemo(
    () => ({
      all: sorted.length,
      market: sorted.filter((i) => i.scope === "market").length,
      symbol: sorted.filter((i) => i.scope !== "market").length,
    }),
    [sorted],
  );

  const visible = useMemo(
    () =>
      filter === "all"
        ? sorted
        : sorted.filter((i) => (filter === "market" ? i.scope === "market" : i.scope !== "market")),
    [sorted, filter],
  );

  const loading = insights === null && err === null;
  const hardError = insights === null && err !== null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">INSIGHTS</h1>
        {insights !== null && (
          <span className="chip tnum">{counts.all} stored</span>
        )}
        {insights !== null && sorted.length > 0 && (
          <span className="chip tnum">latest {ago(sorted[0].ts)}</span>
        )}
        {err !== null && insights !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* what this page is */}
      <section className="panel">
        <div className="panel-h">HOW TO READ THIS FEED</div>
        <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          Every insight is generated from stored data with the numbers inline — headlines state
          measured tendencies, never forecasts.
        </p>
      </section>

      {loading && <Skeleton lines={4} label="loading insights" />}

      {hardError && (
        <ErrorState
          message={err ?? "insights unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {insights !== null && (
        <section className="panel">
          <div className="panel-h">
            FEED
            <div className="ml-auto flex flex-wrap items-center gap-1.5" role="group" aria-label="Filter insights">
              {FILTERS.map((f) => {
                const active = filter === f.key;
                return (
                  <button
                    key={f.key}
                    type="button"
                    aria-pressed={active}
                    onClick={() => setFilter(f.key)}
                    className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
                    style={{
                      color: active ? "var(--text)" : undefined,
                      borderColor: active ? "var(--accent)" : undefined,
                    }}
                  >
                    {f.label} <span className="tnum">{counts[f.key]}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {visible.length === 0 ? (
            sorted.length === 0 ? (
              <EmptyState
                className="m-4"
                message="No insights yet"
                detail="The insight-writer runs every 15 minutes once data flows — check back shortly."
              />
            ) : (
              <EmptyState
                className="m-4"
                message="Nothing matches this filter"
                detail="Try the “all” filter to see every stored insight."
              />
            )
          ) : (
            <div>
              {visible.map((ins) => (
                <InsightCard key={ins.id} ins={ins} />
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}
