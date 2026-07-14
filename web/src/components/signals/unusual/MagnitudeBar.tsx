"use client";

// Per-row |z| magnitude bar for the UNUSUAL tape — a small horizontal fill
// that makes severity scannable at a glance without replacing the honest
// numeric badge next to it. Z rows fill on min(|z|,6)/6; ratio rows are a
// different statistic and use their OWN scale (min(ratio,10)/10), labeled as
// a ratio in the tooltip so the two are never conflated. Fill color follows
// the same severity bands as the badge (gray 2–3, amber 3–4, red 4+).

import type { AnomalyRow } from "@/lib/api";
import {
  magnitudeFrac,
  RATIO_BAR_MAX,
  rowMeasure,
  rowValue,
  severityBand,
  SEVERITY_COLOR,
  Z_BAR_MAX,
} from "./measure";

export default function MagnitudeBar({ row }: { row: AnomalyRow }) {
  const frac = magnitudeFrac(row);
  const band = severityBand(row);
  const measure = rowMeasure(row);
  const v = rowValue(row);
  const title =
    measure === "ratio"
      ? `TR/ATR ratio ${v.toFixed(1)}× on a 0–${RATIO_BAR_MAX}× scale — a ratio, NOT a z-score`
      : `|z| ${Math.abs(v).toFixed(1)} on a 0–${Z_BAR_MAX} scale`;

  return (
    <span
      role="meter"
      aria-valuemin={0}
      aria-valuemax={measure === "ratio" ? RATIO_BAR_MAX : Z_BAR_MAX}
      aria-valuenow={Number(Math.abs(v).toFixed(1))}
      aria-label={title}
      title={title}
      className="inline-flex h-1.5 w-16 shrink-0 self-center overflow-hidden rounded-full border"
      style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
    >
      <span
        className="transition-[width] duration-300"
        style={{ width: `${frac * 100}%`, background: SEVERITY_COLOR[band] }}
      />
    </span>
  );
}
