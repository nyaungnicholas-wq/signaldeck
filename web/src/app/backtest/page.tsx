"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  type BacktestResponse,
  type Market,
  type WatchRow,
} from "@/lib/api";
import { fmtPct, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

const EXAMPLES = [
  "50/200 moving-average crossover",
  "buy when RSI below 30, sell above 70",
  "buy above the 200-day, sell below",
];

/** The daemon serializes the full Strategy struct; the contract type only
 * declares Name, so widen locally to read the honest round-trip cost. */
type StrategyMeta = { Name: string; CostBps?: number };

/** A hard error is the daemon being unreachable; a soft error is a strategy the
 * parser couldn't handle or a symbol without enough history. Soft errors are the
 * API's helpful, expected feedback — shown in --warn, never as a crash. */
function isSoftError(msg: string): boolean {
  const m = msg.toLowerCase();
  return (
    m.includes("supported forms") ||
    m.includes("could not parse") ||
    m.includes("not enough history") ||
    m.includes("unknown symbol")
  );
}

function fmtBps(bps: number): string {
  if (!isFinite(bps)) return "—";
  return Number.isInteger(bps) ? `${bps} bps` : `${bps.toFixed(1)} bps`;
}

/** Full-width equity-curve line chart. Green when the strategy ended above its
 * 1.0 starting equity, red when below. A faint baseline marks break-even (1.0).
 * X is aligned to the supplied bar timestamps for the axis labels only. */
function EquityCurve({ equity, ts }: { equity: number[]; ts: number[] }) {
  const W = 1000;
  const H = 260;
  const padX = 4;
  const padY = 10;

  const geom = useMemo(() => {
    if (!equity || equity.length < 2) return null;
    let min = Infinity;
    let max = -Infinity;
    for (const v of equity) {
      if (!isFinite(v)) continue;
      if (v < min) min = v;
      if (v > max) max = v;
    }
    if (!isFinite(min) || !isFinite(max)) return null;
    // Always include the 1.0 baseline in the visible range.
    min = Math.min(min, 1);
    max = Math.max(max, 1);
    const span = max - min || 1;
    const x = (i: number) =>
      padX + (i / (equity.length - 1)) * (W - 2 * padX);
    const y = (v: number) =>
      padY + (1 - (v - min) / span) * (H - 2 * padY);
    const pts = equity
      .map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`)
      .join(" ");
    const baselineY = y(1);
    const up = equity[equity.length - 1] >= 1;
    return { x, y, pts, baselineY, up, min, max };
  }, [equity]);

  if (!geom) {
    return (
      <div
        className="px-4 py-8 text-center text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        equity curve unavailable — need at least two bars.
      </div>
    );
  }

  const stroke = geom.up ? "var(--bid)" : "var(--ask)";
  const fill = geom.up ? "var(--bid-dim)" : "var(--ask-dim)";
  const areaPts = `${padX.toFixed(1)},${geom.baselineY.toFixed(1)} ${geom.pts} ${(W - padX).toFixed(1)},${geom.baselineY.toFixed(1)}`;
  const first = ts.length ? ts[0] : 0;
  const last = ts.length ? ts[ts.length - 1] : 0;

  return (
    <div className="px-4 py-4">
      <div className="overflow-x-auto">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          width="100%"
          height={H}
          preserveAspectRatio="none"
          role="img"
          aria-label={`equity curve, ${geom.up ? "ended above" : "ended below"} break-even`}
          style={{ display: "block" }}
        >
          {/* break-even baseline at equity = 1.0 */}
          <line
            x1={padX}
            x2={W - padX}
            y1={geom.baselineY}
            y2={geom.baselineY}
            stroke="var(--faint)"
            strokeWidth={1}
            strokeDasharray="4 4"
            vectorEffect="non-scaling-stroke"
          />
          <polygon points={areaPts} fill={fill} stroke="none" />
          <polyline
            points={geom.pts}
            fill="none"
            stroke={stroke}
            strokeWidth={2}
            vectorEffect="non-scaling-stroke"
            strokeLinejoin="round"
          />
        </svg>
      </div>
      <div
        className="tnum mt-2 flex justify-between text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        <span>{first ? fmtDate(first) : "start"}</span>
        <span
          style={{ color: geom.up ? "var(--bid)" : "var(--ask)" }}
        >
          equity 1.00 → {equity[equity.length - 1].toFixed(3)}
        </span>
        <span>{last ? fmtDate(last) : "end"}</span>
      </div>
    </div>
  );
}

function Metric({
  label,
  value,
  color,
  hint,
  title,
}: {
  label: string;
  value: string;
  color?: string;
  hint?: string;
  title?: string;
}) {
  return (
    <div
      className="flex flex-col gap-1 px-4 py-3"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <span
        className="text-[0.75rem] tracking-wide"
        style={{ color: "var(--faint)" }}
        title={title}
      >
        {label}
      </span>
      <span
        className="tnum text-[0.95rem] font-bold"
        style={{ color: color ?? "var(--text)" }}
      >
        {value}
      </span>
      {hint && (
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {hint}
        </span>
      )}
    </div>
  );
}

export default function BacktestPage() {
  // symbol universe (for the picker chips)
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);

  // form state
  const [text, setText] = useState("");
  const [symbol, setSymbol] = useState<string | null>(null);
  const [market, setMarket] = useState<Market>("crypto");

  // run state
  const [running, setRunning] = useState(false);
  const [resp, setResp] = useState<BacktestResponse | null>(null);
  const [ranFor, setRanFor] = useState<{ symbol: string; text: string } | null>(
    null,
  );
  const [softErr, setSoftErr] = useState<string | null>(null);
  const [hardErr, setHardErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // Load the watchlist once for the symbol picker; poll so newly-subscribed
  // symbols appear. The picker never mutates the running result.
  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .watchlist()
        .then((r) => {
          if (!alive) return;
          setWatch(r);
          setWatchErr(null);
          setSymbol((cur) => {
            if (cur && r.some((row) => row.symbol === cur)) return cur;
            return r.length ? r[0].symbol : null;
          });
          setMarket((curMkt) => {
            // keep market in sync with the auto-selected symbol on first load
            const first = r[0];
            return first && watch === null ? first.market : curMkt;
          });
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setWatchErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [retryTick]);

  const selectedRow = useMemo(
    () => watch?.find((r) => r.symbol === symbol && r.market === market) ?? null,
    [watch, symbol, market],
  );

  const run = () => {
    const t = text.trim();
    if (!symbol || !t || running) return;
    setRunning(true);
    setSoftErr(null);
    setHardErr(null);
    api
      .backtest(symbol, market, t)
      .then((r) => {
        setResp(r);
        setRanFor({ symbol, text: t });
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        if (isSoftError(msg)) {
          setSoftErr(msg);
        } else {
          setHardErr(msg);
        }
        setResp(null);
        setRanFor(null);
      })
      .finally(() => setRunning(false));
  };

  const result = resp?.result ?? null;
  const strat = (resp?.strategy ?? null) as StrategyMeta | null;
  const costBps = strat?.CostBps;
  const lowConfidence =
    result !== null && result.NumTrades > 0 && result.NumTrades < 10;
  const noTrades = result !== null && result.NumTrades === 0;

  const canRun = Boolean(symbol) && text.trim().length > 0 && !running;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">BACKTEST</h1>
        <span className="chip">CopilotQuant</span>
        <span className="chip">next-bar fills · no lookahead</span>
        {ranFor && (
          <span className="chip tnum">
            {ranFor.symbol} · {market}
          </span>
        )}
        {watchErr !== null && watch !== null && (
          <span
            className="chip"
            style={{ color: "var(--bad)", borderColor: "var(--bad)" }}
          >
            watchlist poll failed
          </span>
        )}
      </div>

      {/* strategy composer */}
      <section className="panel">
        <div className="panel-h">STRATEGY — PLAIN ENGLISH</div>
        <div className="flex flex-col gap-4 px-4 py-4">
          <label className="flex flex-col gap-1.5">
            <span
              className="text-[0.75rem] tracking-wide"
              style={{ color: "var(--faint)" }}
            >
              describe the rules
            </span>
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") run();
              }}
              rows={2}
              placeholder="e.g. 50/200 moving-average crossover"
              aria-label="strategy in plain English"
              className="w-full resize-y rounded border px-3 py-2.5 text-[0.85rem] leading-relaxed"
              style={{
                background: "var(--panel2)",
                borderColor: "var(--border)",
                color: "var(--text)",
              }}
            />
          </label>

          {/* example fillers */}
          <div className="flex flex-wrap items-center gap-1.5">
            <span
              className="text-[0.75rem]"
              style={{ color: "var(--faint)" }}
            >
              try
            </span>
            {EXAMPLES.map((ex) => (
              <button
                key={ex}
                type="button"
                onClick={() => setText(ex)}
                className="chip cursor-pointer transition-colors duration-150 hover:brightness-125"
                style={{ borderColor: "var(--border)" }}
              >
                {ex}
              </button>
            ))}
          </div>

          {/* symbol picker */}
          <div className="flex flex-col gap-1.5">
            <span
              className="text-[0.75rem] tracking-wide"
              style={{ color: "var(--faint)" }}
            >
              symbol
            </span>
            {watch === null && watchErr === null && (
              <Skeleton lines={2} label="loading symbols" className="border-0 p-0" />
            )}
            {watch !== null && watch.length === 0 && (
              <EmptyState
                className="border-0 p-0"
                message="Your watchlist is empty"
                detail="Subscribe to symbols first, then a backtest has bars to run on."
              />
            )}
            {watchErr !== null && watch === null && (
              <ErrorState
                className="border-0 p-0"
                message="could not load symbols"
                hint="is the daemon running? start it with signaldeckd."
                retry={() => {
                  setWatchErr(null);
                  setRetryTick((t) => t + 1);
                }}
              />
            )}
            {watch !== null && watch.length > 0 && (
              <div
                className="flex flex-wrap gap-1.5"
                role="group"
                aria-label="symbol picker"
              >
                {watch.map((row) => {
                  const active =
                    row.symbol === symbol && row.market === market;
                  return (
                    <button
                      key={`${row.market}:${row.symbol}`}
                      type="button"
                      aria-pressed={active}
                      onClick={() => {
                        setSymbol(row.symbol);
                        setMarket(row.market);
                      }}
                      className="chip cursor-pointer tnum transition-colors duration-150 hover:brightness-125"
                      style={{
                        color: active ? "var(--text)" : "var(--dim)",
                        borderColor: active ? "var(--accent)" : "var(--border)",
                        background: active
                          ? "rgba(251,191,36,.08)"
                          : "var(--panel2)",
                      }}
                    >
                      {row.symbol}
                      <span
                        className="ml-1.5 text-[0.75rem]"
                        style={{ color: "var(--faint)" }}
                      >
                        {row.market}
                      </span>
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          {/* run */}
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="button"
              onClick={run}
              disabled={!canRun}
              className="min-h-[40px] cursor-pointer rounded border px-5 py-2 text-[0.78rem] font-bold tracking-wide transition-colors duration-150 disabled:cursor-not-allowed"
              style={{
                borderColor: canRun ? "var(--accent)" : "var(--border)",
                background: canRun ? "rgba(251,191,36,.10)" : "var(--panel2)",
                color: canRun ? "var(--accent)" : "var(--faint)",
              }}
            >
              {running ? "running…" : "Run backtest"}
            </button>
            <span
              className="text-[0.75rem]"
              style={{ color: "var(--faint)" }}
            >
              ⌘/Ctrl+Enter to run
              {selectedRow && (
                <>
                  {" · "}
                  {selectedRow.symbol} last {fmtPct(selectedRow.dayChangePct)}{" "}
                  on the day
                </>
              )}
            </span>
          </div>

          {/* soft (parse / history) feedback — the API's helpful message */}
          {softErr && (
            <div
              className="rounded border px-3 py-2.5 text-[0.78rem] leading-relaxed whitespace-pre-wrap"
              style={{
                color: "var(--warn)",
                borderColor: "var(--warn)",
                background: "rgba(251,191,36,.06)",
              }}
            >
              {softErr}
            </div>
          )}

          {/* hard (daemon down) error */}
          {hardErr && (
            <ErrorState
              className="border-0 p-0"
              message={hardErr}
              hint="is the daemon running? start it with signaldeckd, then retry."
              retry={run}
            />
          )}
        </div>
      </section>

      {/* results */}
      {result && resp && ranFor && (
        <>
          {/* headline read: the Explain string */}
          <section className="panel">
            <div className="panel-h">
              THE READ
              <span className="tnum" style={{ color: "var(--faint)" }}>
                {ranFor.symbol} · {(strat?.Name ?? ranFor.text).slice(0, 64)}
              </span>
            </div>
            <div className="px-4 py-4">
              <p
                className="text-[0.85rem] leading-relaxed"
                style={{ color: "var(--text)" }}
              >
                {resp.explain}
              </p>
              {/* honesty chips */}
              <div className="mt-3 flex flex-wrap gap-1.5">
                <span className="chip">next-bar fills, no lookahead</span>
                <span className="chip tnum">
                  round-trip cost{" "}
                  {costBps !== undefined ? fmtBps(costBps) : "—"} / side
                </span>
                <span className="chip tnum">
                  {result.NumTrades} trade{result.NumTrades === 1 ? "" : "s"}
                </span>
                <span
                  className="chip"
                  style={{
                    color:
                      result.VsBuyHold > 0
                        ? "var(--bid)"
                        : result.VsBuyHold < 0
                          ? "var(--ask)"
                          : "var(--dim)",
                    borderColor:
                      result.VsBuyHold > 0
                        ? "var(--bid)"
                        : result.VsBuyHold < 0
                          ? "var(--ask)"
                          : "var(--border)",
                  }}
                >
                  {result.VsBuyHold > 0
                    ? "beat buy & hold"
                    : result.VsBuyHold < 0
                      ? "lagged buy & hold"
                      : "matched buy & hold"}
                </span>
                {lowConfidence && (
                  <span
                    className="chip"
                    style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                  >
                    few trades — low confidence
                  </span>
                )}
                {noTrades && (
                  <span
                    className="chip"
                    style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                  >
                    no trades fired — nothing to conclude
                  </span>
                )}
                <span
                  className="chip"
                  style={{ color: "var(--faint)" }}
                >
                  in-sample — hypothesis, not proof
                </span>
              </div>
            </div>
          </section>

          {/* metrics grid */}
          <section className="panel">
            <div className="panel-h">
              PERFORMANCE
              <span className="tnum" style={{ color: "var(--faint)" }}>
                returns as fractions of starting equity (1.00)
              </span>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4">
              <Metric
                label="VS BUY & HOLD"
                value={fmtPct(result.VsBuyHold * 100)}
                color={
                  result.VsBuyHold > 0
                    ? "var(--bid)"
                    : result.VsBuyHold < 0
                      ? "var(--ask)"
                      : "var(--dim)"
                }
                hint="strategy return minus passive holding, after costs"
              />
              <Metric
                label="TOTAL RETURN"
                value={fmtPct(result.TotalReturn * 100)}
                color={result.TotalReturn >= 0 ? "var(--bid)" : "var(--ask)"}
              />
              <Metric
                label="CAGR"
                title="Compound annual growth rate"
                value={fmtPct(result.CAGR * 100)}
                color={result.CAGR >= 0 ? "var(--bid)" : "var(--ask)"}
                hint="assumes ~252 bars/yr"
              />
              <Metric
                label="MAX DRAWDOWN"
                title="Largest peak-to-trough decline in equity"
                value={fmtPct(-result.MaxDrawdown * 100)}
                color="var(--ask)"
                hint="worst peak-to-trough"
              />
              <Metric
                label="SHARPE"
                title="Sharpe ratio (risk-adjusted return)"
                value={
                  isFinite(result.Sharpe) ? result.Sharpe.toFixed(2) : "—"
                }
                hint="rf=0, annualized; read as relative"
              />
              <Metric
                label="NUM TRADES"
                value={String(result.NumTrades)}
                color={lowConfidence || noTrades ? "var(--warn)" : "var(--text)"}
                hint={
                  noTrades
                    ? "nothing fired"
                    : lowConfidence
                      ? "low confidence"
                      : undefined
                }
              />
              <Metric
                label="WIN RATE"
                value={fmtPct(result.WinRate * 100, false)}
                hint="of closed trades"
              />
              <Metric
                label="EXPOSURE"
                value={fmtPct(result.ExposurePct * 100, false)}
                hint="share of bars in the market"
              />
            </div>
          </section>

          {/* equity curve */}
          <section className="panel">
            <div className="panel-h">
              EQUITY CURVE
              <span className="tnum" style={{ color: "var(--faint)" }}>
                growth of 1.00 · dashed line = break-even
              </span>
            </div>
            <EquityCurve equity={result.Equity} ts={resp.ts} />
          </section>
        </>
      )}

      {/* static explainer — always visible so the method is never hidden */}
      <section className="panel">
        <div className="panel-h">HOW THIS BACKTEST STAYS HONEST</div>
        <div
          className="px-4 py-4 text-[0.76rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          Signals compute on each bar using only prior data and fill at the NEXT
          bar&apos;s open. Costs are charged per trade. No survivorship beyond
          what is stored. The number is honest, not flattering.
        </div>
      </section>
    </div>
  );
}
