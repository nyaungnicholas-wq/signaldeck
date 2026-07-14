"use client";

// COMPOSITE LEADERBOARD — the fleet ranked by SignalScore (api.compositeTop).
// Rank, deep-linked symbol chip, decile-colored score badge, rank-change
// arrow (null = "new", honest absence — never a fabricated 0), edge in pp,
// and the 30d sparkline joined client-side from the public screener payload.
// HONESTY FOOTER: minCurveN gate + rankNote + curveNote + edgeNote +
// trackLabel render verbatim under the rows. Table in PRO, card grid in
// SIMPLE (same pattern as the screener) — a click on either aims the hero.

import Link from "next/link";
import { useEffect, useState } from "react";
import {
  compositeTop,
  pollMs,
  POLL_DEFAULT,
  screenerRows,
  type CompositeTop,
  type CompositeTopRow,
  type Market,
} from "@/lib/api";
import { ago } from "@/lib/format";
import { useViewMode } from "@/components/Plain";
import Sparkline from "@/components/viz/Sparkline";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { edgePp, scoreColor } from "./compositeUi";

type View = "cards" | "table";

/** Rows shown before the "show all" expander is clicked. */
const COLLAPSED_COUNT = 50;
/** API max — fetch the whole scored fleet once, expand client-side. */
const FETCH_LIMIT = 500;

/** Rank movement vs the previous pass: ▲/▼ with the step count, "new" when
 *  the symbol wasn't in the previous ranking (null — honest absence). */
function RankDelta({ row }: { row: CompositeTopRow }) {
  if (row.rankChange === null) {
    return (
      <span
        className="chip"
        style={{ color: "var(--crossed)", borderColor: "var(--crossed)" }}
        title="not in the previous pass — no change to report"
      >
        new
      </span>
    );
  }
  if (row.rankChange === 0) {
    return (
      <span className="tnum" style={{ color: "var(--faint)" }} title={`prev #${row.prevRank}`}>
        —
      </span>
    );
  }
  const up = row.rankChange > 0;
  return (
    <span
      className="tnum"
      style={{ color: up ? "var(--bid)" : "var(--ask)" }}
      title={`prev #${row.prevRank}`}
    >
      {up ? "▲" : "▼"}
      {Math.abs(row.rankChange)}
    </span>
  );
}

function ScoreBadge({ score }: { score: number }) {
  const c = scoreColor(score);
  return (
    <span
      className="chip tnum font-bold"
      style={{ color: c, borderColor: c }}
      title="1–10 forced-curve rank on today's cross-section — not a probability"
    >
      {score}
    </span>
  );
}

