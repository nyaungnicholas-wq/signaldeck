"use client";

import { useRef, useState } from "react";
import {
  optionPrice,
  optionVolEdge,
  type OptionPriceResult,
  type VolEdgeResult,
} from "@/lib/api";
import ErrorState from "@/components/ErrorState";
import { PageHero } from "@/components/ui/Kit";

const pct = (v: number | undefined, digits = 1) =>
  v == null || !isFinite(v) ? "—" : `${(v * 100).toFixed(digits)}%`;
const num = (v: number | undefined, digits = 2) =>
  v == null || !isFinite(v) ? "—" : v.toFixed(digits);
const money = (v: number | undefined) =>
  v == null || !isFinite(v) ? "—" : `${v < 0 ? "-" : ""}$${Math.abs(v).toFixed(2)}`;

const VERDICT_COLOR: Record<string, string> = {
  "iv-rich": "var(--bid)",
  "iv-cheap": "var(--ask)",
  "in-line": "var(--dim)",
};

const inputStyle = {
  background: "var(--panel2)",
  borderColor: "var(--border)",
  color: "var(--text)",
} as const;

function Field({
  id,
  label,
  value,
  onChange,
  width = "w-24",
  hint,
  onEnter,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  width?: string;
  hint?: string;
  onEnter?: () => void;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {label}
      </label>
      <input
        id={id}
        type="text"
        value={value}
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && onEnter) onEnter();
        }}
        className={`${width} rounded-lg border px-2.5 py-1.5 text-[0.75rem] tnum`}
        style={inputStyle}
      />
      {hint && (
        <span className="text-[0.7rem]" style={{ color: "var(--faint)" }}>
          {hint}
        </span>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  note,
  color,
}: {
  label: string;
  value: string;
  note?: string;
  color?: string;
}) {
  return (
    <div className="flex min-w-[7.5rem] flex-col gap-1">
      <span className="text-[0.7rem] uppercase tracking-[0.12em]" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      <span className="tnum text-[1.05rem] font-semibold" style={{ color: color ?? "var(--text)" }}>
        {value}
      </span>
      {note && (
        <span className="text-[0.7rem]" style={{ color: "var(--dim)" }}>
          {note}
        </span>
      )}
    </div>
  );
}

/** A caveat that cannot be dismissed — the payload's own words, verbatim. */
function Caveat({ text }: { text: string }) {
  return (
    <p
      className="border-t px-4 py-3 text-[0.72rem] leading-relaxed"
      style={{ borderColor: "var(--border)", color: "var(--dim)" }}
    >
      {text}
    </p>
  );
}

