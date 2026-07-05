"use client";

// EARNINGS ESTIMATE CALENDAR (Signal8 wave, Stage 5): per universe symbol,
// the NEXT 10-Q/10-K due ESTIMATE — last periodic filing date + ~91 days.
// Backed by GET /api/earnings-est (filings-table cadence, amendments
// excluded), distinct from CalendarsCard's near-window "reports soon" strip:
// this is the full soonest-first list with the cadence anchor shown per row.
//
// HONESTY (non-negotiable): every row wears the EST chip and the API's
// "estimated from filing cadence — not a confirmed date" note is rendered
// verbatim. Overdue rows (estimate already passed) say so instead of
// pretending precision.

import Link from "next/link";
import { useEffect, useState } from "react";
import { earningsEst, type EarningsEstResponse } from "@/lib/api";
import { ago, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

const SHOW = 15;

export default function EarningsEstCard() {
  const [resp, setResp] = useState<EarningsEstResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      earningsEst(200)
        .then((r) => {
          if (!alive) return;
          setResp(r);
          setError(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          setError(
            msg.includes("404")
              ? "the running daemon predates GET /api/earnings-est — restart signaldeckd with the current binary and this card fills in"
              : msg,
          );
        });
    load();
    const t = setInterval(load, 5 * 60_000); // filing cadence — slow poll
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  const rows = resp?.rows ?? [];
  const shown = showAll ? rows : rows.slice(0, SHOW);

  return (
    <section className="panel" aria-label="estimated earnings calendar">
      <div className="panel-h flex-wrap gap-2">
        <span>EARNINGS — ESTIMATED FROM FILING CADENCE</span>
        <span
          className="chip px-1.5 py-0 text-[0.62rem] tracking-wider"
          style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
        >
          EST — not confirmed dates
        </span>
        {resp !== null && (
          <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
            {resp.total} symbols with a 10-Q/10-K anchor
          </span>
        )}
      </div>

      {resp === null && !error && (
        <div className="p-3">
          <Skeleton lines={4} label="loading earnings estimates" />
        </div>
      )}
      {resp === null && error && (
        <div className="p-3">
          <ErrorState
            message={error}
            retry={() => {
              setError(null);
              setRetryTick((t) => t + 1);
            }}
          />
        </div>
      )}

      {resp !== null && rows.length === 0 && (
        <EmptyState
          className="m-3"
          message="No periodic filings stored yet"
          detail="The filings-poller sweeps the universe ~2h; estimates appear once 10-Q/10-K history exists."
        />
      )}

      {resp !== null && rows.length > 0 && (
        <>
          <ul style={{ borderTop: "1px solid var(--border)" }}>
            {shown.map((r) => (
              <li
                key={r.symbol}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-3 py-1.5 text-[0.75rem]"
                style={{ borderBottom: "1px solid var(--border)" }}
              >
                <Link
                  href={`/s/stocks/${encodeURIComponent(r.symbol)}`}
                  className="cursor-pointer font-bold tracking-wide hover:text-[var(--accent)]"
                  title={r.name || r.symbol}
                >
                  {r.symbol}
                </Link>
                <span
                  className="chip px-1.5 py-0 text-[0.62rem] tracking-wider"
                  style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                  title={resp.note}
                >
                  EST
                </span>
                {r.overdue && (
                  <span
                    className="chip px-1.5 py-0 text-[0.62rem] tracking-wider"
                    style={{ color: "var(--ask)", borderColor: "var(--ask)" }}
                    title="The +91d estimate has already passed — the filer's cadence slipped (or it pre-announced); this is exactly why these are estimates."
                  >
                    overdue
                  </span>
                )}
                <span className="tnum ml-auto" style={{ color: "var(--dim)" }}>
                  ~{fmtDate(r.estTs)}
                </span>
                <span className="tnum text-[0.65rem]" style={{ color: "var(--faint)" }}>
                  last {r.lastForm} {ago(r.lastFiledTs)}
                </span>
              </li>
            ))}
          </ul>
          {rows.length > SHOW && (
            <button
              type="button"
              onClick={() => setShowAll((v) => !v)}
              className="chip m-3 min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:brightness-125"
              style={{ color: "var(--dim)" }}
            >
              {showAll ? "show fewer" : `show all ${rows.length}`}
            </button>
          )}
          <p className="px-3 pb-2 text-[0.65rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {resp.note}
          </p>
        </>
      )}
    </section>
  );
}
