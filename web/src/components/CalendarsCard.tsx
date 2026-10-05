"use client";

// CALENDARS (Signal8 wave, Stage 4): the honest free-data calendar card.
//
//   - ECON: the LATEST OBSERVED prints of the tracked FRED series (VIX, 10y,
//     10y-2y spread, fed funds). NOT a forward release calendar — the keyless
//     FRED endpoint has no release-date feed, and no paid econ feed is wired.
//     The API's subset note is rendered verbatim.
//   - EARNINGS (EST): "reports soon" rows derived from each company's last
//     SEC filing date + ~91 days. EVERY row is an estimate and wears the EST
//     chip; the heuristic note is rendered verbatim.
//   - IPO calendar: DELIBERATELY ABSENT. There is no free, redistributable
//     source of IPO pricing/listing dates (EDGAR S-1s show intent to list,
//     not when or at what price). Per the honesty doctrine we omit the
//     surface entirely rather than fake one from scraped or guessed data.

import Link from "next/link";
import { useEffect, useState } from "react";
import { calendar, pollMs, POLL_SLOW, type CalendarResponse } from "@/lib/api";
import { ago, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

function fmtEconValue(series: string, v: number): string {
  if (!isFinite(v)) return "—";
  // Yields/spreads/rates are percentages; VIX is an index level.
  if (series === "VIXCLS") return v.toFixed(2);
  return `${v.toFixed(2)}%`;
}

export default function CalendarsCard() {
  const [resp, setResp] = useState<CalendarResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      calendar()
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
    const stop = pollMs(load, POLL_SLOW); // calendars move slowly — slow tier
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const econ = resp?.econ ?? [];
  const ests = resp?.earningsEst ?? [];

  return (
    <section className="panel" aria-label="economic and earnings calendars">
      <div className="panel-h flex-wrap gap-2">
        <span>CALENDARS</span>
        <span
          className="text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          free data only — see notes
        </span>
      </div>

      {resp === null && !error && (
        <div className="p-3">
          <Skeleton lines={4} label="loading calendars" />
        </div>
      )}
      {resp === null && error && (
        <div className="p-3">
          <ErrorState
            message={error}
            hint="Is the daemon running? Calendars come from /api/calendar."
            retry={() => {
              setError(null);
              setRetryTick((t) => t + 1);
            }}
          />
        </div>
      )}

      {resp !== null && (
        <>
          {/* ECON — latest FRED prints */}
          <div className="border-t" style={{ borderColor: "var(--border)" }}>
            <div className="px-3 pt-2 text-[0.75rem] font-bold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
              ECON — LATEST FRED PRINTS
            </div>
            {econ.length === 0 ? (
              <p className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                No FRED prints stored yet — the fred-poller fills these on its 6h cadence.
              </p>
            ) : (
              <div className="flex flex-col px-1 py-1">
                {econ.map((e) => (
                  <div
                    key={e.series}
                    className="flex items-baseline gap-2 px-2 py-1 text-[0.75rem]"
                  >
                    <span style={{ color: "var(--dim)" }}>{e.label}</span>
                    <span className="tnum ml-auto font-bold">{fmtEconValue(e.series, e.value)}</span>
                    <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      {fmtDate(e.ts)}
                    </span>
                  </div>
                ))}
              </div>
            )}
            <p className="px-3 pb-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              {resp.econNote}
            </p>
          </div>

          {/* EARNINGS — filing-derived ESTIMATES only */}
          <div className="border-t" style={{ borderColor: "var(--border)" }}>
            <div className="px-3 pt-2 text-[0.75rem] font-bold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
              EARNINGS — REPORTS SOON (EST)
            </div>
            {ests.length === 0 ? (
              <p className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                No estimated reports in the window — the heuristic needs EDGAR filing dates
                (swept daily) and only shows companies due within ~3 weeks.
              </p>
            ) : (
              <div className="flex flex-col px-1 py-1">
                {ests.map((e) => (
                  <div
                    key={e.symbol}
                    className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-2 py-1 text-[0.75rem]"
                  >
                    <Link
                      href={`/s/stocks/${encodeURIComponent(e.symbol)}`}
                      className="mono cursor-pointer font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
                      title={e.name || e.symbol}
                    >
                      {e.symbol}
                    </Link>
                    <span
                      className="chip px-1.5 py-0 text-[0.75rem] tracking-wider"
                      style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                      title={resp.earningsNote}
                    >
                      EST
                    </span>
                    <span className="tnum ml-auto" style={{ color: "var(--dim)" }}>
                      ~{fmtDate(e.estTs)}
                    </span>
                    <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      last filed {ago(e.lastFilingTs)}
                    </span>
                  </div>
                ))}
              </div>
            )}
            <p className="px-3 pb-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              {resp.earningsNote}
            </p>
          </div>

          {/* No IPO section — see the header comment: no free source exists,
              so per the honesty doctrine none is fabricated. */}
        </>
      )}
    </section>
  );
}
