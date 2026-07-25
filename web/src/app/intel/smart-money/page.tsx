"use client";

// SMART MONEY (SMART MONEY FACTS wave): ONE transparent, decomposed per-symbol
// "Smart Money Score" built from ALREADY-INGESTED positioning data — SEC Form 4
// open-market insider trades, FINRA short interest / Reg SHO short volume,
// crypto perp funding, and SEC 13F holdings. HONESTY: this is a read of what
// INFORMED PARTICIPANTS ARE DOING, NOT a price forecast. The caveat renders
// verbatim, every factor shows its line + source, and absent sources display
// "—" rather than an imputed zero.

import { useEffect, useMemo, useState } from "react";
import {
  smartMoney,
  smartMoneyTop,
  pollMs,
  POLL_SLOW,
  type SmartMoneyResponse,
  type SmartMoneyTopResponse,
  type SmartMoneyFactor,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import PagePurpose from "@/components/PagePurpose";

const LABEL_TEXT: Record<string, string> = {
  strong_accumulation: "Strong accumulation",
  accumulation: "Accumulation",
  neutral: "Neutral",
  distribution: "Distribution",
  strong_distribution: "Strong distribution",
};

function labelColor(label?: string): string {
  switch (label) {
    case "strong_accumulation":
    case "accumulation":
      return "var(--bid)";
    case "distribution":
    case "strong_distribution":
      return "var(--ask)";
    default:
      return "var(--dim)";
  }
}

function labelText(label?: string): string {
  if (!label) return "—";
  return LABEL_TEXT[label] ?? label.replace(/_/g, " ");
}

function fmtUSD(v: number): string {
  if (!isFinite(v)) return "—";
  const a = Math.abs(v);
  if (a >= 1e9) return `$${(v / 1e9).toFixed(1)}B`;
  if (a >= 1e6) return `$${(v / 1e6).toFixed(1)}M`;
  if (a >= 1e3) return `$${(v / 1e3).toFixed(0)}K`;
  return `$${v.toFixed(0)}`;
}

/** Null-safe number tile value — honest "—" when the source is absent. */
function tile(v: number | null | undefined, digits: number, suffix = ""): string {
  if (v === null || v === undefined || !isFinite(v)) return "—";
  return `${v.toFixed(digits)}${suffix}`;
}

/** Funding is a tiny hourly decimal; show it as a signed %/hr. */
function fmtFunding(v: number | null | undefined): string {
  if (v === null || v === undefined || !isFinite(v)) return "—";
  return `${v >= 0 ? "+" : ""}${(v * 100).toFixed(4)}%/hr`;
}

// A centered [-1,1] bar: fill runs from the middle toward the value, green for
// accumulation and red for distribution.
function FactorBar({ value }: { value: number }) {
  const pct = Math.min(Math.abs(value), 1) * 50; // half-track per unit
  const pos = value >= 0;
  return (
    <div
      className="relative h-2 w-full overflow-hidden rounded"
      style={{ background: "var(--panel3)" }}
      aria-hidden
    >
      <div className="absolute bottom-0 top-0" style={{ left: "50%", width: 1, background: "var(--border)" }} />
      <div
        className="absolute bottom-0 top-0"
        style={{
          background: pos ? "var(--bid)" : "var(--ask)",
          width: `${pct}%`,
          left: pos ? "50%" : `${50 - pct}%`,
        }}
      />
    </div>
  );
}

function FactorRow({ f }: { f: SmartMoneyFactor }) {
  const pos = f.value >= 0;
  return (
    <li className="flex flex-col gap-1.5 px-4 py-3" style={{ borderBottom: "1px solid var(--border)" }}>
      <div className="flex flex-wrap items-baseline gap-x-2">
        <span className="text-[0.8rem] font-bold" style={{ color: "var(--text)" }}>
          {f.label}
        </span>
        <span className="chip" style={{ color: "var(--faint)" }}>
          {f.source}
        </span>
        <span className="tnum ml-auto text-[0.8rem] font-bold" style={{ color: pos ? "var(--bid)" : "var(--ask)" }}>
          {pos ? "+" : ""}
          {f.value.toFixed(2)}
        </span>
        <span className="tnum text-[0.7rem]" style={{ color: "var(--faint)" }}>
          {(f.weight * 100).toFixed(0)}% of score
        </span>
      </div>
      <FactorBar value={f.value} />
      <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        {f.line}
      </p>
    </li>
  );
}

function StatTile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="panel px-3 py-2.5">
      <div className="text-[0.65rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
        {label}
      </div>
      <div className="tnum text-[0.95rem] font-bold" style={{ color: "var(--text)" }}>
        {value}
      </div>
      {hint ? (
        <div className="text-[0.65rem]" style={{ color: "var(--dim)" }}>
          {hint}
        </div>
      ) : null}
    </div>
  );
}

