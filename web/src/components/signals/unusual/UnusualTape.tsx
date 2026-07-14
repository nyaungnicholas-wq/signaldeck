"use client";

// RAW TAPE — the left lane of the rebuilt UNUSUAL tab: every fetched anomaly
// event as one dense row (time · symbol · kind · severity/measure badge ·
// one-line detail). UW-style feed on free data, with the honesty framing
// rendered, not implied: PROXY chips on stock volume-side imbalance rows, the
// API note verbatim in the footer, and every badge stating its real measure.

import Link from "next/link";
import { useViewMode } from "@/components/Plain";
import Skeleton from "@/components/Skeleton";
import EmptyState from "@/components/EmptyState";
import { ago } from "@/lib/format";
import type { AnomalyRow } from "@/lib/api";
import SeverityBadge from "./SeverityBadge";
import MagnitudeBar from "./MagnitudeBar";
import {
  kindColor,
  kindLabel,
  rowProxy,
  severityBand,
  SEVERITY_COLOR,
  SEVERITY_ORDER,
} from "./measure";

export default function UnusualTape({
  rows,
  note,
  proxyNote,
  emptyMessage,
  emptyDetail,
}: {
  /** null = still loading (the page owns fetching; error states render there). */
  rows: AnomalyRow[] | null;
  note?: string;
  proxyNote?: string;
  emptyMessage: string;
  emptyDetail: string;
}) {
  const mode = useViewMode();

  const counts =
    rows === null
      ? null
      : rows.reduce(
          (acc, r) => {
            acc[severityBand(r)]++;
            return acc;
          },
          { notable: 0, elevated: 0, extreme: 0 },
        );

  return (
    <section className="panel" aria-label="raw tape — every anomaly event">
      <div className="panel-h flex-wrap gap-2">
        <span>RAW TAPE</span>
        {counts && (
          <span className="flex items-center gap-2 text-[0.75rem] font-normal tracking-wider">
            {SEVERITY_ORDER.map((b) => (
              <span key={b} className="tnum" style={{ color: SEVERITY_COLOR[b] }}>
                {b.toUpperCase()} {counts[b]}
              </span>
            ))}
          </span>
        )}
        {rows !== null && (
          <span className="tnum ml-auto text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
            {rows.length} shown
          </span>
        )}
      </div>

      <div className="flex flex-col">
        {rows === null && (
          <div className="p-3">
            <Skeleton lines={4} label="loading the anomaly tape" />
          </div>
        )}

        {rows !== null && rows.length === 0 && (
          <EmptyState message={emptyMessage} detail={emptyDetail} className="border-0" />
        )}

        {rows?.map((a) => (
          <div
            key={a.id}
            className="flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t px-3 py-1.5 text-[0.75rem]"
            style={{ borderColor: "var(--border)" }}
          >
            <span className="tnum w-14 shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {ago(a.ts)}
            </span>
            <Link
              href={`/s/${a.market}/${encodeURIComponent(a.symbol)}`}
              className="chip mono cursor-pointer px-2 py-[1px] font-bold tracking-wide hover:text-[var(--accent)]"
              style={{ borderColor: SEVERITY_COLOR[severityBand(a)] }}
              title={`${a.symbol} — ${severityBand(a)}`}
            >
              {a.symbol}
            </Link>
            <span
              className="chip px-2 py-[1px] text-[0.75rem] tracking-wider whitespace-nowrap"
              style={{ color: kindColor(a), borderColor: kindColor(a) }}
            >
              {kindLabel(a.kind, mode)}
            </span>
            <SeverityBadge row={a} />
            <MagnitudeBar row={a} />
            {rowProxy(a) && (
              <span
                className="chip px-2 py-[1px] text-[0.75rem] tracking-wider"
                title={proxyNote}
                style={{ color: "var(--faint)" }}
              >
                PROXY
              </span>
            )}
            <span
              className="min-w-0 flex-1 basis-48 truncate text-[0.75rem] leading-relaxed"
              style={{ color: "var(--dim)" }}
              title={a.detail}
            >
              {a.detail}
            </span>
          </div>
        ))}

        {rows !== null && (note || proxyNote) && (
          <p
            className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
            style={{ borderColor: "var(--border)", color: "var(--faint)" }}
          >
            {note} {proxyNote}
          </p>
        )}
      </div>
    </section>
  );
}
