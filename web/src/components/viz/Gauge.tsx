"use client";

// VISUAL KIT (Stage 3) — <Gauge/>: compact semicircular dial (pure SVG).
// Background arc + optional tinted zones + needle + big tabular value.
// THE CAPTION LINE ALWAYS RENDERS — it is the honesty gate text ("breadth
// over 502 symbols", "n=1/30 — not significant") and per the doctrine a
// gauge without its gate caption is a lie of omission. hasData=false keeps
// the dial footprint (no layout shift) but hides the needle and shows "—".
//
// ui-ux-pro-max notes applied: value + caption in text so color/needle are
// never the only signal; fixed footprint (no content jumping); needle motion
// is a CSS transition that globals.css disables under prefers-reduced-motion.

export interface GaugeZone {
  /** Zone start/end in VALUE units (clamped to [min,max]). */
  from: number;
  to: number;
  /** Any CSS color; keep to the terminal palette (--bid/--warn/--ask/...). */
  color: string;
}

const CX = 100;
const CY = 100;
const R = 78;
const STROKE = 12;

/** Map a value (0..1 normalized) to a point on the semicircle. */
function polar(t: number, r: number): { x: number; y: number } {
  const a = Math.PI * (1 - t); // 180° (left/min) → 0° (right/max)
  return { x: CX + r * Math.cos(a), y: CY - r * Math.sin(a) };
}

/** SVG arc path along the dial between normalized t0..t1. */
function arcPath(t0: number, t1: number, r: number): string {
  const p0 = polar(t0, r);
  const p1 = polar(t1, r);
  const large = t1 - t0 > 0.5 ? 1 : 0;
  return `M ${p0.x.toFixed(2)} ${p0.y.toFixed(2)} A ${r} ${r} 0 ${large} 1 ${p1.x.toFixed(2)} ${p1.y.toFixed(2)}`;
}

export default function Gauge({
  value,
  min,
  max,
  label,
  caption,
  zones,
  format,
  hasData = true,
  width = 168,
}: {
  value: number;
  min: number;
  max: number;
  /** Short dial title, e.g. "BREADTH" or "VIX". */
  label: string;
  /** The honesty gate caption — ALWAYS rendered under the dial. */
  caption: string;
  zones?: GaugeZone[];
  format?: (v: number) => string;
  /** false = keep footprint, hide needle, show "—" (honest absence). */
  hasData?: boolean;
  width?: number;
}) {
  const span = max - min || 1;
  const clamp = (v: number) => Math.max(min, Math.min(max, v));
  const norm = (v: number) => (clamp(v) - min) / span;
  const fmt = format ?? ((v: number) => v.toFixed(1));

  const t = norm(value);
  const needleTip = polar(t, R - STROKE / 2 - 4);
  const shown = hasData ? fmt(value) : "—";

  return (
    <figure
      className="m-0 flex flex-col items-center"
      style={{ width }}
      role="img"
      aria-label={`${label}: ${hasData ? `${shown} on a ${fmt(min)}–${fmt(max)} dial` : "no data yet"}. ${caption}`}
    >
      <svg viewBox="0 0 200 118" width={width} height={(width * 118) / 200} aria-hidden="true">
        {/* dial background */}
        <path d={arcPath(0, 1, R)} fill="none" stroke="var(--border)" strokeWidth={STROKE} strokeLinecap="round" />
        {/* zone tinting */}
        {hasData &&
          (zones ?? []).map((z, i) => {
            const z0 = norm(Math.min(z.from, z.to));
            const z1 = norm(Math.max(z.from, z.to));
            if (z1 <= z0) return null;
            return (
              <path
                key={i}
                d={arcPath(z0, z1, R)}
                fill="none"
                stroke={z.color}
                strokeOpacity={0.4}
                strokeWidth={STROKE}
              />
            );
          })}
        {/* min/max ticks */}
        <text x={CX - R} y={CY + 14} textAnchor="middle" fontSize="12" fill="var(--faint)" className="tnum">
          {fmt(min)}
        </text>
        <text x={CX + R} y={CY + 14} textAnchor="middle" fontSize="12" fill="var(--faint)" className="tnum">
          {fmt(max)}
        </text>
        {/* needle (hidden when there is honestly nothing to point at) */}
        {hasData && (
          <g style={{ transition: "all 300ms ease" }}>
            <line x1={CX} y1={CY} x2={needleTip.x} y2={needleTip.y} stroke="var(--text)" strokeWidth={2} />
            <circle cx={CX} cy={CY} r={4} fill="var(--text)" />
          </g>
        )}
        {/* big value, tabular */}
        <text
          x={CX}
          y={CY - 22}
          textAnchor="middle"
          fontSize="22"
          fontWeight="bold"
          fill={hasData ? "var(--text)" : "var(--faint)"}
          className="tnum"
        >
          {shown}
        </text>
      </svg>
      <figcaption className="flex flex-col items-center gap-0.5 text-center">
        <span className="text-[0.75rem] font-medium tracking-[0.14em]" style={{ color: "var(--dim)" }}>
          {label}
        </span>
        {/* THE gate caption — never conditionally removed */}
        <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
          {caption}
        </span>
      </figcaption>
    </figure>
  );
}
