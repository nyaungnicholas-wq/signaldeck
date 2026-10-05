"use client";

// SHORT INTEREST (why-it's-moving wave) — the REAL FINRA bi-monthly short
// interest: shares actually reported short at a settlement date, with
// days-to-cover against average daily volume.
//
// This is the SIBLING of ShortVolumePanel and the two are routinely confused,
// so the distinction is stated on the panel itself rather than left implied:
//
//   SHORT INTEREST (here)  — reported SHARES HELD short on a settlement date,
//                            published ~2 weeks lagged, twice a month.
//   SHORT VOLUME (sibling) — the share of one DAY's trading that was sold
//                            short, includes market-maker activity, daily.
//
// A high number in either is descriptive positioning, not a bearish signal.
// The daemon's note ships verbatim.

import { useEffect, useState } from "react";
import { pollMs, POLL_SLOW, shortInterest, type ShortInterestResponse } from "@/lib/api";
import HelpTip from "@/components/HelpTip";

function fmtQty(v: number): string {
  if (!isFinite(v)) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

export default function ShortInterestPanel({ symbol }: { symbol: string }) {
  // Keyed by symbol so a symbol switch shows a loading state without a bare
  // setState inside the effect body (the page's own convention).
  const [state, setState] = useState<{ key: string; r: ShortInterestResponse } | null>(null);
  const [errState, setErrState] = useState<{ key: string; msg: string } | null>(null);
  const data = state && state.key === symbol ? state.r : null;
  const err = errState && errState.key === symbol ? errState.msg : null;

  useEffect(() => {
    let alive = true;
    const load = () =>
      shortInterest(symbol, "stocks")
        .then((r) => {
          if (!alive) return;
          setState({ key: symbol, r });
          setErrState(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErrState({ key: symbol, msg: e instanceof Error ? e.message : String(e) });
        });
    load();
    // POLL_SLOW: two publications a month — there is nothing to poll fast for.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol]);

  const latest = data?.latest ?? null;
  const previous = data?.previous ?? null;
  const recent = data?.recent ?? [];

  return (
    <section className="panel" aria-label={`FINRA bi-monthly short interest for ${symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        SHORT INTEREST · {symbol}
        <HelpTip label="short INTEREST vs short VOLUME">
          Short interest is the number of shares reported held short at a
          settlement date, published about two weeks later, twice a month. Short
          volume (the panel next to this one) is the share of a single day&rsquo;s
          trading that was sold short and includes market-maker activity. They
          answer different questions and neither one is bearish by itself.
        </HelpTip>
        <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
          FINRA bi-monthly (free) · ~2wk publication lag
          {latest !== null ? ` · settled ${latest.settlementDate}` : ""}
        </span>
      </div>

      {err !== null && data === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {data === null && err === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading FINRA short interest…
        </p>
      )}

      {data !== null && latest === null && (
        <p className="px-4 py-4 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {data.emptyNote ??
            "no short interest stored yet — the finra-shortint worker probes the newest bi-monthly file each run. Honest absence, not an error."}
        </p>
      )}

      {data !== null && latest !== null && (
        <div className="flex flex-wrap items-center gap-4 px-4 py-3">
          <div>
            <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              DAYS TO COVER
            </div>
            <div className="tnum text-lg font-bold">{latest.daysToCover.toFixed(2)}</div>
          </div>
          <div>
            <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              SHARES SHORT
            </div>
            <div className="tnum text-[0.85rem]">{fmtQty(latest.shortQty)}</div>
          </div>
          <div>
            <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              CHANGE VS PRIOR PERIOD
            </div>
            {/* Direction-neutral color: a rise in short interest is not bearish. */}
            <div className="tnum text-[0.85rem]" style={{ color: "var(--text)" }}>
              {latest.changePct >= 0 ? "+" : ""}
              {latest.changePct.toFixed(2)}%
              {previous !== null && (
                <span className="ml-1.5" style={{ color: "var(--faint)" }}>
                  from {fmtQty(previous.shortQty)} ({previous.settlementDate})
                </span>
              )}
            </div>
          </div>
          <div>
            <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              AVG DAILY VOLUME
            </div>
            <div className="tnum text-[0.85rem]">{fmtQty(latest.adv)}</div>
          </div>
          <div>
            <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              PERIODS STORED
            </div>
            <div className="tnum text-[0.85rem]">{recent.length}</div>
          </div>
        </div>
      )}

      {/* the daemon's note — verbatim, always rendered */}
      <p className="px-4 pb-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
        {data?.note ??
          "bi-monthly FINRA short interest (settlement-dated, published ~2wks lagged) — descriptive positioning, not advice"}
      </p>
    </section>
  );
}