export default function OptionsPage() {
  const [symbol, setSymbol] = useState("NVDA");
  const [iv, setIv] = useState("0.35");
  const [days, setDays] = useState("91");
  const [vrp, setVrp] = useState("0.02");
  const [edge, setEdge] = useState<VolEdgeResult | null>(null);
  const [edgeLoading, setEdgeLoading] = useState(false);
  const [edgeErr, setEdgeErr] = useState<string | null>(null);
  const edgeReq = useRef(0);

  const [spot, setSpot] = useState("100");
  const [strike, setStrike] = useState("100");
  const [calcDays, setCalcDays] = useState("30");
  const [calcVol, setCalcVol] = useState("0.30");
  const [rate, setRate] = useState("0.04");
  const [kind, setKind] = useState<"call" | "put" | "straddle">("call");
  const [quote, setQuote] = useState("");
  const [priced, setPriced] = useState<OptionPriceResult | null>(null);
  const [calcErr, setCalcErr] = useState<string | null>(null);
  const calcReq = useRef(0);

  const runEdge = async () => {
    const sym = symbol.trim().toUpperCase();
    if (!sym) return;
    const id = ++edgeReq.current;
    setEdgeLoading(true);
    setEdgeErr(null);
    try {
      const res = await optionVolEdge({
        symbol: sym,
        market: "stocks",
        iv: Number(iv) || undefined,
        days: Number(days) || undefined,
        vrp: vrp === "" ? undefined : Number(vrp),
      });
      if (edgeReq.current !== id) return;
      setEdge(res);
    } catch (e) {
      if (edgeReq.current !== id) return;
      setEdgeErr(e instanceof Error ? e.message : String(e));
      setEdge(null);
    } finally {
      if (edgeReq.current === id) setEdgeLoading(false);
    }
  };

  const runCalc = async () => {
    const id = ++calcReq.current;
    setCalcErr(null);
    try {
      const res = await optionPrice({
        spot: Number(spot),
        strike: Number(strike),
        days: Number(calcDays),
        vol: Number(calcVol),
        rate: Number(rate),
        type: kind,
        price: quote === "" ? undefined : Number(quote),
      });
      if (calcReq.current !== id) return;
      setPriced(res);
    } catch (e) {
      if (calcReq.current !== id) return;
      setCalcErr(e instanceof Error ? e.message : String(e));
      setPriced(null);
    }
  };

  const f = edge?.forecast;
  const exp = edge?.expectation;
  const ed = edge?.edge;
  const st = edge?.volStats;
  const trade = edge?.trade;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="OPTIONS"
        subtitle="Compare your broker's implied volatility against this symbol's own history and price options with Black-Scholes-Merton."
      />

      <section className="hud-panel">
        <div className="panel-h">
          VOL EDGE
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            the validated vol-regime forecast vs the implied vol you supply
          </span>
        </div>

        <div className="flex flex-wrap items-end gap-x-5 gap-y-4 px-4 py-4">
          <Field
            id="edge-symbol"
            label="symbol"
            value={symbol}
            onChange={(v) => setSymbol(v.toUpperCase())}
            width="w-28"
            onEnter={runEdge}
          />
          <Field
            id="edge-iv"
            label="market implied vol"
            value={iv}
            onChange={setIv}
            hint="decimal — 0.35 = 35%, from YOUR broker"
            onEnter={runEdge}
          />
          <Field
            id="edge-days"
            label="days to expiry"
            value={days}
            onChange={setDays}
            hint="91 ≈ the forecast's 63 trading days"
            onEnter={runEdge}
          />
          <Field
            id="edge-vrp"
            label="assumed premium"
            value={vrp}
            onChange={setVrp}
            hint="variance risk premium — assumed, not measured"
            onEnter={runEdge}
          />
          <button
            type="button"
            onClick={runEdge}
            disabled={edgeLoading || !symbol.trim()}
            className="rounded-lg border px-3 py-1.5 text-[0.75rem] font-semibold tracking-[0.1em] disabled:opacity-50"
            style={inputStyle}
          >
            {edgeLoading ? "RUNNING…" : "RUN"}
          </button>
        </div>

        {edgeErr && <ErrorState message={edgeErr} retry={runEdge} />}

        {edge && (
          <>
            <p
              className="px-4 pb-3 text-[0.72rem] leading-relaxed"
              style={{ color: "var(--dim)" }}
            >
              {edge.whyNoChain}
            </p>

            {edge.gate && (
              <p
                className="mx-4 mb-4 rounded-lg border px-3 py-2 text-[0.75rem] leading-relaxed"
                style={{ borderColor: "var(--border)", color: "var(--dim)" }}
              >
                WITHHELD — {edge.gate}
              </p>
            )}

            {f && (
              <div className="flex flex-wrap gap-x-8 gap-y-4 border-t px-4 py-4" style={{ borderColor: "var(--border)" }}>
                <Stat
                  label="regime call"
                  value={f.regime.toUpperCase()}
                  note={f.tier}
                  color={f.regime === "elevated" ? "var(--ask)" : "var(--bid)"}
                />
                <Stat label="conviction" value={num(f.conviction, 2)} note={`vol rank ${num(f.rank, 2)}`} />
                <Stat
                  label="measured accuracy"
                  value={pct(f.historicalAccuracy)}
                  note="walk-forward, this conviction band"
                />
                {exp && (
                  <Stat
                    label="expected vol"
                    value={pct(exp.expected)}
                    note={`if right ${pct(exp.ifRight)} · if wrong ${pct(exp.ifWrong)}`}
                  />
                )}
                {st && (
                  <Stat
                    label="realized now"
                    value={pct(st.realized63)}
                    note={`21d ${pct(st.realized21)} · ${st.n} windows`}
                  />
                )}
              </div>
            )}

            {ed && (
              <div className="flex flex-wrap gap-x-8 gap-y-4 border-t px-4 py-4" style={{ borderColor: "var(--border)" }}>
                <Stat
                  label="verdict"
                  value={ed.verdict.toUpperCase()}
                  note={ed.robust ? "robust either way" : "conditional on the call"}
                  color={VERDICT_COLOR[ed.verdict] ?? "var(--text)"}
                />
                <Stat label="market IV" value={pct(ed.marketIV)} />
                <Stat
                  label="fair IV"
                  value={pct(ed.fairIV)}
                  note={`forecast ${pct(ed.expectedVol)} + ${pct(ed.vrp)} premium`}
                />
                <Stat
                  label="gap"
                  value={`${ed.edgeVol >= 0 ? "+" : ""}${(ed.edgeVol * 100).toFixed(1)} vol pts`}
                />
                <Stat
                  label="flips at premium"
                  value={pct(ed.breakevenVRP)}
                  note="above this, the verdict is the assumption talking"
                />
                {trade && (
                  <Stat
                    label="straddle gap"
                    value={money(trade.edgePerContract)}
                    note={`per contract, gross — market prices a ${pct(trade.marketBreakevenPct)} move vs forecast ${pct(trade.forecastMovePct)}`}
                  />
                )}
              </div>
            )}

            {ed && (
              <div className="flex flex-col gap-2 border-t px-4 py-3" style={{ borderColor: "var(--border)" }}>
                <p className="text-[0.78rem] leading-relaxed">{ed.expression}</p>
                <p className="text-[0.74rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                  {ed.robustNote}
                </p>
              </div>
            )}

            {edge.horizonMismatch && (
              <p
                className="mx-4 mb-3 rounded-lg border px-3 py-2 text-[0.74rem] leading-relaxed"
                style={{ borderColor: "var(--border)", color: "var(--dim)" }}
              >
                {edge.horizonMismatch}
              </p>
            )}

            {edge.next && !ed && (
              <p className="px-4 pb-3 text-[0.75rem]" style={{ color: "var(--dim)" }}>
                {edge.next}
              </p>
            )}

            {exp?.driftWarning && (
              <p
                className="mx-4 mb-3 rounded-lg border px-3 py-2 text-[0.74rem] leading-relaxed"
                style={{ borderColor: "var(--border)", color: "var(--ask)" }}
              >
                {exp.driftWarning}
              </p>
            )}

            {exp && <Caveat text={exp.basis} />}
            {edge.vrpNote && <Caveat text={edge.vrpNote} />}
            {ed && <Caveat text={ed.caveat} />}
            {trade && <Caveat text={trade.caveat} />}
          </>
        )}
      </section>

      <section className="panel">
        <div className="panel-h">
          CALCULATOR
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            Black-Scholes-Merton value, Greeks, and implied vol from a quote
          </span>
        </div>

        <div className="flex flex-wrap items-end gap-x-5 gap-y-4 px-4 py-4">
          <Field id="calc-spot" label="spot" value={spot} onChange={setSpot} onEnter={runCalc} />
          <Field id="calc-strike" label="strike" value={strike} onChange={setStrike} onEnter={runCalc} />
          <Field id="calc-days" label="days" value={calcDays} onChange={setCalcDays} onEnter={runCalc} />
          <Field id="calc-vol" label="vol" value={calcVol} onChange={setCalcVol} hint="decimal" onEnter={runCalc} />
          <Field id="calc-rate" label="rate" value={rate} onChange={setRate} hint="decimal" onEnter={runCalc} />
          <div className="flex flex-col gap-1.5">
            <label htmlFor="calc-kind" className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              type
            </label>
            <select
              id="calc-kind"
              value={kind}
              onChange={(e) => setKind(e.target.value as "call" | "put" | "straddle")}
              className="cursor-pointer rounded-lg border px-2 py-1.5 text-[0.75rem]"
              style={inputStyle}
            >
              <option value="call">call</option>
              <option value="put">put</option>
              <option value="straddle">straddle</option>
            </select>
          </div>
          <Field
            id="calc-quote"
            label="market price"
            value={quote}
            onChange={setQuote}
            hint="optional — solves implied vol"
            onEnter={runCalc}
          />
          <button
            type="button"
            onClick={runCalc}
            className="rounded-lg border px-3 py-1.5 text-[0.75rem] font-semibold tracking-[0.1em]"
            style={inputStyle}
          >
            PRICE
          </button>
        </div>

        {calcErr && <ErrorState message={calcErr} retry={runCalc} />}

        {priced?.priced && (
          <div className="flex flex-wrap gap-x-8 gap-y-4 border-t px-4 py-4" style={{ borderColor: "var(--border)" }}>
            <Stat label="value" value={money(priced.priced.price)} />
            <Stat label="delta" value={num(priced.priced.delta, 4)} />
            <Stat label="gamma" value={num(priced.priced.gamma, 4)} />
            <Stat label="vega" value={money(priced.priced.vega)} note="per 1 vol point" />
            <Stat label="theta" value={money(priced.priced.theta)} note="per calendar day" />
            <Stat label="rho" value={money(priced.priced.rho)} note="per 1pp of rate" />
            <Stat
              label="P(ITM)"
              value={pct(priced.priced.probITM)}
              note="risk-neutral — not a forecast"
              color="var(--dim)"
            />
          </div>
        )}

        {priced?.straddle && (
          <div className="flex flex-wrap gap-x-8 gap-y-4 border-t px-4 py-4" style={{ borderColor: "var(--border)" }}>
            <Stat label="straddle" value={money(priced.straddle.price)} />
            <Stat
              label="breakeven move"
              value={pct(priced.straddle.breakevenMovePct)}
              note={`${num(priced.straddle.lowerBreakeven)} — ${num(priced.straddle.upperBreakeven)}`}
            />
            <Stat label="vega" value={money(priced.straddle.vega)} note="per 1 vol point" />
            <Stat label="theta" value={money(priced.straddle.theta)} note="per calendar day" />
            <Stat label="net delta" value={num(priced.straddle.netDelta, 4)} />
          </div>
        )}

        {priced && (
          <div className="flex flex-wrap gap-x-8 gap-y-3 border-t px-4 py-4" style={{ borderColor: "var(--border)" }}>
            {priced.impliedVol != null && (
              <Stat label="implied vol" value={pct(priced.impliedVol)} note="solved from your quote" />
            )}
            {priced.impliedVolNote && (
              <p className="max-w-3xl text-[0.74rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                {priced.impliedVolNote}
              </p>
            )}
          </div>
        )}

        {priced && <Caveat text={priced.assumptions} />}
      </section>
    </div>
  );
}