// ── per-symbol decomposition panel ──────────────────────────────────────────
function SymbolDetail({ symbol }: { symbol: string }) {
  const [data, setData] = useState<SmartMoneyResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      smartMoney(symbol)
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          // Exact-ticker API: unknown/partial symbol 404s — a filter miss, not
          // an outage. Show an honest "not tracked" empty state, not an error.
          if (msg.includes("404")) {
            setData({
              available: false,
              symbol,
              caveat: "",
              reason: "not a tracked symbol — smart-money scoring is universe-scoped",
            });
            setErr(null);
            return;
          }
          setErr(msg);
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, retryTick]);

  if (data === null && err === null) return <Skeleton lines={5} label={`loading ${symbol} smart money`} />;
  if (data === null && err !== null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? The smart-money-scorer refreshes every hour."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  }
  if (data && !data.available) {
    return (
      <EmptyState
        message={`No smart-money score for ${symbol} yet`}
        detail={
          data.reason ??
          "no insider, short, or institutional data for this symbol yet — the scorer stores nothing rather than a fabricated neutral"
        }
      />
    );
  }
  if (!data) return null;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        {data.symbol} SMART MONEY SCORE
        <span
          className="chip tnum"
          style={{ color: labelColor(data.label), borderColor: labelColor(data.label) }}
        >
          {data.score !== undefined ? `${data.score >= 0 ? "+" : ""}${data.score.toFixed(2)}` : "—"} ·{" "}
          {labelText(data.label)}
        </span>
        {data.ts ? (
          <span className="chip ml-auto tnum" style={{ color: "var(--faint)" }}>
            scored {ago(data.ts)}
          </span>
        ) : null}
      </div>

      {/* caveat, verbatim + prominent */}
      {data.caveat ? (
        <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
          {data.caveat}
        </p>
      ) : null}

      {data.factors && data.factors.length > 0 ? (
        <ul style={{ borderTop: "1px solid var(--border)" }}>
          {data.factors.map((f) => (
            <FactorRow key={f.key} f={f} />
          ))}
        </ul>
      ) : (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          score present but no factor breakdown stored.
        </p>
      )}

      {/* raw positioning tiles */}
      <div className="grid grid-cols-2 gap-2 px-4 py-3 sm:grid-cols-3">
        <StatTile
          label="distinct insider buyers"
          value={tile(data.insiderCluster?.distinctBuyers, 0)}
          hint={`net ${fmtUSD(data.insiderCluster?.netValue ?? 0)} over ${data.insiderCluster?.windowDays ?? 90}d`}
        />
        <StatTile label="days to cover" value={tile(data.squeeze?.daysToCover, 1)} hint="FINRA short interest" />
        <StatTile
          label="short-vol z"
          value={data.squeeze?.shortVolZ != null ? `${data.squeeze.shortVolZ >= 0 ? "+" : ""}${data.squeeze.shortVolZ.toFixed(1)}σ` : "—"}
          hint="vs own 30d (Reg SHO)"
        />
        {data.market === "crypto" ? (
          <StatTile label="perp funding" value={fmtFunding(data.squeeze?.funding)} hint="Hyperliquid" />
        ) : null}
      </div>
    </section>
  );
}

// ── accumulation leaderboard ────────────────────────────────────────────────
function Leaderboard({ onPick }: { onPick: (symbol: string) => void }) {
  const [data, setData] = useState<SmartMoneyTopResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      smartMoneyTop(undefined, 50)
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

  const rows = useMemo(() => data?.rows ?? [], [data]);

  if (data === null && err === null) return <Skeleton lines={6} label="loading smart-money leaderboard" />;
  if (data === null && err !== null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? The smart-money-scorer refreshes every hour."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  }
  if (data && !data.available) {
    return (
      <EmptyState
        message="No smart-money scores stored yet"
        detail={
          data.reason ??
          "the smart-money-scorer runs every hour and needs insider/short/institutional data for at least one tracked symbol"
        }
      />
    );
  }

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        ACCUMULATION LEADERBOARD
        <span className="chip tnum">{rows.length} scored</span>
        <span className="chip" style={{ color: "var(--faint)" }}>
          most accumulation first
        </span>
      </div>
      <ul style={{ borderTop: "1px solid var(--border)" }}>
        {rows.map((r) => (
          <li key={`${r.market}:${r.symbol}`} style={{ borderBottom: "1px solid var(--border)" }}>
            <button
              type="button"
              onClick={() => onPick(r.symbol)}
              className="flex w-full flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-left text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)]"
            >
              <span className="tnum w-20 font-bold" style={{ color: "var(--text)" }}>
                {r.symbol}
              </span>
              <span className="chip" style={{ color: "var(--faint)" }}>
                {r.market}
              </span>
              <span
                className="tnum font-bold"
                style={{ color: r.score >= 0 ? "var(--bid)" : "var(--ask)" }}
              >
                {r.score >= 0 ? "+" : ""}
                {r.score.toFixed(2)}
              </span>
              <span className="chip" style={{ color: labelColor(r.label), borderColor: labelColor(r.label) }}>
                {labelText(r.label)}
              </span>
              {r.topFactor ? (
                <span style={{ color: "var(--faint)" }}>· {r.topFactor}</span>
              ) : null}
              <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
                {ago(r.ts)}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

export default function SmartMoneyPage() {
  const { symbol, setSymbol } = useIntelSymbol();

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">SMART MONEY</h1>
        <span className="chip">positioning read · not a forecast</span>
      </div>

      <PagePurpose
        id="intel-smart-money"
        text="What are informed participants DOING — buying, shorting, holding? This blends open-market insider buys (Form 4), short interest and short-volume, crypto perp funding, and 13F ownership into ONE decomposed score. It reads POSITIONING, not price: it is NOT a forecast, and every part shows its source and limits."
      />

      {symbol ? (
        <SymbolDetail symbol={symbol} />
      ) : (
        <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Pick a symbol from the leaderboard below (or the shared intel filter above) to see its full
          decomposition — insider, squeeze, and institutional factors, each with its evidence line and
          filing source.
        </p>
      )}

      {symbol ? (
        <button
          type="button"
          onClick={() => setSymbol("")}
          className="chip w-fit min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
        >
          ← back to the leaderboard
        </button>
      ) : null}

      <Leaderboard onPick={setSymbol} />
    </div>
  );
}
