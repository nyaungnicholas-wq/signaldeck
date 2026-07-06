"use client";

// STAGE 7 — LIVE OUT-OF-SAMPLE TRACK RECORD.
// The honest scoreboard: grades the platform's OWN calibrated predictions
// (prob frozen at prediction time, graded against realized bars) — winrate,
// Brier, reliability curve, IC — each with a confidence interval and an EXPLICIT
// independent-N gate. Below the gate the headline numbers are withheld and the
// page says why. It renders honest and mostly-empty today (the system is ~1 day
// old, ~0 resolved live outcomes) — being visibly honest while empty IS the
// point. The record links its own tamper-evidence (ledger, Stage 3) and its
// costed paper P&L + turnover + capacity (Stage 4) so it is self-verifying.

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  HORIZONS,
  pollMs,
  trackRecordWithGate,
  type Horizon,
  type TrackRecordWithGate,
} from "@/lib/api";
import { ago, fmtDate, fmtPct } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import ReliabilityCurve from "@/components/trackrecord/ReliabilityCurve";

function pct(v: number | null | undefined, digits = 1): string {
  if (v == null || !isFinite(v)) return "—";
  return `${(v * 100).toFixed(digits)}%`;
}
function num(v: number | null | undefined, digits = 3): string {
  if (v == null || !isFinite(v)) return "—";
  return v.toFixed(digits);
}

