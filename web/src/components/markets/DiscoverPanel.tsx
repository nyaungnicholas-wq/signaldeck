"use client";

// ── DISCOVER panel (universe-discovery wave) ─────────────────────────────
// Extracted verbatim from the screener page in the page split; the only
// behavior changes are the accessible HelpTip on the symbol budget (was a
// hover-only [title]), a visible at-cap note, and the POLL_SLOW tier
// (discovery sweeps every 6h — polling gently is honest).

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  api,
  pollMs,
  POLL_SLOW,
  candidates as fetchCandidates,
  addCandidate,
  monitorAllCandidates,
  dismissCandidate,
  type CandidatesResponse,
  type Market,
  isAuthError,
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
  const router = useRouter();
  const [data, setData] = useState<CandidatesResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [hidden, setHidden] = useState(false); // 401 → logged out → hide
  const [busy, setBusy] = useState<string | null>(null); // symbol being acted on
  const [busyAll, setBusyAll] = useState(false); // "monitor all" in flight
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null); // success note
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
          if (isAuthError(e)) {
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
    setActionMsg(null);
    fn()
      .then(() => fetchCandidates("new"))
      .then((r) => setData(r))
      .catch((e: unknown) => setActionErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(null));
  };

  // Monitor a candidate AND open its chart: subscribe/backfill it (so the chart
  // has data to draw), then navigate to its symbol page. This is what "Monitor"
  // means to the user — go look at the actual charts, not just silently add it.
  const monitorAndOpen = (symbol: string, market: Market) => {
    setBusy(symbol);
    setActionErr(null);
    setActionMsg(null);
    addCandidate(symbol, market)
      .then(() => router.push(`/s/${market}/${encodeURIComponent(symbol)}`))
      .catch((e: unknown) => {
        setActionErr(e instanceof Error ? e.message : String(e));
        setBusy(null); // only reset on error — on success we're navigating away
      });
  };

  const monitorAll = () => {
    setBusyAll(true);
    setActionErr(null);
    setActionMsg(null);
    monitorAllCandidates()
      .then((r) => {
        setActionMsg(
          `Monitoring ${r.added} symbol${r.added === 1 ? "" : "s"}${r.skipped > 0 ? ` (${r.skipped} skipped)` : ""}.${r.note ? " " + r.note : ""}`,
        );
        return fetchCandidates("new");
      })
      .then((r) => setData(r))
      .catch((e: unknown) => setActionErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusyAll(false));
  };

  if (hidden) return null;

  const loading = data === null && err === null;
  // The STREAM cap (small, free-ws) is full — new symbols are MONITORED via
  // polling rather than streamed. The MONITOR cap (universe) is the real ceiling.
  const streamCap = data?.streamCap ?? data?.cap ?? 0;
  const atStreamCap = data !== null && data.active >= streamCap;
  const monitorCap = data?.monitorCap ?? 0;
  const atMonitorCap = data !== null && monitorCap > 0 && data.monitored >= monitorCap;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        DISCOVER
        {data !== null && (
          <span className="inline-flex items-center gap-2">
            <span
              className="tnum"
              style={{ color: atStreamCap ? "var(--warn)" : "var(--faint)" }}
              title="live-websocket hot set — real-time tick (free-tier limit)"
            >
              {data.active}/{streamCap} streaming
            </span>
            <span
              className="tnum"
              style={{ color: atMonitorCap ? "var(--bad)" : "var(--faint)" }}
              title="total monitored: streamed + polled universe (scored & predicted, minute bars)"
            >
              {data.monitored ?? data.active}/{monitorCap || "∞"} monitored
            </span>
            <HelpTip label="About the symbol budgets">
              Two budgets. <b>Streaming</b> is the small real-time hot set bounded by Alpaca&apos;s
              free websocket limit. <b>Monitored</b> is every tracked symbol — streamed PLUS the
              broad polled universe (minute bars, fully scored &amp; predicted, just not real-time
              tick). When the stream slots are full, adding a symbol monitors it via polling, so you
              can monitor far more than you can stream.
            </HelpTip>
          </span>
        )}
        {data !== null && data.candidates.length > 0 && (
          <button
            type="button"
            disabled={busyAll || busy !== null}
            onClick={monitorAll}
            title="Monitor every new candidate below (adds each to your watchlist; streamed if a live slot is free, otherwise polled)"
            className="chip ml-auto min-h-[36px] cursor-pointer px-3 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
          >
            {busyAll ? "monitoring…" : `Monitor all ${data.candidates.length}`}
          </button>
        )}
      </div>

      <p className="px-4 pt-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        Candidates from Alpaca&apos;s most-actives / movers screeners (swept every 6h). While
        under the stream cap, symbols seen in ≥2 sweeps are auto-added by dollar volume
        {data !== null && (
          <> — max {data.autoAddDailyLimit}/day, {data.autoAddsToday} used today</>
        )}
        . Beyond the stream cap, symbols are still monitored via minute polling.
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

      {actionMsg !== null && (
        <p className="px-4 pb-1 text-[0.75rem]" style={{ color: "var(--ok)" }} role="status">
          {actionMsg}
        </p>
      )}

      {data !== null && atStreamCap && !atMonitorCap && (
        <p className="px-4 pb-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          Streaming slots full ({data.active}/{streamCap}) — new symbols are still fully monitored
          via minute polling (scored &amp; predicted, just not real-time tick).
        </p>
      )}

      {data !== null && atMonitorCap && (
        <p className="px-4 pb-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          Monitor cap reached ({data.monitored}/{monitorCap}) — dismiss a symbol or raise
          SIGNALDECK_UNIVERSE_CAP to monitor more.
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
                  <td className="px-3 py-2 font-bold">
                    <Link
                      href={`/s/${c.market}/${encodeURIComponent(c.symbol)}`}
                      title={`Open ${c.symbol} charts`}
                      className="cursor-pointer transition-colors duration-150 hover:text-[var(--accent)]"
                    >
                      {c.symbol}
                    </Link>
                  </td>
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
                        disabled={busy !== null || busyAll}
                        title={`Monitor ${c.symbol} — subscribe it (streamed if a live slot is free, otherwise polled) and open its charts`}
                        onClick={() => monitorAndOpen(c.symbol, c.market)}
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
