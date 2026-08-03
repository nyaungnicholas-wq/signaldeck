"use client";
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
import { PageHero, Reveal, StatTile } from "@/components/ui/Kit";

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
    <div className="page-enter space-y-4">
      <PageHero title="Market Overview" live subtitle="The whole tape in one screen — indices, movers and market state." right={<ExportMenu items={exportItems} />} />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Total Symbols" value={rows?.length ?? "—"} i={0} glow="hud" />
        <StatTile label="Filtered" value={f.filtered.length} i={1} glow="hud" />
        <StatTile label="Top Score" value={top?.scores?.[f.horizon]?.score ?? 0} decimals={2} i={2} glow="accent" />
        <StatTile label="Regime" value={regimes?.[0]?.label ?? "—"} i={3} glow="hud" />
      </div>

      <Reveal>
        <div className="panel">
          <PagePurpose id="markets-screener" text="Which stocks look strongest right now? Ranks every tracked symbol by its pressure score — stored daily data on worker cadence, not live quotes, and never advice." />
        </div>
      </Reveal>

      <Reveal>
        <div className="panel">
          <MoversPanel limit={20} />
        </div>
      </Reveal>

      <Reveal>
        <div className="panel">
          <DiscoverPanel />
        </div>
      </Reveal>

      <Reveal>
        <div className="panel">
          <div className="px-1">
            <SavedViewsBar pageKey="screener" currentState={f.currentState} onApply={f.applyState} presets={PRESETS} />
          </div>
        </div>
      </Reveal>

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

      <Reveal>
        <div className="panel">
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
      </Reveal>
    </div>
  );
}
