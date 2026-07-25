"use client";

// WHY IT'S MOVING (why-it's-moving wave) — the symbol page's attribution
// surface, built on the daemon's two finished-but-unconsumed engines:
//
//   /api/explain     — signed contributors (supports vs opposes), a REAL
//                      historical analog with its realized outcome, the
//                      accuracy of THIS conviction band, and the engine's
//                      explicit refusal to offer a BUY/SELL (whyNotDirection).
//   /api/attribution — a state-conditioned historical prior blended with this
//                      symbol's live resolved outcomes. It USED to cost ~29s
//                      per call (it walked the whole resolved-outcome ledger)
//                      and so hid behind an opt-in button; the daemon now caches
//                      that ledger walk fleet-wide per horizon and pre-warms it,
//                      so it loads on mount alongside /api/explain — which is
//                      the honest pairing, since neither half of the evidence
//                      means much without the other.
//
// HONESTY: `caveat`, `whyNotDirection`, `analog.note` and the attribution
// `explanation` are rendered VERBATIM. Absence is a real answer here — an
// unavailable explanation says why it is unavailable and stops.

import { useEffect, useState } from "react";
import {
  attributionFor,
  explain,
  pollMs,
  POLL_SLOW,
  type AttributionResponse,
  type Explain,
  type ExplainContribution,
  type Market,
} from "@/lib/api";
import { fmtTs } from "@/lib/format";
import HelpTip from "@/components/HelpTip";

function pct(v: number | undefined, dec = 1): string {
  return v == null || !isFinite(v) ? "—" : `${(v * 100).toFixed(dec)}%`;
}

function signed(v: number, dec = 2): string {
  return `${v >= 0 ? "+" : ""}${v.toFixed(dec)}`;
}

/** One signed contributor row. Color follows the SIGN of the weight, never a
 *  bullish/bearish reading — these push toward or against a STRUCTURE call. */
function ContribRow({ c }: { c: ExplainContribution }) {
  const good = c.weight >= 0;
  return (
    <li className="flex flex-col gap-0.5 border-t px-3 py-2" style={{ borderColor: "var(--border)" }}>
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="text-[0.8rem]" style={{ color: "var(--text)" }}>
          {c.name}
        </span>
        <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
          {signed(c.value)}
        </span>
        <span
          className="chip tnum ml-auto text-[0.75rem]"
          style={{
            color: good ? "var(--bid)" : "var(--ask)",
            borderColor: good ? "var(--bid)" : "var(--ask)",
          }}
        >
          weight {signed(c.weight)}
        </span>
      </div>
      <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        {c.detail}
      </p>
    </li>
  );
}

