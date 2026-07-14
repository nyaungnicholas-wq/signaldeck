"use client";

import { useEffect, useMemo, useState } from "react";
import {
  pollMs,
  POLL_SLOW,
  signalBacktestLive,
  signalBacktestPinned,
  type SignalBacktestPinnedResponse,
  type SignalEquityPoint,
} from "@/lib/api";
import { fmtPct, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";

type SignalHorizon = "1d" | "1w";
const SIGNAL_HORIZONS: SignalHorizon[] = ["1d", "1w"];

/** Two overlaid equity curves — the signal-driven strategy (net of cost) vs the
 *  SPY buy-and-hold benchmark. Both start at 1.0; a faint dashed baseline marks
 *  1.0. Strategy is green above / red below its start; the benchmark is a thin
 *  neutral line so the reader can see edge (or its honest absence) at a glance. */
function EquityCurve({ curve }: { curve: SignalEquityPoint[] }) {
  const W = 1000;
  const H = 260;
  const padX = 4;
  const padY = 10;

  const geom = useMemo(() => {
    if (!curve || curve.length < 2) return null;
    const hasBench = curve.some((p) => p.benchmark > 0);
    let min = Infinity;
    let max = -Infinity;
    for (const p of curve) {
      for (const v of [p.strategy, hasBench ? p.benchmark : p.strategy]) {
        if (!isFinite(v) || v <= 0) continue;
        if (v < min) min = v;
        if (v > max) max = v;
      }
    }
    if (!isFinite(min) || !isFinite(max)) return null;
    min = Math.min(min, 1);
    max = Math.max(max, 1);
    const span = max - min || 1;
    const x = (i: number) => padX + (i / (curve.length - 1)) * (W - 2 * padX);
    const y = (v: number) => padY + (1 - (v - min) / span) * (H - 2 * padY);
    const stratPts = curve
      .map((p, i) => `${x(i).toFixed(1)},${y(p.strategy).toFixed(1)}`)
      .join(" ");
    const benchPts = hasBench
      ? curve
          .map((p, i) => `${x(i).toFixed(1)},${y(p.benchmark).toFixed(1)}`)
          .join(" ")
      : "";
    const baselineY = y(1);
    const up = curve[curve.length - 1].strategy >= 1;
    return { stratPts, benchPts, baselineY, up, hasBench };
  }, [curve]);

  if (!geom) {
    return (
      <div
        className="px-4 py-8 text-center text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        equity curve unavailable — need at least two independent observations.
      </div>
    );
  }

  const stroke = geom.up ? "var(--bid)" : "var(--ask)";
  const fill = geom.up ? "var(--bid-dim)" : "var(--ask-dim)";
  const areaPts = `${padX.toFixed(1)},${geom.baselineY.toFixed(1)} ${geom.stratPts} ${(W - padX).toFixed(1)},${geom.baselineY.toFixed(1)}`;
  const first = curve[0].ts;
  const last = curve[curve.length - 1].ts;

  return (
    <div className="px-4 py-4">
      <div className="overflow-x-auto">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          width="100%"
          height={H}
          preserveAspectRatio="none"
          role="img"
          aria-label={`signal strategy equity curve vs SPY buy-and-hold, ${geom.up ? "above" : "below"} its start`}
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
          {geom.hasBench && (
            <polyline
              points={geom.benchPts}
              fill="none"
              stroke="var(--dim)"
              strokeWidth={1.25}
              strokeDasharray="6 3"
              vectorEffect="non-scaling-stroke"
              strokeLinejoin="round"
            />
          )}
          <polyline
            points={geom.stratPts}
            fill="none"
            stroke={stroke}
            strokeWidth={2}
            vectorEffect="non-scaling-stroke"
            strokeLinejoin="round"
          />
        </svg>
      </div>
      <div
        className="tnum mt-2 flex flex-wrap justify-between gap-2 text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        <span>{first ? fmtDate(first) : "start"}</span>
        <span className="flex items-center gap-3">
          <span style={{ color: stroke }}>■ signal (net of cost)</span>
          {geom.hasBench && <span style={{ color: "var(--dim)" }}>┄ SPY buy &amp; hold</span>}
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
  help,
}: {
  label: string;
  value: string;
  color?: string;
  hint?: string;
  /** Load-bearing explanation — rendered as a click/keyboard HelpTip, not a hover title. */
  help?: string;
}) {
  return (
    <div
      className="flex flex-col gap-1 px-4 py-3"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <span
        className="flex items-center gap-1 text-[0.75rem] tracking-wide"
        style={{ color: "var(--faint)" }}
      >
        {label}
        {help && <HelpTip label={`What does ${label.toLowerCase()} mean?`}>{help}</HelpTip>}
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

export default function SignalBacktestPage() {
  const [horizon, setHorizon] = useState<SignalHorizon>("1d");
  // STAGE 2: default to the WEEKLY PINNED Sunday snapshot ("as of Sunday"),
  // with a one-click live recompute via the existing endpoint. The daemon
  // falls back to a live compute (labeled via pinnedNote) when no pin exists.
  const [mode, setMode] = useState<"pinned" | "live">("pinned");
  const [data, setData] = useState<SignalBacktestPinnedResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      (mode === "pinned" ? signalBacktestPinned(horizon) : signalBacktestLive(horizon))
        .then((r) => {
          if (!alive) return;
          setData(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // Pinned weekly snapshot / slow-moving evaluation — no tick-rate polling.
    // Switching horizon or mode re-runs the effect and fetches immediately.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [horizon, mode, retryTick]);

  // Only trust data tagged for the selected horizon (avoids a stale mix while
  // switching chips).
  const current =
    data && data.result.horizon === horizon ? data : null;
  const res = current?.result;
  const upColor = "var(--bid)";
  const downColor = "var(--ask)";

  return (
    <div className="flex flex-col gap-4">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.1rem] font-bold tracking-wide">SIGNAL BACKTEST</h1>
        <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          The platform&apos;s <strong>own</strong> flagship signal, graded out of
          sample. We replay the <strong>feature store</strong> — every resolved
          prediction&apos;s calibrated probability joined to what the market
          actually did afterwards — and measure information coefficient and its
          decay by lag, the quintile forward-return spread, hit-rate, turnover,
          and a <strong>costed</strong> equity curve versus SPY buy-and-hold.
          Every figure is over <strong>independent</strong> (symbol, day)
          observations, net of per-side cost, with no lookahead.
        </p>
        {/* Non-negotiable honesty label. */}
        <div
          className="panel px-3 py-2 text-[0.75rem] font-semibold"
          role="note"
          style={{ color: "var(--ask)", borderColor: "var(--ask)" }}
        >
          {res?.trackLabel ??
            "backtested — not live (own-signal replay of the feature store, net of cost)"}
        </div>
      </header>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-signal-backtest"
        text="Does SignalDeck's own flagship signal predict returns out of sample, after costs? Graded on the record it actually made, gates included."
      />

      {/* Horizon switcher. */}
      <div className="flex flex-wrap items-center gap-2">
        <div role="group" aria-label="Signal horizon" className="flex items-center gap-1">
          {SIGNAL_HORIZONS.map((h) => {
            const active = h === horizon;
            return (
              <button
                key={h}
                type="button"
                onClick={() => setHorizon(h)}
                aria-pressed={active}
                className="cursor-pointer rounded-lg px-3 py-1 text-[0.75rem] font-semibold tracking-wide transition-colors duration-150 hover:brightness-125"
                style={{
                  border: `1px solid ${active ? "var(--accent)" : "var(--border)"}`,
                  background: "transparent",
                  color: active ? "var(--accent)" : "var(--dim)",
                }}
                title={`Grade the ${h} calibrated signal against realized ${h} returns`}
              >
                {h.toUpperCase()}
              </button>
            );
          })}
        </div>
        {res && (
          <span className="text-[0.75rem] tnum" style={{ color: "var(--faint)" }}>
            {res.rawN.toLocaleString("en-US")} raw rows →{" "}
            {res.independentN.toLocaleString("en-US")} independent · cost{" "}
            {res.costBps.toFixed(1)}bps/side
          </span>
        )}
        {/* STAGE 2 — pinned Sunday snapshot vs live recompute. */}
        {current && (
          <span className="ml-auto flex flex-wrap items-center gap-2">
            <span
              className="rounded-lg px-2 py-1 text-[0.75rem] font-semibold tnum"
              style={{
                border: "1px solid var(--border)",
                color: current.pinned ? "var(--accent)" : "var(--dim)",
              }}
              title={
                current.pinned
                  ? `Stored by the signalbt-weekly worker — the exact result the weekly insight described (computed ${current.pinnedTs ? fmtDate(current.pinnedTs) : "—"}).`
                  : "Computed just now from the current feature store."
              }
            >
              {current.pinned
                ? `pinned · as of ${current.pinnedDay ?? "—"}`
                : "live compute"}
            </span>
            <button
              type="button"
              onClick={() => setMode(mode === "pinned" ? "live" : "pinned")}
              className="cursor-pointer rounded-lg px-2 py-1 text-[0.75rem] font-semibold transition-colors duration-150 hover:brightness-125"
              style={{
                border: "1px solid var(--border)",
                background: "transparent",
                color: "var(--dim)",
              }}
              title={
                mode === "pinned"
                  ? "Re-run the evaluation on the current feature store right now"
                  : "Show the weekly Sunday snapshot instead"
              }
            >
              {mode === "pinned" ? "recompute live" : "show weekly pin"}
            </button>
          </span>
        )}
      </div>

      {/* Requested the pin, none stored yet — say so instead of pretending. */}
      {current?.pinnedNote && (
        <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {current.pinnedNote}
        </p>
      )}

      {err && !current ? (
        <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />
      ) : !current ? (
        <Skeleton lines={6} label="loading signal backtest" />
      ) : res && res.gated ? (
        // HONEST insufficient-data state — the truth today (~0 resolved live
        // outcomes). We still show sample accounting, never a fabricated number.
        <EmptyState
          message={`Insufficient data — ${res.independentN} of ${res.minIndependentN} independent resolutions.`}
          detail={
            res.note ||
            "The flagship signal needs a real out-of-sample track record before any skill number can be trusted. Headline IC, quintile spread, and hit-rate are withheld until the gate clears."
          }
        />
      ) : res ? (
        <>
          {/* Costed equity vs SPY. */}
          <section className="panel">
            <div className="panel-h">COSTED EQUITY — SIGNAL vs SPY BUY &amp; HOLD</div>
            {res.equity.length >= 2 ? (
              <EquityCurve curve={res.equity} />
            ) : (
              <EmptyState
                message="No equity marks yet."
                detail="Need at least two independent observations to draw a curve."
                className="border-0"
              />
            )}
          </section>

          {/* Headline skill + costed returns. */}
          <section
            className="panel grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4"
            aria-label="out-of-sample skill and returns"
          >
            <Metric
              label="INFORMATION COEFFICIENT"
              value={res.ic.toFixed(3)}
              color={res.ic > 0 ? upColor : res.ic < 0 ? downColor : undefined}
              help="Spearman rank correlation of the signal with the realized forward return, over independent (symbol, day) observations. ~0 means no edge."
              hint="rank corr · signal vs fwd"
            />
            <Metric
              label="QUINTILE SPREAD"
              value={fmtPct(res.quintileSpread * 100)}
              color={res.quintileSpread > 0 ? upColor : downColor}
              help="Mean forward return of the top signal quintile minus the bottom. Positive = higher signals really did precede higher returns."
              hint="Q5 − Q1 mean fwd"
            />
            <Metric
              label="HIT RATE"
              value={fmtPct(res.hitRate * 100, false)}
              help="Fraction of independent observations where the signal's directional lean matched the realized direction. 50% = coin flip."
              hint="direction correct"
            />
            <Metric
              label="MEAN FWD RETURN"
              value={fmtPct(res.meanFwd * 100)}
              color={res.meanFwd >= 0 ? upColor : downColor}
              hint="avg realized, independent set"
            />
            <Metric
              label="STRATEGY RETURN"
              value={fmtPct(res.strategyReturn * 100)}
              color={res.strategyReturn >= 0 ? upColor : downColor}
              help="Net-of-cost total return of the long/flat strategy driven by the calibrated signal."
              hint="net of cost"
            />
            <Metric
              label="SPY BUY & HOLD"
              value={
                current.hasBenchmark ? fmtPct(res.benchmarkReturn * 100) : "n/a"
              }
              color={res.benchmarkReturn >= 0 ? upColor : downColor}
              hint={current.hasBenchmark ? "benchmark" : "SPY not tracked"}
            />
            <Metric
              label="EXCESS vs SPY"
              value={current.hasBenchmark ? fmtPct(res.excessReturn * 100) : "n/a"}
              color={res.excessReturn >= 0 ? upColor : downColor}
              help="Strategy return minus SPY buy-and-hold over the same window."
              hint="strategy − benchmark"
            />
            <Metric
              label="TURNOVER"
              value={res.turnover.toFixed(2) + "×"}
              help="Mean absolute position change per observation — a churn proxy. High turnover means costs bite harder."
              hint="mean |Δposition|"
            />
          </section>

          {/* IC decay + quintile profile are methodology detail — SIMPLE mode
              folds them behind one disclosure; the honesty label, equity curve
              and headline metrics above stay visible in both modes. */}
          <ProOnly summary="Show methodology detail">
          <div className="flex flex-col gap-4">
          {/* IC decay by lag. */}
          <section className="panel">
            <div className="panel-h">IC DECAY BY LAG</div>
            <div className="overflow-x-auto">
              <table className="tnum w-full text-[0.75rem]">
                <thead>
                  <tr style={{ color: "var(--faint)" }}>
                    <th className="px-4 py-2 text-left font-normal">FWD LAG</th>
                    <th className="px-4 py-2 text-right font-normal">IC</th>
                    <th className="px-4 py-2 text-right font-normal">N</th>
                  </tr>
                </thead>
                <tbody>
                  {res.icDecay.map((p) => (
                    <tr key={p.lagDays} style={{ borderTop: "1px solid var(--border)" }}>
                      <td className="px-4 py-2 font-semibold">
                        {p.lagDays} {p.lagDays === 1 ? "bar" : "bars"}
                      </td>
                      <td
                        className="px-4 py-2 text-right font-semibold"
                        style={{
                          color: p.ic > 0 ? upColor : p.ic < 0 ? downColor : "var(--dim)",
                        }}
                      >
                        {p.ic.toFixed(3)}
                      </td>
                      <td className="px-4 py-2 text-right" style={{ color: "var(--faint)" }}>
                        {p.n.toLocaleString("en-US")}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          {/* Quintile forward-return profile. */}
          <section className="panel">
            <div className="panel-h">SIGNAL QUINTILES — FORWARD-RETURN PROFILE</div>
            <div className="overflow-x-auto">
              <table className="tnum w-full text-[0.75rem]">
                <thead>
                  <tr style={{ color: "var(--faint)" }}>
                    <th className="px-4 py-2 text-left font-normal">QUINTILE</th>
                    <th className="px-4 py-2 text-right font-normal">MEAN SIGNAL</th>
                    <th className="px-4 py-2 text-right font-normal">MEAN FWD</th>
                    <th className="px-4 py-2 text-right font-normal">HIT RATE</th>
                    <th className="px-4 py-2 text-right font-normal">N</th>
                  </tr>
                </thead>
                <tbody>
                  {res.quintiles.map((q) => (
                    <tr key={q.quintile} style={{ borderTop: "1px solid var(--border)" }}>
                      <td className="px-4 py-2 font-semibold">
                        Q{q.quintile}
                        {q.quintile === 1
                          ? " (lowest)"
                          : q.quintile === 5
                            ? " (highest)"
                            : ""}
                      </td>
                      <td className="px-4 py-2 text-right">{q.meanSignal.toFixed(3)}</td>
                      <td
                        className="px-4 py-2 text-right font-semibold"
                        style={{ color: q.meanFwd >= 0 ? upColor : downColor }}
                      >
                        {fmtPct(q.meanFwd * 100)}
                      </td>
                      <td className="px-4 py-2 text-right">
                        {fmtPct(q.hitRate * 100, false)}
                      </td>
                      <td className="px-4 py-2 text-right" style={{ color: "var(--faint)" }}>
                        {q.n.toLocaleString("en-US")}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p
              className="px-4 py-3 text-[0.75rem] leading-relaxed"
              style={{ color: "var(--faint)", borderTop: "1px solid var(--border)" }}
            >
              A signal with edge shows mean forward return rising monotonically
              from Q1 to Q5. A flat profile is the honest &quot;no edge&quot;
              result.
            </p>
          </section>
          </div>
          </ProOnly>
        </>
      ) : null}
    </div>
  );
}
