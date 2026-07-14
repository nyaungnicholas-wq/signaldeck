"use client";

// ── DISCOVER panel (universe-discovery wave) ─────────────────────────────
// Extracted verbatim from the screener page in the page split; the only
// behavior changes are the accessible HelpTip on the symbol budget (was a
// hover-only [title]), a visible at-cap note, and the POLL_SLOW tier
// (discovery sweeps every 6h — polling gently is honest).

import { useEffect, useState } from "react";
import {
  api,
  pollMs,
  POLL_SLOW,
  candidates as fetchCandidates,
  addCandidate,
  dismissCandidate,
  type CandidatesResponse,
  type Market,
} from "@/lib/api";
import { fmtPct, scoreColor } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";

function fmtDollarVol(v: number): string {
  if (!isFinite(v) || v <= 0) return "—";
  if (v >= 1e12) return `$${(v / 1e12).toFixed(1)}T`;
  if (v >= 1e9) return `$${(v / 1e9).toFixed(1)}B`;
  if (v >= 1e6) return `$${(v / 1e6).toFixed(0)}M`;
  if (v >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

/**
 * Candidate symbols found by the universe-discovery worker, with one-click
 * Monitor / Dismiss. Hidden entirely when logged out (the mutations are
 * session-scoped, so an anonymous panel would be all dead buttons).
 */
export default function DiscoverPanel() {
  const [data, setData] = useState<CandidatesResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [hidden, setHidden] = useState(false); // 401 → logged out → hide
  const [busy, setBusy] = useState<string | null>(null); // symbol being acted on
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () => {
      // Session check first: the panel is user-scoped by design.
      api
        .me()
        .then(() => fetchCandidates("new"))
        .then((r) => {
          if (!alive) return;
          setData(r);
          setErr(null);
          setHidden(false);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (msg.includes("401")) {
            setHidden(true); // not signed in — hide, don't error
            return;
          }
          setErr(msg);
        });
    };
    load();
    const stop = pollMs(load, POLL_SLOW); // discovery moves slowly; poll gently
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const act = (symbol: string, market: Market, fn: () => Promise<unknown>) => {
    setBusy(symbol);
    setActionErr(null);
    fn()
      .then(() => fetchCandidates("new"))
      .then((r) => setData(r))
      .catch((e: unknown) => setActionErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(null));
  };

  if (hidden) return null;

  const loading = data === null && err === null;
  const atCap = data !== null && data.active >= data.cap;

  return (
    <section className="panel">
      <div className="panel-h">
        DISCOVER
        {data !== null && (
          <span className="inline-flex items-center gap-1">
            <span className="tnum" style={{ color: atCap ? "var(--bad)" : "var(--faint)" }}>
              {data.active}/{data.cap} symbols active
            </span>
            <HelpTip label="About the symbol budget">
              Currently active symbols vs the SIGNALDECK_SYMBOL_CAP budget. At the cap, no new
              candidate can be monitored until one is dismissed or unsubscribed.
            </HelpTip>
          </span>
        )}
      </div>

      <p className="px-4 pt-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        Candidates from Alpaca&apos;s most-actives / movers screeners (swept every 6h). While
        under the cap, symbols seen in ≥2 sweeps are auto-added by dollar volume
        {data !== null && (
          <> — max {data.autoAddDailyLimit}/day, {data.autoAddsToday} used today</>
        )}
        .
      </p>

      {loading && <Skeleton lines={3} label="loading candidates" className="m-4" />}

      {err !== null && data === null && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Candidate discovery lives in the daemon."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
          className="m-4"
        />
      )}

      {data !== null && data.candidates.length === 0 && (
        <EmptyState
          message="No new candidates right now"
          detail="The universe-discovery worker sweeps Alpaca's screeners every 6 hours; anything not already tracked shows up here."
        />
      )}

      {actionErr !== null && (
        <p className="px-4 pb-1 text-[0.75rem]" style={{ color: "var(--bad)" }} role="alert">
          {actionErr}
        </p>
      )}

      {data !== null && atCap && (
        <p className="px-4 pb-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          Symbol cap reached ({data.active}/{data.cap}) — dismiss a candidate or unsubscribe a
          symbol to make room before monitoring another.
        </p>
      )}

      {data !== null && data.candidates.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-[0.75rem]">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)" }}>
                {["SYMBOL", "$ VOL", "% CHG", "SEEN", ""].map((h, i) => (
                  <th
                    key={h || "actions"}
                    scope="col"
                    className={`px-3 py-2 text-[0.75rem] font-medium tracking-wide ${
                      i >= 1 && i <= 3 ? "text-right" : "text-left"
                    }`}
                    style={{ color: "var(--faint)" }}
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="tnum">
              {data.candidates.map((c) => (
                <tr
                  key={`${c.market}:${c.symbol}`}
                  className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                  style={{ borderBottom: "1px solid var(--border)" }}
                >
                  <td className="px-3 py-2 font-bold">{c.symbol}</td>
                  <td className="px-3 py-2 text-right">{fmtDollarVol(c.dollarVol)}</td>
                  <td className="px-3 py-2 text-right" style={{ color: scoreColor(c.pctChange) }}>
                    {c.pctChange === 0 ? "—" : fmtPct(c.pctChange)}
                  </td>
                  <td
                    className="px-3 py-2 text-right"
                    title={`Seen in ${c.seenCount} discovery sweep${c.seenCount === 1 ? "" : "s"}`}
                  >
                    {c.seenCount}×
                  </td>
                  <td className="px-3 py-2">
                    <div className="flex justify-end gap-2">
                      <button
                        type="button"
                        disabled={busy !== null || atCap}
                        title={`Monitor ${c.symbol} — subscribe it and add it to your watchlist`}
                        onClick={() => act(c.symbol, c.market, () => addCandidate(c.symbol, c.market))}
                        className="chip min-h-[44px] min-w-[44px] cursor-pointer px-4 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                        style={{ color: "var(--ok)", borderColor: "var(--ok)" }}
                      >
                        {busy === c.symbol ? "…" : "Monitor"}
                      </button>
                      <button
                        type="button"
                        disabled={busy !== null}
                        title={`Hide ${c.symbol} from future discovery sweeps`}
                        onClick={() =>
                          act(c.symbol, c.market, () => dismissCandidate(c.symbol, c.market))
                        }
                        className="chip min-h-[44px] min-w-[44px] cursor-pointer px-4 transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                        style={{ color: "var(--dim)", borderColor: "var(--border)" }}
                      >
                        {busy === c.symbol ? "…" : "Dismiss"}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
