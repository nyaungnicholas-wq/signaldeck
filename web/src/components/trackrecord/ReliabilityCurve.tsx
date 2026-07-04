"use client";

// Reliability (calibration) diagram: for each probability bin, plot the mean
// predicted probability (x) against the mean realized frequency (y). Points on
// the diagonal are perfectly calibrated; above the line the model is
// under-confident, below it over-confident. Dot size ∝ bin count. Renders an
// honest, near-empty shape when the record is thin (most bins N==0).

import type { ReliabilityBin } from "@/lib/api";

const W = 320;
const H = 320;
const PAD = 34;

export default function ReliabilityCurve({ bins }: { bins: ReliabilityBin[] }) {
  const populated = bins.filter((b) => b.n > 0);
  const maxN = populated.reduce((m, b) => Math.max(m, b.n), 1);

  // Map [0,1] to plot coords (y inverted so 0 is at the bottom).
  const x = (v: number) => PAD + v * (W - 2 * PAD);
  const y = (v: number) => H - PAD - v * (H - 2 * PAD);

  return (
    <div className="w-full" style={{ maxWidth: W }}>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        width="100%"
        role="img"
        aria-label="reliability calibration diagram"
        style={{ display: "block" }}
      >
        {/* frame */}
        <rect x={PAD} y={PAD} width={W - 2 * PAD} height={H - 2 * PAD} fill="none" stroke="var(--border)" />
        {/* perfect-calibration diagonal */}
        <line x1={x(0)} y1={y(0)} x2={x(1)} y2={y(1)} stroke="var(--faint)" strokeDasharray="4 4" />
        {/* gridlines at 0.5 */}
        <line x1={x(0.5)} y1={y(0)} x2={x(0.5)} y2={y(1)} stroke="var(--border)" strokeWidth={0.5} />
        <line x1={x(0)} y1={y(0.5)} x2={x(1)} y2={y(0.5)} stroke="var(--border)" strokeWidth={0.5} />

        {/* axis labels */}
        <text x={x(0.5)} y={H - 8} textAnchor="middle" fontSize="10" fill="var(--faint)">
          mean predicted →
        </text>
        <text
          x={12}
          y={y(0.5)}
          textAnchor="middle"
          fontSize="10"
          fill="var(--faint)"
          transform={`rotate(-90 12 ${y(0.5)})`}
        >
          mean realized →
        </text>

        {/* connecting line through populated bins (in x order) */}
        {populated.length > 1 && (
          <polyline
            points={populated.map((b) => `${x(b.meanPred)},${y(b.meanActual)}`).join(" ")}
            fill="none"
            stroke="var(--accent)"
            strokeWidth={1.2}
            opacity={0.6}
          />
        )}

        {/* bin dots, radius ∝ sqrt(count) */}
        {populated.map((b, i) => {
          const r = 3 + 6 * Math.sqrt(b.n / maxN);
          return (
            <circle
              key={i}
              cx={x(b.meanPred)}
              cy={y(b.meanActual)}
              r={r}
              fill="var(--accent)"
              fillOpacity={0.75}
              stroke="var(--bg)"
              strokeWidth={0.75}
            >
              <title>
                {`bin ${b.lo.toFixed(1)}–${b.hi.toFixed(1)}: n=${b.n}, pred ${(b.meanPred * 100).toFixed(0)}%, actual ${(b.meanActual * 100).toFixed(0)}%`}
              </title>
            </circle>
          );
        })}
      </svg>
      {populated.length === 0 && (
        <p className="mt-1 text-center text-[0.72rem]" style={{ color: "var(--faint)" }}>
          no populated bins yet
        </p>
      )}
    </div>
  );
}
