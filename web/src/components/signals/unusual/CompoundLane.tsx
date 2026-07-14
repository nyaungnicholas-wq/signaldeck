"use client";

// COMPOUND SIGNALS — the right lane of the rebuilt UNUSUAL tab: symbols whose
// tape shows ≥2 DISTINCT anomaly kinds inside a rolling 24h window, computed
// client-side from the SAME rows the raw tape renders (measure.compoundGroups
// — no extra fetch, no hidden data). HONESTY: the lane is labeled
// "co-occurrence, not causation" verbatim — flags firing together are still
// descriptive observations about the same symbol, never a prediction and
// never evidence that one flag caused the other.

import Link from "next/link";
import { useViewMode } from "@/components/Plain";
import Skeleton from "@/components/Skeleton";
import EmptyState from "@/components/EmptyState";
import { ago } from "@/lib/format";
import type { AnomalyRow } from "@/lib/api";
import SeverityBadge from "./SeverityBadge";
import {
  bandRank,
  compoundGroups,
  kindColor,
  kindLabel,
  rowProxy,
  severityBand,
  SEVERITY_COLOR,
  type CompoundGroup,
  type SeverityBand,
} from "./measure";

/** Worst severity across a group's strongest rows — tints the symbol chip. */
function groupBand(g: CompoundGroup): SeverityBand {
  return g.kinds
    .map((k) => severityBand(k.strongest))
    .reduce<SeverityBand>((worst, b) => (bandRank(b) > bandRank(worst) ? b : worst), "notable");
}

/** Compact "within Xm/Xh" span between a group's first and last event. */
function spanLabel(earliestTs: number, latestTs: number): string {
  const s = Math.max(0, latestTs - earliestTs);
  if (s < 90) return "same scan";
  if (s < 3600) return `within ${Math.round(s / 60)}m`;
  return `within ${(s / 3600).toFixed(s < 10 * 3600 ? 1 : 0)}h`;
}

export default function CompoundLane({
  rows,
  proxyNote,
}: {
  /** null = still loading (the page owns fetching). */
  rows: AnomalyRow[] | null;
  proxyNote?: string;
}) {
  const mode = useViewMode();
  const groups = rows === null ? null : compoundGroups(rows);

  return (
    <section className="panel" aria-label="compound signals — co-occurrence, not causation">
      <div className="panel-h flex-wrap gap-2">
        <span>COMPOUND SIGNALS</span>
        <span className="chip px-2 py-[1px] text-[0.75rem] font-normal tracking-wider">
          co-occurrence, not causation
        </span>
        {groups !== null && (
          <span className="tnum ml-auto text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
            {groups.length} symbol{groups.length === 1 ? "" : "s"}
          </span>
        )}
      </div>

      <div className="flex flex-col">
        {groups === null && (
          <div className="p-3">
            <Skeleton lines={3} label="loading compound signals" />
          </div>
        )}

        {groups !== null && groups.length === 0 && (
          <EmptyState
            message="No compound signals in the current view"
            detail="No symbol shown on the tape has 2+ distinct anomaly kinds inside a 24h window. Single flags stay in the raw tape — nothing is promoted here until kinds co-occur."
            className="border-0"
          />
        )}

        {groups?.map((g) => (
          <div
            key={`${g.market}:${g.symbol}`}
            className="flex flex-col gap-1.5 border-t px-3 py-2"
            style={{ borderColor: "var(--border)" }}
          >
            <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-[0.75rem]">
              <Link
                href={`/s/${g.market}/${encodeURIComponent(g.symbol)}`}
                className="chip mono cursor-pointer px-2 py-[1px] font-bold tracking-wide hover:text-[var(--accent)]"
                style={{ borderColor: SEVERITY_COLOR[groupBand(g)] }}
                title={`${g.symbol} — worst flag ${groupBand(g)}`}
              >
                {g.symbol}
              </Link>
              <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {g.market}
              </span>
              <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
                {g.kinds.length} kinds · {g.events} event{g.events === 1 ? "" : "s"} ·{" "}
                {spanLabel(g.earliestTs, g.latestTs)}
              </span>
              <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {ago(g.latestTs)}
              </span>
            </div>
            {g.kinds.map((k) => (
              <div key={k.kind} className="flex flex-wrap items-baseline gap-x-2 gap-y-1 pl-2">
                <span
                  className="chip px-2 py-[1px] text-[0.75rem] tracking-wider whitespace-nowrap"
                  style={{ color: kindColor(k.strongest), borderColor: kindColor(k.strongest) }}
                >
                  {kindLabel(k.kind, mode)}
                </span>
                <SeverityBadge row={k.strongest} />
                {k.count > 1 && (
                  <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    ×{k.count} (strongest shown)
                  </span>
                )}
                {rowProxy(k.strongest) && (
                  <span
                    className="chip px-2 py-[1px] text-[0.75rem] tracking-wider"
                    title={proxyNote}
                    style={{ color: "var(--faint)" }}
                  >
                    PROXY
                  </span>
                )}
              </div>
            ))}
          </div>
        ))}

        {groups !== null && groups.length > 0 && (
          <p
            className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
            style={{ borderColor: "var(--border)", color: "var(--faint)" }}
          >
            Symbols with 2+ distinct anomaly kinds within a rolling 24h window, computed from the
            rows shown on the tape. Co-occurrence, not causation — simultaneous flags are still
            descriptive statistics vs each symbol&rsquo;s own baseline, not predictions.
          </p>
        )}
      </div>
    </section>
  );
}