export default function WhyMovingPanel({
  symbol,
  market,
}: {
  symbol: string;
  market: Market;
}) {
  // Every slice is keyed by symbol|market so a symbol switch never shows
  // another symbol's attribution (and never needs a setState in an effect body).
  const key = `${symbol}|${market}`;
  const [state, setState] = useState<{ key: string; e: Explain } | null>(null);
  const [errState, setErrState] = useState<{ key: string; msg: string } | null>(null);
  const err = errState && errState.key === key ? errState.msg : null;

  // Blended attribution rides the SAME load as explain: allSettled, not all —
  // one half failing must never blank the other, and the two are separately
  // keyed so a symbol switch invalidates a stale report by construction rather
  // than mislabeling it with the new symbol's name.
  const [attrState, setAttrState] =
    useState<{ key: string; a: AttributionResponse } | null>(null);
  const [attrErrState, setAttrErrState] = useState<{ key: string; msg: string } | null>(null);
  const attr = attrState && attrState.key === key ? attrState.a : null;
  const attrErr = attrErrState && attrErrState.key === key ? attrErrState.msg : null;

  useEffect(() => {
    let alive = true;
    const k = `${symbol}|${market}`;
    const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
    // POLL_SLOW: the trend audit moves on daily bars.
    const load = async () => {
      const [ex, at] = await Promise.allSettled([
        explain(symbol, market),
        attributionFor(symbol, market, "1d"),
      ]);
      if (!alive) return;
      if (ex.status === "fulfilled") {
        setState({ key: k, e: ex.value });
        setErrState(null);
      } else {
        setErrState({ key: k, msg: msg(ex.reason) });
      }
      if (at.status === "fulfilled") {
        setAttrState({ key: k, a: at.value });
        setAttrErrState(null);
      } else {
        setAttrErrState({ key: k, msg: msg(at.reason) });
      }
    };
    void load();
    const stop = pollMs(() => void load(), POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market]);

  const ex = state && state.key === key ? state.e : null;
  const analog = ex?.analog;

  return (
    <section className="panel" aria-label={`why ${symbol} is moving — measured attribution`}>
      <div className="panel-h flex-wrap gap-2">
        <span>WHY IT&rsquo;S MOVING · {symbol}</span>
        <HelpTip label="what this panel is">
          The auditable decomposition of the structural regime call: every
          contributor is a measured quantity with a signed weight, and the analog
          is a real lookup in this symbol&rsquo;s own history with what actually
          happened next — not an illustration. No BUY/SELL is offered here, and
          the engine states why.
        </HelpTip>
        {ex?.asOf != null && (
          <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
            as of {fmtTs(ex.asOf)}
          </span>
        )}
      </div>

      {err !== null && ex === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          no attribution available yet — {err}
        </p>
      )}
      {ex === null && err === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading the measured attribution…
        </p>
      )}

      {/* Honest absence: the engine's own reason, verbatim. */}
      {ex !== null && !ex.available && (
        <div className="px-4 py-4">
          <p className="m-0 text-[0.8rem]" style={{ color: "var(--dim)" }}>
            no attribution available yet
          </p>
          {ex.reason && (
            <p className="mt-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              {ex.reason}
            </p>
          )}
          {ex.barsHave != null && ex.barsNeed != null && (
            <p className="tnum mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {ex.barsHave} daily bars stored · {ex.barsNeed} needed
            </p>
          )}
        </div>
      )}

      {ex !== null && ex.available && (
        <>
          {/* the call itself — banded accuracy, tier, conviction, tradeability */}
          <div className="flex flex-col gap-2 px-4 py-3">
            {ex.prediction && (
              <p className="m-0 text-[0.85rem] leading-relaxed" style={{ color: "var(--text)" }}>
                {ex.prediction}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              {ex.kind && <span className="chip uppercase tracking-wider">{ex.kind}</span>}
              {ex.regime && (
                <span className="chip" style={{ color: "var(--text)" }}>
                  {ex.regime}
                </span>
              )}
              {ex.horizonDays != null && (
                <span className="chip tnum">{ex.horizonDays}d horizon</span>
              )}
              {ex.bandedAccuracy != null && (
                <span className="chip tnum" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                  banded accuracy {pct(ex.bandedAccuracy)}
                </span>
              )}
              {ex.tier && <span className="chip">{ex.tier}</span>}
              {ex.conviction != null && (
                <span className="chip tnum">conviction {pct(ex.conviction)}</span>
              )}
              {/* Optional field — rendered prominently only when present. */}
              {ex.tradeability && (
                <span
                  className="chip"
                  style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                >
                  tradeability: {ex.tradeability}
                </span>
              )}
            </div>
          </div>

          {/* supports vs opposes — two signed lists, side by side on desktop */}
          <div className="grid grid-cols-1 gap-0 lg:grid-cols-2">
            <div className="border-t" style={{ borderColor: "var(--border)" }}>
              <div
                className="px-3 pt-2 text-[0.75rem] font-bold tracking-[0.14em]"
                style={{ color: "var(--bid)" }}
              >
                SUPPORTS THE CALL ({ex.supports.length})
              </div>
              {ex.supports.length === 0 ? (
                <p className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  nothing measurably supports it
                </p>
              ) : (
                <ul className="m-0 list-none p-0">
                  {ex.supports.map((c) => (
                    <ContribRow key={`sup-${c.name}`} c={c} />
                  ))}
                </ul>
              )}
            </div>
            <div className="border-t" style={{ borderColor: "var(--border)" }}>
              <div
                className="px-3 pt-2 text-[0.75rem] font-bold tracking-[0.14em]"
                style={{ color: "var(--ask)" }}
              >
                OPPOSES THE CALL ({ex.opposes.length})
              </div>
              {ex.opposes.length === 0 ? (
                <p className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  nothing measurably opposes it
                </p>
              ) : (
                <ul className="m-0 list-none p-0">
                  {ex.opposes.map((c) => (
                    <ContribRow key={`opp-${c.name}`} c={c} />
                  ))}
                </ul>
              )}
            </div>
          </div>

          {/* the historical analog + its REALIZED outcome (a lookup, not a demo) */}
          {analog && (
            <div className="border-t px-4 py-3" style={{ borderColor: "var(--border)" }}>
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[0.75rem] font-bold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                  CLOSEST HISTORICAL ANALOG
                </span>
                {analog.found && (
                  <>
                    <span
                      className="chip tnum"
                      style={{
                        color: analog.fwdReturnPct >= 0 ? "var(--bid)" : "var(--ask)",
                        borderColor: analog.fwdReturnPct >= 0 ? "var(--bid)" : "var(--ask)",
                      }}
                    >
                      realized {signed(analog.fwdReturnPct, 1)}%
                    </span>
                    <span className="chip">
                      {analog.heldSide ? "held the same side" : "left the side"}
                    </span>
                    <span className="chip tnum">
                      then {signed(analog.priorDistPct, 1)}% from its average
                    </span>
                  </>
                )}
              </div>
              {/* the engine's own note — verbatim */}
              <p className="mt-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                {analog.note}
              </p>
            </div>
          )}

          {/* ── BLENDED ATTRIBUTION (loaded with explain — see header note) ── */}
          <div className="border-t px-4 py-3" style={{ borderColor: "var(--border)" }}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[0.75rem] font-bold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                BLENDED EVIDENCE (HISTORICAL PRIOR ⊕ LIVE OUTCOMES)
              </span>
              <HelpTip label="what is being blended">
                Two sources kept strictly separate: a state-conditioned
                HISTORICAL prior over ~2y of this symbol&rsquo;s bars, and the
                deployed engine&rsquo;s LIVE resolved outcomes for this symbol,
                deduped to one independent observation per day. They are weighted
                by sample size, so a thin live record is reported as
                underpowered — not as a measured absence of edge.
              </HelpTip>
            </div>

            {attrErr !== null && (
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
                no blended attribution available — {attrErr}
              </p>
            )}
            {attr === null && attrErr === null && (
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                blending the state-conditioned prior with this symbol&rsquo;s live
                resolved outcomes…
              </p>
            )}

            {attr !== null && attr.report !== null && (
              <div className="mt-2 flex flex-col gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="chip tnum">
                    blended P(up) {pct(attr.report.calibratedProbability)}
                  </span>
                  <span className="chip tnum">
                    band {pct(attr.report.uncertaintyLo)}–{pct(attr.report.uncertaintyHi)}
                  </span>
                  <span className="chip tnum">
                    prior n={attr.report.historicalPriorN} · live n={attr.report.liveResolvedN}
                  </span>
                  <span className="chip">driver: {attr.report.driver}</span>
                  <span className="chip">match: {attr.report.regimeMatch}</span>
                  {attr.currentState && (
                    <span className="chip mono">{attr.currentState}</span>
                  )}
                </div>
                {/* the engine's honest statement — verbatim */}
                <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                  {attr.report.explanation}
                </p>
                {attr.doctrine && (
                  <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                    {attr.doctrine}
                  </p>
                )}
              </div>
            )}
            {attr !== null && attr.report === null && (
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                no attribution available yet — the daemon returned no report for this
                symbol and horizon.
              </p>
            )}
          </div>
        </>
      )}

      {/* THE two caveats — verbatim, always rendered, even while empty. */}
      {ex?.caveat && (
        <p className="border-t px-4 pt-3 text-[0.75rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--warn)" }}>
          {ex.caveat}
        </p>
      )}
      <p className="px-4 pb-3 pt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
        {ex?.whyNotDirection ??
          "No BUY/SELL or expected-return field is offered here: the directional model was automatically retired after scoring below its own naive baseline, and dressing a rejected model in contributors would make it look more credible."}
      </p>
    </section>
  );
}
