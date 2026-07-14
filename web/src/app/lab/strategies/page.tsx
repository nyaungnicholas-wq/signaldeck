"use client";

// LAB → STRATEGIES — the strategy lab (/api/strategy-lab): 8 classic
// PUBLISHED strategies, backtested walk-forward on our own bars with costs.
// The fleet table says which published rule actually survives our data
// (median Sharpe, % of symbols profitable, median total return); the symbol
// picker drills into one symbol's per-strategy rows with the engine's own
// honesty flags honored — a gated CAGR or win rate renders "n/a" with the
// stated reason, exactly like /lab/backtest. The API note renders verbatim.

import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  POLL_DEFAULT,
  POLL_SLOW,
  screenerRows,
  strategyLab,
  type Market,
  type StrategyFleetAgg,
  type StrategyResultRow,
  type WatchRow,
} from "@/lib/api";
import { fmtPct } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import { useViewMode } from "@/components/Plain";

interface Selected {
  symbol: string;
  market: Market;
}

/** Plain-English label + citation for each engine strategy key. The tooltip
 *  carries the published source — measured, not marketed. */
const STRATEGY_META: Record<string, { label: string; cite: string }> = {
  sma_cross_50_200: {
    label: "golden cross (50/200)",
    cite: "The classic golden-cross trend filter — long while the 50-day SMA sits above the 200-day. Technical-analysis canon (Edwards & Magee lineage).",
  },
  donchian_20: {
    label: "Donchian 20 breakout",
    cite: "Richard Donchian's 20-day channel breakout — the rule behind the 1980s Turtle traders.",
  },
  rsi2_meanrev: {
    label: "RSI-2 mean reversion",
    cite: "Larry Connors' RSI(2) pullback — buy short-term oversold inside a long-term uptrend.",
  },
  momentum_12_1: {
    label: "12-1 momentum",
    cite: "Jegadeesh & Titman (1993) — 12-month momentum skipping the most recent month.",
  },
  macd_trend: {
    label: "MACD trend",
    cite: "Gerald Appel's MACD (12/26/9) trend-following crossover.",
  },
  bollinger_meanrev: {
    label: "Bollinger mean reversion",
    cite: "John Bollinger's bands (20-day, 2σ) — fade closes below the lower band back to the mean.",
  },
  breakout_52w_high: {
    label: "52-week-high breakout",
    cite: "George & Hwang (2004) — proximity to the 52-week high carries momentum information.",
  },
  dual_momentum: {
    label: "dual momentum",
    cite: "Gary Antonacci's dual momentum — absolute (vs cash) plus relative momentum.",
  },
};

const stratLabel = (key: string) => STRATEGY_META[key]?.label ?? key;
const stratCite = (key: string) => STRATEGY_META[key]?.cite ?? `engine strategy "${key}"`;

const signColor = (v: number) =>
  v > 0 ? "var(--bid)" : v < 0 ? "var(--ask)" : "var(--dim)";

