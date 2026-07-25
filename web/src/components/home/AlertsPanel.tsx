"use client";

// Unread-alerts sidebar panel — extracted from the old monolithic page.tsx
// (pure refactor; behavior identical, CTA now uses verb language).

import Link from "next/link";
import type { AlertRow } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";

function alertKindColor(kind: string): string {
  if (kind === "breakout") return "var(--accent)";
  if (kind === "regime_change") return "var(--crossed)";
  if (kind === "prediction_high") return "var(--bid)";
  if (kind === "prediction_low") return "var(--ask)";
  return "var(--dim)";
}

export default function AlertsPanel({
  alerts,
  unseen,
}: {
  alerts: AlertRow[] | null;
  unseen: number;
}) {
  return (
    <section className="panel" aria-label="unread alerts">
      <div className="panel-h">
        <span>ALERTS</span>
        {unseen > 0 && (
          <span
            className="chip tnum px-2 py-[1px] text-[0.75rem]"
            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
          >
            {unseen} unread
          </span>
        )}
        <Link
          href="/market/activity"
          className="ml-auto cursor-pointer text-[0.75rem] font-normal normal-case tracking-wider text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
        >
          Monitor all →
        </Link>
      </div>
      {alerts === null && <Skeleton lines={2} label="loading alerts" />}
      {alerts !== null && alerts.length === 0 && (
        <p className="px-3 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          No unread alerts — the alerts engine writes breakout / regime-change /
          prediction events for your watched symbols as workers detect them.
        </p>
      )}
      {alerts !== null && alerts.length > 0 && (
        <ul className="m-0 list-none p-0">
          {alerts.map((a) => (
            <li
              key={a.id}
              className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 border-t px-3 py-2 text-[0.75rem]"
              style={{ borderColor: "var(--border)" }}
            >
              <span
                className="chip px-1.5 py-[1px] text-[0.75rem] tracking-wider"
                style={{ color: alertKindColor(a.kind), borderColor: alertKindColor(a.kind) }}
              >
                {a.kind.replace("_", " ")}
              </span>
              {a.symbol && (
                <Link
                  href={`/s/${a.market ?? "stocks"}/${encodeURIComponent(a.symbol)}`}
                  className="mono cursor-pointer font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
                >
                  {a.symbol}
                </Link>
              )}
              <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {ago(a.ts)}
              </span>
              <span className="w-full leading-snug" style={{ color: "var(--dim)" }}>
                {a.detail}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
