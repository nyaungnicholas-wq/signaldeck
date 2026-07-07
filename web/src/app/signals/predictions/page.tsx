"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import {
  adaptive,
  api,
  latestPredictions,
  pollMs,
  screenerRows,
  type AdaptiveCell,
  type AdaptiveResponse,
  type Calibration,
  type LatestPredictionsResponse,
  type Market,
  type Prediction,
  type WatchRow,
} from "@/lib/api";
import { ago } from "@/lib/format";
import { metricLabel } from "@/lib/plain";
import { useViewMode } from "@/components/Plain";
import VerdictCard from "@/components/VerdictCard";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PredictionGauge from "@/components/predict/PredictionGauge";
import CalibrationPanel from "@/components/predict/CalibrationPanel";
import FusionExplainer from "@/components/predict/FusionExplainer";
import Sparkline from "@/components/viz/Sparkline";
import ProbHistogram from "@/components/viz/ProbHistogram";
import PagePurpose from "@/components/PagePurpose";

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

  // Stage 5: logged-out fallback — the watchlist is session-scoped (401 when
  // signed out), so the picker falls back to the strongest-scored symbols
  // from the PUBLIC universe screener instead of erroring the whole page.
  const [anonPicker, setAnonPicker] = useState(false);

  useEffect(() => {
    let alive = true;
    const applyList = (w: WatchRow[]) => {
      setWatch(w);
      setWatchErr(null);
      // Default to the first symbol once, without clobbering the user's pick.
      setPicked((cur) => {
        if (cur && w.some((r) => r.symbol === cur.symbol && r.market === cur.market)) return cur;
        const first = w[0];
        return first ? { symbol: first.symbol, market: first.market } : cur;
      });
    };
    const load = () =>
      api
        .watchlist()
        .then((w) => {
          if (!alive) return;
          setAnonPicker(false);
          applyList(w);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (!msg.includes("401")) {
            setWatchErr(msg);
            return;
          }
          // Signed out → top 24 universe symbols by |1d score| (public read).
          screenerRows()
            .then((rows) => {
              if (!alive) return;
              const top = [...rows]
                .sort(
                  (a, b) =>
                    Math.abs(b.scores?.["1d"]?.score ?? 0) - Math.abs(a.scores?.["1d"]?.score ?? 0),
                )
                .slice(0, 24);
              setAnonPicker(true);
              applyList(top);
            })
            .catch((e2: unknown) => {
              if (!alive) return;
              setWatchErr(e2 instanceof Error ? e2.message : String(e2));
            });
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

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="signals-predictions"
        text="How likely is an up move for each tracked symbol, per the calibrated model? Every probability carries its sample size — thin evidence says so."
      />

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
        <div className="panel-h flex-wrap gap-2">
          SYMBOL
          {anonPicker && (
            <span
              className="text-[0.68rem] font-normal normal-case tracking-normal"
              style={{ color: "var(--faint)" }}
            >
              signed out — showing the 24 strongest-scored universe symbols; log in to pick from
              your own watchlist
            </span>
          )}
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

      {/* Stage 5: fleet-wide predictions table — every symbol's latest
          calibrated P(up) with its confidence badge, gate caption, and a 30d
          sparkline. Clicking a row re-targets the dials above. */}
      <PredictionsTablePanel onPick={(s, m) => setPicked({ symbol: s, market: m })} />

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

// ── PREDICTIONS TABLE (Stage 5) ───────────────────────────────────────────
// Fleet-wide latest calibrated prediction per symbol (one batched endpoint),
// strongest conviction first, joined client-side with the public screener's
// stored daily closes for the 30d sparkline. HONESTY: the endpoint's gate
// caption ("n=X/30 resolved — not significant yet …") and the backtested-
// not-live trackLabel render verbatim in the panel header — a confidence
// badge never appears without them. A 404 means the running daemon predates
// this endpoint; the panel says exactly that instead of faking rows.

// Stage-1 translation layer: the confidence chip IS the verdict card seed —
// colored arrow + "LEANS UP — 62%" straight from the REAL calibrated prob
// (probVerdict never invents a lean; near-50% reads NO CLEAR LEAN).

function PredictionsTablePanel({ onPick }: { onPick: (symbol: string, market: Market) => void }) {
  const [ph, setPh] = useState<"1d" | "1w">("1d");
  const [resp, setResp] = useState<LatestPredictionsResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [sparks, setSparks] = useState<Map<string, number[]>>(new Map());
  const [tableRetry, setTableRetry] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      latestPredictions(ph)
        .then((r) => {
          if (!alive) return;
          setResp(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs() * 6);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [ph, tableRetry]);

  // Sparkline closes from the public screener payload (fetched gently — it is
  // the whole universe). Failure is soft: rows render without sparklines.
  useEffect(() => {
    let alive = true;
    const load = () =>
      screenerRows()
        .then((rows) => {
          if (!alive) return;
          const m = new Map<string, number[]>();
          for (const r of rows) m.set(`${r.market}:${r.symbol}`, r.spark ?? []);
          setSparks(m);
        })
        .catch(() => {});
    load();
    const t = setInterval(load, 300_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  const rows = resp?.rows ?? [];
  const notDeployed = err !== null && err.includes("404");
  const mode = useViewMode();
  // Stage 4 (tables→charts): in SIMPLE mode the raw numeric columns
  // (cal % / raw % / blend n) fold behind an explicit "show raw columns"
  // toggle — the verdict card row IS the reading. PRO mode always shows
  // everything. Data is never lost, only one click away.
  const [showRaw, setShowRaw] = useState(false);
  const rawCols = mode === "pro" || showRaw;

  // Column headers follow the SIMPLE/PRO toggle — same columns, translated
  // wording. Tooltips carry the technical definition in both modes.
  const HEADERS: { key: string; label: string; tip?: string }[] = [
    { key: "symbol", label: "SYMBOL" },
    { key: "market", label: "MARKET" },
    { key: "trend", label: mode === "simple" ? "LAST 30 DAYS" : "TREND 30D" },
    {
      key: "verdict",
      label: "VERDICT",
      tip: "Direction + calibrated probability — near-50% reads NO CLEAR LEAN. See the gate caption above.",
    },
    ...(rawCols
      ? [
          {
            key: "cal",
            label: metricLabel("cal_prob", mode).toUpperCase(),
            tip: "calibrated probability the price is higher at the horizon — corrected against resolved outcomes",
          },
          {
            key: "raw",
            label: mode === "simple" ? "BEFORE TUNING" : "RAW",
            tip: "raw ensemble probability before calibration",
          },
          {
            key: "blend",
            label: mode === "simple" ? "SIGNALS USED" : "BLEND N",
            tip: "how many ensemble legs contributed to this prediction",
          },
        ]
      : []),
    { key: "updated", label: "UPDATED" },
  ];

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        ALL PREDICTIONS
        <span className="flex items-center gap-1" role="tablist" aria-label="prediction horizon">
          {(["1d", "1w"] as const).map((h) => (
            <button
              key={h}
              type="button"
              role="tab"
              aria-selected={ph === h}
              onClick={() => setPh(h)}
              className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
              style={{
                color: ph === h ? "var(--accent)" : "var(--dim)",
                borderColor: ph === h ? "var(--accent)" : "var(--border)",
              }}
            >
              {h}
            </button>
          ))}
        </span>
        {resp !== null && (
          <span className="chip tnum">{rows.length} symbols predicted</span>
        )}
        {/* Stage 4: simple mode folds the raw numeric columns behind this
            toggle — reachable in one click, never deleted. */}
        {mode === "simple" && rows.length > 0 && (
          <button
            type="button"
            onClick={() => setShowRaw((s) => !s)}
            aria-pressed={showRaw}
            className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
            style={{
              color: showRaw ? "var(--accent)" : "var(--dim)",
              borderColor: showRaw ? "var(--accent)" : "var(--border)",
            }}
            title="show/hide the raw numeric columns (calibrated %, pre-calibration %, blend size)"
          >
            {showRaw ? "hide raw columns" : "show raw columns"}
          </button>
        )}
        {resp !== null && (
          <span
            className="ml-auto text-[0.68rem] font-normal normal-case tracking-normal"
            style={{ color: resp.gated ? "var(--warn)" : "var(--faint)" }}
          >
            {resp.trackLabel}
          </span>
        )}
      </div>

      {/* THE gate caption — always rendered, verbatim from the API. */}
      {resp !== null && (
        <p className="px-4 py-2.5 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {resp.caption}
        </p>
      )}

      {/* Stage 4 (tables→charts): the distribution strip — where the fleet's
          CURRENT calibrated {ph} probabilities pile up, before the row-by-row
          table. Same rows, nothing resampled; near-50% bars are dim because a
          pile of coin flips is not conviction. */}
      {rows.length > 0 && (
        <div className="px-4 pb-1">
          <ProbHistogram probs={rows.map((r) => r.calProb)} />
          <p className="mt-1 text-[0.68rem]" style={{ color: "var(--faint)" }}>
            distribution of the {rows.length} current calibrated {ph} P(up) values below —
            left of the dashed line leans down, right leans up, the middle is coin-flip
            territory. Same gate caveat as above: backtested calibration, not a live track record.
          </p>
        </div>
      )}

      {resp === null && err === null && (
        <div className="p-4">
          <Skeleton lines={4} label="loading fleet predictions" />
        </div>
      )}

      {resp === null && err !== null && (
        <ErrorState
          className="m-4"
          message={notDeployed ? "predictions feed not available on this daemon build" : err}
          hint={
            notDeployed
              ? "The running daemon predates GET /api/predictions/latest — restart it with the current binary and this table fills in. Nothing is faked meanwhile."
              : "Is the daemon running? Start signaldeckd and this table will recover."
          }
          retry={() => {
            setErr(null);
            setTableRetry((t) => t + 1);
          }}
        />
      )}

      {resp !== null && rows.length === 0 && (
        <EmptyState
          className="m-4"
          message="No predictions stored yet"
          detail="The predictor writes one calibrated prediction per symbol on worker cadence; rows appear as they land — absent symbols are honest absence, not zeros."
        />
      )}

      {rows.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-[0.8rem]">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)" }}>
                {HEADERS.map((h, i) => (
                  <th
                    key={h.key}
                    scope="col"
                    className={`px-3 py-2 text-[0.72rem] font-medium tracking-wide ${
                      i >= 4 ? "text-right" : "text-left"
                    }`}
                    style={{ color: "var(--faint)" }}
                    title={h.tip}
                  >
                    {h.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="tnum">
              {rows.map((r) => {
                return (
                  <tr
                    key={`${r.market}:${r.symbol}`}
                    className="cursor-pointer transition-colors duration-150 hover:bg-[var(--panel2)]"
                    onClick={() => onPick(r.symbol, r.market)}
                    title={`aim the P(up) dials at ${r.symbol}`}
                  >
                    <td className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
                      <Link
                        href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                        onClick={(e) => e.stopPropagation()}
                        className="font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                      >
                        {r.symbol}
                      </Link>
                    </td>
                    <td className="px-3 py-2" style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}>
                      {r.market}
                    </td>
                    <td className="px-3 py-1" style={{ borderBottom: "1px solid var(--border)" }}>
                      <Sparkline
                        closes={sparks.get(`${r.market}:${r.symbol}`) ?? []}
                        width={96}
                        height={26}
                        area={false}
                      />
                    </td>
                    {/* Stage 2: the verdict card — real calibrated prob +
                        the always-visible evidence-tier badge. */}
                    <td className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
                      <VerdictCard
                        size="sm"
                        symbol={r.symbol}
                        market={r.market}
                        horizon={ph}
                        calProb={r.calProb}
                        nUsed={r.nUsed}
                        tier={r.tier ?? ""}
                        tierProgress={{ nSamples: r.nSamples ?? 0, threshold: resp?.tierThreshold ?? 0 }}
                      />
                    </td>
                    {rawCols && (
                      <>
                        <td
                          className="px-3 py-2 text-right"
                          style={{ borderBottom: "1px solid var(--border)" }}
                          title={mode === "simple" ? "chance the price is higher at the horizon — 50% would be a coin flip" : undefined}
                        >
                          {(r.calProb * 100).toFixed(1)}%
                        </td>
                        <td
                          className="px-3 py-2 text-right"
                          style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}
                          title="raw ensemble probability before calibration"
                        >
                          {(r.rawProb * 100).toFixed(1)}%
                        </td>
                        <td className="px-3 py-2 text-right" style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}>
                          {r.nUsed}
                        </td>
                      </>
                    )}
                    <td className="px-3 py-2 text-right" style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                      {ago(r.ts)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