/** Age string computed once at fetch time — no Date.now() in render. */
function agoAtFetch(ts: number): string {
  if (!ts) return "—";
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** Per-symbol rows with the tested-age pre-rendered at fetch time. */
type SymRow = StrategyResultRow & { testedAgo: string };

/** One symbol's payload, keyed so switching symbols never shows stale rows. */
interface SymSlot {
  key: string;
  rows: SymRow[];
  emptyNote: string | null;
}

/** % PROFITABLE cell — bar fill + number, color not the only signal. */
function ProfitableBar({ frac }: { frac: number }) {
  const pct = Math.max(0, Math.min(1, frac)) * 100;
  return (
    <div className="flex items-center gap-2">
      <div
        className="h-2 w-20 overflow-hidden rounded-sm"
        style={{ background: "var(--panel2)" }}
        role="img"
        aria-label={`${pct.toFixed(0)}% of symbols profitable`}
      >
        <div
          className="h-full"
          style={{
            width: `${pct}%`,
            background: frac >= 0.5 ? "var(--bid)" : "var(--ask)",
            opacity: 0.7,
          }}
        />
      </div>
      <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
        {pct.toFixed(0)}%
      </span>
    </div>
  );
}

export default function StrategiesPage() {
  const mode = useViewMode();
  const [retryTick, setRetryTick] = useState(0);

  // fleet table — kept across symbol switches; only replaced by fresh data
  const [fleet, setFleet] = useState<StrategyFleetAgg[] | null>(null);
  const [fleetErr, setFleetErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  // watchlist for the symbol picker (same fallback chain as MODEL RACE:
  // 401/empty watchlist → strongest-scored public-universe symbols)
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [rowsErr, setRowsErr] = useState<string | null>(null);
  const [selected, setSelected] = useState<Selected | null>(null);

  // per-symbol rows, keyed by the selected symbol
  const [symSlot, setSymSlot] = useState<SymSlot | null>(null);
  const [symErrState, setSymErrState] = useState<{ key: string; msg: string } | null>(null);

  // Watchlist for the picker. Unlike MODEL RACE there is no auto-select:
  // the fleet table is the page's backbone and works without a symbol.
  useEffect(() => {
    let alive = true;
    const apply = (r: WatchRow[]) => {
      setRows(r);
      setRowsErr(null);
    };
    const applyScreenerTop = () =>
      screenerRows()
        .then((all) => {
          if (!alive) return;
          const top = [...all]
            .sort(
              (a, b) =>
                Math.abs(b.scores?.["1d"]?.score ?? 0) - Math.abs(a.scores?.["1d"]?.score ?? 0),
            )
            .slice(0, 24);
          apply(top);
        })
        .catch((e2: unknown) => {
          if (!alive) return;
          setRowsErr(e2 instanceof Error ? e2.message : String(e2));
        });
    const load = () =>
      api
        .watchlist()
        .then((r) => {
          if (!alive) return;
          if (r.some((x) => x.active)) {
            apply(r);
            return;
          }
          return applyScreenerTop();
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (!msg.includes("401")) {
            setRowsErr(msg);
            return;
          }
          return applyScreenerTop();
        });
    load();
    // POLL_DEFAULT tier — managed loop (hidden-tab pause, failure backoff).
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const selKey = selected ? `${selected.symbol}|${selected.market}` : "";

  // One poll serves both surfaces: with a symbol the payload carries the
  // fleet table AND that symbol's rows; without, the fleet table alone.
  useEffect(() => {
    let alive = true;
    const sel = selected;
    const key = sel ? `${sel.symbol}|${sel.market}` : "";
    const load = () =>
      strategyLab(sel?.symbol, sel?.market)
        .then((d) => {
          if (!alive) return;
          // Go nil slices arrive as JSON null — normalize every array.
          setFleet(d.fleet ?? []);
          setNote(d.note);
          setFleetErr(null);
          if (sel) {
            setSymSlot({
              key,
              rows: (d.strategies ?? []).map((r) => ({ ...r, testedAgo: agoAtFetch(r.ts) })),
              emptyNote: d.emptyNote ?? null,
            });
            setSymErrState(null);
          }
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          setFleetErr(msg);
          if (sel) setSymErrState({ key, msg });
        });
    load();
    // POLL_SLOW tier — the strategy-lab worker runs once per UTC day.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [selected, retryTick]);

  // Daemon already sorts by median Sharpe desc — re-assert deterministically.
  const fleetSorted = useMemo(
    () =>
      fleet === null
        ? null
        : [...fleet].sort(
            (a, b) => b.medianSharpe - a.medianSharpe || a.strategy.localeCompare(b.strategy),
          ),
    [fleet],
  );

  const activeRows = useMemo(() => (rows ? rows.filter((r) => r.active) : []), [rows]);

  const fleetLoading = fleet === null && fleetErr === null;
  const fleetHardError = fleet === null && fleetErr !== null;

  const symData = symSlot && symSlot.key === selKey ? symSlot : null;
  const symErr = symErrState && symErrState.key === selKey ? symErrState.msg : null;
  const symLoading = selected !== null && symData === null && symErr === null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">STRATEGY LAB</h1>
        {fleetSorted !== null && (
          <span className="chip tnum">{fleetSorted.length} strategies</span>
        )}
        {selected && <span className="chip mono">{selected.symbol}</span>}
        {fleetErr !== null && fleet !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="lab-strategies"
        text="8 classic published strategies, backtested nightly on our own bars with costs — measured, not marketed. The fleet table says which published rule actually survives our data; pick a symbol to see how each rule did there, honesty flags included."
      />

      {fleetLoading && <Skeleton lines={4} label="loading the strategy fleet" />}

      {fleetHardError && (
        <ErrorState
          message={fleetErr ?? "strategy lab unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setFleetErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {/* FLEET — per-strategy medians across every symbol tested */}
      {fleetSorted !== null && (
        <section className="panel">
          <div className="panel-h">
            FLEET
            <span
              className="text-[0.75rem] font-normal normal-case tracking-normal"
              style={{ color: "var(--faint)" }}
            >
              per-strategy medians across every symbol tested · sorted by median Sharpe
            </span>
          </div>
          {fleetSorted.length === 0 ? (
            <EmptyState
              className="m-4"
              message="No strategy results yet"
              detail="The strategy-lab worker runs once per UTC day over the streamed hot set + crypto — check back after its first pass."
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-[0.8rem]">
                <thead>
                  <tr
                    className="text-left text-[0.7rem] tracking-wider"
                    style={{ color: "var(--faint)" }}
                  >
                    <th className="px-4 py-2 font-normal">STRATEGY</th>
                    <th className="px-3 py-2 font-normal">MEDIAN SHARPE</th>
                    <th className="px-3 py-2 font-normal">% PROFITABLE</th>
                    <th className="px-3 py-2 font-normal">MEDIAN TOTAL RET</th>
                    <th className="px-3 py-2 font-normal">SYMBOLS</th>
                  </tr>
                </thead>
                <tbody>
                  {fleetSorted.map((f) => (
                    <tr
                      key={f.strategy}
                      className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ borderTop: "1px solid var(--border)" }}
                    >
                      <td className="px-4 py-2" title={stratCite(f.strategy)}>
                        <span className="font-bold">{stratLabel(f.strategy)}</span>
                        {mode === "pro" && (
                          <span className="mono ml-2 text-[0.7rem]" style={{ color: "var(--faint)" }}>
                            {f.strategy}
                          </span>
                        )}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: signColor(f.medianSharpe) }}>
                        {f.medianSharpe.toFixed(2)}
                      </td>
                      <td className="px-3 py-2">
                        <ProfitableBar frac={f.pctProfitable} />
                      </td>
                      <td
                        className="tnum px-3 py-2"
                        style={{ color: signColor(f.medianTotalRet) }}
                      >
                        {fmtPct(f.medianTotalRet * 100)}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: "var(--dim)" }}>
                        {f.nSymbols}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}

      {/* SYMBOL picker — same chip pattern as MODEL RACE */}
      <section className="panel">
        <div className="panel-h">
          SYMBOL
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            pick a symbol to see each strategy&apos;s result there
          </span>
        </div>

        {rows === null && rowsErr === null && (
          <Skeleton lines={2} label="loading watchlist" className="m-4" />
        )}

        {rows === null && rowsErr !== null && (
          <ErrorState
            className="m-4"
            message={rowsErr}
            hint="Is the daemon running? Start it with signaldeckd."
            retry={() => {
              setRowsErr(null);
              setRetryTick((t) => t + 1);
            }}
          />
        )}

        {rows !== null && activeRows.length === 0 && (
          <EmptyState
            className="m-4"
            message="No active symbols yet"
            detail="Subscribe to symbols on the watchlist page and the strategy-lab worker will pick them up."
          />
        )}

        {activeRows.length > 0 && (
          <div role="group" aria-label="symbol picker" className="flex flex-wrap gap-2 px-4 py-3">
            {activeRows.map((r) => {
              const active = selected?.symbol === r.symbol && selected?.market === r.market;
              return (
                <button
                  key={`${r.market}:${r.symbol}`}
                  type="button"
                  onClick={() => setSelected({ symbol: r.symbol, market: r.market })}
                  aria-pressed={active}
                  className="chip mono min-h-[40px] cursor-pointer transition-colors duration-150 hover:brightness-125"
                  style={{
                    color: active ? "var(--text)" : "var(--dim)",
                    borderColor: active ? "var(--accent)" : "var(--border)",
                    background: active ? "rgba(251,191,36,.08)" : "var(--panel2)",
                  }}
                >
                  {r.symbol}
                  <span className="ml-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {r.market}
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </section>

      {/* PER-SYMBOL rows — engine honesty flags honored, /lab/backtest style */}
      {selected && (
        <section className="panel">
          <div className="panel-h">
            {selected.symbol} — PER-STRATEGY RESULTS
            <span
              className="text-[0.75rem] font-normal normal-case tracking-normal"
              style={{ color: "var(--warn)" }}
            >
              backtested / in-sample — not a live track record
            </span>
          </div>

          {symLoading && (
            <Skeleton lines={4} label={`loading strategies for ${selected.symbol}`} className="m-4" />
          )}

          {selected !== null && symData === null && symErr !== null && (
            <ErrorState
              className="m-4"
              message={symErr}
              hint="Is the daemon running? Start it with signaldeckd."
              retry={() => {
                setSymErrState(null);
                setRetryTick((t) => t + 1);
              }}
            />
          )}

          {symData !== null && symData.rows.length === 0 && (
            <EmptyState
              className="m-4"
              message={`No strategy results for ${selected.symbol} yet`}
              detail={
                symData.emptyNote ??
                "the strategy-lab worker covers the streamed hot set + crypto once per UTC day"
              }
            />
          )}

          {symData !== null && symData.rows.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-[0.8rem]">
                <thead>
                  <tr
                    className="text-left text-[0.7rem] tracking-wider"
                    style={{ color: "var(--faint)" }}
                  >
                    <th className="px-4 py-2 font-normal">STRATEGY</th>
                    <th className="px-3 py-2 font-normal">TOTAL RET</th>
                    <th className="px-3 py-2 font-normal">CAGR</th>
                    <th className="px-3 py-2 font-normal">SHARPE</th>
                    <th className="px-3 py-2 font-normal">MAX DD</th>
                    <th className="px-3 py-2 font-normal">WIN RATE</th>
                    <th className="px-3 py-2 font-normal">TRADES</th>
                    <th className="px-3 py-2 font-normal">BARS</th>
                    <th className="px-3 py-2 font-normal">TESTED</th>
                  </tr>
                </thead>
                <tbody>
                  {symData.rows.map((r) => (
                    <tr
                      key={r.strategy}
                      className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ borderTop: "1px solid var(--border)" }}
                    >
                      <td className="px-4 py-2" title={stratCite(r.strategy)}>
                        <span className="font-bold">{stratLabel(r.strategy)}</span>
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: signColor(r.totalReturn) }}>
                        {fmtPct(r.totalReturn * 100)}
                      </td>
                      <td
                        className="tnum px-3 py-2"
                        style={{ color: r.cagrReported ? signColor(r.cagr) : "var(--dim)" }}
                        title={
                          r.cagrReported
                            ? "compound annual growth rate"
                            : `span too short/thin to annualize (${r.nBars} bars, ${r.nTrades} trades)`
                        }
                      >
                        {r.cagrReported ? fmtPct(r.cagr * 100) : "n/a (span too short)"}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: signColor(r.sharpe) }}>
                        {isFinite(r.sharpe) ? r.sharpe.toFixed(2) : "—"}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: "var(--ask)" }}>
                        {fmtPct(-r.maxDrawdown * 100)}
                      </td>
                      <td
                        className="tnum px-3 py-2"
                        style={{ color: r.winRateMeaningful ? undefined : "var(--dim)" }}
                        title={
                          r.winRateMeaningful
                            ? "share of closed trades net-positive"
                            : "too few closed trades — one trade is not a win rate"
                        }
                      >
                        {r.winRateMeaningful ? fmtPct(r.winRate * 100, false) : "n/a (too few trades)"}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: "var(--dim)" }}>
                        {r.nTrades}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: "var(--dim)" }}>
                        {r.nBars}
                      </td>
                      <td className="tnum px-3 py-2" style={{ color: "var(--faint)" }}>
                        {r.testedAgo}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}

      {/* API note — verbatim footer, the honesty contract in one line */}
      {note !== null && (
        <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {note}
        </p>
      )}
    </div>
  );
}
