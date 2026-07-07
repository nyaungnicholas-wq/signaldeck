"use client";

// Forecast page: the real backtested logistic model (beyond mechanical
// expectancy). Pick a symbol from the watchlist; for each horizon we show
// P(up) RIGHT NEXT TO its out-of-sample grade. The honesty rule is the
// product — a probability with lift <= 0 is grayed out and labeled noise.

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  api,
  pollMs,
  screenerRows,
  type Forecast,
  type Market,
  type WatchRow,
} from "@/lib/api";
import { ago, fmtTs } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";

interface Selected {
  symbol: string;
  market: Market;
}

// ── grade helpers ──────────────────────────────────────────────────────────

function pctText(v: number, digits = 1): string {
  if (!isFinite(v)) return "—";
  return `${(v * 100).toFixed(digits)}%`;
}

/** Signed percentage-point lift vs the base rate (accuracy − baseRate). */
function liftText(lift: number): string {
  if (!isFinite(lift)) return "—";
  const pp = lift * 100;
  return `${pp >= 0 ? "+" : ""}${pp.toFixed(1)}pp`;
}

function num(v: number, digits = 3): string {
  if (!isFinite(v)) return "—";
  return v.toFixed(digits);
}

/** Grade cell — a small stat with a caption underneath. */
function Stat({
  label,
  value,
  color,
  title,
}: {
  label: string;
  value: string;
  color?: string;
  title?: string;
}) {
  return (
    <div className="flex flex-col gap-0.5" title={title}>
      <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      <span className="tnum text-[0.82rem]" style={{ color: color ?? "var(--text)" }}>
        {value}
      </span>
    </div>
  );
}

function ForecastCard({ f, symbol }: { f: Forecast; symbol: string }) {
  // Honesty gate: if the model can't beat the base rate out-of-sample, we
  // gray the probability and call it noise. lift is accuracy − baseRate.
  const beatsBaseRate = isFinite(f.lift) && f.lift > 0;
  const smallSample = beatsBaseRate && f.nEval < 100;
  const probPct = pctText(f.prob, 1);

  // The bar visualizes P(up); a live green (up) fill against a red remainder.
  const probClamped = Math.max(0, Math.min(1, isFinite(f.prob) ? f.prob : 0.5));
  const probColor = beatsBaseRate
    ? f.prob >= 0.5
      ? "var(--bid)"
      : "var(--ask)"
    : "var(--faint)";

  return (
    <section className="panel">
      <div className="panel-h">
        <span style={{ color: "var(--text)" }}>{f.horizon.toUpperCase()} HORIZON</span>
        <span className="tnum" style={{ color: "var(--faint)" }}>
          trained {ago(f.ts)}
        </span>
      </div>

      <div className="grid grid-cols-1 gap-4 px-4 py-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
        {/* ── LEFT: the probability, prominent, with its bar ── */}
        <div className="flex flex-col gap-2">
          <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
            P(up over {f.horizon})
          </span>
          <div className="flex items-baseline gap-2">
            <span
              className="tnum text-4xl font-bold leading-none"
              style={{ color: beatsBaseRate ? "var(--text)" : "var(--faint)" }}
            >
              {probPct}
            </span>
            {!beatsBaseRate && (
              <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                (ungraded)
              </span>
            )}
          </div>

          <div
            role="meter"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Number((probClamped * 100).toFixed(1))}
            aria-label={`probability up over ${f.horizon} for ${symbol}`}
            className="mt-1 flex h-3 overflow-hidden rounded-full border"
            style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
          >
            <div
              className="transition-[width] duration-300"
              style={{
                width: `${probClamped * 100}%`,
                background: beatsBaseRate ? probColor : "var(--faint)",
                opacity: beatsBaseRate ? 1 : 0.5,
              }}
            />
          </div>
          <div className="tnum flex justify-between text-[0.75rem]" style={{ color: "var(--faint)" }}>
            <span>0%</span>
            <span>base rate {pctText(f.baseRate, 0)}</span>
            <span>100%</span>
          </div>

          {/* honesty verdict — the whole point of the page */}
          {beatsBaseRate ? (
            <div
              className="mt-1 rounded border px-2.5 py-1.5 text-[0.78rem] leading-snug"
              style={{
                borderColor: "var(--ok)",
                background: "var(--bid-dim)",
                color: "var(--ok)",
              }}
            >
              beats the base rate out-of-sample by {liftText(f.lift)}
              {smallSample && (
                <span style={{ color: "var(--warn)" }}> (small sample)</span>
              )}
            </div>
          ) : (
            <div
              className="mt-1 rounded border px-2.5 py-1.5 text-[0.78rem] leading-snug"
              style={{
                borderColor: "var(--border)",
                background: "var(--panel2)",
                color: "var(--dim)",
              }}
            >
              no measurable edge — this model doesn&apos;t beat the base rate on {symbol}; treat
              P(up) as noise.
            </div>
          )}
        </div>

        {/* ── RIGHT: the out-of-sample grade, right next to the probability ── */}
        <div className="flex flex-col gap-3">
          <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
            OUT-OF-SAMPLE GRADE
          </span>
          <div className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
            <Stat
              label="ACCURACY"
              value={pctText(f.accuracy, 1)}
              title="Share of out-of-sample predictions the model got right."
            />
            <Stat
              label="BASE RATE"
              value={pctText(f.baseRate, 1)}
              color="var(--dim)"
              title="How often up happens regardless of the model — the bar to clear."
            />
            <Stat
              label="LIFT"
              value={liftText(f.lift)}
              color={beatsBaseRate ? "var(--ok)" : "var(--bad)"}
              title="Accuracy minus base rate. Must be positive to earn a probability."
            />
            <Stat
              label="BRIER"
              value={num(f.brier, 3)}
              title="Mean squared probability error (lower is better; 0 is perfect, 0.25 is a coin flip at 50%)."
            />
            <Stat
              label="AUC"
              value={num(f.auc, 3)}
              color={
                isFinite(f.auc) ? (f.auc > 0.5 ? "var(--ok)" : "var(--bad)") : undefined
              }
              title="Ranking quality: probability a random up bar scored above a random down bar. 0.50 = no skill."
            />
            <Stat
              label="SAMPLE (nEval)"
              value={isFinite(f.nEval) ? f.nEval.toLocaleString("en-US") : "—"}
              color={smallSample ? "var(--warn)" : undefined}
              title="Number of out-of-sample predictions graded via walk-forward."
            />
          </div>

          <p className="text-[0.78rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            Trained on {isFinite(f.nTrain) ? f.nTrain.toLocaleString("en-US") : "—"} bars; graded on{" "}
            {isFinite(f.nEval) ? f.nEval.toLocaleString("en-US") : "—"} out-of-sample predictions via
            walk-forward.
          </p>
        </div>
      </div>
    </section>
  );
}

