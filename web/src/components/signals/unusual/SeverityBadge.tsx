"use client";

// Severity badge for one anomaly row — the measure-aware replacement for the
// old bare "z=+3.1" text. Band word (notable/elevated/extreme) + the honest
// statistic: PRO shows the raw number labeled by its ACTUAL measure (z vs
// TR/ATR ratio — never conflated); SIMPLE translates the same number into
// "top X% vs its own history" (zTail buckets from lib/plain) or "N× its
// normal bar range" for ratio rows. Both modes badge the same statistic.

import { useViewMode } from "@/components/Plain";
import { zTail } from "@/lib/plain";
import type { AnomalyRow } from "@/lib/api";
import { rowMeasure, rowValue, severityBand, SEVERITY_COLOR } from "./measure";

export default function SeverityBadge({ row }: { row: AnomalyRow }) {
  const mode = useViewMode();
  const band = severityBand(row);
  const color = SEVERITY_COLOR[band];
  const v = rowValue(row);
  const measure = rowMeasure(row);

  let stat: string;
  let title: string;
  if (measure === "ratio") {
    stat = mode === "pro" ? `TR/ATR=${v.toFixed(1)}×` : `${v.toFixed(1)}× its normal bar range`;
    title =
      "True-range spike: the last bar's range as a multiple of the trailing ATR(14). This is a RATIO, not a z-score. Descriptive vs this symbol's own history — not a prediction.";
  } else {
    const tail = zTail(Math.abs(v));
    stat =
      mode === "pro"
        ? `z=${v >= 0 ? "+" : ""}${v.toFixed(1)}`
        : `${tail ?? "unusual"} vs its own history`;
    title =
      "Z-score vs this symbol's own trailing baseline — how many standard deviations the recent reading is from its own normal. Descriptive, not a prediction.";
  }

  return (
    <span
      className="chip tnum px-2 py-[1px] text-[0.75rem] tracking-wider whitespace-nowrap"
      style={{ color, borderColor: color }}
      title={title}
    >
      {band.toUpperCase()} · {stat}
    </span>
  );
}
