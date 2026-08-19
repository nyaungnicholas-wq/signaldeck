"use client";
import { API_BASE } from "@/lib/api";
import PagePurpose from "@/components/PagePurpose";
import ExportMenu from "@/components/ExportMenu";
import MoversPanel from "@/components/MoversPanel";
import SavedViewsBar from "@/components/SavedViewsBar";
import DiscoverPanel from "@/components/markets/DiscoverPanel";
import ScreenerFilters from "@/components/markets/ScreenerFilters";
import ScreenerResults from "@/components/markets/ScreenerResults";
import GoalBanner from "@/components/home/GoalBanner";
import { useScreenerData } from "@/hooks/useScreenerData";
import { useScreenerFilters } from "@/hooks/useScreenerFilters";
import { PageHero, Reveal, StatTile } from "@/components/ui/Kit";
import {
  foldedOverviewBlocks,
  overviewLayoutFor,
  useGoal,
  type OverviewBlockId,
} from "@/lib/goal";

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
  const { rows, err, ranking, regimes, rankingFailed, retry } = useScreenerData();
  const f = useScreenerFilters(rows, ranking, regimes);
  const goal = useGoal();
  const layout = overviewLayoutFor(goal);
  const folded = foldedOverviewBlocks(layout);
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

  // Every block this page can show, keyed by id. Which of these lead and which
  // fold comes from the reader's goal — see OVERVIEW_LAYOUTS in lib/goal.ts.
  const blocks: Record<OverviewBlockId, React.ReactNode> = {
    movers: (
      <Reveal>
        <div className="panel">
          <MoversPanel limit={20} />
        </div>
      </Reveal>
    ),
    discover: (
      <Reveal>
        <div className="panel">
          <DiscoverPanel />
        </div>
      </Reveal>
    ),
    views: (
      <Reveal>
        <div className="panel">
          <div className="px-1">
            <SavedViewsBar pageKey="screener" currentState={f.currentState} onApply={f.applyState} presets={PRESETS} />
          </div>
        </div>
      </Reveal>
    ),
    filters: (
      <Reveal>
        <div className="panel">
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
        </div>
      </Reveal>
    ),
    results: (
      <Reveal>
        <div className="panel">
          <ScreenerResults
            rankingFailed={rankingFailed}
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
            limit={layout.rowLimit}
          />
        </div>
      </Reveal>
    ),
  };

  return (
    <div className="page-enter space-y-4">
      {/* No `live`. This page has no polling of any kind, and its own
          PagePurpose ten lines below says "stored daily data on worker cadence,
          not live quotes" — the pulsing dot contradicted the page's own
          disclosure. */}
      <PageHero title="Market Overview" subtitle="The whole tape in one screen — indices, movers and market state." right={<ExportMenu items={exportItems} />} />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Total Symbols" value={rows?.length ?? "—"} i={0} glow="hud" />
        <StatTile label="Filtered" value={f.filtered.length} i={1} glow="hud" />
        {/* scores is Partial<Record<Horizon, Score>>, so `?? 0` rendered "0.00"
            — a real neutral-pressure reading — for a horizon that simply has no
            stored score. The two tiles either side of this one already use "—". */}
        <StatTile label="Top Score" value={top?.scores?.[f.horizon]?.score} decimals={2} i={2} glow="accent" />
        <StatTile label="Regime" value={regimes?.[0]?.label ?? "—"} i={3} glow="hud" />
      </div>

      <Reveal>
        <div className="panel">
          <PagePurpose id="markets-screener" text="Which stocks look strongest right now? Ranks every tracked symbol by its pressure score — stored daily data on worker cadence, not live quotes, and never advice." />
        </div>
      </Reveal>

      <GoalBanner note={layout.note} />

      {layout.lead.map((id) => (
        <div key={id} data-block={id}>
          {blocks[id]}
        </div>
      ))}

      {/* Demoted, never deleted — same doctrine as the dashboard. Native
          <details>: keyboard- and screen-reader-correct with no state. */}
      {folded.length > 0 && (
        <details className="panel">
          <summary
            className="flex min-h-[44px] cursor-pointer list-none items-center px-4 py-2 text-[0.8rem] sm:px-5"
            style={{ color: "var(--dim)" }}
          >
            Show the rest of this page ({folded.length}{" "}
            {folded.length === 1 ? "panel" : "panels"})
          </summary>
          <div className="flex flex-col gap-3 border-t p-3" style={{ borderColor: "var(--border)" }}>
            {folded.map((id) => (
              <div key={id} data-block={id}>
                {blocks[id]}
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}
