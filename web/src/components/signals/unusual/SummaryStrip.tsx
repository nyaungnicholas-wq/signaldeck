"use client";

// SUMMARY STRIP — the visual header of the UNUSUAL tab: count-by-severity
// chips plus a 24h mini timeline (one SVG bar per hour, colored by the worst
// severity that fired in that hour, height by event count). Pure local SVG,
// computed from the SAME rows the tape renders — no extra fetch, and the
// clock anchor (`nowSec`) is stamped at fetch time by the page, never
// Date.now() in render. Descriptive counts of descriptive flags — the strip
// inherits the tab's honesty framing, it does not add predictions.

import type { AnomalyRow } from "@/lib/api";
import {
  bandRank,
  severityBand,
  SEVERITY_COLOR,
  SEVERITY_ORDER,
  type SeverityBand,
} from "./measure";

const HOURS = 24;
const BAR_W = 8;
const GAP = 2;
const PLOT_H = 26;
const AXIS_H = 16;
const W = HOURS * (BAR_W + GAP) - GAP;
const H = PLOT_H + AXIS_H;

interface Bucket {
  count: number;
  worst: SeverityBand | null;
}

export default function SummaryStrip({
  rows,
  nowSec,
}: {
  /** null = still loading (the page owns fetching). */
  rows: AnomalyRow[] | null;
  /** Unix seconds stamped when the rows were fetched — the timeline's clock. */
  nowSec: number;
}) {
  if (rows === null || rows.length === 0) return null;

  const counts: Record<SeverityBand, number> = { notable: 0, elevated: 0, extreme: 0 };
  // index 0 = 24h ago … index 23 = the current hour
  const buckets: Bucket[] = Array.from({ length: HOURS }, () => ({ count: 0, worst: null }));
  for (const r of rows) {
    const band = severityBand(r);
    counts[band]++;
    const ageH = (nowSec - r.ts) / 3600;
    if (ageH < 0 || ageH >= HOURS) continue;
    const b = buckets[HOURS - 1 - Math.floor(ageH)];
    b.count++;
    if (b.worst === null || bandRank(band) > bandRank(b.worst)) b.worst = band;
  }
  const maxCount = Math.max(1, ...buckets.map((b) => b.count));
  const inWindow = buckets.reduce((s, b) => s + b.count, 0);

  return (
    <section className="panel" aria-label="unusual activity summary — last 24 hours">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2.5">
        <div className="flex flex-wrap items-center gap-2" role="group" aria-label="events by severity">
          {SEVERITY_ORDER.map((b) => (
            <span
              key={b}
              className="chip tnum px-2 py-[1px] text-[0.75rem] tracking-wider"
              style={{ color: SEVERITY_COLOR[b], borderColor: SEVERITY_COLOR[b] }}
              title={
                b === "notable"
                  ? "|z| (or ratio) 2–3 — outside its own normal, mildly"
                  : b === "elevated"
                    ? "|z| (or ratio) 3–4 — well outside its own normal"
                    : "|z| (or ratio) 4+ — far outside its own normal"
              }
            >
              {b.toUpperCase()} {counts[b]}
            </span>
          ))}
          <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
            {rows.length} events shown
          </span>
        </div>

        <div className="ml-auto flex items-center gap-2">
          <svg
            viewBox={`0 0 ${W} ${H}`}
            width={W}
            height={H}
            role="img"
            aria-label={`hourly anomaly timeline, last 24 hours: ${inWindow} of ${rows.length} shown events fall inside the window`}
            className="shrink-0"
          >
            {buckets.map((b, i) => {
              const x = i * (BAR_W + GAP);
              if (b.count === 0 || b.worst === null) {
                // empty hour — a faint baseline dot, so the axis stays readable
                return (
                  <circle
                    key={i}
                    cx={x + BAR_W / 2}
                    cy={PLOT_H - 1}
                    r={1}
                    fill="var(--border)"
                  />
                );
              }
              const h = Math.max(3, (b.count / maxCount) * PLOT_H);
              return (
                <rect
                  key={i}
                  x={x}
                  y={PLOT_H - h}
                  width={BAR_W}
                  height={h}
                  rx={1.5}
                  fill={SEVERITY_COLOR[b.worst]}
                >
                  <title>{`${HOURS - 1 - i}h ago: ${b.count} event${b.count === 1 ? "" : "s"}, worst ${b.worst}`}</title>
                </rect>
              );
            })}
            <text x={0} y={H - 2} fontSize={12} fill="var(--faint)">
              24h ago
            </text>
            <text x={W} y={H - 2} fontSize={12} textAnchor="end" fill="var(--faint)">
              now
            </text>
          </svg>
          <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
            per hour, colored by worst severity
          </span>
        </div>
      </div>
    </section>
  );
}
