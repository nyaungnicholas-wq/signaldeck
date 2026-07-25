"use client";

// SCREENER — thin composition page (page-split refactor). Data fetching lives
// in hooks/useScreenerData (POLL_DEFAULT tier), filter/sort/view state in
// hooks/useScreenerFilters, and the panels in components/markets/*. Behavior
// is identical to the old single-file page, plus a SavedViewsBar that
// remembers filter/sort state on this device.

import { API_BASE } from "@/lib/api";
import PagePurpose from "@/components/PagePurpose";
import ExportMenu from "@/components/ExportMenu";
import MoversPanel from "@/components/MoversPanel";
import SavedViewsBar from "@/components/SavedViewsBar";
import DiscoverPanel from "@/components/markets/DiscoverPanel";
import ScreenerFilters from "@/components/markets/ScreenerFilters";
import ScreenerResults from "@/components/markets/ScreenerResults";
import { useScreenerData } from "@/hooks/useScreenerData";
import { useScreenerFilters } from "@/hooks/useScreenerFilters";

// Built-in starting points for the SavedViewsBar. Only states the existing
// filters can express honestly: there is no "my watchlist" preset because the
// screener ranks the whole tracked universe and has no per-user watchlist
// filter, and no "anomalies" preset because anomaly detection lives on
// /signals/unusual — a day-% sort is movers, so it is named as movers.
const PRESETS = [
  {
    name: "Today's big movers",
    state: {
      horizon: "1d",
      minScore: -1,
      direction: "all",
      market: "all",
      search: "",
      view: null,
      sortKey: "dayChangePct",
      sortDir: "desc",
    },
  },
  {
    name: "Strong buy pressure",
    state: {
      horizon: "1d",
      minScore: 0.5,
      direction: "buy",
      market: "all",
      search: "",
      view: null,
      sortKey: "score",
      sortDir: "desc",
    },
  },
];

export default function ScreenerPage() {
  const { rows, err, ranking, regimes, retry } = useScreenerData();
  const f = useScreenerFilters(rows, ranking, regimes);

  // Export unification (#23). The daemon's scores.csv / bars.csv both REQUIRE
  // symbol+market (checked in ../daemon/internal/api/api.go), and the screener
  // has no selection concept — so the export follows the TOP row of the
  // current filter/sort and says so in the label. No rows → no menu.
  const top = f.filtered[0]?.row;
  const exportItems = top
    ? [
        {
          label: `scores.csv · ${top.symbol} (top row) · ${f.horizon}`,
          href: `${API_BASE}/api/export/scores.csv?symbol=${encodeURIComponent(top.symbol)}&market=${top.market}&horizon=${f.horizon}`,
        },
        {
          label: `bars.csv · ${top.symbol} (top row) · 1d`,
          href: `${API_BASE}/api/export/bars.csv?symbol=${encodeURIComponent(top.symbol)}&market=${top.market}&tf=1d`,
        },
      ]
    : [];

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">SCREENER</h1>
        <span className="chip tnum">horizon {f.horizon}</span>
        {rows !== null && (
          <span className="chip tnum">
            {f.filtered.length} of {rows.length} symbols
          </span>
        )}
        {err !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
        <div className="ml-auto">
          <ExportMenu items={exportItems} />
        </div>
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="markets-screener"
        text="Which stocks look strongest right now? Ranks every tracked symbol by its pressure score — stored daily data on worker cadence, not live quotes, and never advice."
      />

      {/* Signal8 wave Stage 4: gainers/losers over the daily universe with
          the best-effort mcap filter (unknown mcap = excluded + counted,
          never guessed). */}
      <MoversPanel limit={20} />

      {/* discovery panel (hidden when logged out) */}
      <DiscoverPanel />

      {/* saved views — filter/sort state remembered on this device */}
      <div className="px-1">
        <SavedViewsBar
          pageKey="screener"
          currentState={f.currentState}
          onApply={f.applyState}
          presets={PRESETS}
        />
      </div>

      {/* filter bar */}
      <ScreenerFilters
        horizon={f.horizon}
        onHorizon={f.setHorizon}
        minScore={f.minScore}
        onMinScore={f.setMinScore}
        direction={f.direction}
        onDirection={f.setDirection}
        market={f.market}
        onMarket={f.setMarket}
        search={f.search}
        onSearch={f.setSearch}
      />

      {/* results */}
      <ScreenerResults
        rows={rows}
        err={err}
        filtered={f.filtered}
        horizon={f.horizon}
        effView={f.effView}
        onView={f.setView}
        sortKey={f.sortKey}
        sortDir={f.sortDir}
        onSort={f.onSort}
        onRetry={retry}
      />
    </div>
  );
}
