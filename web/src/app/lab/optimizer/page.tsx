"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { api, type OptimizeResult, type OptWeights } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import AllocationBars from "@/components/optimizer/AllocationBars";
import { PageHero, StatTile } from "@/components/ui/Kit";

const DEFAULT_SYMBOLS = "NVDA,AAPL,SPY,QQQ,TSLA,AMD";
const LOOKBACKS = [60, 90, 120, 180, 252, 365];

function fmtRetDaily(v: number): string {
  if (!isFinite(v)) return "—";
  const p = v * 100;
  return `${p >= 0 ? "+" : "−"}${Math.abs(p).toFixed(2)}%`;
}
function fmtVolDaily(v: number): string {
  if (!isFinite(v)) return "—";
  return `${(v * 100).toFixed(2)}%`;
}
function fmtSharpe(v: number): string {
  if (!isFinite(v)) return "—";
  return `${v >= 0 ? "+" : "−"}${Math.abs(v).toFixed(3)}`;
}
function retColor(v: number): string {
  if (!isFinite(v) || v === 0) return "var(--text)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

const METHOD_LABEL: Record<string, string> = {
  min_variance: "min-variance",
  max_sharpe: "max-Sharpe",
  equal_weight_fallback: "equal-weight fallback",
};

function parseSymbols(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(",")) {
    const s = part.trim().toUpperCase();
    if (s && !seen.has(s)) {
      seen.add(s);
      out.push(s);
    }
  }
  return out;
}

function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      <span className="tnum text-base font-semibold" style={{ color: color ?? "var(--text)" }}>
        {value}
      </span>
    </div>
  );
}

function OptCard({
  title,
  subtitle,
  result,
  showReturn,
}: {
  title: string;
  subtitle: string;
  result: OptWeights;
  showReturn: boolean;
}) {
  const fallback = result.Method === "equal_weight_fallback";
  return (
    <section className="panel flex flex-col">
      <div className="panel-h">
        {title}
        <span
          className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: fallback ? "var(--warn)" : "var(--faint)" }}
          title={
            fallback
              ? "The optimizer could not solve this cleanly and fell back to equal weight — see the note below."
              : undefined
          }
        >
          {METHOD_LABEL[result.Method] ?? result.Method}
        </span>
      </div>

      <div className="flex flex-col gap-4 p-4">
        <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {subtitle}
        </p>

        <div className="flex flex-wrap gap-x-8 gap-y-3">
          {showReturn && (
            <Stat
              label="EXP. RET / DAY"
              value={fmtRetDaily(result.ExpRet)}
              color={retColor(result.ExpRet)}
            />
          )}
          <Stat label="VOL / DAY" value={fmtVolDaily(result.Vol)} />
          {showReturn && (
            <Stat label="SHARPE / DAY" value={fmtSharpe(result.Sharpe)} color={retColor(result.Sharpe)} />
          )}
        </div>

        <AllocationBars symbols={result.Symbols ?? []} weights={result.Weights ?? []} />

        {result.Note ? (
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {result.Note}
          </p>
        ) : null}
      </div>
    </section>
  );
}

