"use client";

import { useEffect, useMemo, useState } from "react";
import {
  smartMoney,
  smartMoneyTop,
  pollMs,
  POLL_SLOW,
  type SmartMoneyResponse,
  type SmartMoneyTopResponse,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import SortHeader from "@/components/SortHeader";
import {
  StatTile,
  PageHero,
  MiniBar,
} from "@/components/ui/Kit";

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

function FactorBar({ value }: { value: number }) {
  const pct = Math.min(Math.abs(value), 1) * 50;
  const pos = value >= 0;
  return (
    <div
      className="relative h-2 w-full overflow-hidden rounded"
      style={{ background: "var(--panel3)" }}
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

// SortIcon removed — SortHeader owns the arrow now.

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
        retry={() => { setErr(null); setRetryTick((t) => t + 1); }}
      />
    );
  }
  if (data && !data.available) {
    return (
      <EmptyState
        message={`No smart-money score for ${symbol} yet`}
        detail={data.reason ?? "no insider, short, or institutional data for this symbol yet — the scorer stores nothing rather than a fabricated neutral"}
      />
    );
  }
  if (!data) return null;

  return (
    <section className="panel hud-panel">
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

      {data.caveat ? (
        <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
          {data.caveat}
        </p>
      ) : null}

      {data.factors && data.factors.length > 0 ? (
        <ul style={{ borderTop: "1px solid var(--border)" }}>
          {data.factors.map((f) => (
            <li key={f.key} className="reveal-item flex flex-col gap-1.5 px-4 py-3" style={{ borderBottom: "1px solid var(--border)" }}>
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="text-[0.8rem] font-bold" style={{ color: "var(--text)" }}>
                  {f.label}
                </span>
                <span className="chip" style={{ color: "var(--faint)" }}>
                  {f.source}
                </span>
                <span className="tnum ml-auto text-[0.8rem] font-bold" style={{ color: f.value >= 0 ? "var(--bid)" : "var(--ask)" }}>
                  {f.value >= 0 ? "+" : ""}{f.value.toFixed(2)}
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
          ))}
        </ul>
      ) : (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          score present but no factor breakdown stored.
        </p>
      )}

      <div className="grid grid-cols-2 gap-2 px-4 py-3 sm:grid-cols-3">
        {/* No `?? 0` on any of these. The types say what the nulls mean —
            daysToCover is "no short-interest row", shortVolZ is "below the z
            gate", funding is "no fresh perp snapshot" — and this grid renders
            whenever data.available is true, which a symbol reaches on insider
            or institutional data alone. So a symbol with no short-interest row
            at all was showing "Days to cover 0.0 · FINRA short interest": a
            specific reading, attributed to a named regulator, that no regulator
            published. Line 134 of this file says the scorer "stores nothing
            rather than a fabricated neutral" — this is the display half of it. */}
        <StatTile
          label="Insider buyers"
          value={data.insiderCluster?.distinctBuyers}
          sub={data.insiderCluster ? `net ${fmtUSD(data.insiderCluster.netValue)} over ${data.insiderCluster.windowDays}d` : undefined}
        />
        <StatTile
          label="Days to cover"
          value={data.squeeze?.daysToCover}
          decimals={1}
          sub="FINRA short interest"
        />
        <StatTile
          label="Short-vol z"
          value={data.squeeze?.shortVolZ}
          decimals={1}
          suffix="σ"
          sub="vs own 30d (Reg SHO)"
        />
        {data.market === "crypto" ? (
          <StatTile
            label="Perp funding"
            value={data.squeeze?.funding}
            decimals={4}
            suffix="%/hr"
            sub="Hyperliquid"
          />
        ) : null}
      </div>
    </section>
  );
}

