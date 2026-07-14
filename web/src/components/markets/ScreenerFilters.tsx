"use client";

// SCREENER filter bar, extracted verbatim from the page split — horizon,
// min-score slider, direction/market chip groups, and free-text search.
// Controlled entirely by useScreenerFilters state passed down from the page.

import { HORIZONS, type Horizon } from "@/lib/api";
import { fmtScore } from "@/lib/format";
import type { Direction, MarketFilter } from "@/components/markets/screenerModel";

function FilterChip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:brightness-125"
      style={{
        color: active ? "var(--text)" : "var(--dim)",
        borderColor: active ? "var(--accent)" : "var(--border)",
        background: active ? "rgba(251,191,36,.08)" : "var(--panel2)",
      }}
    >
      {children}
    </button>
  );
}

export default function ScreenerFilters({
  horizon,
  onHorizon,
  minScore,
  onMinScore,
  direction,
  onDirection,
  market,
  onMarket,
  search,
  onSearch,
}: {
  horizon: Horizon;
  onHorizon: (h: Horizon) => void;
  minScore: number;
  onMinScore: (v: number) => void;
  direction: Direction;
  onDirection: (d: Direction) => void;
  market: MarketFilter;
  onMarket: (m: MarketFilter) => void;
  search: string;
  onSearch: (q: string) => void;
}) {
  return (
    <section className="panel">
      <div className="panel-h">FILTERS</div>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3 text-[0.75rem]">
        <label className="flex items-center gap-2">
          <span style={{ color: "var(--faint)" }}>horizon</span>
          <select
            value={horizon}
            onChange={(e) => onHorizon(e.target.value as Horizon)}
            className="cursor-pointer rounded-lg border px-2 py-1 text-[0.75rem]"
            style={{
              background: "var(--panel2)",
              borderColor: "var(--border)",
              color: "var(--text)",
            }}
          >
            {HORIZONS.map((h) => (
              <option key={h} value={h}>
                {h}
              </option>
            ))}
          </select>
        </label>

        <label className="flex items-center gap-2">
          <span style={{ color: "var(--faint)" }}>min score</span>
          <input
            type="range"
            min={-1}
            max={1}
            step={0.05}
            value={minScore}
            onChange={(e) => onMinScore(Number(e.target.value))}
            className="w-32 cursor-pointer"
            style={{ accentColor: "var(--accent)" }}
            aria-label="minimum score filter"
          />
          <span className="chip tnum">
            {minScore <= -1 ? "any" : `≥ ${fmtScore(minScore)}`}
          </span>
        </label>

        <div className="flex items-center gap-1.5" role="group" aria-label="direction filter">
          <span style={{ color: "var(--faint)" }}>direction</span>
          {(["all", "buy", "sell"] as Direction[]).map((d) => (
            <FilterChip key={d} active={direction === d} onClick={() => onDirection(d)}>
              {d}
            </FilterChip>
          ))}
        </div>

        <div className="flex items-center gap-1.5" role="group" aria-label="market filter">
          <span style={{ color: "var(--faint)" }}>market</span>
          {(["all", "crypto", "stocks"] as MarketFilter[]).map((m) => (
            <FilterChip key={m} active={market === m} onClick={() => onMarket(m)}>
              {m}
            </FilterChip>
          ))}
        </div>

        <input
          type="search"
          value={search}
          onChange={(e) => onSearch(e.target.value)}
          placeholder="search symbol or name…"
          aria-label="search symbols"
          className="min-w-40 flex-1 rounded-lg border px-2.5 py-1.5 text-[0.75rem]"
          style={{
            background: "var(--panel2)",
            borderColor: "var(--border)",
            color: "var(--text)",
          }}
        />
      </div>
    </section>
  );
}
