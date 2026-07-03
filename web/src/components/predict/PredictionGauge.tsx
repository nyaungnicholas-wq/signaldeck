"use client";

import type { Horizon, Prediction } from "@/lib/api";

const HORIZON_LABEL: Record<string, string> = {
  "1h": "next hour",
  "1d": "next day",
  "1w": "next week",
};

function pctColor(p: number): string {
  // Above coin-flip leans up (green), below leans down (red), near 50 is neutral.
  if (!Number.isFinite(p)) return "var(--dim)";
  if (p >= 0.55) return "var(--bid)";
  if (p <= 0.45) return "var(--ask)";
  return "var(--dim)";
}

function fmtProbPct(p: number): string {
  if (!Number.isFinite(p)) return "—";
  return `${(p * 100).toFixed(0)}%`;
}

/**
 * One horizon's calibrated P(up): a prominent calibrated percentage with a
 * filled bar, the raw (pre-calibration) probability beside it, and the blend
 * depth (nUsed). Honesty first — a thin blend is called out explicitly.
 */
export default function PredictionGauge({
  horizon,
  pred,
}: {
  horizon: Horizon;
  pred: Prediction | undefined;
}) {
  const label = HORIZON_LABEL[horizon] ?? horizon;

  if (!pred) {
    return (
      <section className="panel">
        <div className="panel-h">
          P(UP) · {horizon.toUpperCase()}
          <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
            {label}
          </span>
        </div>
        <div className="px-4 py-6 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          no prediction at this horizon yet — the predictor fills it in once the symbol has
          enough bars and signals to fuse.
        </div>
      </section>
    );
  }

  const cal = pred.calProb;
  const raw = pred.rawProb;
  const nUsed = pred.nUsed ?? 0;
  const thin = nUsed < 2;
  const calValid = Number.isFinite(cal);
  const barPct = calValid ? Math.max(0, Math.min(100, cal * 100)) : 50;
  const color = pctColor(cal);
  const drift = calValid && Number.isFinite(raw) ? (cal - raw) * 100 : NaN;

  return (
    <section className="panel">
      <div className="panel-h">
        P(UP) · {horizon.toUpperCase()}
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          {label}
        </span>
      </div>
      <div className="px-4 py-4">
        {/* Big calibrated probability */}
        <div className="flex items-end justify-between gap-3">
          <div>
            <div className="tnum text-4xl font-extrabold leading-none" style={{ color }}>
              {fmtProbPct(cal)}
            </div>
            <div className="mt-1.5 text-[0.75rem]" style={{ color: "var(--dim)" }}>
              calibrated · probability the price is higher {label}
            </div>
          </div>
          <div className="text-right">
            <div className="tnum text-lg font-bold" style={{ color: "var(--dim)" }}>
              {fmtProbPct(raw)}
            </div>
            <div className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              raw
            </div>
          </div>
        </div>

        {/* Filled bar — 50% mark shows the coin-flip line */}
        <div
          role="meter"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={calValid ? Number((cal * 100).toFixed(1)) : 50}
          aria-label={`Calibrated probability of an up move at the ${horizon} horizon: ${fmtProbPct(cal)}`}
          className="relative mt-4 h-3 overflow-hidden rounded-full border"
          style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
        >
          <div
            className="h-full transition-[width] duration-300"
            style={{ width: `${barPct}%`, background: color, opacity: 0.85 }}
          />
          {/* coin-flip reference line */}
          <div
            className="absolute top-[-2px] bottom-[-2px] w-px"
            style={{ left: "50%", background: "var(--faint)" }}
            aria-hidden="true"
          />
        </div>
        <div className="tnum mt-1 flex justify-between text-[0.75rem]" style={{ color: "var(--faint)" }}>
          <span>0%</span>
          <span>50% coin-flip</span>
          <span>100%</span>
        </div>

        {/* Blend depth + calibration drift */}
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <span
            className="chip tnum"
            style={
              thin
                ? { color: "var(--warn)", borderColor: "var(--warn)" }
                : undefined
            }
          >
            blended from {nUsed} signal{nUsed === 1 ? "" : "s"}
          </span>
          {Number.isFinite(drift) && Math.abs(drift) >= 0.5 && (
            <span className="chip tnum" style={{ color: "var(--crossed)", borderColor: "var(--crossed)" }}>
              calibration {drift > 0 ? "raised" : "lowered"} it {Math.abs(drift).toFixed(0)} pts
            </span>
          )}
        </div>
        {thin && (
          <div className="mt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
            thin blend — only {nUsed} underlying signal{nUsed === 1 ? "" : "s"} agreed to weigh in,
            so this probability is less reliable than a full blend. Treat it as tentative.
          </div>
        )}
      </div>
    </section>
  );
}