export default function TrackRecordPage() {
  const [horizon, setHorizon] = useState<Horizon>("1d");
  const [data, setData] = useState<TrackRecordWithGate | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [fetchedAt, setFetchedAt] = useState(0);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      trackRecordWithGate(horizon)
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
          setFetchedAt(Math.floor(Date.now() / 1000));
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
  }, [horizon, retryTick]);

  const current = data && data.horizon === horizon ? data : null;
  const loading = !current && !err;
  const gated = current?.gated ?? true;

  return (
    <div className="flex flex-col gap-4">
      {/* header */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">TRACK RECORD</h1>
        <span className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
          live out-of-sample — calibrated predictions vs what the market did
        </span>
        <div role="group" aria-label="Outcome horizon" className="flex items-center gap-1">
          {HORIZONS.map((h) => {
            const active = h === horizon;
            return (
              <button
                key={h}
                type="button"
                onClick={() => setHorizon(h)}
                aria-pressed={active}
                className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
                title={`Grade calibrated predictions against realized ${h} outcomes`}
                style={active ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
              >
                {h}
              </button>
            );
          })}
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {/* This is a LIVE forward record, but until the gate clears it carries
              no claimable skill — so the badge stays honest. */}
          <span
            className="chip"
            style={
              gated
                ? { color: "var(--warn)", borderColor: "var(--warn)" }
                : { color: "var(--ok)", borderColor: "var(--ok)" }
            }
            title={
              gated
                ? "Too few independent resolutions to claim skill yet — numbers withheld."
                : "Enough independent resolutions to report measured skill."
            }
          >
            {gated ? "not yet significant" : "live · significant"}
          </span>
          {current && (
            <span
              className="chip tnum"
              title={`${(current.rawN ?? 0).toLocaleString("en-US")} raw resolved rows collapse to ${(current.independentN ?? 0).toLocaleString("en-US")} independent (symbol, UTC-day) observations`}
            >
              {(current.independentN ?? 0).toLocaleString("en-US")} independent
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

      {err && !current && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Start signaldeckd and this page will pick it up."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}
      {loading && <Skeleton lines={6} label="loading track record" />}

      {current && (
        <>
          {/* GATE COUNTDOWN — the wait itself, made visible: progress toward
              the independent-N threshold, a LABELED estimate of when the gate
              clears (from the measured last-7-day accrual), and what is
              already accruing per horizon. Honest: no accrual → no ETA. */}
          {gated && (
            <section className="panel p-5">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <p className="text-[0.85rem] font-semibold" style={{ color: "var(--warn)" }}>
                  <span className="tnum">
                    {current.independentN}/{current.gate?.threshold ?? current.minIndependentN}
                  </span>{" "}
                  independent (symbol, UTC-day) resolutions
                </p>
                <p className="text-[0.78rem] tnum" style={{ color: "var(--dim)" }}>
                  {current.gate == null
                    ? (current.note ?? "not yet significant")
                    : current.gate.estDaysToUngate == null
                      ? "unlock ETA unknown — nothing resolved in the last 7 days to measure an accrual rate from"
                      : `win rate, Brier & IC unlock in ~${current.gate.estDaysToUngate} trading day${current.gate.estDaysToUngate === 1 ? "" : "s"} (estimate)`}
                </p>
              </div>
              {/* progress bar */}
              <div
                className="mt-3 h-2 w-full overflow-hidden rounded"
                style={{ background: "var(--border)" }}
                role="progressbar"
                aria-valuemin={0}
                aria-valuemax={current.gate?.threshold ?? current.minIndependentN}
                aria-valuenow={current.independentN}
                aria-label="independent resolutions toward the significance gate"
              >
                <div
                  className="h-full rounded transition-[width] duration-500"
                  style={{
                    width: `${Math.min(100, (current.independentN / (current.gate?.threshold ?? current.minIndependentN)) * 100)}%`,
                    background: "var(--warn)",
                  }}
                />
              </div>
              {current.gate && (
                <p className="mt-2 text-[0.72rem] tnum" style={{ color: "var(--faint)" }}>
                  last 7 days: +{current.gate.accrual7d.independentNew} independent
                  symbol-day{current.gate.accrual7d.independentNew === 1 ? "" : "s"} over{" "}
                  {current.gate.accrual7d.tradingDays} trading days (
                  {current.gate.accrual7d.perTradingDay.toFixed(1)}/day) ·{" "}
                  {current.gate.estBasis}
                </p>
              )}
              {/* what's already accruing, per horizon — incl. first-outcome ETAs */}
              <div className="mt-3 flex flex-wrap gap-2">
                {HORIZONS.map((h) => {
                  const c = current.coverage?.[h];
                  const eta = current.gate?.firstResolveEta?.[h];
                  if (c && c.resolved > 0) {
                    return (
                      <span key={h} className="chip tnum" title={`${c.resolved} resolved of ${c.total} ${h} predictions — already accruing`}>
                        {h}: {c.resolved.toLocaleString("en-US")} resolved
                      </span>
                    );
                  }
                  return (
                    <span
                      key={h}
                      className="chip tnum"
                      style={{ color: "var(--faint)" }}
                      title={
                        eta != null
                          ? (current.gate?.firstResolveEtaNote ?? "estimate")
                          : `no ${h} predictions old enough to grade yet`
                      }
                    >
                      {h}: 0 resolved
                      {eta != null ? ` — first outcomes expected ~${fmtDate(eta)} (estimate)` : ""}
                    </span>
                  );
                })}
              </div>
              <p className="mt-3 text-[0.78rem]" style={{ color: "var(--dim)" }}>
                A signal has no value without a verifiable, costed, out-of-sample track
                record. This page grades the platform&rsquo;s own calibrated predictions
                against realized outcomes and withholds every skill number
                (win-rate, Brier, IC) until there are at least{" "}
                <span className="tnum">{current.minIndependentN}</span> independent
                (symbol, UTC-day) resolutions. The system is young — an honest
                &ldquo;no live edge yet&rdquo; is the correct output. It fills in as
                predictions mature; the bar above is the record accruing.
              </p>
            </section>
          )}

          {/* HERO STATS — measured only when ungated; each with its CI. */}
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Stat
              label="WIN RATE"
              value={pct(current.winRate)}
              sub={
                current.winRateCI
                  ? `95% CI ${pct(current.winRateCI[0])}–${pct(current.winRateCI[1])}`
                  : gated
                    ? "withheld — too few obs"
                    : undefined
              }
              tip="Fraction of resolved predictions where the market moved up. Base rate shown as the honest benchmark."
            />
            <Stat
              label="BRIER"
              value={num(current.brier)}
              sub={
                current.brierSkill != null
                  ? `skill ${current.brierSkill >= 0 ? "+" : ""}${(current.brierSkill * 100).toFixed(0)}% vs base rate`
                  : gated
                    ? "withheld — too few obs"
                    : undefined
              }
              tip="Mean squared error of the calibrated probability vs the {0,1} outcome. Lower is better; 0.25 is a coin flip. Brier skill > 0 beats always predicting the base rate."
              good={current.brier != null && current.brier < 0.25}
            />
            <Stat
              label="IC"
              value={num(current.ic)}
              sub={
                current.icCI
                  ? `95% CI ${num(current.icCI[0], 2)}–${num(current.icCI[1], 2)}`
                  : gated
                    ? "withheld — too few obs"
                    : undefined
              }
              tip="Information coefficient — correlation of the signal (prob−0.5) with the realized forward return over independent obs. Fisher-z 95% CI."
              good={current.ic != null && current.ic > 0}
            />
            <Stat
              label="BASE RATE"
              value={pct(current.baseRate)}
              sub={gated ? "withheld — too few obs" : "the constant-forecast benchmark"}
              tip="Realized up-rate of the sample — the honest benchmark any signal must beat."
            />
          </div>

          {/* RELIABILITY CURVE + COVERAGE */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <section className="panel">
              <div className="panel-h">
                <span>RELIABILITY (CALIBRATION)</span>
                {current.reliabilityScore != null && !gated && (
                  <span className="ml-auto chip tnum" title="Lower reliability = better calibration (mean |predicted − realized| across bins)">
                    score {num(current.reliabilityScore)}
                  </span>
                )}
              </div>
              <div className="p-3">
                <ReliabilityCurve bins={current.reliability ?? []} />
                <p className="mt-2 text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  Each dot: predictions in a probability bin, plotted mean-predicted
                  (x) vs mean-realized (y). On the diagonal = perfectly calibrated.
                  {gated && " Too thin to read yet — shown for shape only."}
                </p>
              </div>
            </section>

            <section className="panel">
              <div className="panel-h">
                <span>RESOLVED COVERAGE · ALL HORIZONS</span>
              </div>
              <div className="table-wrap">
                <table className="w-full text-[0.78rem]">
                  <thead>
                    <tr style={{ color: "var(--faint)" }}>
                      <th className="px-4 py-2 text-left font-medium">horizon</th>
                      <th className="px-4 py-2 text-right font-medium">resolved</th>
                      <th className="px-4 py-2 text-right font-medium">total</th>
                      <th className="px-4 py-2 text-right font-medium">maturing</th>
                    </tr>
                  </thead>
                  <tbody>
                    {HORIZONS.map((h) => {
                      const c = current.coverage?.[h] ?? { resolved: 0, total: 0 };
                      const maturing = Math.max(0, c.total - c.resolved);
                      return (
                        <tr key={h} style={{ borderTop: "1px solid var(--border)" }}>
                          <td className="px-4 py-2">
                            <span style={h === horizon ? { color: "var(--accent)" } : undefined}>{h}</span>
                          </td>
                          <td className="px-4 py-2 text-right tnum" style={{ color: "var(--text)" }}>
                            {c.resolved.toLocaleString("en-US")}
                          </td>
                          <td className="px-4 py-2 text-right tnum" style={{ color: "var(--dim)" }}>
                            {c.total.toLocaleString("en-US")}
                          </td>
                          <td className="px-4 py-2 text-right tnum" style={{ color: "var(--faint)" }}>
                            {maturing.toLocaleString("en-US")}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              <p className="px-4 pb-3 pt-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
                &ldquo;maturing&rdquo; predictions haven&rsquo;t hit their horizon yet — they
                can&rsquo;t be graded without lookahead, so they wait.
              </p>
            </section>
          </div>

          {/* BY MARKET (descriptive) */}
          {current.byMarket && current.byMarket.length > 0 && (
            <section className="panel">
              <div className="panel-h">
                <span>BY MARKET · DESCRIPTIVE</span>
                <span className="ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  describes the sample, not a per-market skill claim
                </span>
              </div>
              <div className="table-wrap">
                <table className="w-full text-[0.78rem]">
                  <thead>
                    <tr style={{ color: "var(--faint)" }}>
                      <th className="px-4 py-2 text-left font-medium">market</th>
                      <th className="px-4 py-2 text-right font-medium">n</th>
                      <th className="px-4 py-2 text-right font-medium">up rate</th>
                      <th className="px-4 py-2 text-right font-medium">dir. hit rate</th>
                      <th className="px-4 py-2 text-right font-medium">mean fwd</th>
                    </tr>
                  </thead>
                  <tbody>
                    {current.byMarket.map((m) => (
                      <tr key={m.market} style={{ borderTop: "1px solid var(--border)" }}>
                        <td className="px-4 py-2 uppercase tracking-wider">{m.market}</td>
                        <td className="px-4 py-2 text-right tnum">{m.n.toLocaleString("en-US")}</td>
                        <td className="px-4 py-2 text-right tnum">{pct(m.upRate)}</td>
                        <td className="px-4 py-2 text-right tnum">{pct(m.dirHitRate)}</td>
                        <td
                          className="px-4 py-2 text-right tnum"
                          style={{ color: m.meanFwd >= 0 ? "var(--bid)" : "var(--ask)" }}
                        >
                          {fmtPct(m.meanFwd * 100)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          {/* SELF-VERIFYING LINKS: ledger integrity + paper P&L + turnover/capacity */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <section className="panel">
              <div className="panel-h">
                <span>SELF-VERIFYING · LEDGER</span>
                <Link
                  href="/lab/signal-backtest"
                  className="ml-auto chip cursor-pointer"
                  style={{ color: "var(--dim)" }}
                  title="See the own-signal backtester (OOS IC / quintiles / costed equity vs SPY)"
                >
                  signal backtest →
                </Link>
              </div>
              <div className="p-4 text-[0.8rem]">
                {current.ledger ? (
                  <div className="flex flex-col gap-2">
                    <div className="flex items-center gap-2">
                      <span
                        className="chip"
                        style={
                          current.ledger.intact
                            ? { color: "var(--ok)", borderColor: "var(--ok)" }
                            : { color: "var(--bad)", borderColor: "var(--bad)" }
                        }
                      >
                        {current.ledger.intact ? "chain intact" : "chain BROKEN"}
                      </span>
                      <span className="tnum" style={{ color: "var(--dim)" }}>
                        {current.ledger.count.toLocaleString("en-US")} entries
                      </span>
                    </div>
                    <p className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
                      Every flagship prediction is hash-chained (append-only). A verified
                      chain means no historical prediction was silently edited or deleted —
                      the record you&rsquo;re grading is the record that was made.
                    </p>
                    {current.ledger.head && (
                      <p className="tnum break-all text-[0.68rem]" style={{ color: "var(--faint)" }}>
                        head {current.ledger.head.slice(0, 24)}…
                      </p>
                    )}
                  </div>
                ) : (
                  <p style={{ color: "var(--faint)" }}>ledger unavailable.</p>
                )}
              </div>
            </section>

            <section className="panel">
              <div className="panel-h">
                <span>SELF-VERIFYING · PAPER P&amp;L</span>
                <Link
                  href="/lab/paper"
                  className="ml-auto chip cursor-pointer"
                  style={{ color: "var(--dim)" }}
                  title="Open the full simulated paper-trading book"
                >
                  paper book →
                </Link>
              </div>
              <div className="p-4 text-[0.8rem]">
                {current.paper?.available ? (
                  <div className="grid grid-cols-2 gap-3">
                    <Mini label="total return" value={fmtPct((current.paper.totalReturn ?? 0) * 100)} good={(current.paper.totalReturn ?? 0) >= 0} />
                    <Mini label="max drawdown" value={pct(current.paper.maxDrawdown)} good={false} />
                    <Mini
                      label="turnover"
                      value={`${num(current.paper.turnover, 2)}×`}
                      tip="Total traded notional / starting equity — how much the simulated book churns. High turnover magnifies cost drag and limits capacity."
                    />
                    <Mini
                      label="fills"
                      value={(current.paper.numFills ?? 0).toLocaleString("en-US")}
                      tip="Total buy+sell fills in the simulated book."
                    />
                    <div className="col-span-2 mt-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
                      Capacity note: this is a SIMULATION on free IEX / public-crypto data
                      with next-bar fills and per-side costs. Turnover of{" "}
                      <span className="tnum">{num(current.paper.turnover, 2)}×</span> over{" "}
                      <span className="tnum">{num(current.paper.spanYears, 2)}</span> yr means
                      real-world capacity is bounded by slippage and the tradable size at each
                      fill — not modeled here. Treat returns as an upper bound.
                    </div>
                  </div>
                ) : (
                  <p style={{ color: "var(--faint)" }}>
                    No simulated equity yet — the paper book fills in as predictions resolve.
                  </p>
                )}
              </div>
            </section>
          </div>

          {/* HONESTY FOOTER */}
          <section className="panel p-4 text-[0.72rem]" style={{ color: "var(--faint)" }}>
            <p>
              {current.trackLabel}. Numbers are computed over INDEPENDENT (symbol,
              UTC-day) resolutions — the minute-cadence pipeline writes many
              predictions per symbol per day that resolve against the same move, so
              pooling them would overstate confidence. There is no lookahead: a
              prediction&rsquo;s calibrated probability is frozen when it&rsquo;s made and
              only graded once the horizon has elapsed. Regime-conditioned IC is
              omitted because the regime at prediction time isn&rsquo;t persisted
              per-prediction, and using the current regime would be lookahead — an
              honest gap rather than a fabricated breakdown.
            </p>
          </section>
        </>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  sub,
  tip,
  good,
}: {
  label: string;
  value: string;
  sub?: string;
  tip?: string;
  good?: boolean;
}) {
  const color = value === "—" ? "var(--faint)" : good === undefined ? "var(--text)" : good ? "var(--bid)" : "var(--ask)";
  return (
    <div className="panel p-3">
      <div className="text-[0.66rem] tracking-[0.14em]" style={{ color: "var(--faint)" }} title={tip}>
        {label}
      </div>
      <div className="mt-1 text-lg font-bold tnum" style={{ color }}>
        {value}
      </div>
      {sub && (
        <div className="mt-0.5 text-[0.66rem] tnum" style={{ color: "var(--faint)" }}>
          {sub}
        </div>
      )}
    </div>
  );
}

function Mini({ label, value, tip, good }: { label: string; value: string; tip?: string; good?: boolean }) {
  const color = good === undefined ? "var(--text)" : good ? "var(--bid)" : "var(--ask)";
  return (
    <div>
      <div className="text-[0.64rem] tracking-wider" style={{ color: "var(--faint)" }} title={tip}>
        {label}
      </div>
      <div className="mt-0.5 text-[0.95rem] font-semibold tnum" style={{ color }}>
        {value}
      </div>
    </div>
  );
}
