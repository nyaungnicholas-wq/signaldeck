"use client";

// Extracted verbatim from the old monolithic /lab/backtest page (pure
// refactor; behavior identical).

import { useMemo } from "react";
import { fmtDate } from "@/lib/format";

/** Full-width equity-curve line chart. Green when the strategy ended above its
 * 1.0 starting equity, red when below. A faint baseline marks break-even (1.0).
 * X is aligned to the supplied bar timestamps for the axis labels only. */
export default function EquityCurve({ equity, ts }: { equity: number[]; ts: number[] }) {
  const W = 1000;
  const H = 260;
  const padX = 4;
  const padY = 10;

  const geom = useMemo(() => {
    if (!equity || equity.length < 2) return null;
    let min = Infinity;
    let max = -Infinity;
    for (const v of equity) {
      if (!isFinite(v)) continue;
      if (v < min) min = v;
      if (v > max) max = v;
    }
    if (!isFinite(min) || !isFinite(max)) return null;
    // Always include the 1.0 baseline in the visible range.
    min = Math.min(min, 1);
    max = Math.max(max, 1);
    const span = max - min || 1;
    const x = (i: number) =>
      padX + (i / (equity.length - 1)) * (W - 2 * padX);
    const y = (v: number) =>
      padY + (1 - (v - min) / span) * (H - 2 * padY);
    const pts = equity
      .map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`)
      .join(" ");
    const baselineY = y(1);
    const up = equity[equity.length - 1] >= 1;
    return { x, y, pts, baselineY, up, min, max };
  }, [equity]);

  if (!geom) {
    return (
      <div
        className="px-4 py-8 text-center text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        equity curve unavailable — need at least two bars.
      </div>
    );
  }

  const stroke = geom.up ? "var(--bid)" : "var(--ask)";
  const fill = geom.up ? "var(--bid-dim)" : "var(--ask-dim)";
  const areaPts = `${padX.toFixed(1)},${geom.baselineY.toFixed(1)} ${geom.pts} ${(W - padX).toFixed(1)},${geom.baselineY.toFixed(1)}`;
  const first = ts.length ? ts[0] : 0;
  const last = ts.length ? ts[ts.length - 1] : 0;

  return (
    <div className="px-4 py-4">
      <div className="overflow-x-auto">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          width="100%"
          height={H}
          preserveAspectRatio="none"
          role="img"
          aria-label={`equity curve, ${geom.up ? "ended above" : "ended below"} break-even`}
          style={{ display: "block" }}
        >
          {/* break-even baseline at equity = 1.0 */}
          <line
            x1={padX}
            x2={W - padX}
            y1={geom.baselineY}
            y2={geom.baselineY}
            stroke="var(--faint)"
            strokeWidth={1}
            strokeDasharray="4 4"
            vectorEffect="non-scaling-stroke"
          />
          <polygon points={areaPts} fill={fill} stroke="none" />
          <polyline
            points={geom.pts}
            fill="none"
            stroke={stroke}
            strokeWidth={2}
            vectorEffect="non-scaling-stroke"
            strokeLinejoin="round"
          />
        </svg>
      </div>
      <div
        className="tnum mt-2 flex justify-between text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        <span>{first ? fmtDate(first) : "start"}</span>
        <span
          style={{ color: geom.up ? "var(--bid)" : "var(--ask)" }}
        >
          equity 1.00 → {equity[equity.length - 1].toFixed(3)}
        </span>
        <span>{last ? fmtDate(last) : "end"}</span>
      </div>
    </div>
  );
}
