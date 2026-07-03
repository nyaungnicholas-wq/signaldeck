"use client";

import type { CalBin } from "@/lib/api";

const W = 440;
const H = 340;
const M = { top: 18, right: 18, bottom: 44, left: 52 };
const IW = W - M.left - M.right;
const IH = H - M.top - M.bottom;

// x = mean predicted prob (0..1), y = mean actual up-frequency (0..1).
const px = (p: number) => M.left + Math.max(0, Math.min(1, p)) * IW;
const py = (p: number) => M.top + (1 - Math.max(0, Math.min(1, p))) * IH;

// Point radius scales with the number of resolved predictions in the bin.
function rFor(n: number, maxN: number): number {
  if (maxN <= 0 || n <= 0) return 3;
  const t = Math.sqrt(n / maxN); // area-proportional
  return 3 + t * 9; // 3..12 px
}

const TICKS = [0, 0.25, 0.5, 0.75, 1];

/**
 * Reliability diagram (calibration curve). Each bin is plotted at its mean
 * predicted probability (x) against the fraction that actually went up (y),
 * sized by how many resolved predictions fell in it. The y=x diagonal is
 * perfect calibration: points ABOVE it are underconfident (reality beat the
 * forecast), points BELOW are overconfident.
 */
export default function ReliabilityDiagram({ bins }: { bins: CalBin[] }) {
  const valid = (bins ?? []).filter(
    (b) => b && b.N > 0 && Number.isFinite(b.MeanPred) && Number.isFinite(b.MeanActual),
  );
  const maxN = valid.reduce((m, b) => Math.max(m, b.N), 0);
  const totalN = valid.reduce((s, b) => s + b.N, 0);

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      className="h-auto w-full"
      role="img"
      aria-label={
        valid.length > 0
          ? `Reliability diagram: ${valid.length} probability bins over ${totalN} resolved predictions, plotted against the perfect-calibration diagonal`
          : "Reliability diagram: no resolved predictions plotted yet"
      }
    >
      {/* plot frame */}
      <rect
        x={M.left}
        y={M.top}
        width={IW}
        height={IH}
        fill="none"
        stroke="var(--border)"
        strokeWidth="1"
      />

      {/* gridlines + tick labels */}
      {TICKS.map((t) => (
        <g key={`gx-${t}`}>
          <line
            x1={px(t)}
            y1={M.top}
            x2={px(t)}
            y2={M.top + IH}
            stroke="var(--border)"
            strokeWidth="1"
            strokeOpacity={t === 0 || t === 1 ? 0 : 0.4}
          />
          <text x={px(t)} y={H - M.bottom + 16} fontSize="9" fill="var(--faint)" textAnchor="middle">
            {t.toFixed(2)}
          </text>
        </g>
      ))}
      {TICKS.map((t) => (
        <g key={`gy-${t}`}>
          <line
            x1={M.left}
            y1={py(t)}
            x2={M.left + IW}
            y2={py(t)}
            stroke="var(--border)"
            strokeWidth="1"
            strokeOpacity={t === 0 || t === 1 ? 0 : 0.4}
          />
          <text x={M.left - 8} y={py(t) + 3} fontSize="9" fill="var(--faint)" textAnchor="end">
            {t.toFixed(2)}
          </text>
        </g>
      ))}

      {/* y = x perfect-calibration diagonal */}
      <line
        x1={px(0)}
        y1={py(0)}
        x2={px(1)}
        y2={py(1)}
        stroke="var(--faint)"
        strokeWidth="1.5"
        strokeDasharray="4 3"
      />
      <text
        x={px(0.82)}
        y={py(0.82) - 6}
        fontSize="9"
        fill="var(--faint)"
        textAnchor="middle"
        transform={`rotate(-45 ${px(0.82)} ${py(0.82) - 6})`}
      >
        perfect calibration
      </text>

      {/* connecting polyline through bin points (in predicted-prob order) */}
      {valid.length >= 2 && (
        <polyline
          points={[...valid]
            .sort((a, b) => a.MeanPred - b.MeanPred)
            .map((b) => `${px(b.MeanPred).toFixed(1)},${py(b.MeanActual).toFixed(1)}`)
            .join(" ")}
          fill="none"
          stroke="var(--accent)"
          strokeWidth="1"
          strokeOpacity="0.4"
        />
      )}

      {/* bin points, sized by N; colored by over/under-confidence */}
      {valid.map((b, i) => {
        const over = b.MeanActual < b.MeanPred; // predicted higher than reality → overconfident
        const fill = over ? "var(--ask)" : "var(--bid)";
        return (
          <circle
            key={i}
            cx={px(b.MeanPred)}
            cy={py(b.MeanActual)}
            r={rFor(b.N, maxN)}
            fill={fill}
            fillOpacity="0.55"
            stroke={fill}
            strokeWidth="1"
          >
            <title>
              {`predicted ${(b.MeanPred * 100).toFixed(0)}% → actual ${(b.MeanActual * 100).toFixed(0)}% up · ${b.N} resolved · ${over ? "overconfident" : "underconfident"}`}
            </title>
          </circle>
        );
      })}

      {/* axis titles */}
      <text x={M.left + IW / 2} y={H - 6} fontSize="10" fill="var(--dim)" textAnchor="middle">
        mean predicted P(up)
      </text>
      <text
        x={13}
        y={M.top + IH / 2}
        fontSize="10"
        fill="var(--dim)"
        textAnchor="middle"
        transform={`rotate(-90 13 ${M.top + IH / 2})`}
      >
        actual up-frequency
      </text>
    </svg>
  );
}
