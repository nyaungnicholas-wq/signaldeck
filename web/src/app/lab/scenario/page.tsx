"use client";

import { useRef, useState } from "react";
import { api, type ScenarioResult } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { PageHero } from "@/components/ui/Kit";

interface FactorDef {
  id: string;
  label: string;
  unit: string;
  min: number;
  max: number;
  step: number;
  default: number;
}

const FACTORS: FactorDef[] = [
  { id: "VIXCLS", label: "VIX", unit: "pts", min: -20, max: 20, step: 1, default: 10 },
  { id: "DGS10", label: "10Y yield", unit: "pp", min: -2, max: 2, step: 0.25, default: 0.5 },
  { id: "DGS2", label: "2Y yield", unit: "pp", min: -2, max: 2, step: 0.25, default: 0.5 },
  { id: "DFF", label: "Fed funds", unit: "pp", min: -2, max: 2, step: 0.25, default: 0.5 },
  { id: "BAMLH0A0HYM2", label: "high-yield spread", unit: "pp", min: -5, max: 5, step: 0.25, default: 1 },
];

const signShock = (v: number) => `${v > 0 ? "+" : ""}${v}`;
const signPct2 = (v: number) => `${v >= 0 ? "+" : ""}${v.toFixed(2)}%`;
const signBeta = (v: number) =>
  !isFinite(v) ? "—" : `${v >= 0 ? "+" : ""}${v.toFixed(3)}`;
const clamp01 = (v: number) => (!isFinite(v) ? 0 : Math.max(0, Math.min(1, v)));

