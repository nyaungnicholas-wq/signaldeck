"use client";

// UNUSUAL ACTIVITY panel (Signal8 wave, Stage 3): recent anomaly detections —
// trade imbalance, unusual volatility, unusual volume. Fleet-wide on the home
// page (no props) or scoped to one symbol on its page.
//
// HONESTY (rendered, not implied): every row is a DESCRIPTIVE z-score of
// recent activity vs the SAME symbol's own trailing baseline — the row detail
// states the window + baseline, and the API `note` ("not predictions") is
// always shown. Stock imbalance rows are a volume-side PROXY (no order book
// exists on free stock data) — they carry the proxy label in their detail and
// get a PROXY chip here.

import { useEffect, useState } from "react";
import Link from "next/link";
import { anomalies, type AnomaliesResponse, type AnomalyRow, type Market } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import EmptyState from "@/components/EmptyState";

const KIND_LABEL: Record<AnomalyRow["kind"], string> = {
  anomaly_imbalance: "IMBALANCE",
  anomaly_vol: "VOLATILITY",
  anomaly_volume: "VOLUME",
};

function zColor(row: AnomalyRow): string {
  if (row.kind === "anomaly_imbalance") {
    return row.z >= 0 ? "var(--bid)" : "var(--ask)";
  }
  return "var(--warn)";
}

const isProxy = (row: AnomalyRow) => row.detail.includes("volume-side proxy");

/** True-range spike rows store the TR/ATR RATIO in `z` (the detail says so
 *  verbatim: "value here is the TR/ATR ratio, not a z-score") — rendering it
 *  as "z=" would misstate the statistic. Same detail-sniffing pattern as
 *  isProxy: the API row carries no separate measure field. */
const isTrAtrRatio = (row: AnomalyRow) => row.detail.includes("TR/ATR ratio");

export default function UnusualActivityPanel({
  symbol,
  market,
  kind,
  limit = 12,
}: {
  symbol?: string;
  market?: Market;
  /** Stage 5: optional server-side kind filter (imbalance/vol/volume). */
  kind?: AnomalyRow["kind"];
  limit?: number;
}) {
  const [resp, setResp] = useState<AnomaliesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      anomalies(symbol, market, kind, limit)
        .then((r) => {
          if (!alive) return;
          setResp(r);
          setError(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setError(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, 60_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol, market, kind, limit]);

  const rows = resp?.anomalies ?? [];

  // On a symbol page an empty panel is honest quiet — say so compactly.
  // On the home page keep the panel visible so users learn it exists.
  return (
    <section className="panel" aria-label="unusual activity">
      <div className="panel-h flex-wrap gap-2">
        <span>UNUSUAL ACTIVITY{symbol ? ` · ${symbol}` : ""}</span>
        <span
          className="text-[0.68rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          z-scores vs each symbol&rsquo;s own baseline — descriptive, not predictions
        </span>
        {resp !== null && <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>{rows.length} shown</span>}
      </div>
      <div className="flex flex-col">
        {resp === null && !error && (
          <div className="p-3">
            <Skeleton lines={3} label="loading unusual activity" />
          </div>
        )}
        {resp === null && error && (
          <div className="p-3 text-[0.78rem]" style={{ color: "var(--bad)" }}>
            {error} — is the daemon running?
          </div>
        )}
        {resp !== null && rows.length === 0 && (
          <EmptyState
            message={kind ? `No ${KIND_LABEL[kind].toLowerCase()} anomalies detected` : "No unusual activity detected"}
            detail="Nothing is currently outside its own statistical baseline (|z| threshold applies). Quiet is the honest default state."
          />
        )}
        {rows.map((a) => (
          <div
            key={a.id}
            className="flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t px-4 py-2 text-[0.78rem]"
            style={{ borderColor: "var(--border)" }}
          >
            {!symbol && (
              <Link
                href={`/s/${a.market}/${encodeURIComponent(a.symbol)}`}
                className="cursor-pointer font-bold tracking-wide hover:text-[var(--accent)]"
              >
                {a.symbol}
              </Link>
            )}
            <span
              className="chip px-2 py-[1px] text-[0.68rem] tracking-wider"
              style={{ color: zColor(a), borderColor: zColor(a) }}
            >
              {KIND_LABEL[a.kind]}
            </span>
            {isProxy(a) && (
              <span
                className="chip px-2 py-[1px] text-[0.68rem] tracking-wider"
                title={resp?.proxyNote}
                style={{ color: "var(--faint)" }}
              >
                PROXY
              </span>
            )}
            <span className="tnum font-bold" style={{ color: zColor(a) }}>
              {isTrAtrRatio(a)
                ? `TR/ATR=${a.z.toFixed(1)}x`
                : `z=${a.z >= 0 ? "+" : ""}${a.z.toFixed(1)}`}
            </span>
            <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
              {ago(a.ts)}
            </span>
            <span className="w-full text-[0.72rem] leading-relaxed" style={{ color: "var(--dim)" }}>
              {a.detail}
            </span>
          </div>
        ))}
        {resp !== null && (
          <p
            className="border-t px-4 py-2 text-[0.68rem] leading-relaxed"
            style={{ borderColor: "var(--border)", color: "var(--faint)" }}
          >
            {resp.note} {resp.proxyNote}
          </p>
        )}
      </div>
    </section>
  );
}
