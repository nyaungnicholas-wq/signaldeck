"use client";

import type { Honesty } from "@/lib/api";

const W = 600;
const H = 300;
const M = { top: 16, right: 16, bottom: 34, left: 56 };
const IW = W - M.left - M.right;
const IH = H - M.top - M.bottom;
const MAX_POINTS = 1500;

function fmtLim(lim: number): string {
  const pct = lim * 100;
  return `${pct >= 10 ? pct.toFixed(0) : pct >= 1 ? pct.toFixed(1) : pct.toFixed(2)}%`;
}

/** Hand-rolled SVG scatter: score [-1,1] on x, forward return on y. */
export default function ScatterPlot({ points }: { points: Honesty["points"] }) {
  const valid = (points ?? []).filter(
    (p) => p && Number.isFinite(p.score) && Number.isFinite(p.fwd),
  );

  if (valid.length < 10) {
    return (
      <section className="panel">
        <div className="panel-h">SCATTER · SCORE vs FORWARD RETURN</div>
        <div className="px-4 py-6 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          not enough resolved outcomes yet — scores resolve after their horizon elapses (1h/1d/1w).
        </div>
      </section>
    );
  }

  // Clamp the display range to the ~98th percentile of |fwd| so one outlier
  // doesn't flatten the cloud; floor 0.5%, cap 25%.
  const abs = valid.map((p) => Math.abs(p.fwd)).sort((a, b) => a - b);
  const p98 = abs[Math.min(abs.length - 1, Math.floor(abs.length * 0.98))] ?? 0;
  const yLim = Math.min(0.25, Math.max(0.005, p98));
  const clamped = valid.filter((p) => Math.abs(p.fwd) > yLim).length;

  // Cap rendered points (even sampling) so huge histories stay light.
  const step = Math.max(1, Math.ceil(valid.length / MAX_POINTS));
  const shown = step > 1 ? valid.filter((_, i) => i % step === 0) : valid;

  const x = (s: number) => M.left + ((Math.max(-1, Math.min(1, s)) + 1) / 2) * IW;
  const y = (f: number) => M.top + (1 - (Math.max(-yLim, Math.min(yLim, f)) + yLim) / (2 * yLim)) * IH;

  return (
    <section className="panel">
      <div className="panel-h">
        SCATTER · SCORE vs FORWARD RETURN
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          {valid.length.toLocaleString("en-US")} pts
        </span>
      </div>
      <div className="px-4 py-3">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          className="h-auto w-full"
          role="img"
          aria-label={`Scatter of ${valid.length} resolved outcomes: score on x from -1 to +1, forward return on y clamped to ±${fmtLim(yLim)}`}
        >
          {/* frame */}
          <rect
            x={M.left}
            y={M.top}
            width={IW}
            height={IH}
            fill="none"
            stroke="var(--border)"
            strokeWidth="1"
          />
          {/* zero axes */}
          <line x1={M.left} y1={y(0)} x2={W - M.right} y2={y(0)} stroke="var(--border)" strokeWidth="1" />
          <line x1={x(0)} y1={M.top} x2={x(0)} y2={H - M.bottom} stroke="var(--border)" strokeWidth="1" />
          {/* points */}
          {shown.map((p, i) => (
            <circle key={i} cx={x(p.score)} cy={y(p.fwd)} r="2" fill="var(--accent)" fillOpacity="0.5" />
          ))}
          {/* x tick labels */}
          <text x={x(-1)} y={H - 18} fontSize="10" fill="var(--faint)" textAnchor="start">
            −1
          </text>
          <text x={x(0)} y={H - 18} fontSize="10" fill="var(--faint)" textAnchor="middle">
            0
          </text>
          <text x={x(1)} y={H - 18} fontSize="10" fill="var(--faint)" textAnchor="end">
            +1
          </text>
          <text x={M.left + IW / 2} y={H - 5} fontSize="10" fill="var(--dim)" textAnchor="middle">
            score
          </text>
          {/* y tick labels */}
          <text x={M.left - 6} y={M.top + 9} fontSize="10" fill="var(--faint)" textAnchor="end">
            +{fmtLim(yLim)}
          </text>
          <text x={M.left - 6} y={y(0) + 3} fontSize="10" fill="var(--faint)" textAnchor="end">
            0%
          </text>
          <text x={M.left - 6} y={H - M.bottom} fontSize="10" fill="var(--faint)" textAnchor="end">
            −{fmtLim(yLim)}
          </text>
          <text
            x={12}
            y={M.top + IH / 2}
            fontSize="10"
            fill="var(--dim)"
            textAnchor="middle"
            transform={`rotate(-90 12 ${M.top + IH / 2})`}
          >
            fwd return
          </text>
        </svg>
        <div className="mt-1 text-[0.66rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          each dot = one persisted score and what the market did next.
          {clamped > 0 && (
            <>
              {" "}
              {clamped.toLocaleString("en-US")} outlier{clamped === 1 ? "" : "s"} beyond ±{fmtLim(yLim)}{" "}
              clamped to the edge for display.
            </>
          )}
          {step > 1 && <> showing every {step}th of {valid.length.toLocaleString("en-US")} points.</>}
        </div>
      </div>
    </section>
  );
}