export default function ScenarioPage() {
  const [symbol, setSymbol] = useState("NVDA");
  const [factorId, setFactorId] = useState(FACTORS[0].id);
  const [shock, setShock] = useState(FACTORS[0].default);

  const [result, setResult] = useState<ScenarioResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const reqId = useRef(0);
  const hasRun = useRef(false);

  const factor = FACTORS.find((f) => f.id === factorId) ?? FACTORS[0];
  const canRun = symbol.trim().length > 0 && !loading;

  const selectFactor = (id: string) => {
    const f = FACTORS.find((x) => x.id === id) ?? FACTORS[0];
    setFactorId(f.id);
    setShock(f.default);
  };

  const run = async () => {
    const sym = symbol.trim().toUpperCase();
    if (!sym) return;
    const id = ++reqId.current;
    hasRun.current = true;
    setLoading(true);
    setErr(null);
    try {
      const res = await api.scenario(sym, factorId, shock);
      if (reqId.current !== id) return;
      setResult(res);
    } catch (e) {
      if (reqId.current !== id) return;
      setErr(e instanceof Error ? e.message : String(e));
      setResult(null);
    } finally {
      if (reqId.current === id) setLoading(false);
    }
  };

  const onSliderRelease = () => {
    if (hasRun.current && symbol.trim()) void run();
  };

  const impact = result?.impact;
  const move = impact?.ExpectedMovePct ?? 0;
  const moveColor = move >= 0 ? "var(--bid)" : "var(--ask)";
  const resFactorLabel = result
    ? (FACTORS.find((f) => f.id === result.factor)?.label ?? result.factor)
    : "";
  const resUnit = result
    ? (FACTORS.find((f) => f.id === result.factor)?.unit ?? "")
    : "";

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="MACRO SCENARIO"
        subtitle="Estimate how a macro shock would move a symbol, from its historical sensitivity (OLS beta) to that factor."
        right={
          result && (
            <div className="flex items-center gap-2">
              <span className="mono text-sm" style={{ color: 'var(--hud)' }}>{result.symbol}</span>
              <span className="text-sm" style={{ color: 'var(--dim)' }}>{resFactorLabel}</span>
            </div>
          )
        }
      />

      <section className="panel">
        <div className="panel-h">
          SCENARIO
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            pick a symbol, a macro factor, and a shock — then run
          </span>
        </div>

        <div className="flex flex-wrap items-end gap-x-5 gap-y-4 px-4 py-4">
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="scenario-symbol"
              className="text-[0.75rem]"
              style={{ color: "var(--faint)" }}
            >
              symbol
            </label>
            <input
              id="scenario-symbol"
              type="text"
              value={symbol}
              onChange={(e) => setSymbol(e.target.value.toUpperCase())}
              onKeyDown={(e) => {
                if (e.key === "Enter") void run();
              }}
              placeholder="NVDA"
              spellCheck={false}
              autoComplete="off"
              className="w-28 rounded-lg border px-2.5 py-1.5 text-[0.75rem] uppercase tnum"
              style={{
                background: "var(--panel2)",
                borderColor: "var(--border)",
                color: "var(--text)",
              }}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="scenario-factor"
              className="text-[0.75rem]"
              style={{ color: "var(--faint)" }}
            >
              macro factor
            </label>
            <select
              id="scenario-factor"
              value={factorId}
              onChange={(e) => selectFactor(e.target.value)}
              className="cursor-pointer rounded-lg border px-2 py-1.5 text-[0.75rem]"
              style={{
                background: "var(--panel2)",
                borderColor: "var(--border)",
                color: "var(--text)",
              }}
            >
              {FACTORS.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.label} ({f.id})
                </option>
              ))}
            </select>
          </div>

          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="scenario-shock"
              className="flex items-center gap-2 text-[0.75rem]"
              style={{ color: "var(--faint)" }}
            >
              <span>shock</span>
              <span className="tnum" style={{ color: "var(--text)" }}>
                {signShock(shock)} {factor.unit}
              </span>
            </label>
            <input
              id="scenario-shock"
              type="range"
              min={factor.min}
              max={factor.max}
              step={factor.step}
              value={shock}
              onChange={(e) => setShock(Number(e.target.value))}
              onPointerUp={onSliderRelease}
              onKeyUp={onSliderRelease}
              aria-label={`${factor.label} shock: ${signShock(shock)} ${factor.unit}`}
              aria-valuetext={`${signShock(shock)} ${factor.unit}`}
              className="w-48 cursor-pointer sm:w-64"
              style={{ accentColor: "var(--accent)" }}
            />
            <div
              className="flex justify-between text-[0.75rem] tnum"
              style={{ color: "var(--faint)" }}
            >
              <span>{signShock(factor.min)}</span>
              <span>{signShock(factor.max)}</span>
            </div>
          </div>

          <button
            type="button"
            onClick={() => void run()}
            disabled={!canRun}
            className="min-h-[40px] cursor-pointer rounded-lg border px-4 py-1.5 text-[0.75rem] font-bold tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
            style={{
              background: "rgba(251,191,36,.10)",
              borderColor: "var(--accent)",
              color: "var(--accent)",
            }}
          >
            {loading ? "running…" : "Run"}
          </button>
        </div>
      </section>

      {err !== null && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Start it with signaldeckd, then Run again."
          retry={() => void run()}
        />
      )}

      {err === null && loading && (
        <Skeleton lines={4} label={`estimating ${symbol}'s sensitivity to ${factor.label}`} />
      )}

      {err === null && !loading && result === null && (
        <EmptyState
          message="No scenario yet"
          detail="Pick a symbol, a macro factor, and a shock, then Run to estimate the move from historical sensitivity. Sensitivity is not a forecast."
        />
      )}

      {err === null && !loading && result !== null && impact && (
        <section className="hud-panel">
          <div className="panel-h">
            SCENARIO ESTIMATE
            {impact.ShockLabel && (
              <span className="tnum" style={{ color: "var(--faint)" }}>
                shock applied: {impact.ShockLabel}
              </span>
            )}
          </div>

          <div className="flex flex-col gap-5 px-4 py-5">
            {impact.Gated ? (
              <p className="text-lg leading-snug sm:text-xl" style={{ color: "var(--dim)" }}>
                <span className="mono" style={{ color: "var(--text)" }}>
                  {resFactorLabel}
                </span>{" "}
                <span className="tnum" style={{ color: "var(--text)" }}>
                  {signShock(result.shock)} {resUnit}
                </span>
                {" → "}
                <span className="font-semibold" style={{ color: "var(--warn)" }}>
                  insufficient history (N={impact.N})
                </span>
              </p>
            ) : (
              <>
                <p className="text-lg leading-snug sm:text-xl" style={{ color: "var(--dim)" }}>
                  <span className="mono" style={{ color: "var(--text)" }}>
                    {resFactorLabel}
                  </span>{" "}
                  <span className="tnum" style={{ color: "var(--text)" }}>
                    {signShock(result.shock)} {resUnit}
                  </span>
                  {" → estimated "}
                  <span className="tnum font-bold" style={{ color: moveColor }}>
                    <span aria-hidden="true">{move >= 0 ? "▲" : "▼"}</span> {signPct2(move)}
                  </span>
                  {" move in "}
                  <span className="mono" style={{ color: "var(--text)" }}>
                    {result.symbol}
                  </span>
                </p>

                <div className="flex flex-wrap gap-x-8 gap-y-3">
                  <div className="flex flex-col gap-1">
                    <span
                      className="text-[0.75rem]"
                      style={{ color: "var(--faint)" }}
                      title="OLS slope — the symbol's average % move per 1-unit move in the factor, from paired daily history."
                    >
                      Beta
                    </span>
                    <span className="tnum text-sm" style={{ color: "var(--text)" }}>
                      {signBeta(impact.Beta)}
                    </span>
                  </div>

                  <div className="flex flex-col gap-1">
                    <span
                      className="text-[0.75rem]"
                      style={{ color: "var(--faint)" }}
                      title="Share of the symbol's variance explained by this factor (0–1). A low R² means a weak, noisy relationship — treat the estimate with caution."
                    >
                      R²
                    </span>
                    <div className="flex items-center gap-2">
                      <div
                        className="h-1.5 w-24 overflow-hidden rounded-full"
                        role="meter"
                        aria-label={`R squared ${(clamp01(impact.R2) * 100).toFixed(0)} percent`}
                        aria-valuenow={Number(clamp01(impact.R2).toFixed(2))}
                        aria-valuemin={0}
                        aria-valuemax={1}
                        style={{ background: "var(--panel2)", border: "1px solid var(--border)" }}
                      >
                        <div
                          className="h-full rounded-full"
                          style={{
                            width: `${clamp01(impact.R2) * 100}%`,
                            background: "var(--accent)",
                          }}
                        />
                      </div>
                      <span className="tnum text-sm" style={{ color: "var(--dim)" }}>
                        {(clamp01(impact.R2) * 100).toFixed(0)}%
                      </span>
                    </div>
                  </div>

                  <div className="flex flex-col gap-1">
                    <span
                      className="text-[0.75rem]"
                      style={{ color: "var(--faint)" }}
                      title="Paired trading days used in the regression — more days, more reliable a beta."
                    >
                      N (paired days)
                    </span>
                    <span className="tnum text-sm" style={{ color: "var(--text)" }}>
                      {impact.N}
                    </span>
                  </div>
                </div>
              </>
            )}

            {impact.Note && (
              <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                {impact.Note}
              </p>
            )}

            {result.disclaimer && (
              <p
                className="text-[0.75rem] leading-relaxed"
                style={{ color: "var(--faint)", borderTop: "1px solid var(--border)", paddingTop: "0.75rem" }}
              >
                {result.disclaimer}
              </p>
            )}
          </div>
        </section>
      )}
    </div>
  );
}
