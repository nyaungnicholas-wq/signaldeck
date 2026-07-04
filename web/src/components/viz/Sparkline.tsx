"use client";

// VISUAL KIT (Stage 3) — <Sparkline/>: inline mini price chart for ~60 daily
// closes. Pure SVG, no deps. Stroke color follows the PERIOD trend (first→
// last close): green up, red down, dim flat. Optional soft area fill and a
// dotted baseline at the period-start close so the eye reads "vs where it
// started". Under 5 points it renders a reserved-size placeholder (never a
// fabricated line) with a tooltip saying WHY — honesty + no layout shift.
//
// ui-ux-pro-max notes applied: reserved space (no content jumping), color is
// not the only signal (aria-label carries direction + %), fixed sizing so
// table rows stay aligned, transitions left to CSS (globals.css disables them
// under prefers-reduced-motion).

export default function Sparkline({
  closes,
  width = 120,
  height = 32,
  area = true,
  label,
}: {
  closes: number[];
  width?: number;
  height?: number;
  /** Soft under-line fill in the trend color. */
  area?: boolean;
  /** Accessible label override; default announces direction + period change. */
  label?: string;
}) {
  const pts = (closes ?? []).filter((v) => Number.isFinite(v));

  // Honest placeholder: same footprint, no invented shape, reason on hover.
  if (pts.length < 5) {
    return (
      <svg
        width={width}
        height={height}
        role="img"
        aria-label="not enough daily closes for a trend yet (needs 5+)"
        style={{ display: "inline-block", verticalAlign: "middle" }}
      >
        <title>not enough daily closes yet (needs 5+) — bars accrue on worker cadence</title>
        <line
          x1={2}
          y1={height / 2}
          x2={width - 2}
          y2={height / 2}
          stroke="var(--border)"
          strokeDasharray="2 4"
        />
      </svg>
    );
  }

  const min = Math.min(...pts);
  const max = Math.max(...pts);
  const span = max - min || 1;
  const padX = 2;
  const padY = 3;
  const x = (i: number) => padX + (i / (pts.length - 1)) * (width - 2 * padX);
  const y = (v: number) => height - padY - ((v - min) / span) * (height - 2 * padY);

  const line = pts.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const areaPath =
    `M ${x(0).toFixed(1)} ${(height - padY).toFixed(1)} ` +
    pts.map((v, i) => `L ${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join(" ") +
    ` L ${x(pts.length - 1).toFixed(1)} ${(height - padY).toFixed(1)} Z`;

  const first = pts[0];
  const last = pts[pts.length - 1];
  const chgPct = first !== 0 ? ((last / first - 1) * 100) : 0;
  const dir = last > first ? "up" : last < first ? "down" : "flat";
  const stroke = dir === "up" ? "var(--bid)" : dir === "down" ? "var(--ask)" : "var(--dim)";
  const fill = dir === "up" ? "var(--bid-dim)" : dir === "down" ? "var(--ask-dim)" : "transparent";

  return (
    <svg
      width={width}
      height={height}
      role="img"
      aria-label={
        label ??
        `${pts.length}-day trend ${dir}, ${chgPct >= 0 ? "+" : ""}${chgPct.toFixed(1)}% over the period`
      }
      style={{ display: "inline-block", verticalAlign: "middle" }}
    >
      {/* baseline at the period-start close: "vs where it started" */}
      <line
        x1={padX}
        y1={y(first)}
        x2={width - padX}
        y2={y(first)}
        stroke="var(--faint)"
        strokeOpacity={0.45}
        strokeDasharray="2 3"
      />
      {area && <path d={areaPath} fill={fill} stroke="none" />}
      <polyline points={line} fill="none" stroke={stroke} strokeWidth={1.5} strokeLinejoin="round" />
      {/* end dot anchors the eye on the latest close */}
      <circle cx={x(pts.length - 1)} cy={y(last)} r={2} fill={stroke} />
    </svg>
  );
}
