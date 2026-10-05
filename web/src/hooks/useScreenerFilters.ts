"use client";

// Screener filter/sort/view state, extracted from the page split. Also
// serializes the whole state to/from a plain Record for <SavedViewsBar>
// (applyState validates every field — a stale or hand-edited saved view can
// never put the page in an impossible state).

import { useCallback, useMemo, useState } from "react";
import { HORIZONS, type Horizon, type RankedRow, type RegimeState, type WatchRow } from "@/lib/api";
import { useViewMode } from "@/components/Plain";
import {
  COLUMNS,
  deriveRows,
  sortValue,
  type Derived,
  type Direction,
  type MarketFilter,
  type SortDir,
  type SortKey,
  type View,
} from "@/components/markets/screenerModel";

const SORTABLE_KEYS = COLUMNS.filter((c) => c.sortable !== false).map((c) => c.key);

export function useScreenerFilters(
  rows: WatchRow[] | null,
  ranking: RankedRow[] | null,
  regimes: RegimeState[] | null,
) {
  // filters
  const [horizon, setHorizon] = useState<Horizon>("1d");
  const [minScore, setMinScore] = useState(-1);
  const [direction, setDirection] = useState<Direction>("all");
  const [market, setMarket] = useState<MarketFilter>("all");
  const [search, setSearch] = useState("");
  // Stage 4 (tables→charts): null = "follow the SIMPLE/PRO mode default"
  // (cards in simple, table in pro); a click pins an explicit choice.
  const [view, setView] = useState<View | null>(null);
  const mode = useViewMode();
  const effView: View = view ?? (mode === "simple" ? "cards" : "table");

  // sorting
  const [sortKey, setSortKey] = useState<SortKey>("score");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const derived = useMemo<Derived[]>(
    () => (rows ? deriveRows(rows, horizon, ranking, regimes) : []),
    [rows, horizon, ranking, regimes],
  );

  const filtered = useMemo<Derived[]>(() => {
    const q = search.trim().toLowerCase();
    const list = derived.filter((d) => {
      if (market !== "all" && d.row.market !== market) return false;
      if (
        q &&
        !d.row.symbol.toLowerCase().includes(q) &&
        !(d.row.name ?? "").toLowerCase().includes(q)
      )
        return false;
      if (direction === "buy" && !(d.score !== null && d.score >= 0.15)) return false;
      if (direction === "sell" && !(d.score !== null && d.score <= -0.15)) return false;
      if (minScore > -1 && !(d.score !== null && d.score >= minScore)) return false;
      return true;
    });
    list.sort((a, b) => {
      const va = sortValue(a, sortKey);
      const vb = sortValue(b, sortKey);
      if (va === null && vb === null) return 0;
      if (va === null) return 1; // missing values always sink to the bottom
      if (vb === null) return -1;
      const cmp =
        typeof va === "string" && typeof vb === "string"
          ? va.localeCompare(vb)
          : (va as number) - (vb as number);
      return sortDir === "asc" ? cmp : -cmp;
    });
    return list;
  }, [derived, market, search, direction, minScore, sortKey, sortDir]);

  const onSort = useCallback((key: SortKey, numericDefaultDesc: boolean) => {
    setSortKey((prev) => {
      if (key === prev) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
        return prev;
      }
      setSortDir(numericDefaultDesc ? "desc" : "asc");
      return key;
    });
  }, []);

  // ── SavedViewsBar serialization ──────────────────────────────────────────
  const currentState = useMemo<Record<string, unknown>>(
    () => ({ horizon, minScore, direction, market, search, view, sortKey, sortDir }),
    [horizon, minScore, direction, market, search, view, sortKey, sortDir],
  );

  const applyState = useCallback((s: Record<string, unknown>) => {
    if (typeof s.horizon === "string" && (HORIZONS as string[]).includes(s.horizon))
      setHorizon(s.horizon as Horizon);
    if (typeof s.minScore === "number" && isFinite(s.minScore))
      setMinScore(Math.max(-1, Math.min(1, s.minScore)));
    if (s.direction === "all" || s.direction === "buy" || s.direction === "sell")
      setDirection(s.direction);
    if (s.market === "all" || s.market === "crypto" || s.market === "stocks") setMarket(s.market);
    if (typeof s.search === "string") setSearch(s.search);
    if (s.view === "cards" || s.view === "table" || s.view === "heatmap" || s.view === null)
      setView(s.view ?? null);
    if (typeof s.sortKey === "string" && (SORTABLE_KEYS as string[]).includes(s.sortKey))
      setSortKey(s.sortKey as SortKey);
    if (s.sortDir === "asc" || s.sortDir === "desc") setSortDir(s.sortDir);
  }, []);

  return {
    horizon,
    setHorizon,
    minScore,
    setMinScore,
    direction,
    setDirection,
    market,
    setMarket,
    search,
    setSearch,
    view,
    setView,
    effView,
    sortKey,
    sortDir,
    onSort,
    derived,
    filtered,
    currentState,
    applyState,
  };
}
