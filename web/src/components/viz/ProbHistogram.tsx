"use client";

// ProbHistogram (Stage 4 of the "tables → charts" pass) — the distribution
// strip: a small SVG histogram of the fleet's CURRENT calibrated P(up)
// values, so "where does the model lean right now?" reads in one glance
// before the row-by-row table.
//
// HONESTY: it bins only the REAL calibrated probabilities passed in (the
// same rows the table shows — nothing resampled or smoothed), the 50%
// coin-flip line is always drawn, and bins near 50% render dim because a
// pile of near-coin-flips is NOT a pile of conviction. Empty input renders
// nothing (the parent's empty state speaks instead).

const BINS = 10; // 0–10%, 10–20%, … 90–100%

export default function ProbHistogram({
  probs,
  height = 74,
}: {
  /** Calibrated P(up) values, 0..1 — non-finite entries are dropped. */
  probs: number[];
  height?: number;
}) {
  const vals = (probs ?? []).filter((p) => Number.isFinite(p) && p >= 0 && p <= 1);
  if (vals.length === 0) return null;

  const counts = new Array<number>(BINS).fill(0);
  for (const p of vals) counts[Math.min(BINS - 1, Math.floor(p * BINS))]++;
  const maxCount = Math.max(...counts, 1);

  const W = 560;
  const padX = 6;
  const axisH = 14;
  const plotH = height - axisH;
  const binW = (W - padX * 2) / BINS;

  const binColor = (i: number): string => {
    const center = (i + 0.5) / BINS; // bin midpoint as a probability
    if (Math.abs(center - 0.5) < 0.05) return "var(--dim)"; // near coin flip — no lean
    return center > 0.5 ? "var(--bid)" : "var(--ask)";
  };

  return (
    <svg
      viewBox={`0 0 ${W} ${height}`}
      className="h-auto w-full"
      role="img"
      aria-label={`distribution of ${vals.length} current calibrated up-move probabilities across ${BINS} bins; 50% = coin flip`}
    >
      <title>
        {`Current calibrated P(up) across ${vals.length} symbols — bars left of the 50% line lean down, right lean up, near-50% is a coin flip (dim on purpose).`}
      </title>
      {counts.map((c, i) => {
        const h = (c / maxCount) * (plotH - 4);
        const x = padX + i * binW;
        return (
          <g key={i}>
            <rect
              x={x + 1.5}
              y={plotH - h}
              width={binW - 3}
              height={Math.max(h, c > 0 ? 2 : 0)}
              rx={2}
              fill={binColor(i)}
              opacity={0.75}
            >
              <title>{`${i * 10}–${(i + 1) * 10}%: ${c} symbol${c === 1 ? "" : "s"}`}</title>
            </rect>
            {c > 0 && h > 12 && (
              <text
                x={x + binW / 2}
                y={plotH - h + 11}
                textAnchor="middle"
                fontSize="12"
                fill="var(--panel)"
                fontWeight="700"
              >
                {c}
              </text>
            )}
          </g>
        );
      })}
      {/* the coin-flip line — always drawn */}
      <line
        x1={padX + (W - padX * 2) / 2}
        y1={0}
        x2={padX + (W - padX * 2) / 2}
        y2={plotH}
        stroke="var(--faint)"
        strokeDasharray="3 3"
      />
      <text x={padX} y={height - 3} fontSize="12" fill="var(--faint)">
        0% · leans down
      </text>
      <text x={padX + (W - padX * 2) / 2} y={height - 3} fontSize="12" fill="var(--faint)" textAnchor="middle">
        50% coin flip
      </text>
      <text x={W - padX} y={height - 3} fontSize="12" fill="var(--faint)" textAnchor="end">
        leans up · 100%
      </text>
    </svg>
  );
}