function Leaderboard({ onPick }: { onPick: (symbol: string) => void }) {
  const [data, setData] = useState<SmartMoneyTopResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [sortKey, setSortKey] = useState<"score" | "symbol" | "ts">("score");
  const [sortAsc, setSortAsc] = useState(false);

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
    return () => { alive = false; stop(); };
  }, [retryTick]);

  const rows = useMemo(() => {
    const list = data?.rows ?? [];
    return [...list].sort((a, b) => {
      if (sortKey === "symbol") return sortAsc ? a.symbol.localeCompare(b.symbol) : b.symbol.localeCompare(a.symbol);
      if (sortKey === "ts") return sortAsc ? a.ts - b.ts : b.ts - a.ts;
      return sortAsc ? a.score - b.score : b.score - a.score;
    });
  }, [data, sortKey, sortAsc]);

  const maxScore = useMemo(() => {
    if (rows.length === 0) return 1;
    return Math.max(...rows.map((r) => Math.abs(r.score)));
  }, [rows]);

  if (data === null && err === null) return <Skeleton lines={6} label="loading smart-money leaderboard" />;
  if (data === null && err !== null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? The smart-money-scorer refreshes every hour."
        retry={() => { setErr(null); setRetryTick((t) => t + 1); }}
      />
    );
  }
  if (data && !data.available) {
    return (
      <EmptyState
        message="No smart-money scores stored yet"
        detail={data.reason ?? "the smart-money-scorer runs every hour and needs insider/short/institutional data for at least one tracked symbol"}
      />
    );
  }

  const toggleSort = (key: "score" | "symbol" | "ts") => {
    if (sortKey === key) setSortAsc(!sortAsc);
    else { setSortKey(key); setSortAsc(key === "symbol"); }
  };

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        ACCUMULATION LEADERBOARD
        <span className="chip tnum">{rows.length} scored</span>
        <span className="chip" style={{ color: "var(--faint)" }}>
          sorted by {sortKey === "score" ? "score" : sortKey === "symbol" ? "symbol" : "date"} {sortAsc ? "↑" : "↓"}
        </span>
      </div>
      <div className="table-wrap" style={{ borderTop: "1px solid var(--border)" }}>
        <table className="v4-table">
          <thead>
            {/* onClick on the <th> made sorting mouse-only and silent. */}
            <tr>
              <SortHeader
                label="Symbol"
                active={sortKey === "symbol"}
                dir={sortAsc ? "asc" : "desc"}
                onSort={() => toggleSort("symbol")}
              />
              <SortHeader
                label="Score"
                active={sortKey === "score"}
                dir={sortAsc ? "asc" : "desc"}
                onSort={() => toggleSort("score")}
              />
              <th>Label</th>
              <th>Signal</th>
              <SortHeader
                label="Updated"
                active={sortKey === "ts"}
                dir={sortAsc ? "asc" : "desc"}
                onSort={() => toggleSort("ts")}
              />
            </tr>
          </thead>
          <tbody>
            {/* No <Reveal> here. Its wrapper is a <div>, and a <div> is not a
                legal child of <tbody> (nor is <tr> a legal child of a <div>) —
                React reported a hydration error and the browser hoists the div
                out of the table. Each <tr> already carries `reveal-item`, whose
                CSS keyframes stagger the rows on their own, so the wrapper was
                buying nothing here anyway. */}
              {rows.map((r, i) => (
                <tr
                  key={`${r.market}:${r.symbol}`}
                  className={`reveal-item cursor-pointer hover:bg-[var(--panel3)] ${r.score >= 0 ? "border-l-2 border-l-[var(--bid)]" : "border-l-2 border-l-[var(--ask)]"}`}
                  style={{ "--i": Math.min(i, 12) } as React.CSSProperties}
                  onClick={() => onPick(r.symbol)}
                >
                  {/* The row click is a mouse convenience; the real control is
                      this button. Before it existed the row was the ONLY way
                      to pick a symbol here, so the whole table was unusable
                      without a mouse. */}
                  <td className="mono font-bold">
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        onPick(r.symbol);
                      }}
                      aria-label={`Filter to ${r.symbol}`}
                      className="inline-flex min-h-[40px] cursor-pointer items-center font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                    >
                      {r.symbol}
                    </button>
                  </td>
                  <td className="tnum">
                    <span className="mr-2">{r.score >= 0 ? "+" : ""}{r.score.toFixed(2)}</span>
                    <MiniBar value={Math.abs(r.score)} max={maxScore} color={r.score >= 0 ? "var(--bid)" : "var(--ask)"} i={i} />
                  </td>
                  <td>
                    <span className="chip" style={{ color: labelColor(r.label), borderColor: labelColor(r.label) }}>
                      {labelText(r.label)}
                    </span>
                  </td>
                  <td className="truncate max-w-[120px]" title={r.topFactor ?? ""}>{r.topFactor ?? "—"}</td>
                  <td className="tnum" style={{ color: "var(--faint)" }}>{ago(r.ts)}</td>
                </tr>
              ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export default function SmartMoneyPage() {
  const { symbol, setSymbol } = useIntelSymbol();

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Smart Money"
        subtitle="The composite of what informed traders are doing - insiders, institutions and congress in one view."
        right={
          <div className="flex items-center gap-2">
            <span className="chip">positioning read · not a forecast</span>
            {symbol ? (
              <button
                type="button"
                onClick={() => setSymbol("")}
                className="chip cursor-pointer hover:text-[var(--text)]"
              >
                ← back to leaderboard
              </button>
            ) : null}
          </div>
        }
      />

      {symbol ? (
        <SymbolDetail symbol={symbol} />
      ) : (
        // Removed: a static <StatTile label="Symbols tracked" value={0}
        // sub="Loading…" />. Nothing ever wrote to it, so the landing view
        // permanently read "SYMBOLS TRACKED 0 / Loading…" directly above a
        // leaderboard listing real scored symbols. A count nobody computes is
        // better absent than wrong.
        null
      )}

      {!symbol && <Leaderboard onPick={setSymbol} />}
    </div>
  );
}