"use client";

// SIGNALS → UNUSUAL — rebuilt as a UW-style two-lane feed on free data
// (signals-hub overhaul). LEFT: the raw tape — every anomaly event as one
// dense row with severity lanes (notable/elevated/extreme) badged from the
// daemon's STRUCTURED {measure,value,proxy} fields, never string-sniffing.
// RIGHT: compound signals — symbols with 2+ distinct anomaly kinds within
// 24h, computed client-side from the same rows and labeled "co-occurrence,
// not causation". Filters: kind chips + symbol search (?symbol=) + market
// toggle (?market=), all passed to the API. All honesty framing renders:
// the verbatim header chip, PROXY chips on stock imbalance, the API note in
// the tape footer, and a collapsible METHODOLOGY quoting the daemon's exact
// windows and thresholds.

import { useEffect, useState } from "react";
import { anomalies, pollMs, POLL_DEFAULT, type AnomaliesResponse, type AnomalyRow, type Market } from "@/lib/api";
import { useViewMode } from "@/components/Plain";
import PagePurpose from "@/components/PagePurpose";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import UnusualTape from "@/components/signals/unusual/UnusualTape";
import SummaryStrip from "@/components/signals/unusual/SummaryStrip";
import CompoundLane from "@/components/signals/unusual/CompoundLane";
import MethodologyPanel from "@/components/signals/unusual/MethodologyPanel";
import { KIND_ORDER, kindLabel } from "@/components/signals/unusual/measure";

type KindFilter = AnomalyRow["kind"] | undefined;
type MarketFilter = Market | undefined;

// One fetch feeds both lanes; generous so the co-occurrence window has depth.
const FETCH_LIMIT = 150;

const KIND_TITLES: Record<AnomalyRow["kind"], string> = {
  anomaly_imbalance:
    "trade imbalance vs own baseline (stocks = volume-side proxy, labeled)",
  anomaly_vol: "true-range / volatility spikes vs own baseline",
  anomaly_volume: "volume spikes vs own baseline",
};

function FilterChip({
  active,
  onClick,
  title,
  children,
}: {
  active: boolean;
  onClick: () => void;
  title?: string;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title}
      aria-pressed={active}
      onClick={onClick}
      className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
      style={{
        color: active ? "var(--accent)" : "var(--dim)",
        borderColor: active ? "var(--accent)" : "var(--border)",
      }}
    >
      {children}
    </button>
  );
}

export default function UnusualPage() {
  const mode = useViewMode();
  const [kind, setKind] = useState<KindFilter>(undefined);
  const [market, setMarket] = useState<MarketFilter>(undefined);
  const [searchInput, setSearchInput] = useState("");
  const [symbolQ, setSymbolQ] = useState(""); // debounced, uppercased
  // `at` = fetch-time unix seconds — the summary timeline's clock anchor
  // (ages are computed against it, never Date.now() in render).
  const [data, setData] = useState<{ key: string; resp: AnomaliesResponse; at: number } | null>(
    null,
  );
  const [err, setErr] = useState<{ key: string; msg: string } | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // Debounce the symbol box — the API wants one exact ticker, not keystrokes.
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
    // POLL_DEFAULT tier — the scanner sweeps on minute cadence; the managed
    // loop pauses hidden tabs and backs off on failures.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [symbolQ, market, kind, retryTick]);

  const resp = data && data.key === queryKey ? data.resp : null;
  const errMsg = err && err.key === queryKey ? err.msg : null;
  // 404 = the searched ticker isn't in the tracked universe — an honest miss,
  // not a daemon failure (the API only matches exact symbols).
  const unknownSymbol = symbolQ !== "" && errMsg !== null && errMsg.includes("API 404");

  // ?market= is applied server-side when a symbol is present (it scopes the
  // symbol lookup); the fleet-wide feed returns both markets, so the toggle
  // also filters on each row's own `market` field — an exact stored field,
  // never inferred.
  const rows: AnomalyRow[] | null = resp
    ? (resp.anomalies ?? []).filter((r) => !market || r.market === market)
    : null;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">UNUSUAL ACTIVITY</h1>
        <span className="chip">
          descriptive anomaly layer · z-scores vs own baseline · not predictions
        </span>
        {errMsg !== null && resp !== null && (
          <span
            className="chip px-2 py-[1px] text-[0.75rem]"
            style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
          >
            refresh failed — showing last fetch
          </span>
        )}
      </div>

      <PagePurpose
        id="signals-unusual"
        text="Which symbols are behaving unusually versus their own normal? Left: every descriptive flag (volume, volatility, imbalance) as a raw tape with severity lanes. Right: symbols flagging in 2+ distinct ways within 24h — co-occurrence, not causation. Observations, never predictions."
      />

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-1 text-[0.75rem]">
        <div className="flex items-center gap-1.5" role="group" aria-label="anomaly kind filter">
          <span style={{ color: "var(--faint)" }}>kind</span>
          <FilterChip active={kind === undefined} onClick={() => setKind(undefined)} title="every anomaly kind">
            all
          </FilterChip>
          {KIND_ORDER.map((k) => (
            <FilterChip key={k} active={kind === k} onClick={() => setKind(k)} title={KIND_TITLES[k]}>
              {kindLabel(k, mode).toLowerCase()}
            </FilterChip>
          ))}
        </div>

        <div className="flex items-center gap-1.5" role="group" aria-label="market filter">
          <span style={{ color: "var(--faint)" }}>market</span>
          <FilterChip active={market === undefined} onClick={() => setMarket(undefined)}>
            all
          </FilterChip>
          {(["crypto", "stocks"] as Market[]).map((m) => (
            <FilterChip key={m} active={market === m} onClick={() => setMarket(m)}>
              {m}
            </FilterChip>
          ))}
        </div>

        <input
          type="search"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder="symbol (exact, e.g. AAPL or BTC/USD)…"
          aria-label="filter by symbol"
          className="mono min-w-52 flex-1 rounded-lg border px-2.5 py-1.5 text-[0.75rem]"
          style={{
            background: "var(--panel2)",
            borderColor: "var(--border)",
            color: "var(--text)",
          }}
        />
      </div>

      {resp === null && errMsg !== null && !unknownSymbol && (
        <ErrorState message={errMsg} retry={() => setRetryTick((n) => n + 1)} />
      )}

      {unknownSymbol && (
        <EmptyState
          message={`No symbol "${symbolQ}" in the tracked universe`}
          detail="The search matches exact tickers only (e.g. AAPL, BTC/USD). Clear the box to return to the fleet-wide tape."
        />
      )}

      {!(resp === null && errMsg !== null) && (
        <SummaryStrip rows={rows} nowSec={data?.at ?? 0} />
      )}

      {!(resp === null && errMsg !== null) && (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
          <UnusualTape
            rows={rows}
            note={resp?.note}
            proxyNote={resp?.proxyNote}
            emptyMessage={
              kind
                ? `No ${kindLabel(kind, mode).toLowerCase()} anomalies in the current view`
                : "No unusual activity in the current view"
            }
            emptyDetail="Nothing is currently outside its own statistical baseline (|z| ≥ 2.5 default threshold applies). Quiet is the honest default state."
          />
          <CompoundLane rows={rows} proxyNote={resp?.proxyNote} />
        </div>
      )}

      <MethodologyPanel />
    </div>
  );
}