export default function ForecastPage() {
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [rowsErr, setRowsErr] = useState<string | null>(null);
  const [selected, setSelected] = useState<Selected | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // Forecasts are keyed by "symbol|market" so switching symbols shows a clean
  // loading state instead of stale cards.
  const [fcState, setFcState] = useState<{ key: string; list: Forecast[] } | null>(null);
  const [fcErrState, setFcErrState] = useState<{ key: string; msg: string } | null>(null);

  // Load the watchlist (for the picker); default to the first active symbol.
  // Stage 5: signed out the watchlist 401s — fall back to the strongest-
  // scored symbols from the PUBLIC universe screener so the page still works.
  useEffect(() => {
    let alive = true;
    const apply = (r: WatchRow[]) => {
      setRows(r);
      setRowsErr(null);
      setSelected((cur) => {
        if (cur && r.some((x) => x.symbol === cur.symbol && x.market === cur.market)) {
          return cur;
        }
        const first = r.find((x) => x.active) ?? r[0];
        return first ? { symbol: first.symbol, market: first.market } : null;
      });
    };
    const load = () =>
      api
        .watchlist()
        .then((r) => {
          if (!alive) return;
          apply(r);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (!msg.includes("401")) {
            setRowsErr(msg);
            return;
          }
          screenerRows()
            .then((all) => {
              if (!alive) return;
              const top = [...all]
                .sort(
                  (a, b) =>
                    Math.abs(b.scores?.["1d"]?.score ?? 0) - Math.abs(a.scores?.["1d"]?.score ?? 0),
                )
                .slice(0, 24);
              apply(top);
            })
            .catch((e2: unknown) => {
              if (!alive) return;
              setRowsErr(e2 instanceof Error ? e2.message : String(e2));
            });
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  const selKey = selected ? `${selected.symbol}|${selected.market}` : "";

  // Poll the forecast for the selected symbol.
  useEffect(() => {
    if (!selected) return;
    let alive = true;
    const key = `${selected.symbol}|${selected.market}`;
    const load = () =>
      api
        .forecast(selected.symbol, selected.market)
        .then((list) => {
          if (!alive) return;
          setFcState({ key, list });
          setFcErrState(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setFcErrState({ key, msg: e instanceof Error ? e.message : String(e) });
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [selected, retryTick]);

  const forecasts = fcState && fcState.key === selKey ? fcState.list : null;
  const fcErr = fcErrState && fcErrState.key === selKey ? fcErrState.msg : null;

  // newest training timestamp across returned horizons — for the header chip
  const trainedAt = useMemo(() => {
    if (!forecasts || forecasts.length === 0) return 0;
    return forecasts.reduce((m, f) => (f.ts > m ? f.ts : m), 0);
  }, [forecasts]);

  const activeRows = useMemo(
    () => (rows ? rows.filter((r) => r.active) : []),
    [rows],
  );

  const rowsLoading = rows === null && rowsErr === null;
  const rowsHardError = rows === null && rowsErr !== null;

  const fcLoading = selected !== null && forecasts === null && fcErr === null;
  const fcHardError = selected !== null && forecasts === null && fcErr !== null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">FORECAST</h1>
        {selected && <span className="chip">{selected.symbol}</span>}
        {selected && (
          <span className="chip" style={{ color: "var(--dim)" }}>
            {selected.market}
          </span>
        )}
        {selected && trainedAt > 0 && (
          <span className="chip tnum" title={fmtTs(trainedAt)}>
            trained {ago(trainedAt)}
          </span>
        )}
        {rowsErr !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="signals-forecasts"
        text="How likely is this symbol to rise over each horizon — shown right next to how that same forecast has actually scored out of sample? A probability with no proven lift is labeled noise."
      />

      {/* explainer — always visible; the honesty framing IS the product */}
      <section className="panel">
        <div className="panel-h">HOW THIS MODEL EARNS THE RIGHT TO A NUMBER</div>
        <p
          className="px-4 py-4 text-[0.78rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          This is a logistic model trained walk-forward on stored bars with{" "}
          <span style={{ color: "var(--text)" }}>no lookahead</span>. It only earns the right to
          show a probability by beating the base rate out-of-sample; when it can&apos;t, we say so.
          Forecasts refresh hourly (
          <span style={{ color: "var(--dim)" }}>forecast-trainer</span> agent).
        </p>
      </section>

      {/* symbol picker */}
      <section className="panel">
        <div className="panel-h">
          SYMBOL
          <span className="tnum" style={{ color: "var(--faint)" }}>
            pick a symbol to inspect its forecast
          </span>
        </div>

        {rowsLoading && <Skeleton lines={2} label="loading watchlist" className="m-4" />}

        {rowsHardError && (
          <ErrorState
            className="m-4"
            message={rowsErr ?? "watchlist unavailable"}
            hint="Is the daemon running? Start it with signaldeckd."
            retry={() => {
              setRowsErr(null);
              setRetryTick((t) => t + 1);
            }}
          />
        )}

        {rows !== null && activeRows.length === 0 && (
          <EmptyState
            className="m-4"
            message="No active symbols yet"
            detail="Subscribe to symbols on the watchlist page and the forecast-trainer will pick them up."
          />
        )}

        {activeRows.length > 0 && (
          <div
            role="group"
            aria-label="symbol picker"
            className="flex flex-wrap gap-2 px-4 py-3"
          >
            {activeRows.map((r) => {
              const active =
                selected?.symbol === r.symbol && selected?.market === r.market;
              return (
                <button
                  key={`${r.market}:${r.symbol}`}
                  type="button"
                  onClick={() => setSelected({ symbol: r.symbol, market: r.market })}
                  aria-pressed={active}
                  className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:brightness-125"
                  style={{
                    color: active ? "var(--text)" : "var(--dim)",
                    borderColor: active ? "var(--accent)" : "var(--border)",
                    background: active ? "rgba(251,191,36,.08)" : "var(--panel2)",
                  }}
                >
                  {r.symbol}
                  <span className="ml-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {r.market}
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </section>

      {/* forecast cards */}
      {selected && (
        <>
          {fcLoading && (
            <Skeleton lines={4} label={`loading forecast for ${selected.symbol}`} />
          )}

          {fcHardError && (
            <ErrorState
              message={fcErr ?? "forecast unavailable"}
              hint="Is the daemon running? Start it with signaldeckd."
              retry={() => {
                setFcErrState(null);
                setRetryTick((t) => t + 1);
              }}
            />
          )}

          {forecasts !== null && forecasts.length === 0 && (
            <EmptyState
              message={`No forecast yet for ${selected.symbol}`}
              detail="The forecast-trainer runs hourly and needs ~150 daily bars before it can grade a model."
            />
          )}

          {forecasts !== null && forecasts.length > 0 && (
            <div className="flex flex-col gap-4">
              {forecasts.map((f) => (
                <ForecastCard key={`${selKey}:${f.horizon}`} f={f} symbol={selected.symbol} />
              ))}
              <div className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                Want the mechanical baseline instead? See{" "}
                <Link
                  href={`/s/${selected.market}/${encodeURIComponent(selected.symbol)}`}
                  className="cursor-pointer transition-colors duration-150 hover:text-[var(--accent)]"
                  style={{ color: "var(--dim)" }}
                >
                  {selected.symbol}&apos;s expectancy
                </Link>{" "}
                (what usually happens next by state) on the symbol page.
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
