"use client";

// GradeMeter (Stage 4 of the "tables → charts" pass) — the big report-card
// meter: one metric's plain.ts reading rendered as a −1..+1 bar that grows
// from the neutral center toward good (right, green) or bad (left, red).
//
// It renders a PlainReading straight from lib/plain.ts readMetric(), so the
// bar can NEVER disagree with the sentence: goodness null (gated / no data /
// neutral quantity) = empty track + the honest "no read yet" wording, never
// a fake fill. The plain sentence — including its sample-size caveats — is
// always printed under the bar in BOTH view modes.

import type { PlainReading } from "@/lib/plain";
import { goodnessColor } from "@/lib/plain";

export default function GradeMeter({
  label,
  reading,
  className = "",
}: {
  /** Mode-appropriate metric label (caller picks via metricLabel()). */
  label: string;
  /** The central-dictionary reading — bar + sentence come from the SAME source. */
  reading: PlainReading;
  className?: string;
}) {
  const g = reading.goodness;
  const has = g != null && Number.isFinite(g);
  const clamped = has ? Math.max(-1, Math.min(1, g as number)) : 0;
  const color = goodnessColor(has ? clamped : null);
  // Bar grows from the 50% center: left half = bad, right half = good.
  const left = has ? (clamped < 0 ? 50 + clamped * 50 : 50) : 50;
  const width = has ? Math.abs(clamped) * 50 : 0;

  return (
    <div className={`flex flex-col gap-1 ${className}`} title={reading.detail}>
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[0.66rem] uppercase tracking-[0.14em]" style={{ color: "var(--faint)" }}>
          {label}
        </span>
        <span className="tnum text-[0.72rem]" style={{ color: has ? color : "var(--faint)" }}>
          {reading.raw}
        </span>
      </div>
      <div
        className="relative h-2.5 w-full overflow-hidden rounded-full"
        style={{ background: "color-mix(in srgb, var(--faint) 16%, transparent)" }}
        role="img"
        aria-label={
          has
            ? `${label}: ${reading.plain} — meter ${clamped >= 0 ? "toward good" : "toward bad"} (${Math.round(Math.abs(clamped) * 100)}%)`
            : `${label}: ${reading.plain} — meter empty`
        }
      >
        {has && (
          <div
            className="absolute top-0 h-full"
            style={{ left: `${left}%`, width: `${width}%`, background: color }}
          />
        )}
        {/* the neutral tick — always visible so distance-from-neutral reads at a glance */}
        <div
          aria-hidden="true"
          className="absolute top-0 h-full"
          style={{ left: "50%", width: 1, background: "var(--dim)" }}
        />
      </div>
      <div className="flex items-center justify-between text-[0.62rem]" style={{ color: "var(--faint)" }}>
        <span>bad</span>
        <span>neutral</span>
        <span>good</span>
      </div>
      {/* the honest sentence (with its caveats) rides on the meter in both modes */}
      <p className="m-0 text-[0.74rem] leading-snug" style={{ color: has ? "var(--dim)" : "var(--faint)" }}>
        {reading.plain}
      </p>
    </div>
  );
}
