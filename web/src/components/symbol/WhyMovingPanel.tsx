"use client";

// WHY IT'S MOVING (why-it's-moving wave) — the symbol page's attribution
// surface, built on the daemon's two finished-but-unconsumed engines:
//
//   /api/explain     — signed contributors (supports vs opposes), a REAL
//                      historical analog with its realized outcome, the
//                      accuracy of THIS conviction band, and the engine's
//                      explicit refusal to offer a BUY/SELL (whyNotDirection).
//   /api/attribution — a state-conditioned historical prior blended with this
//                      symbol's live resolved outcomes. MEASURED at ~63s per
//                      call against the live daemon (it walks the whole
//                      resolved-outcome ledger), so it is loaded ON DEMAND
//                      behind a button that says so — never on mount, because
//                      one minute of spinner is not an explanation.
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
  const [data, setData] = useState<Explain | null>(null);
  const [err, setErr] = useState<string | null>(null);

  // Keyed by symbol|market so a symbol switch never shows another symbol's
  // attribution; POLL_SLOW because the trend audit moves on daily bars.
  const key = `${symbol}|${market}`;
  const [dataKey, setDataKey] = useState<string>("");

  useEffect(() => {
    let alive = true;
    const load = () =>
      explain(symbol, market)
        .then((e) => {
          if (!alive) return;
          setData(e);
          setDataKey(`${symbol}|${market}`);
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
  }, [symbol, market]);

  // ── on-demand blended attribution (see the header note on its cost) ──
  const [attr, setAttr] = useState<AttributionResponse | null>(null);
  const [attrErr, setAttrErr] = useState<string | null>(null);
  const [attrLoading, setAttrLoading] = useState(false);
  // A symbol switch invalidates a loaded report rather than mislabeling it.
  useEffect(() => {
    setAttr(null);
    setAttrErr(null);
    setAttrLoading(false);
  }, [symbol, market]);

  const loadAttribution = () => {
    setAttrLoading(true);
    setAttrErr(null);
    attributionFor(symbol, market, "1d")
      .then((a) => setAttr(a))
      .catch((e: unknown) => setAttrErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setAttrLoading(false));
  };

  const ex = dataKey === key ? data : null;
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

          {/* ── BLENDED ATTRIBUTION (on demand — it is genuinely slow) ── */}
          <div className="border-t px-4 py-3" style={{ borderColor: "var(--border)" }}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[0.75rem] font-bold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                BLENDED EVIDENCE (HISTORICAL PRIOR ⊕ LIVE OUTCOMES)
              </span>
              {attr === null && (
                <button
                  type="button"
                  onClick={loadAttribution}
                  disabled={attrLoading}
                  className="chip min-h-[36px] cursor-pointer px-3 text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)]"
                  style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
                >
                  {attrLoading ? "scanning the ledger…" : "load (≈1 min scan) →"}
                </button>
              )}
              <HelpTip label="why this one is a button">
                It walks the entire resolved-outcome ledger for this symbol and
                measured ~63 seconds against the live daemon. Loading it on page
                open would hold the whole page hostage, so it is opt-in.
              </HelpTip>
            </div>

            {attrErr !== null && (
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
                no blended attribution available — {attrErr}
              </p>
            )}
            {attrLoading && attr === null && attrErr === null && (
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                blending the state-conditioned prior with this symbol&rsquo;s live
                resolved outcomes… this takes about a minute.
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