export default function CompositeLeaderboard({
  onPick,
}: {
  onPick: (symbol: string, market: Market) => void;
}) {
  const [resp, setResp] = useState<CompositeTop | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [sparks, setSparks] = useState<Map<string, number[]>>(new Map());
  const [retryTick, setRetryTick] = useState(0);
  // View follows the SIMPLE/PRO toggle until the user pins a choice.
  const [view, setView] = useState<View | null>(null);
  const [expanded, setExpanded] = useState(false);
  const mode = useViewMode();
  const effView: View = view ?? (mode === "simple" ? "cards" : "table");

  useEffect(() => {
    let alive = true;
    const load = () =>
      compositeTop(FETCH_LIMIT)
        .then((r) => {
          if (!alive) return;
          setResp(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // POLL_DEFAULT tier — the scorer passes are minutes apart, not seconds.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  // 30d closes from the public screener payload (fetched gently — it is the
  // whole universe). Failure is soft: rows render without sparklines.
  useEffect(() => {
    let alive = true;
    const load = () =>
      screenerRows()
        .then((rows) => {
          if (!alive) return;
          const m = new Map<string, number[]>();
          for (const r of rows) m.set(`${r.market}:${r.symbol}`, r.spark ?? []);
          setSparks(m);
        })
        .catch(() => {});
    load();
    // Slower than POLL_SLOW on purpose: the payload is the whole universe
    // and the sparklines are daily closes.
    const stop = pollMs(load, 300_000);
    return () => {
      alive = false;
      stop();
    };
  }, []);

  const allRows = resp?.rows ?? [];
  const rows = expanded ? allRows : allRows.slice(0, COLLAPSED_COUNT);
  const total = resp?.total ?? 0;
  const notDeployed = err !== null && err.includes("404");

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        {mode === "simple" ? "TOP-RANKED SYMBOLS" : "LEADERBOARD — COMPOSITE SCORE"}
        {resp !== null && (
          <>
            <span className="chip tnum">{resp.horizon}</span>
            <span className="chip tnum" title="rows shown of symbols scored in the latest pass">
              {rows.length} of {resp.total} scored
            </span>
          </>
        )}
        {/* cards ↔ table toggle — default follows SIMPLE/PRO, a click pins */}
        <span className="flex items-center gap-1" role="tablist" aria-label="leaderboard view">
          {(["cards", "table"] as View[]).map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={effView === v}
              onClick={() => setView(v)}
              className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:brightness-125"
              style={{
                color: effView === v ? "var(--accent)" : "var(--dim)",
                borderColor: effView === v ? "var(--accent)" : "var(--border)",
              }}
            >
              {v}
            </button>
          ))}
        </span>
        {resp !== null && (
          <span
            className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--warn)" }}
          >
            {resp.trackLabel}
          </span>
        )}
      </div>

      {resp === null && err === null && (
        <div className="p-4">
          <Skeleton lines={5} label="loading the composite leaderboard" />
        </div>
      )}

      {resp === null && err !== null && (
        <ErrorState
          className="m-4"
          message={notDeployed ? "composite leaderboard not available on this daemon build" : err}
          hint={
            notDeployed
              ? "The running daemon predates GET /api/composite/top — restart it with the current binary and this ranking fills in. Nothing is faked meanwhile."
              : "Is the daemon running? Start signaldeckd and the leaderboard will recover."
          }
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {resp !== null && rows.length === 0 && (
        <EmptyState
          className="m-4"
          message="No composite scores stored yet"
          detail={`The composite-scorer stores nothing until ${resp.minCurveN}+ symbols carry fresh predictions (the forced-curve gate) — an empty board is honest absence, not zeros.`}
        />
      )}

      {/* CARD GRID (simple default) */}
      {rows.length > 0 && effView === "cards" && (
        <div className="grid grid-cols-1 gap-2 px-4 py-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {rows.map((r) => (
            <button
              key={`${r.market}:${r.symbol}`}
              type="button"
              onClick={() => onPick(r.symbol, r.market)}
              title={`aim the SignalScore card at ${r.symbol}`}
              className="flex cursor-pointer flex-col gap-2 rounded-lg border border-[var(--border)] p-3 text-left transition-colors duration-150 hover:border-[var(--accent)]"
              style={{ background: "var(--panel2)" }}
            >
              <div className="flex items-center gap-2">
                <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  #{r.rank}
                </span>
                <Link
                  href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                  onClick={(e) => e.stopPropagation()}
                  className="mono font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                >
                  {r.symbol}
                </Link>
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  {r.market}
                </span>
                <span className="ml-auto">
                  <RankDelta row={r} />
                </span>
              </div>
              <div className="flex items-baseline gap-2">
                <span
                  className="tnum text-[1.6rem] font-extrabold leading-none"
                  style={{ color: scoreColor(r.score) }}
                >
                  {r.score}
                </span>
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  /10
                </span>
                <span
                  className="tnum ml-auto text-[0.75rem]"
                  style={{ color: r.edge >= 0 ? "var(--bid)" : "var(--ask)" }}
                  title="calibrated P(up,1d) − 50%, in percentage points"
                >
                  {edgePp(r.edge)}
                </span>
              </div>
              <Sparkline closes={sparks.get(`${r.market}:${r.symbol}`) ?? []} width={200} height={28} />
            </button>
          ))}
        </div>
      )}

      {/* TABLE (pro default) */}
      {rows.length > 0 && effView === "table" && (
        <div className="table-wrap">
          <table className="w-full text-[0.75rem]">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)" }}>
                {[
                  { k: "rank", l: "#", right: false },
                  { k: "delta", l: "Δ RANK", right: false, tip: "vs each symbol's newest row before today — null renders as new" },
                  { k: "symbol", l: "SYMBOL", right: false },
                  { k: "market", l: "MARKET", right: false },
                  { k: "score", l: "SCORE", right: false, tip: "1–10 forced-curve rank — not a probability" },
                  { k: "edge", l: mode === "simple" ? "EDGE VS COIN FLIP" : "EDGE (1d)", right: true, tip: "calibrated P(up,1d) − 50%, in percentage points" },
                  { k: "trend", l: mode === "simple" ? "LAST 30 DAYS" : "TREND 30D", right: false },
                  { k: "scored", l: "SCORED", right: true },
                ].map((h) => (
                  <th
                    key={h.k}
                    scope="col"
                    className={`px-3 py-2 text-[0.75rem] font-medium tracking-wide ${h.right ? "text-right" : "text-left"}`}
                    style={{ color: "var(--faint)" }}
                    title={h.tip}
                  >
                    {h.l}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="tnum">
              {rows.map((r) => (
                <tr
                  key={`${r.market}:${r.symbol}`}
                  className="cursor-pointer transition-colors duration-150 hover:bg-[var(--panel2)]"
                  onClick={() => onPick(r.symbol, r.market)}
                  title={`aim the SignalScore card at ${r.symbol}`}
                >
                  <td className="px-3 py-2" style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                    {r.rank}
                  </td>
                  <td className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
                    <RankDelta row={r} />
                  </td>
                  <td className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
                    <Link
                      href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                      onClick={(e) => e.stopPropagation()}
                      className="mono font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                    >
                      {r.symbol}
                    </Link>
                  </td>
                  <td className="px-3 py-2" style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}>
                    {r.market}
                  </td>
                  <td className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
                    <ScoreBadge score={r.score} />
                  </td>
                  <td
                    className="px-3 py-2 text-right"
                    style={{ color: r.edge >= 0 ? "var(--bid)" : "var(--ask)", borderBottom: "1px solid var(--border)" }}
                  >
                    {edgePp(r.edge)}
                  </td>
                  <td className="px-3 py-1" style={{ borderBottom: "1px solid var(--border)" }}>
                    <Sparkline closes={sparks.get(`${r.market}:${r.symbol}`) ?? []} width={96} height={26} area={false} />
                  </td>
                  <td className="px-3 py-2 text-right" style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                    {ago(r.ts)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* expander — the rest of the scored fleet, already fetched */}
      {allRows.length > COLLAPSED_COUNT && (
        <div className="flex justify-center px-4 pb-3">
          <button
            type="button"
            onClick={() => setExpanded((e) => !e)}
            className="chip min-h-[36px] cursor-pointer px-4 transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
          >
            {expanded
              ? `collapse to top ${COLLAPSED_COUNT}`
              : `show all ${allRows.length} scored`}
          </button>
        </div>
      )}

      {/* honesty footer — the gates and notes, verbatim and always visible */}
      {resp !== null && (
        <div className="flex flex-col gap-1 px-4 py-2.5" style={{ borderTop: "1px solid var(--border)" }}>
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            showing {rows.length} of {total} scored
          </p>
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {resp.rankNote}
          </p>
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {resp.curveNote}
          </p>
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {resp.edgeNote} · forced curve requires ≥{resp.minCurveN} usable predictions per pass ·{" "}
            {resp.trackLabel}
          </p>
        </div>
      )}
    </section>
  );
}