export default function OptimizerPage() {
  const [symbolsInput, setSymbolsInput] = useState(DEFAULT_SYMBOLS);
  const [lookback, setLookback] = useState(180);

  const [data, setData] = useState<OptimizeResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);

  const runIdRef = useRef(0);

  const parsed = useMemo(() => parseSymbols(symbolsInput), [symbolsInput]);
  const tooFew = parsed.length < 2;

  const run = useCallback(async () => {
    const syms = parseSymbols(symbolsInput);
    if (syms.length < 2) return;
    const rid = ++runIdRef.current;
    setSubmitted(true);
    setLoading(true);
    setErr(null);
    try {
      const res = await api.optimize(syms.join(","), lookback);
      if (rid !== runIdRef.current) return;
      setData(res);
      setErr(null);
    } catch (e) {
      if (rid !== runIdRef.current) return;
      setErr(e instanceof Error ? e.message : String(e));
      setData(null);
    } finally {
      if (rid === runIdRef.current) setLoading(false);
    }
  }, [symbolsInput, lookback]);

  const dropped = useMemo(() => {
    if (!data || data.gated || !data.symbols) return [];
    const used = new Set<string>();
    for (const s of data.symbols) {
      const u = s.toUpperCase();
      used.add(u);
      const slash = u.indexOf("/");
      if (slash > 0) used.add(u.slice(0, slash));
    }
    return parseSymbols(symbolsInput).filter((s) => !used.has(s));
  }, [data, symbolsInput]);

  const is401 = err !== null && /\b401\b/.test(err);
  const is400 = err !== null && /\b400\b/.test(err);

  const gated = data !== null && (data.gated === true || !data.minVariance || !data.maxSharpe);
  const showResults = data !== null && !gated;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Portfolio Optimizer"
        subtitle="Compare minimum-variance and maximum-Sharpe allocations over your tracked symbols."
      />

      {showResults && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <StatTile label="Common Days" value={data.commonDays} i={0} />
          <StatTile label="Lookback" value={data.lookback} suffix="d" i={1} />
          <StatTile label="Symbols" value={data.symbols.length} i={2} />
          <StatTile label="Min-Variance Vol" value={data.minVariance.Vol * 100} suffix="%" decimals={2} i={3} />
        </div>
      )}

      <section className="panel">
        <div className="panel-h">UNIVERSE</div>
        <form
          className="flex flex-col gap-4 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            void run();
          }}
        >
          <div className="flex flex-col gap-4 md:flex-row md:items-end">
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <label
                htmlFor="opt-symbols"
                className="text-[0.75rem] tracking-wide"
                style={{ color: "var(--faint)" }}
              >
                SYMBOLS (comma-separated tickers)
              </label>
              <input
                id="opt-symbols"
                type="text"
                value={symbolsInput}
                onChange={(e) => setSymbolsInput(e.target.value)}
                spellCheck={false}
                autoComplete="off"
                placeholder="NVDA,AAPL,SPY,QQQ,TSLA,AMD"
                className="mono min-h-[40px] w-full rounded-lg border px-3 text-[0.8125rem] outline-none"
                style={{
                  background: "var(--panel2)",
                  borderColor: "var(--border)",
                  color: "var(--text)",
                }}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label
                htmlFor="opt-lookback"
                className="text-[0.75rem] tracking-wide"
                style={{ color: "var(--faint)" }}
              >
                LOOKBACK (trading days)
              </label>
              <select
                id="opt-lookback"
                value={lookback}
                onChange={(e) => setLookback(Number(e.target.value))}
                className="tnum min-h-[40px] cursor-pointer rounded-lg border px-3 text-[0.8125rem] outline-none"
                style={{
                  background: "var(--panel2)",
                  borderColor: "var(--border)",
                  color: "var(--text)",
                }}
              >
                {LOOKBACKS.map((d) => (
                  <option key={d} value={d}>
                    {d}
                  </option>
                ))}
              </select>
            </div>

            <button
              type="submit"
              disabled={loading || tooFew}
              className="min-h-[40px] cursor-pointer rounded-lg border px-5 text-[0.8125rem] font-semibold tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
              style={{
                borderColor: "var(--accent)",
                color: "var(--accent)",
                background: "var(--accent-dim)",
              }}
            >
              {loading ? "optimizing…" : "Optimize"}
            </button>
          </div>

          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {tooFew
              ? "Enter at least two tickers to optimize."
              : `${parsed.length} symbols. The optimizer only uses symbols you track that share overlapping daily history.`}
          </p>
        </form>
      </section>

      {!submitted && (
        <EmptyState
          message="Ready to optimize."
          detail="Choose your symbols and lookback above, then hit Optimize to see the minimum-variance and maximum-Sharpe allocations side by side."
        />
      )}

      {submitted && loading && <Skeleton lines={6} label="running the optimizer" />}

      {submitted && !loading && err !== null && (
        <ErrorState
          message={
            is401
              ? "Sign in to run the optimizer"
              : is400
                ? err.replace(/^API \d+:\s*/, "")
                : err
          }
          hint={
            is401
              ? "The optimizer runs over your tracked symbols' daily bars, so it needs your account — log in and run it again."
              : is400
                ? "Pass symbols you actually track (subscribe on the watchlist), at least two of them with overlapping daily history."
                : undefined
          }
          retry={() => void run()}
        />
      )}

      {submitted && !loading && err === null && gated && (
        <EmptyState
          message="Not enough overlapping history to optimize."
          detail={
            data?.note ??
            "The selected symbols don't share enough common daily bars yet (the optimizer needs at least 30). Let bars accrue, or pick symbols with longer shared history."
          }
        />
      )}

      {showResults && (
        <>
          {dropped.length > 0 && (
            <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
              Skipped {dropped.join(", ")} — not tracked, or no overlapping history with the rest.
              Optimized over {data.symbols.join(", ")}.
            </p>
          )}

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <OptCard
              title="MINIMUM VARIANCE"
              subtitle="Lowest-volatility long-only mix. This objective never looks at expected return, so no return or Sharpe is reported for it."
              result={data.minVariance}
              showReturn={false}
            />
            <OptCard
              title="MAXIMUM SHARPE"
              subtitle="Highest return-per-unit-risk long-only mix, using the trailing mean daily return as the (descriptive) expected-return input."
              result={data.maxSharpe}
              showReturn
            />
          </div>

          <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            <span className="tnum">{data.commonDays}</span> common trading days over a{" "}
            <span className="tnum">{data.lookback}</span>-day lookback. {data.note}
          </p>
        </>
      )}
    </div>
  );
}
