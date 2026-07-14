"use client";

// SCREENER results panel, extracted from the page split — the cards ↔ table ↔
// heatmap view toggle (all three render the SAME filtered rows; nothing is
// lost by switching), plus the loading/error/empty states. The load-bearing
// column explanations that used to live only in hover [title]s are surfaced
// here: a HelpTip covers how to read the columns, and the calibration caveat
// is a visible caption in every view and both SIMPLE/PRO modes.

import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";
import Heatmap, { type HeatmapItem } from "@/components/viz/Heatmap";
import ScreenerCards from "@/components/markets/ScreenerCards";
import ScreenerTable from "@/components/markets/ScreenerTable";
import type { Derived, SortDir, SortKey, View } from "@/components/markets/screenerModel";
import type { Horizon, WatchRow } from "@/lib/api";

export default function ScreenerResults({
  rows,
  err,
  filtered,
  horizon,
  effView,
  onView,
  sortKey,
  sortDir,
  onSort,
  onRetry,
}: {
  rows: WatchRow[] | null;
  err: string | null;
  filtered: Derived[];
  horizon: Horizon;
  effView: View;
  onView: (v: View) => void;
  sortKey: SortKey;
  sortDir: SortDir;
  onSort: (key: SortKey, numericDefaultDesc: boolean) => void;
  onRetry: () => void;
}) {
  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        RESULTS
        <HelpTip label="How to read these columns">
          SCORE is the pressure score, −1 (sell pressure) to +1 (buy pressure). VERDICT is the
          calibrated 1d P(up) — near-50% reads NO CLEAR LEAN, and a symbol with no stored
          prediction reads NO READ YET (never a fabricated lean). RANK is the cross-sectional
          relative-strength rank (1 = strongest). REGIME is the current detected state,
          described from stored bars with no lookahead. “—” always means the data doesn&apos;t
          exist yet.
        </HelpTip>
        {/* Stage 4 (tables→charts): cards ↔ table ↔ heatmap view toggle —
            all three render the SAME filtered rows; nothing is lost by
            switching. Default: cards in simple mode, table in pro. */}
        <span className="flex items-center gap-1" role="tablist" aria-label="results view">
          {(["cards", "table", "heatmap"] as View[]).map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={effView === v}
              onClick={() => onView(v)}
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
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          {effView === "table"
            ? `score horizon = ${horizon} · sorted by ${sortKey} ${sortDir}`
            : effView === "cards"
              ? `verdict per symbol (1d) · sorted by ${sortKey} ${sortDir} · same rows as the table`
              : "color = day % change vs previous stored close"}
        </span>
      </div>

      {/* the calibration caveat stays visible in every view and both modes */}
      <p className="px-4 pt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Verdicts come from backtested calibration — not a live track record, and never financial
        advice.
      </p>

      {loading && <Skeleton lines={4} label="loading screener" className="m-4" />}

      {hardError && (
        <ErrorState
          message={err ?? "request failed"}
          hint="Is the daemon running? Start it with signaldeckd and this page will recover."
          retry={onRetry}
          className="m-4"
        />
      )}

      {rows !== null && rows.length === 0 && (
        <EmptyState
          message="No symbols tracked yet"
          detail="The screener lists the whole tracked universe. Subscribe to symbols (or let discovery auto-add them) and rows appear as bars and scores arrive."
        />
      )}

      {rows !== null && rows.length > 0 && filtered.length === 0 && (
        <EmptyState
          message="No symbols match the current filters"
          detail="Lower the min score, widen direction/market, or clear the search."
        />
      )}

      {effView === "cards" && filtered.length > 0 && <ScreenerCards filtered={filtered} />}

      {/* Stage 5: HEATMAP view — same filtered set as color tiles. Rows
          without any stored daily bar are OMITTED and counted (a tile needs
          a real close; nothing is faked to fill the grid). mcap is not part
          of the screener payload, so tiles are uniform and say so. */}
      {effView === "heatmap" && filtered.length > 0 && (
        <div className="px-4 py-3">
          <Heatmap
            items={filtered
              .filter((d) => d.row.latestBarTs > 0)
              .map<HeatmapItem>((d) => ({
                symbol: d.row.symbol,
                changePct: isFinite(d.row.dayChangePct) ? d.row.dayChangePct : 0,
                mcap: null,
                name: d.row.name,
                market: d.row.market,
              }))}
          />
          {filtered.some((d) => d.row.latestBarTs === 0) && (
            <p className="mt-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {filtered.filter((d) => d.row.latestBarTs === 0).length} filtered symbols have no
              stored daily bar yet and are omitted from the grid — bars accrue on worker cadence.
            </p>
          )}
        </div>
      )}

      {effView === "table" && filtered.length > 0 && (
        <ScreenerTable filtered={filtered} sortKey={sortKey} sortDir={sortDir} onSort={onSort} />
      )}
    </section>
  );
}
