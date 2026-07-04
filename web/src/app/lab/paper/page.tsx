"use client";

import { useEffect, useMemo, useState } from "react";
import {
  paper,
  pollMs,
  type PaperResponse,
  type PaperEquityPoint,
} from "@/lib/api";
import { fmtPct, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

/** USD formatter for book values. */
function usd(v: number): string {
  return v.toLocaleString("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  });
}
function usd2(v: number): string {
  return v.toLocaleString("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 2,
  });
}

/** Absolute-dollar equity curve. Green when the book ended above its starting
 *  cash, red when below; a faint dashed baseline marks the starting equity. */
function EquityCurve({
  curve,
  start,
}: {
  curve: PaperEquityPoint[];
  start: number;
}) {
  const W = 1000;
  const H = 260;
  const padX = 4;
  const padY = 10;

  const geom = useMemo(() => {
    if (!curve || curve.length < 2) return null;
    const eq = curve.map((p) => p.equity);
    let min = Infinity;
    let max = -Infinity;
    for (const v of eq) {
      if (!isFinite(v)) continue;
      if (v < min) min = v;
      if (v > max) max = v;
    }
    if (!isFinite(min) || !isFinite(max)) return null;
    // Always include the starting-equity baseline in the visible range.
    min = Math.min(min, start);
    max = Math.max(max, start);
    const span = max - min || 1;
    const x = (i: number) => padX + (i / (eq.length - 1)) * (W - 2 * padX);
    const y = (v: number) => padY + (1 - (v - min) / span) * (H - 2 * padY);
    const pts = eq.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
    const baselineY = y(start);
    const up = eq[eq.length - 1] >= start;
    return { pts, baselineY, up };
  }, [curve, start]);

  if (!geom) {
    return (
      <div
        className="px-4 py-8 text-center text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        equity curve unavailable — need at least two marks (the book marks once
        per new daily bar).
      </div>
    );
  }

  const stroke = geom.up ? "var(--bid)" : "var(--ask)";
  const fill = geom.up ? "var(--bid-dim)" : "var(--ask-dim)";
  const areaPts = `${padX.toFixed(1)},${geom.baselineY.toFixed(1)} ${geom.pts} ${(W - padX).toFixed(1)},${geom.baselineY.toFixed(1)}`;
  const first = curve[0].ts;
  const last = curve[curve.length - 1].ts;
  const lastEq = curve[curve.length - 1].equity;

  return (
    <div className="px-4 py-4">
      <div className="overflow-x-auto">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          width="100%"
          height={H}
          preserveAspectRatio="none"
          role="img"
          aria-label={`simulated equity curve, ${geom.up ? "above" : "below"} starting cash`}
          style={{ display: "block" }}
        >
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
        <span style={{ color: geom.up ? "var(--bid)" : "var(--ask)" }}>
          {usd(start)} → {usd(lastEq)}
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

export default function PaperPage() {
  const [strategy, setStrategy] = useState("flagship-1d");
  const [data, setData] = useState<PaperResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      paper(strategy)
        .then((r) => {
          if (!alive) return;
          setData(r);
          setErr(null);
        })
        .catch((e: Error) => {
          if (!alive) return;
          setErr(e.message);
        });
    load();
    const id = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [strategy, retryTick]);

  const s = data?.summary;
  const upColor = "var(--bid)";
  const downColor = "var(--ask)";

  return (
    <div className="flex flex-col gap-4">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.1rem] font-bold tracking-wide">PAPER TRADING</h1>
        <p className="text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          An <strong>internal simulation</strong> that trades the platform&apos;s own
          flagship calibrated prediction: go long when a symbol&apos;s calibrated
          probability crosses the long threshold, exit when it crosses the flat
          threshold — filling at the <strong>next bar&apos;s open</strong> (no
          lookahead) with realistic per-side costs. It is an honest, costed,
          out-of-sample record of whether the signal would have made money.
        </p>
        {/* Non-negotiable honesty label. */}
        <div
          className="panel px-3 py-2 text-[0.75rem] font-semibold"
          role="note"
          style={{
            color: "var(--ask)",
            borderColor: "var(--ask)",
          }}
        >
          {data?.label ??
            "simulated paper trading — not live money, not advice"}{" "}
          — no broker is ever contacted; every fill is computed from the
          daemon&apos;s own stored bars.
        </div>
      </header>

      {/* Strategy switcher (one simulated portfolio per prediction horizon). */}
      <div className="flex flex-wrap items-center gap-2">
        {(data?.strategies ?? ["flagship-1d", "flagship-1w"]).map((name) => {
          const active = name === strategy;
          return (
            <button
              key={name}
              onClick={() => setStrategy(name)}
              className="px-3 py-1 text-[0.75rem] font-semibold tracking-wide"
              style={{
                border: `1px solid ${active ? "var(--accent)" : "var(--border)"}`,
                background: "transparent",
                color: active ? "var(--accent)" : "var(--dim)",
              }}
              aria-pressed={active}
            >
              {name.toUpperCase()}
            </button>
          );
        })}
        {data ? (
          <span className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
            long ≥ {data.longThresh.toFixed(2)} · flat ≤{" "}
            {data.flatThresh.toFixed(2)} · book {usd(data.startCash)}
          </span>
        ) : null}
      </div>

      {err && !data ? (
        <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />
      ) : !data ? (
        <Skeleton lines={6} label="loading simulated book" />
      ) : (
        <>
          {/* Equity curve. */}
          <section className="panel">
            <div
              className="px-4 py-2 text-[0.75rem] font-semibold tracking-wide"
              style={{ borderBottom: "1px solid var(--border)", color: "var(--dim)" }}
            >
              SIMULATED EQUITY CURVE
            </div>
            {data.equity.length >= 2 ? (
              <EquityCurve curve={data.equity} start={s?.startEquity || data.startCash} />
            ) : (
              <EmptyState
                message="No equity marks yet."
                detail="The book marks once per new daily bar. Give the paper-trader a few bars to act on the live predictions."
                className="border-0"
              />
            )}
          </section>

          {/* Costed summary — every stat gated to what the sample supports. */}
          {s ? (
            <section
              className="panel grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4"
              aria-label="costed summary"
            >
              <Metric
                label="TOTAL RETURN"
                value={fmtPct(s.totalReturn * 100)}
                color={s.totalReturn >= 0 ? upColor : downColor}
                hint={`${usd(s.startEquity)} → ${usd(s.lastEquity)}`}
              />
              <Metric
                label="MAX DRAWDOWN"
                value={fmtPct(-s.maxDrawdown * 100, false)}
                color={downColor}
                hint="worst peak-to-trough"
              />
              <Metric
                label="SHARPE"
                value={s.sharpeValid ? s.sharpe.toFixed(2) : "n/a"}
                title="Annualized per-mark Sharpe (rf=0). Withheld until there are enough equity marks to be meaningful."
                hint={s.sharpeValid ? "annualized, rf=0" : "too few marks yet"}
              />
              <Metric
                label="WIN RATE"
                value={s.winRateValid ? fmtPct(s.winRate * 100, false) : "n/a"}
                title="Fraction of closed round-trips that were net-positive after costs. Withheld below 5 closed trades."
                hint={
                  s.winRateValid
                    ? `${s.closedTrades} closed`
                    : `need ≥5 closed (have ${s.closedTrades})`
                }
              />
              <Metric
                label="TURNOVER"
                value={s.turnover.toFixed(2) + "×"}
                hint="traded notional ÷ start"
              />
              <Metric
                label="FILLS"
                value={String(s.numFills)}
                hint="buys + sells"
              />
              <Metric
                label="CLOSED TRADES"
                value={String(s.closedTrades)}
                hint="round-trips"
              />
              <Metric
                label="SPAN"
                value={s.spanYears >= 0.01 ? s.spanYears.toFixed(2) + "y" : "<1d"}
                hint="calendar length"
              />
            </section>
          ) : null}

          {/* Open positions. */}
          <section className="panel">
            <div
              className="px-4 py-2 text-[0.75rem] font-semibold tracking-wide"
              style={{ borderBottom: "1px solid var(--border)", color: "var(--dim)" }}
            >
              OPEN POSITIONS ({data.positions.length})
            </div>
            {data.positions.length === 0 ? (
              <EmptyState message="Flat — no open positions." className="border-0" />
            ) : (
              <div className="overflow-x-auto">
                <table className="tnum w-full text-[0.8rem]">
                  <thead>
                    <tr style={{ color: "var(--faint)" }}>
                      <th className="px-4 py-2 text-left font-normal">SYMBOL</th>
                      <th className="px-4 py-2 text-right font-normal">QTY</th>
                      <th className="px-4 py-2 text-right font-normal">AVG PX</th>
                      <th className="px-4 py-2 text-right font-normal">OPENED</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.positions.map((p) => (
                      <tr
                        key={p.symbol}
                        style={{ borderTop: "1px solid var(--border)" }}
                      >
                        <td className="px-4 py-2 font-semibold">{p.symbol}</td>
                        <td className="px-4 py-2 text-right">{p.qty.toFixed(4)}</td>
                        <td className="px-4 py-2 text-right">{usd2(p.avgPx)}</td>
                        <td className="px-4 py-2 text-right" style={{ color: "var(--faint)" }}>
                          {fmtDate(p.openedTs)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {/* Trade log. */}
          <section className="panel">
            <div
              className="px-4 py-2 text-[0.75rem] font-semibold tracking-wide"
              style={{ borderBottom: "1px solid var(--border)", color: "var(--dim)" }}
            >
              TRADE LOG (most recent {data.trades.length})
            </div>
            {data.trades.length === 0 ? (
              <EmptyState
                message="No simulated trades yet."
                detail="A fill happens the bar AFTER a prediction crosses a threshold."
                className="border-0"
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="tnum w-full text-[0.8rem]">
                  <thead>
                    <tr style={{ color: "var(--faint)" }}>
                      <th className="px-4 py-2 text-left font-normal">DATE</th>
                      <th className="px-4 py-2 text-left font-normal">SYMBOL</th>
                      <th className="px-4 py-2 text-left font-normal">SIDE</th>
                      <th className="px-4 py-2 text-right font-normal">QTY</th>
                      <th className="px-4 py-2 text-right font-normal">FILL PX</th>
                      <th className="px-4 py-2 text-right font-normal">COST</th>
                      <th className="px-4 py-2 text-left font-normal">REASON</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.trades.map((t) => (
                      <tr key={t.id} style={{ borderTop: "1px solid var(--border)" }}>
                        <td className="px-4 py-2" style={{ color: "var(--faint)" }}>
                          {fmtDate(t.ts)}
                        </td>
                        <td className="px-4 py-2 font-semibold">{t.symbol}</td>
                        <td
                          className="px-4 py-2 font-semibold"
                          style={{ color: t.side === "buy" ? upColor : downColor }}
                        >
                          {t.side.toUpperCase()}
                        </td>
                        <td className="px-4 py-2 text-right">{t.qty.toFixed(4)}</td>
                        <td className="px-4 py-2 text-right">{usd2(t.px)}</td>
                        <td className="px-4 py-2 text-right" style={{ color: "var(--faint)" }}>
                          {usd2(t.cost)}
                        </td>
                        <td className="px-4 py-2 text-[0.72rem]" style={{ color: "var(--dim)" }}>
                          {t.reason}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}
