"use client";

import { useEffect, useState } from "react";
import { anomalies, pollMs, POLL_DEFAULT, type AnomaliesResponse, type AnomalyRow, type Market } from "@/lib/api";
import { useViewMode } from "@/components/Plain";
import { Reveal, AnimatedNumber, StatTile, PageHero, MiniBar, DeltaBadge } from "@/components/ui/Kit";

type KindFilter = AnomalyRow["kind"] | undefined;
type MarketFilter = Market | undefined;

const FETCH_LIMIT = 150;

const KIND_TITLES: Record<AnomalyRow["kind"], string> = {
  anomaly_imbalance: "trade imbalance vs own baseline",
  anomaly_vol: "volatility spikes vs own baseline",
  anomaly_volume: "volume spikes vs own baseline",
};

const KIND_LABELS: Record<AnomalyRow["kind"], string> = {
  anomaly_imbalance: "Imbalance",
  anomaly_vol: "Volatility",
  anomaly_volume: "Volume",
};

function getSeverityColor(z: number): string {
  const abs = Math.abs(z);
  if (abs >= 4) return "var(--ask)";
  if (abs >= 3) return "var(--accent)";
  return "var(--hud)";
}

export default function UnusualPage() {
  const mode = useViewMode();
  const [kind, setKind] = useState<KindFilter>(undefined);
  const [market, setMarket] = useState<MarketFilter>(undefined);
  const [searchInput, setSearchInput] = useState("");
  const [symbolQ, setSymbolQ] = useState("");
  const [data, setData] = useState<{ key: string; resp: AnomaliesResponse; at: number } | null>(null);
  const [err, setErr] = useState<{ key: string; msg: string } | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    const t = setTimeout(() => setSymbolQ(searchInput.trim().toUpperCase()), 350);
    return () => clearTimeout(t);
  }, [searchInput]);

  const queryKey = `${symbolQ}|${market ?? ""}|${kind ?? ""}`;

  useEffect(() => {
    let alive = true;
    const key = `${symbolQ}|${market ?? ""}|${kind ?? ""}`;
    const load = () =>
      anomalies(symbolQ || undefined, market, kind, FETCH_LIMIT)
        .then((r) => {
          if (!alive) return;
          setData({ key, resp: r, at: Math.floor(Date.now() / 1000) });
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr({ key, msg: e instanceof Error ? e.message : String(e) });
        });
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => { alive = false; stop(); };
  }, [symbolQ, market, kind, retryTick]);

  const resp = data && data.key === queryKey ? data.resp : null;
  const errMsg = err && err.key === queryKey ? err.msg : null;
  const unknownSymbol = symbolQ !== "" && errMsg !== null && errMsg.includes("API 404");

  const rows: AnomalyRow[] | null = resp
    ? (resp.anomalies ?? []).filter((r) => !market || r.market === market)
    : null;

  // Compute stats from loaded data
  const flagCount = rows?.length ?? 0;
  const sortedRows = rows ? [...rows].sort((a, b) => Math.abs(b.z) - Math.abs(a.z)) : [];
  const mostExtremeSymbol = sortedRows[0]?.symbol ?? "—";
  const mostExtremeZ = sortedRows[0]?.z ?? 0;

  // Breakdown by type
  const typeCounts = rows ? rows.reduce((acc, row) => {
    acc[row.kind] = (acc[row.kind] || 0) + 1;
    return acc;
  }, {} as Record<string, number>) : {};

  // Max absolute z for MiniBar scaling
  const maxAbsZ = sortedRows.length > 0 ? Math.abs(sortedRows[0].z) : 0;

  // Unique kinds for filter chips
  const uniqueKinds = rows ? [...new Set(rows.map(r => r.kind))] : [];

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Unusual Activity"
        live
        subtitle="Outliers the scanners flagged — volume, moves and behavior outside the normal band."
        right={
          <div className="flex items-center gap-2">
            {errMsg && resp && (
              <span className="text-[0.75rem] rounded-full border px-2 py-0.5" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                refresh failed
              </span>
            )}
          </div>
        }
      />

      {/* Hero StatTiles */}
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="FLAGGED"
          value={flagCount}
          glow="hud"
          i={0}
        />
        <StatTile
          label="MOST EXTREME"
          value={mostExtremeSymbol}
          sub={`z-score: ${mostExtremeZ.toFixed(2)}`}
          glow="accent"
          i={1}
        />
        {Object.entries(typeCounts).map(([kind, count], i) => (
          <StatTile
            key={kind}
            label={KIND_LABELS[kind as AnomalyRow["kind"]] ?? kind.toUpperCase()}
            value={count}
            glow={i % 2 === 0 ? "up" : "down"}
            i={i + 2}
          />
        ))}
      </div>

      {/* Filter chips by flag type */}
      {uniqueKinds.length > 1 && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>Filter by type</span>
          <button
            onClick={() => setKind(undefined)}
            className={`chip cursor-pointer px-3 py-1 transition-colors ${
              kind === undefined
                ? "border-[var(--hud)] text-[var(--hud)] bg-[color-mix(in_srgb,var(--hud)_10%,transparent)]"
                : "border-[var(--border)] text-[var(--dim)] hover:bg-[var(--panel3)]"
            }`}
          >
            all
          </button>
          {uniqueKinds.map((k) => (
            <button
              key={k}
              onClick={() => setKind(k)}
              className={`chip cursor-pointer px-3 py-1 transition-colors ${
                kind === k
                  ? "border-[var(--accent)] text-[var(--accent)] bg-[color-mix(in_srgb,var(--accent)_10%,transparent)]"
                  : "border-[var(--border)] text-[var(--dim)] hover:bg-[var(--panel3)]"
              }`}
              title={KIND_TITLES[k]}
            >
              {KIND_LABELS[k] ?? k}
            </button>
          ))}
        </div>
      )}

      {/* Other filters */}
      <div className="flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>Market</span>
          {(["crypto", "stocks"] as Market[]).map((m) => (
            <button
              key={m}
              onClick={() => setMarket(market === m ? undefined : m)}
              className={`chip cursor-pointer px-3 py-1 transition-colors ${
                market === m
                  ? "border-[var(--hud)] text-[var(--hud)] bg-[color-mix(in_srgb,var(--hud)_10%,transparent)]"
                  : "border-[var(--border)] text-[var(--dim)] hover:bg-[var(--panel3)]"
              }`}
            >
              {m}
            </button>
          ))}
        </div>

        <input
          type="search"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder="symbol (exact, e.g. AAPL)…"
          aria-label="filter by symbol"
          className="mono min-w-52 flex-1 rounded-lg border px-2.5 py-1.5 text-[0.75rem]"
          style={{
            background: "var(--panel2)",
            borderColor: "var(--border)",
            color: "var(--text)",
          }}
        />
      </div>

      {/* Error/Empty states */}
      {resp === null && errMsg !== null && !unknownSymbol && (
        <div className="panel p-6 text-center" style={{ color: "var(--dim)" }}>
          <p className="mb-2">Failed to load data</p>
          <p className="text-[0.75rem] mb-4">{errMsg}</p>
          <button
            onClick={() => setRetryTick((n) => n + 1)}
            className="cursor-pointer rounded border px-4 py-2 text-[0.75rem] transition-colors hover:bg-[var(--panel3)]"
            style={{ borderColor: "var(--border)", color: "var(--text)" }}
          >
            Retry
          </button>
        </div>
      )}

      {unknownSymbol && (
        <div className="panel p-6 text-center" style={{ color: "var(--dim)" }}>
          <p>No symbol "{symbolQ}" in the tracked universe</p>
          <p className="text-[0.75rem] mt-2">Search matches exact tickers only.</p>
        </div>
      )}

      {/* Card grid of flagged items */}
      {rows && rows.length > 0 && (
        <Reveal className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {sortedRows.slice(0, 12).map((row, i) => {
            const isExtreme = i === 0;
            const severityColor = getSeverityColor(row.z);
            const absZ = Math.abs(row.z);
            const barColor = row.z > 0 ? "var(--bid)" : "var(--ask)";

            return (
              <div
                key={`${row.symbol}-${row.kind}-${row.ts}`}
                className={`panel reveal-item p-4 ${isExtreme ? "hud-panel glow-hud" : ""}`}
                style={{ "--i": i } as React.CSSProperties}
              >
                <div className="flex items-start justify-between mb-2">
                  <div>
                    <span className="mono text-lg font-bold" style={{ color: severityColor }}>
                      {row.symbol}
                    </span>
                    <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
                      {KIND_LABELS[row.kind] ?? row.kind}
                    </div>
                  </div>
                  <DeltaBadge value={row.z} decimals={2} />
                </div>

                <div className="mb-3 text-sm" style={{ color: "var(--faint)" }}>
                  {row.measure}: {row.value}
                </div>

                <div className="flex items-center gap-2">
                  <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>z</span>
                  <div className="flex-1">
                    <MiniBar value={absZ} max={maxAbsZ} color={barColor} height={6} />
                  </div>
                </div>

                {row.proxy && (
                  <div className="mt-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {resp?.proxyNote}
                  </div>
                )}
              </div>
            );
          })}
        </Reveal>
      )}

      {/* Footer note */}
      {rows && rows.length === 0 && (
        <div className="panel p-6 text-center" style={{ color: "var(--dim)" }}>
          {kind
            ? `No ${KIND_LABELS[kind]?.toLowerCase() ?? kind} anomalies in the current view`
            : "No unusual activity in the current view"
          }
          <p className="text-[0.75rem] mt-2" style={{ color: "var(--faint)" }}>
            Quiet is the honest default state.
          </p>
        </div>
      )}

      {resp?.note && (
        <div className="text-[0.75rem] px-1" style={{ color: "var(--faint)" }}>
          {resp.note}
        </div>
      )}
    </div>
  );
}
