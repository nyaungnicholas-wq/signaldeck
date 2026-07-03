"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  type Calibration,
  type Market,
  type Prediction,
  type WatchRow,
} from "@/lib/api";
import { ago } from "@/lib/format";
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
  }, []);

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
  }, [picked]);

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
  }, [calHorizon]);

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
        <span className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
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
        <div className="panel px-4 py-8 text-center text-[0.75rem]">
          <div style={{ color: "var(--bad)" }}>{watchErr}</div>
          <div className="mt-2" style={{ color: "var(--faint)" }}>
            is the daemon running? start it with{" "}
            <span style={{ color: "var(--dim)" }}>signaldeckd</span> and the picker will populate.
          </div>
        </div>
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
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              loading…
            </span>
          ) : watch && watch.length === 0 ? (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              no symbols tracked yet — subscribe to a symbol and it will appear here.
            </span>
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
                    <span className="ml-1.5 text-[0.6rem]" style={{ color: "var(--faint)" }}>
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
        <div className="panel px-4 py-6 text-[0.75rem] leading-relaxed" style={{ color: "var(--bad)" }}>
          {predErr}
          <span style={{ color: "var(--faint)" }}>
            {" "}
            — is the daemon running? start signaldeckd and predictions will stream in.
          </span>
        </div>
      )}

      {/* per-horizon P(up) gauges */}
      {picked && !predErr && (
        <div className="grid gap-4 lg:grid-cols-2">
          {GAUGE_HORIZONS.map((h) => (
            <PredictionGauge key={h} horizon={h} pred={preds?.[h]} />
          ))}
        </div>
      )}

      {predLoading && (
        <div className="panel px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading predictions…
        </div>
      )}

      {/* calibration / reliability — the differentiator */}
      <CalibrationPanel
        horizon={calHorizon}
        onHorizon={setCalHorizon}
        data={calCurrent}
        loading={cal === null && calErr === null}
        err={cal === null && calErr !== null ? calErr : null}
      />

      {/* plain-English explainer */}
      <FusionExplainer />
    </div>
  );
}
