"use client";

import { useEffect, useMemo, useState } from "react";
import {
  adaptive,
  api,
  pollMs,
  type AdaptiveCell,
  type AdaptiveResponse,
  type Calibration,
  type Market,
  type Prediction,
  type WatchRow,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PredictionGauge from "@/components/predict/PredictionGauge";
import CalibrationPanel from "@/components/predict/CalibrationPanel";
import FusionExplainer from "@/components/predict/FusionExplainer";

type CalHorizon = "1d" | "1w";
// Big P(up) dials are shown for these horizons (per the product spec).
const GAUGE_HORIZONS: ("1d" | "1w")[] = ["1d", "1w"];

interface Picked {
  symbol: string;
  market: Market;
}

/**
 * PREDICT — the flagship. Pick a symbol, get a calibrated P(up) per horizon,
 * then verify the forecaster against itself with a reliability diagram. Honesty
 * is the product: blend depth, raw-vs-calibrated, and resolved sample sizes are
 * always on screen.
 */
export default function PredictPage() {
  // ── watchlist (for the symbol picker) ──
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);
  const [picked, setPicked] = useState<Picked | null>(null);
  // Incremented by ErrorState retry buttons to re-kick the polling effects.
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .watchlist()
        .then((w) => {
          if (!alive) return;
          setWatch(w);
          setWatchErr(null);
          // Default to the first symbol once, without clobbering the user's pick.
          setPicked((cur) => {
            if (cur && w.some((r) => r.symbol === cur.symbol && r.market === cur.market)) return cur;
            const first = w[0];
            return first ? { symbol: first.symbol, market: first.market } : cur;
          });
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setWatchErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  // ── predictions for the picked symbol ──
  const [preds, setPreds] = useState<Record<string, Prediction> | null>(null);
  const [predErr, setPredErr] = useState<string | null>(null);
  const [predAt, setPredAt] = useState(0);

  useEffect(() => {
    if (!picked) return;
    let alive = true;
    // Reset so switching symbols never flashes stale numbers.
    setPreds(null);
    setPredErr(null);
    const load = () =>
      api
        .predictions(picked.symbol, picked.market)
        .then((p) => {
          if (!alive) return;
          setPreds(p);
          setPredErr(null);
          setPredAt(Math.floor(Date.now() / 1000));
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setPredErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [picked, retryTick]);

  // ── calibration (independent horizon state: 1d / 1w) ──
  const [calHorizon, setCalHorizon] = useState<CalHorizon>("1d");
  const [cal, setCal] = useState<Calibration | null>(null);
  const [calErr, setCalErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .calibration(calHorizon)
        .then((c) => {
          if (!alive) return;
          setCal(c);
          setCalErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setCalErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [calHorizon, retryTick]);

  // ── adaptive weights (model self-knowledge) ──
  const [adapt, setAdapt] = useState<AdaptiveResponse | null>(null);
  const [adaptErr, setAdaptErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      adaptive()
        .then((a) => {
          if (!alive) return;
          setAdapt(a);
          setAdaptErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setAdaptErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  // Only trust calibration payload tagged with the selected horizon.
  const calCurrent = cal && cal.horizon === calHorizon ? cal : null;

  const watchLoading = watch === null && watchErr === null;
  const watchHardError = watch === null && watchErr !== null;
  const predLoading = !!picked && preds === null && predErr === null;

  // freshest prediction ts across horizons (for the "updated" chip)
  const latestPredTs = useMemo(() => {
    if (!preds) return 0;
    return Object.values(preds).reduce((m, p) => Math.max(m, p?.ts ?? 0), 0);
  }, [preds]);

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-1">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">PREDICT</h1>
        <span className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
          calibrated probability of an up move — graded against itself
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {picked && (
            <span className="chip tnum">
              {picked.symbol}{" "}
              <span style={{ color: "var(--faint)" }}>{picked.market}</span>
            </span>
          )}
          <span className="chip tnum">
            {predLoading ? (
              <span style={{ color: "var(--faint)" }}>loading…</span>
            ) : predAt ? (
              `updated ${ago(predAt)}`
            ) : (
              "—"
            )}
          </span>
        </div>
      </div>

      {/* watchlist hard error */}
      {watchHardError && (
        <ErrorState
          message={watchErr ?? "could not load the watchlist"}
          hint="is the daemon running? start it with signaldeckd and the picker will populate."
          retry={() => {
            setWatchErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {/* symbol picker */}
      <section className="panel">
        <div className="panel-h">
          SYMBOL
          {watchErr && watch !== null && (
            <span className="tnum ml-auto" style={{ color: "var(--bad)" }}>
              poll failed — showing last list
            </span>
          )}
        </div>
        <div className="px-4 py-3">
          {watchLoading ? (
            <Skeleton lines={2} label="loading symbols" className="border-0 p-0" />
          ) : watch && watch.length === 0 ? (
            <EmptyState
              message="No symbols tracked yet"
              detail="Subscribe to a symbol and it will appear here as a pickable chip."
              className="border-0 p-0"
            />
          ) : (
            <div role="group" aria-label="Pick a symbol" className="flex flex-wrap gap-1.5">
              {(watch ?? []).map((r) => {
                const active = !!picked && picked.symbol === r.symbol && picked.market === r.market;
                return (
                  <button
                    key={`${r.market}:${r.symbol}`}
                    type="button"
                    onClick={() => setPicked({ symbol: r.symbol, market: r.market })}
                    aria-pressed={active}
                    className="chip cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
                    style={
                      active
                        ? { color: "var(--accent)", borderColor: "var(--accent)" }
                        : undefined
                    }
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
        </div>
      </section>

      {/* prediction error (daemon down or symbol not scored) */}
      {predErr && (
        <ErrorState
          message={predErr}
          hint="is the daemon running? start signaldeckd and predictions will stream in."
          retry={() => {
            setPredErr(null);
            setPreds(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {/* per-horizon P(up) gauges */}
      {picked && !predErr && (
        <div className="grid gap-4 lg:grid-cols-2">
          {GAUGE_HORIZONS.map((h) => (
            <PredictionGauge key={h} horizon={h} pred={preds?.[h]} />
          ))}
        </div>
      )}

      {predLoading && <Skeleton lines={4} label="loading predictions" />}

      {/* calibration / reliability — the differentiator */}
      <CalibrationPanel
        horizon={calHorizon}
        onHorizon={setCalHorizon}
        data={calCurrent}
        loading={cal === null && calErr === null}
        err={cal === null && calErr !== null ? calErr : null}
        retry={() => {
          setCalErr(null);
          setRetryTick((t) => t + 1);
        }}
      />

      {/* model self-knowledge — learned per-regime component weights */}
      <SelfKnowledgePanel
        data={adapt}
        err={adapt === null ? adaptErr : null}
        retry={() => {
          setAdaptErr(null);
          setRetryTick((t) => t + 1);
        }}
      />

      {/* plain-English explainer */}
      <FusionExplainer />
    </div>
  );
}

// ── MODEL SELF-KNOWLEDGE ──────────────────────────────────────────────────
// The learning flywheel made visible: per regime cell, the component weights
// the ensemble CURRENTLY uses, learned from resolved outcomes — with sample
// counts and gate status, so an unlearned cell is shown as exactly that.

const LEG_ORDER = ["pressure", "expectancy", "forecast", "sentiment"];

function cellStatus(c: AdaptiveCell, minSamples: number): { label: string; color: string } {
  if (c.weights && Object.keys(c.weights).length > 0)
    return { label: "learned", color: "var(--accent)" };
  if (c.gated)
    return { label: `gated — needs ${minSamples} samples`, color: "var(--faint)" };
  return { label: "no measured edge — static prior", color: "var(--faint)" };
}

function AdaptiveCellRows({ name, cell, minSamples }: { name: string; cell: AdaptiveCell; minSamples: number }) {
  const status = cellStatus(cell, minSamples);
  const legs = LEG_ORDER.filter(
    (l) => cell.weights?.[l] !== undefined || cell.hitRates?.[l] !== undefined,
  );
  return (
    <div className="flex flex-col gap-1.5 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[0.8rem] font-bold tracking-wide">{name}</span>
        <span className="chip tnum">n={cell.n}</span>
        <span className="chip" style={{ color: status.color, borderColor: status.color }}>
          {status.label}
        </span>
      </div>
      {legs.map((leg) => {
        const w = cell.weights?.[leg];
        const hr = cell.hitRates?.[leg];
        return (
          <div key={leg} className="flex items-center gap-2 text-[0.75rem]">
            <span className="w-24 shrink-0" style={{ color: "var(--faint)" }}>
              {leg}
            </span>
            <div
              className="h-2 flex-1 overflow-hidden rounded"
              style={{ background: "color-mix(in srgb, var(--faint) 18%, transparent)" }}
              role="img"
              aria-label={`${leg} weight ${w !== undefined ? Math.round(w * 100) : 0}%`}
            >
              <div
                className="h-full rounded"
                style={{
                  width: `${Math.round((w ?? 0) * 100)}%`,
                  background: "var(--accent)",
                }}
              />
            </div>
            <span className="tnum w-12 text-right">
              {w !== undefined ? `${Math.round(w * 100)}%` : "—"}
            </span>
            <span className="tnum w-16 text-right" style={{ color: "var(--faint)" }}>
              {hr !== undefined ? `hit ${Math.round(hr * 100)}%` : ""}
            </span>
          </div>
        );
      })}
    </div>
  );
}

function SelfKnowledgePanel({
  data,
  err,
  retry,
}: {
  data: AdaptiveResponse | null;
  err: string | null;
  retry: () => void;
}) {
  if (err) {
    return (
      <ErrorState
        message={err}
        hint="is the daemon running? the adaptive-weights agent publishes this panel."
        retry={retry}
      />
    );
  }
  if (data === null) {
    return <Skeleton lines={3} label="loading model self-knowledge" />;
  }
  const cells = data.weights?.cells ?? {};
  // "all" (the pooled fallback cell) first, then regimes alphabetically.
  const names = Object.keys(cells).sort((a, b) =>
    a === "all" ? -1 : b === "all" ? 1 : a.localeCompare(b),
  );
  return (
    <section className="panel">
      <div className="panel-h">
        MODEL SELF-KNOWLEDGE
        {data.available && data.weights && (
          <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
            learned {ago(data.weights.computedTs)}
          </span>
        )}
      </div>
      <div className="flex flex-col gap-1 px-4 py-3">
        {!data.available || names.length === 0 ? (
          <p className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
            No learning pass has produced weights yet — every prediction uses the static
            equal prior. The adaptive-weights agent recomputes from resolved outcomes every 6h.
          </p>
        ) : (
          names.map((n) => (
            <AdaptiveCellRows key={n} name={n} cell={cells[n]} minSamples={data.minSamples} />
          ))
        )}
        <p className="mt-2 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Weights are learned from resolved outcomes (per-component hit-rate over regime
          cells); cells without {data.minSamples} samples use the global or static prior.
          Sentiment additionally needs {data.minSamples} of its own samples before it can
          carry any weight.
        </p>
      </div>
    </section>
  );
}
