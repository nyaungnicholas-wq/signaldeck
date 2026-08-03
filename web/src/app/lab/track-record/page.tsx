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
  POLL_SLOW,
  regimePostmortems,
  trackRecordRegimes,
  trackRecordWithGate,
  type Horizon,
  type RegimeKindRecord,
  type RegimePostmortems,
  type TrackRecordWithGate,
} from "@/lib/api";
import { ago, fmtDate, fmtPct } from "@/lib/format";
import { metricLabel, readMetric, type MetricKey, type PlainCtx } from "@/lib/plain";
import Plain, { useViewMode } from "@/components/Plain";
import GradeMeter from "@/components/viz/GradeMeter";
import CellBar from "@/components/viz/CellBar";
import PagePurpose from "@/components/PagePurpose";
import { StatTile } from "@/components/ui/Kit";
import StorySection from "@/components/StorySection";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import ReliabilityCurve from "@/components/trackrecord/ReliabilityCurve";
import HelpTip from "@/components/HelpTip";

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
  // Credibility wave: high-conviction regime misses (its own read; best-effort —
  // a failed fetch leaves the panel on its honest empty state).
  const [pms, setPms] = useState<RegimePostmortems | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      regimePostmortems()
        .then((p) => {
          if (alive) setPms(p);
        })
        .catch(() => undefined);
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

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
    // The record moves on resolution cadence (hours/days), not tick cadence.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [horizon, retryTick]);

  const current = data && data.horizon === horizon ? data : null;
  const loading = !current && !err;
  const gated = current?.gated ?? true;

  return (
    <div className="page-enter flex flex-col gap-4">
      {/* header */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h1 className="hero-title text-xl font-extrabold tracking-[0.18em]">TRACK RECORD</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
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
          {/* Export unification (#23): the record's raw resolved outcomes.
              A10 (2026-07-26 re-audit) put the raw-data redistribution guard on
              the CSV exports and made outcomes.csv symbol-scoped — an unscoped
              dump of every resolved row is redistribution of licensed vendor
              data through a second door. This page has no symbol in scope, so
              it links to the per-symbol page rather than offering a download
              that would now 404. Better a working pointer than a dead button. */}
          <span className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
            raw outcomes export is per-symbol — open a symbol and use its export menu
          </span>
        </div>
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-track-record"
        text="Is SignalDeck actually right when it predicts? (measured honestly) — its own frozen predictions graded against what the market really did, with every skill number withheld until there is enough independent evidence."
      />

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
          {/* v4 hero band — headline skill numbers, honest about the gate:
              anything the daemon withholds renders as "withheld", never 0. */}
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatTile
              label="Independent N"
              value={current.independentN ?? 0}
              sub={`gate floor ${current.minIndependentN ?? 0} · ${(current.rawN ?? 0).toLocaleString("en-US")} raw rows`}
              glow="hud"
              i={0}
            />
            <StatTile
              label="Win Rate"
              value={current.winRate != null ? current.winRate * 100 : "withheld"}
              decimals={1}
              suffix={current.winRate != null ? "%" : ""}
              sub={current.baseRate != null ? `base rate ${(current.baseRate * 100).toFixed(1)}%` : "vs realized outcomes"}
              glow={current.winRate != null && current.baseRate != null && current.winRate > current.baseRate ? "up" : undefined}
              i={1}
            />
            <StatTile
              label="Brier Skill"
              value={current.brierSkill != null ? current.brierSkill : "withheld"}
              decimals={3}
              sub="above 0 beats the base-rate constant"
              glow={current.brierSkill != null && current.brierSkill > 0 ? "up" : current.brierSkill != null ? "down" : undefined}
              i={2}
            />
            <StatTile
              label="Info Coefficient"
              value={current.ic != null ? current.ic : "withheld"}
              decimals={3}
              sub="rank correlation, prediction vs outcome"
              glow={current.ic != null && current.ic > 0 ? "up" : undefined}
              i={3}
            />
          </div>

          {/* ── CREDIBILITY WAVE: live regime grading + owned misses ── */}
          <RegimesLivePanel regimes={trackRecordRegimes(current)} />
          <RegimeMissesPanel pms={pms} />

          {/* ── STAGE 3 STORY, SECTION 1: the verdict itself ── */}
          <StorySection
            n={1}
            title="THE VERDICT"
            sub="the scoreboard's one honest headline"
          >
          {gated ? (
            <section className="panel reveal-item p-5">
              <p className="m-0 text-[1.05rem] font-extrabold tracking-wide" style={{ color: "var(--warn)" }}>
                TOO EARLY TO GRADE
              </p>
              <div className="mt-1 flex flex-wrap items-baseline justify-between gap-2">
                <p className="m-0 text-[0.85rem] font-semibold" style={{ color: "var(--warn)" }}>
                  <span className="tnum">
                    {current.independentN}/{current.gate?.threshold ?? current.minIndependentN}
                  </span>{" "}
                  independent (symbol, UTC-day) resolutions
                </p>
                <p className="m-0 text-[0.75rem] tnum" style={{ color: "var(--dim)" }}>
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
              <p className="mt-2 text-[0.75rem]" style={{ color: "var(--dim)" }}>
                An honest &ldquo;not enough evidence yet&rdquo; beats a fake verdict — the skill
                numbers below stay withheld until this bar fills.
              </p>
            </section>
          ) : (
            <section className="panel reveal-item p-5">
              <p className="m-0 text-[1.05rem] font-extrabold tracking-wide" style={{ color: "var(--ok)" }}>
                MEASURED{current.winRate != null ? `: right ${pct(current.winRate)} of the time` : ""}
              </p>
              <p className="mt-1 text-[0.75rem] tnum" style={{ color: "var(--dim)" }}>
                over {(current.independentN ?? 0).toLocaleString("en-US")} independent (symbol,
                UTC-day) resolutions
                {current.winRateCI ? ` · 95% CI ${pct(current.winRateCI[0])}–${pct(current.winRateCI[1])}` : ""}
                {current.baseRate != null ? ` · vs a ${pct(current.baseRate)} always-up base rate` : ""}
              </p>
            </section>
          )}
          </StorySection>

          {/* ── STAGE 3 STORY, SECTION 2: why the verdict is what it is ── */}
          <StorySection
            n={2}
            title="WHY"
            sub="what is accruing, what is withheld, and the measured components"
          >
          {gated && (
            <section className="panel reveal-item p-5">
              {current.gate && (
                <p className="mt-2 text-[0.75rem] tnum" style={{ color: "var(--faint)" }}>
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
              <p className="mt-3 text-[0.75rem]" style={{ color: "var(--dim)" }}>
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

          {/* COMPONENT STATS — measured only when ungated; each with its CI.
              Stage-1 translation layer: every number renders through <Plain>
              (SIMPLE: sentence first; PRO: raw first) — the gate still
              withholds values in BOTH modes. */}
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Stat
              metric="win_rate"
              value={current.winRate}
              ctx={{ gated, n: current.independentN, baseRate: current.baseRate ?? undefined }}
              raw={pct(current.winRate)}
              sub={
                current.winRateCI
                  ? `95% CI ${pct(current.winRateCI[0])}–${pct(current.winRateCI[1])}`
                  : gated
                    ? "withheld — too few obs"
                    : undefined
              }
            />
            <Stat
              metric="brier"
              value={current.brier}
              ctx={{ gated, n: current.independentN }}
              raw={num(current.brier)}
              sub={
                current.brierSkill != null
                  ? `skill ${current.brierSkill >= 0 ? "+" : ""}${(current.brierSkill * 100).toFixed(0)}% vs base rate`
                  : gated
                    ? "withheld — too few obs"
                    : undefined
              }
            />
            <Stat
              metric="ic"
              value={current.ic}
              ctx={{ gated, n: current.independentN }}
              raw={num(current.ic)}
              sub={
                current.icCI
                  ? `95% CI ${num(current.icCI[0], 2)}–${num(current.icCI[1], 2)}`
                  : gated
                    ? "withheld — too few obs"
                    : undefined
              }
            />
            <Stat
              metric="base_rate"
              value={current.baseRate}
              ctx={{ gated }}
              raw={pct(current.baseRate)}
              sub={gated ? "withheld — too few obs" : "the constant-forecast benchmark"}
            />
          </div>

          {/* Stage 4 (tables→charts): the REPORT CARD — the same gated
              readings as big grade meters. Below the gate every meter is an
              honest EMPTY bar with the "no read yet" sentence; the gate never
              gets bypassed by a visualization. */}
          <TrackReportCard
            gated={gated}
            n={current.independentN ?? 0}
            winRate={current.winRate}
            baseRate={current.baseRate}
            brier={current.brier}
            reliabilityScore={current.reliabilityScore}
          />
          </StorySection>

          {/* ── STAGE 3 STORY, SECTION 3 (SIMPLE mode starts folded) ── */}
          <StorySection
            n={3}
            title="THE DETAILS"
            sub="calibration curve, coverage, by-market sample, self-verification"
            collapsible
          >
          {/* RELIABILITY CURVE + COVERAGE */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <section className="panel reveal-item">
              <div className="panel-h">
                <span>RELIABILITY (CALIBRATION)</span>
                {current.reliabilityScore != null && !gated && (
                  <span className="ml-auto flex items-center gap-1.5">
                    <span className="chip tnum">score {num(current.reliabilityScore)}</span>
                    <HelpTip label="What does the reliability score mean?">
                      Lower is better: it is the mean gap |predicted − realized| across
                      probability bins, so 0 would be perfect calibration.
                    </HelpTip>
                  </span>
                )}
              </div>
              <div className="p-3">
                <ReliabilityCurve bins={current.reliability ?? []} />
                <p className="mt-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  Each dot: predictions in a probability bin, plotted mean-predicted
                  (x) vs mean-realized (y). On the diagonal = perfectly calibrated.
                  {gated && " Too thin to read yet — shown for shape only."}
                </p>
              </div>
            </section>

            <section className="panel reveal-item">
              <div className="panel-h">
                <span>RESOLVED COVERAGE · ALL HORIZONS</span>
              </div>
              <div className="table-wrap">
                <table className="v4-table w-full text-[0.75rem]">
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
              <p className="px-4 pb-3 pt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                &ldquo;maturing&rdquo; predictions haven&rsquo;t hit their horizon yet — they
                can&rsquo;t be graded without lookahead, so they wait.
              </p>
            </section>
          </div>

          {/* BY MARKET (descriptive) */}
          {current.byMarket && current.byMarket.length > 0 && (
            <section className="panel reveal-item">
              <div className="panel-h">
                <span>BY MARKET · DESCRIPTIVE</span>
                <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  describes the sample, not a per-market skill claim
                </span>
              </div>
              <div className="table-wrap">
                <table className="v4-table w-full text-[0.75rem]">
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
                        {/* Stage 4: inline magnitude bars (absolute 0–100% scale,
                            neutral color — this block is descriptive, not a skill claim) */}
                        <td className="px-4 py-2 text-right">
                          <CellBar
                            frac={Number.isFinite(m.upRate) ? m.upRate : null}
                            label={pct(m.upRate)}
                            color="var(--dim)"
                            title="share of this market's resolved outcomes that went up — descriptive, absolute 0–100% scale"
                          />
                        </td>
                        <td className="px-4 py-2 text-right">
                          <CellBar
                            frac={Number.isFinite(m.dirHitRate) ? m.dirHitRate : null}
                            label={pct(m.dirHitRate)}
                            color="var(--dim)"
                            title="directional hit rate in this market's sample — descriptive (compare against the up rate), absolute 0–100% scale"
                          />
                        </td>
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
            <section className="panel reveal-item">
              <div className="panel-h">
                <span>SELF-VERIFYING · LEDGER</span>
                <Link
                  href="/lab/signal-backtest"
                  className="ml-auto chip cursor-pointer hover:border-[var(--border-strong)] hover:text-[var(--text)]"
                  title="See the own-signal backtester (OOS IC / quintiles / costed equity vs SPY)"
                >
                  compare · signal backtest →
                </Link>
              </div>
              <div className="p-4 text-[0.75rem]">
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
                    <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      Every flagship prediction is hash-chained (append-only). A verified
                      chain means no historical prediction was silently edited or deleted —
                      the record you&rsquo;re grading is the record that was made.
                    </p>
                    {current.ledger.head && (
                      <p className="tnum break-all text-[0.75rem]" style={{ color: "var(--faint)" }}>
                        head {current.ledger.head.slice(0, 24)}…
                      </p>
                    )}
                  </div>
                ) : (
                  <p style={{ color: "var(--faint)" }}>ledger unavailable.</p>
                )}
              </div>
            </section>

            <section className="panel reveal-item">
              <div className="panel-h">
                <span>SELF-VERIFYING · PAPER P&amp;L</span>
                <Link
                  href="/lab/paper"
                  className="ml-auto chip cursor-pointer hover:border-[var(--border-strong)] hover:text-[var(--text)]"
                  title="Open the full simulated paper-trading book"
                >
                  monitor · paper book →
                </Link>
              </div>
              <div className="p-4 text-[0.75rem]">
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
                    <div className="col-span-2 mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
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
          <section className="panel p-4 text-[0.75rem]" style={{ color: "var(--faint)" }}>
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
          </StorySection>
        </>
      )}
    </div>
  );
}

/** Stage 4 (tables→charts): the track record's grade meters — win rate vs
 *  base rate, prediction error, and calibration honesty, each rendered from
 *  the SAME plain.ts reading (bar + sentence can never disagree). Gated →
 *  every meter is empty and says so. */
function TrackReportCard({
  gated,
  n,
  winRate,
  baseRate,
  brier,
  reliabilityScore,
}: {
  gated: boolean;
  n: number;
  winRate: number | null | undefined;
  baseRate: number | null | undefined;
  brier: number | null | undefined;
  reliabilityScore: number | null | undefined;
}) {
  const mode = useViewMode();
  const ctx: PlainCtx = { gated, n };
  const winRead = readMetric("win_rate", winRate, { ...ctx, baseRate: baseRate ?? undefined });
  const brierRead = readMetric("brier", brier, ctx);
  const relRead = readMetric("reliability", reliabilityScore, ctx);
  return (
    <section className="panel reveal-item">
      <div className="panel-h">
        REPORT CARD
        <span className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
          {gated
            ? "all meters empty on purpose — skill numbers are withheld below the significance gate"
            : "bar and sentence come from the same reading"}
        </span>
      </div>
      <div className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-3">
        <GradeMeter label={metricLabel("win_rate", mode)} reading={winRead} />
        <GradeMeter label={metricLabel("brier", mode)} reading={brierRead} />
        <GradeMeter label={metricLabel("reliability", mode)} reading={relRead} />
      </div>
    </section>
  );
}

/** Hero stat backed by the translation layer: label wording follows the
 *  SIMPLE/PRO toggle and the value renders through <Plain>. */
function Stat({
  metric,
  value,
  ctx,
  raw,
  sub,
}: {
  metric: MetricKey;
  value: number | null | undefined;
  ctx?: PlainCtx;
  raw?: string;
  sub?: string;
}) {
  const mode = useViewMode();
  return (
    <div className="panel p-3">
      <div className="text-[0.75rem] uppercase tracking-[0.14em]" style={{ color: "var(--faint)" }}>
        {metricLabel(metric, mode)}
      </div>
      <div className="mt-1 text-[0.82rem]">
        <Plain metric={metric} value={value} ctx={ctx} raw={raw} />
      </div>
      {sub && (
        <div className="mt-0.5 text-[0.75rem] tnum" style={{ color: "var(--faint)" }}>
          {sub}
        </div>
      )}
    </div>
  );
}

/** Credibility wave — REGIMES — LIVE: per-kind live regime grading. The whole
 *  point is CLAIMED (frozen at call time) next to LIVE (what actually
 *  resolved); below 30 resolutions the kind's gate note renders verbatim. */
const REGIME_KIND_ORDER = ["trend21", "trend63", "liquidity21", "vol21"];

function RegimesLivePanel({
  regimes,
}: {
  regimes: ReturnType<typeof trackRecordRegimes>;
}) {
  if (!regimes) return null;
  if (!regimes.available) {
    return (
      <section className="panel reveal-item">
        <div className="panel-h">REGIMES — LIVE</div>
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          regime grading unavailable{regimes.error ? ` — ${regimes.error}` : ""}.
        </p>
      </section>
    );
  }
  const kinds = regimes.kinds ?? {};
  const order = [
    ...REGIME_KIND_ORDER.filter((k) => k in kinds),
    ...Object.keys(kinds).filter((k) => !REGIME_KIND_ORDER.includes(k)).sort(),
  ];
  return (
    <section className="panel reveal-item">
      <div className="panel-h">
        <span>REGIMES — LIVE</span>
        <span
          className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
          title={regimes.dedupNote}
        >
          live resolution vs the accuracy claimed at call time
        </span>
      </div>
      {order.length === 0 ? (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          no regime calls have resolved yet — rows appear as calls hit their horizons.
        </p>
      ) : (
        <div className="table-wrap">
          <table className="v4-table w-full text-[0.75rem]">
            <thead>
              <tr style={{ color: "var(--faint)" }}>
                <th className="px-4 py-2 text-left font-medium">kind</th>
                <th className="px-4 py-2 text-right font-medium">resolved</th>
                <th className="px-4 py-2 text-right font-medium">live accuracy</th>
                <th className="px-4 py-2 text-right font-medium">claimed</th>
              </tr>
            </thead>
            <tbody>
              {order.map((k) => {
                const e: RegimeKindRecord = kinds[k];
                return (
                  <tr key={k} style={{ borderTop: "1px solid var(--border)" }}>
                    <td className="px-4 py-2">{k}</td>
                    <td className="px-4 py-2 text-right tnum">
                      {e.resolvedN.toLocaleString("en-US")}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {e.gated ? (
                        <span style={{ color: "var(--warn)" }}>
                          {e.note ?? "not yet significant"}
                        </span>
                      ) : (
                        <span className="tnum">
                          {pct(e.liveAccuracy)}
                          {e.liveAccuracyCI
                            ? ` (95% CI ${pct(e.liveAccuracyCI[0])}–${pct(e.liveAccuracyCI[1])})`
                            : ""}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right tnum" style={{ color: "var(--dim)" }}>
                      {pct(e.claimed)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <p className="px-4 pb-3 pt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        each call&rsquo;s claimed accuracy was frozen at call time — live resolution grades
        it against what actually happened.
      </p>
    </section>
  );
}

/** Credibility wave — WHEN WE WERE WRONG: the latest high-conviction regime
 *  misses with their plain-English narratives. Owning misses in public IS the
 *  credibility play; the empty state says why empty is expected early. */
function RegimeMissesPanel({ pms }: { pms: RegimePostmortems | null }) {
  const rows = pms?.postmortems ?? [];
  return (
    <section className="panel reveal-item">
      <div className="panel-h">
        <span>WHEN WE WERE WRONG</span>
        <span
          className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
          title={pms?.note}
        >
          high-conviction regime misses, newest first
        </span>
      </div>
      {rows.length === 0 ? (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          no high-conviction regime misses resolved yet — this list is expected to be
          non-empty over time; a 96% tier is still wrong ~1 in 25 times.
        </p>
      ) : (
        <ul className="max-h-[420px] overflow-y-auto px-4 py-2">
          {rows.map((p) => (
            <li
              key={p.outcomeId}
              className="py-2 text-[0.75rem]"
              style={{ borderTop: "1px solid var(--border)" }}
            >
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <span className="font-semibold">{p.symbol}</span>
                <span style={{ color: "var(--dim)" }}>{p.kind}</span>
                <span>
                  called <span style={{ color: "var(--warn)" }}>{p.regime}</span> · realized{" "}
                  <span style={{ color: "var(--ask)" }}>{p.actual}</span>
                </span>
                <span className="tnum" style={{ color: "var(--faint)" }}>
                  claimed {pct(p.claimedAccuracy)} · conviction {pct(p.conviction)}
                </span>
                <span className="ml-auto tnum" style={{ color: "var(--faint)" }}>
                  {ago(p.createdAt)}
                </span>
              </div>
              <p className="mt-1 leading-relaxed" style={{ color: "var(--dim)" }}>
                {p.narrative}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Mini({ label, value, tip, good }: { label: string; value: string; tip?: string; good?: boolean }) {
  const color = good === undefined ? "var(--text)" : good ? "var(--bid)" : "var(--ask)";
  return (
    <div>
      <div className="flex items-center gap-1 text-[0.75rem] tracking-wider" style={{ color: "var(--faint)" }}>
        {label}
        {tip && <HelpTip label={`What does ${label} mean?`}>{tip}</HelpTip>}
      </div>
      <div className="mt-0.5 text-[0.95rem] font-semibold tnum" style={{ color }}>
        {value}
      </div>
    </div>
  );
}
