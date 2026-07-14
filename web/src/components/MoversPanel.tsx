"use client";

// MOVERS (Signal8 wave, Stage 4): top gainers / losers from the stored daily
// universe bars, with a market-cap filter — used on the home page and the
// screener.
//
// HONESTY: mcap = SEC EDGAR SharesOutstanding × last stored close, BEST
// EFFORT — a symbol EDGAR hasn't covered shows "mcap n/a" (never a fabricated
// number), and when a min-mcap filter is active the unknown-mcap symbols it
// excluded are counted on screen. Prices are worker-cadence daily closes, not
// live quotes; index/sector ETFs are excluded (baskets, not single names).
// The API notes are rendered verbatim in the footer.

import Link from "next/link";
import { useEffect, useState } from "react";
import { movers, pollMs, POLL_SLOW, type MoverRow, type MoversResponse } from "@/lib/api";
import { fmtPct, fmtPrice } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

const MCAP_FILTERS: { label: string; min: number }[] = [
  { label: "All", min: 0 },
  { label: "≥ $2B", min: 2e9 },
  { label: "≥ $10B", min: 10e9 },
  { label: "≥ $100B", min: 100e9 },
];

function fmtMcap(v: number | null): string {
  if (v === null || !isFinite(v) || v <= 0) return "n/a";
  if (v >= 1e12) return `$${(v / 1e12).toFixed(2)}T`;
  if (v >= 1e9) return `$${(v / 1e9).toFixed(1)}B`;
  if (v >= 1e6) return `$${(v / 1e6).toFixed(0)}M`;
  return `$${v.toFixed(0)}`;
}

function MoverTable({
  title,
  rows,
  color,
  mcapNote,
}: {
  title: string;
  rows: MoverRow[];
  color: string;
  mcapNote: string;
}) {
  return (
    <div className="min-w-0">
      <div
        className="px-3 py-1.5 text-[0.75rem] font-bold tracking-[0.14em]"
        style={{ color }}
      >
        {title}
      </div>
      {rows.length === 0 ? (
        <p className="px-3 pb-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          none in this filter
        </p>
      ) : (
        <div className="table-wrap">
          <table className="w-full text-[0.75rem]">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)" }}>
                {["SYM", "LAST", "CHG%", "MCAP"].map((h, i) => (
                  <th
                    key={h}
                    scope="col"
                    className={`px-3 py-1 text-[0.75rem] font-medium tracking-wide ${i === 0 ? "text-left" : "text-right"}`}
                    style={{ color: "var(--faint)" }}
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="tnum">
              {rows.map((r) => (
                <tr
                  key={r.symbol}
                  className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                  style={{ borderBottom: "1px solid var(--border)" }}
                >
                  <td className="px-3 py-1.5 text-left">
                    <Link
                      href={`/s/stocks/${encodeURIComponent(r.symbol)}`}
                      className="cursor-pointer font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
                      title={r.name || r.symbol}
                    >
                      {r.symbol}
                    </Link>
                  </td>
                  <td className="px-3 py-1.5 text-right">{fmtPrice(r.price)}</td>
                  <td className="px-3 py-1.5 text-right" style={{ color }}>
                    {fmtPct(r.dayChangePct)}
                  </td>
                  <td
                    className="px-3 py-1.5 text-right"
                    style={{ color: r.mcap === null ? "var(--faint)" : "var(--dim)" }}
                    title={r.mcap === null ? `mcap unavailable — ${mcapNote}` : undefined}
                  >
                    {fmtMcap(r.mcap)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export default function MoversPanel({ limit = 8 }: { limit?: number }) {
  const [resp, setResp] = useState<MoversResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [minMcap, setMinMcap] = useState(0);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      movers(minMcap, limit)
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
    // POLL_SLOW tier — movers come from stored DAILY closes (managed loop:
    // hidden-tab pause, failure backoff).
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [minMcap, limit, retryTick]);

  const gainers = resp?.gainers ?? [];
  const losers = resp?.losers ?? [];

  return (
    <section className="panel" aria-label="top gainers and losers">
      <div className="panel-h flex-wrap gap-2">
        <span>MOVERS</span>
        <span
          className="text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          daily universe closes — not live
        </span>
      </div>

      {/* mcap filter chips */}
      <div className="flex flex-wrap items-center gap-1.5 px-3 py-2">
        <span className="text-[0.75rem] tracking-wider" style={{ color: "var(--faint)" }}>
          MCAP
        </span>
        {MCAP_FILTERS.map((f) => (
          <button
            key={f.label}
            type="button"
            aria-pressed={minMcap === f.min}
            onClick={() => setMinMcap(f.min)}
            className="chip min-h-[36px] cursor-pointer px-2 py-[2px] text-[0.75rem] transition-colors duration-150 hover:border-[var(--border-strong)] hover:text-[var(--text)]"
            style={
              minMcap === f.min
                ? { color: "var(--text)", borderColor: "var(--accent)" }
                : undefined
            }
          >
            {f.label}
          </button>
        ))}
      </div>

      {resp === null && !error && (
        <div className="p-3">
          <Skeleton lines={4} label="loading movers" />
        </div>
      )}
      {resp === null && error && (
        <div className="p-3">
          <ErrorState
            message={error}
            hint="Is the daemon running? Movers come from /api/movers."
            retry={() => {
              setError(null);
              setRetryTick((t) => t + 1);
            }}
          />
        </div>
      )}
      {resp !== null && gainers.length === 0 && losers.length === 0 && (
        <EmptyState
          message="No movers to show"
          detail={
            minMcap > 0
              ? "Nothing passes this mcap filter — EDGAR shares-outstanding coverage is best-effort, and unknown-mcap symbols are excluded (not guessed)."
              : "The daily universe has no fresh bars yet — the universe poller fills them on its own cadence."
          }
        />
      )}

      {resp !== null && (gainers.length > 0 || losers.length > 0) && (
        <div className="grid grid-cols-1 gap-2 border-t sm:grid-cols-2" style={{ borderColor: "var(--border)" }}>
          <MoverTable title="TOP GAINERS" rows={gainers} color="var(--bid)" mcapNote={resp.mcapNote} />
          <MoverTable title="TOP LOSERS" rows={losers} color="var(--ask)" mcapNote={resp.mcapNote} />
        </div>
      )}

      {resp !== null && (
        <p
          className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
          style={{ borderColor: "var(--border)", color: "var(--faint)" }}
        >
          {minMcap > 0 && resp.unknownMcapExcluded > 0 && (
            <>
              {resp.unknownMcapExcluded} symbol(s) excluded for UNKNOWN mcap (EDGAR gap, not
              guessed).{" "}
            </>
          )}
          {resp.note} {resp.mcapNote}
        </p>
      )}
    </section>
  );
}
