"use client";

import type { Calibration } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import ReliabilityDiagram from "./ReliabilityDiagram";

type CalHorizon = "1d" | "1w";
const CAL_HORIZONS: CalHorizon[] = ["1d", "1w"];
const MIN_N = 30;

function Stat({
  label,
  value,
  valueColor,
  sub,
}: {
  label: string;
  value: string;
  valueColor?: string;
  sub: string;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[0.75rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
        {label}
      </div>
      <div className="tnum mt-0.5 text-xl font-bold" style={{ color: valueColor ?? "var(--text)" }}>
        {value}
      </div>
      <div className="mt-0.5 text-[0.75rem] leading-snug" style={{ color: "var(--dim)" }}>
        {sub}
      </div>
    </div>
  );
}

function reliabilityColor(r: number, n: number): string {
  if (!n || !Number.isFinite(r)) return "var(--dim)";
  if (r >= 0.75) return "var(--bid)";
  if (r >= 0.5) return "var(--warn)";
  return "var(--ask)";
}

/**
 * CALIBRATION / RELIABILITY — the differentiator. Horizon chips (1d/1w) drive
 * a reliability diagram plus the headline reliability, Brier score and resolved
 * count. Below MIN_N resolved, the calibrator is still the identity map, so we
 * say so plainly and still draw whatever points exist.
 */
export default function CalibrationPanel({
  horizon,
  onHorizon,
  data,
  loading,
  err,
  retry,
}: {
  horizon: CalHorizon;
  onHorizon: (h: CalHorizon) => void;
  data: Calibration | null;
  loading: boolean;
  err: string | null;
  retry?: () => void;
}) {
  const n = data?.n ?? 0;
  const bins = data?.bins ?? [];
  const thin = n < MIN_N;

  return (
    <section className="panel">
      <div className="panel-h">
        CALIBRATION · RELIABILITY
        {/* Phase 0 labeling: measured on backtested / in-sample resolutions. */}
        {data && data.live !== true && (
          <span
            className="chip"
            style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
            title={
              data.trackLabel ??
              "Calibration is measured on backtested / in-sample resolutions, not a live forward track record."
            }
          >
            backtested — not live
          </span>
        )}
        <div role="group" aria-label="Calibration horizon" className="ml-auto flex items-center gap-1">
          {CAL_HORIZONS.map((h) => {
            const active = h === horizon;
            return (
              <button
                key={h}
                type="button"
                onClick={() => onHorizon(h)}
                aria-pressed={active}
                className="chip cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
                style={
                  active ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined
                }
              >
                {h}
              </button>
            );
          })}
        </div>
      </div>

      {err ? (
        <ErrorState
          message={err}
          hint="is the daemon running? start signaldeckd and the diagram will populate."
          retry={retry}
          className="border-0"
        />
      ) : loading && !data ? (
        <div className="px-4 py-4">
          <Skeleton lines={4} label="loading calibration" className="border-0 p-0" />
        </div>
      ) : (
        <div className="px-4 py-4">
          {/* honest caveat when the calibrator hasn't kicked in yet */}
          {thin && (
            <div
              className="mb-3 rounded-md border px-3 py-2.5 text-[0.78rem] leading-relaxed"
              style={{ borderColor: "var(--warn)", background: "var(--panel2)", color: "var(--warn)" }}
            >
              calibration is still the identity map — only {n.toLocaleString("en-US")} resolved
              prediction{n === 1 ? "" : "s"} so far, below the {MIN_N} needed to fit a correction.
              Predictions resolve after their horizon elapses, so the calibrated probability equals
              the raw one for now. The diagram fills in as outcomes accumulate.
            </div>
          )}

          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_180px]">
            <ReliabilityDiagram bins={bins} />

            <div className="flex flex-col gap-4 lg:justify-center">
              <Stat
                label="reliability"
                value={n && Number.isFinite(data?.reliability ?? NaN) ? (data!.reliability).toFixed(3) : "—"}
                valueColor={reliabilityColor(data?.reliability ?? NaN, n)}
                sub="0…1, higher = forecasts land where they claim"
              />
              <Stat
                label="Brier score"
                value={n && Number.isFinite(data?.brier ?? NaN) ? (data!.brier).toFixed(4) : "—"}
                sub="mean squared error of P(up); lower = sharper, 0.25 = coin-flip"
              />
              <Stat
                label="resolved"
                value={n.toLocaleString("en-US")}
                valueColor={thin ? "var(--warn)" : "var(--text)"}
                sub={`prediction → outcome pairs at the ${horizon} horizon`}
              />
            </div>
          </div>

          {/* legend */}
          <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: "var(--bid)", opacity: 0.7 }} />
              above the line — underconfident (reality beat the forecast)
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: "var(--ask)", opacity: 0.7 }} />
              below the line — overconfident (forecast overshot)
            </span>
            <span>point size = resolved predictions in that bin.</span>
          </div>
        </div>
      )}
    </section>
  );
}
