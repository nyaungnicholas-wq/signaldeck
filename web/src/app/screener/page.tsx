"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  HORIZONS,
  type Horizon,
  type Market,
  type ScoreComponent,
  type WatchRow,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice, fmtScore, scoreColor, verdict } from "@/lib/format";

type Direction = "all" | "buy" | "sell";
type MarketFilter = "all" | Market;
type SortKey =
  | "symbol"
  | "market"
  | "lastClose"
  | "dayChangePct"
  | "score"
  | "verdict"
  | "driver"
  | "latestBarTs";
type SortDir = "asc" | "desc";

const COLUMNS: { key: SortKey; label: string; numeric: boolean }[] = [
  { key: "symbol", label: "SYMBOL", numeric: false },
  { key: "market", label: "MARKET", numeric: false },
  { key: "lastClose", label: "LAST", numeric: true },
  { key: "dayChangePct", label: "DAY %", numeric: true },
  { key: "score", label: "SCORE", numeric: true },
  { key: "verdict", label: "VERDICT", numeric: false },
  { key: "driver", label: "TOP DRIVER", numeric: false },
  { key: "latestBarTs", label: "LAST BAR", numeric: true },
];

interface Derived {
  row: WatchRow;
  /** score for the selected horizon; null when the daemon hasn't scored it yet */
  score: number | null;
  driver: ScoreComponent | null;
}

function topDriver(components: ScoreComponent[] | undefined): ScoreComponent | null {
  if (!components || components.length === 0) return null;
  let best: ScoreComponent | null = null;
  for (const c of components) {
    if (!isFinite(c.contrib)) continue;
    if (best === null || Math.abs(c.contrib) > Math.abs(best.contrib)) best = c;
  }
  return best;
}

function sortValue(d: Derived, key: SortKey): string | number | null {
  switch (key) {
    case "symbol":
      return d.row.symbol;
    case "market":
      return d.row.market;
    case "lastClose":
      return isFinite(d.row.lastClose) ? d.row.lastClose : null;
    case "dayChangePct":
      return isFinite(d.row.dayChangePct) ? d.row.dayChangePct : null;
    case "score":
    case "verdict":
      return d.score;
    case "driver":
      return d.driver ? d.driver.name : null;
    case "latestBarTs":
      return d.row.latestBarTs || null;
  }
}

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
      className="chip cursor-pointer transition-colors duration-150 hover:brightness-125"
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

export default function ScreenerPage() {
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  // filters
  const [horizon, setHorizon] = useState<Horizon>("1d");
  const [minScore, setMinScore] = useState(-1);
  const [direction, setDirection] = useState<Direction>("all");
  const [market, setMarket] = useState<MarketFilter>("all");
  const [search, setSearch] = useState("");

  // sorting
  const [sortKey, setSortKey] = useState<SortKey>("score");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .watchlist()
        .then((r) => {
          if (!alive) return;
          setRows(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  const derived = useMemo<Derived[]>(() => {
    if (!rows) return [];
    return rows.map((row) => {
      const s = row.scores?.[horizon];
      const score = s && isFinite(s.score) ? s.score : null;
      return { row, score, driver: topDriver(s?.components) };
    });
  }, [rows, horizon]);

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

  const onSort = (key: SortKey, numericDefaultDesc: boolean) => {
    if (key === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir(numericDefaultDesc ? "desc" : "asc");
    }
  };

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">SCREENER</h1>
        <span className="chip">horizon {horizon}</span>
        {rows !== null && (
          <span className="chip tnum">
            {filtered.length} of {rows.length} symbols
          </span>
        )}
        {err !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* filter bar */}
      <section className="panel">
        <div className="panel-h">FILTERS</div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3 text-[0.72rem]">
          <label className="flex items-center gap-2">
            <span style={{ color: "var(--faint)" }}>horizon</span>
            <select
              value={horizon}
              onChange={(e) => setHorizon(e.target.value as Horizon)}
              className="cursor-pointer rounded border px-2 py-1 text-[0.72rem]"
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
              onChange={(e) => setMinScore(Number(e.target.value))}
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
              <FilterChip key={d} active={direction === d} onClick={() => setDirection(d)}>
                {d}
              </FilterChip>
            ))}
          </div>

          <div className="flex items-center gap-1.5" role="group" aria-label="market filter">
            <span style={{ color: "var(--faint)" }}>market</span>
            {(["all", "crypto", "stocks"] as MarketFilter[]).map((m) => (
              <FilterChip key={m} active={market === m} onClick={() => setMarket(m)}>
                {m}
              </FilterChip>
            ))}
          </div>

          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="search symbol or name…"
            aria-label="search symbols"
            className="min-w-40 flex-1 rounded border px-2.5 py-1.5 text-[0.72rem]"
            style={{
              background: "var(--panel2)",
              borderColor: "var(--border)",
              color: "var(--text)",
            }}
          />
        </div>
      </section>

      {/* results */}
      <section className="panel">
        <div className="panel-h">
          RESULTS
          <span className="tnum" style={{ color: "var(--faint)" }}>
            score horizon = {horizon} · sorted by {sortKey} {sortDir}
          </span>
        </div>

        {loading && (
          <div className="px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
            loading…
          </div>
        )}

        {hardError && (
          <div className="px-4 py-8 text-center text-[0.75rem]">
            <div style={{ color: "var(--bad)" }}>{err}</div>
            <div className="mt-2" style={{ color: "var(--faint)" }}>
              is the daemon running? start it with <span style={{ color: "var(--dim)" }}>signaldeckd</span>
            </div>
          </div>
        )}

        {rows !== null && rows.length === 0 && (
          <div className="px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
            watchlist is empty — subscribe to symbols from the watchlist page and the screener
            will fill in as bars and scores arrive.
          </div>
        )}

        {rows !== null && rows.length > 0 && filtered.length === 0 && (
          <div className="px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
            no symbols match the current filters — lower the min score, widen direction/market,
            or clear the search.
          </div>
        )}

        {filtered.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-[0.8rem]">
              <thead>
                <tr style={{ borderBottom: "1px solid var(--border)" }}>
                  {COLUMNS.map((c) => (
                    <th
                      key={c.key}
                      scope="col"
                      aria-sort={
                        sortKey === c.key
                          ? sortDir === "asc"
                            ? "ascending"
                            : "descending"
                          : "none"
                      }
                      className={`px-3 py-2 text-[0.64rem] font-medium tracking-wide ${
                        c.numeric ? "text-right" : "text-left"
                      }`}
                      style={{ color: "var(--faint)" }}
                    >
                      <button
                        type="button"
                        onClick={() => onSort(c.key, c.numeric)}
                        className={`cursor-pointer tracking-wide transition-colors duration-150 hover:text-[var(--dim)] ${
                          c.numeric ? "text-right" : "text-left"
                        }`}
                        style={{ color: sortKey === c.key ? "var(--accent)" : undefined }}
                      >
                        {c.label}
                        {sortKey === c.key ? (sortDir === "asc" ? " ▲" : " ▼") : ""}
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="tnum">
                {filtered.map((d) => {
                  const r = d.row;
                  return (
                    <tr
                      key={`${r.market}:${r.symbol}`}
                      className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ borderBottom: "1px solid var(--border)" }}
                    >
                      <td className="px-3 py-2">
                        <Link
                          href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                          className="cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                        >
                          {r.symbol}
                        </Link>
                      </td>
                      <td className="px-3 py-2" style={{ color: "var(--dim)" }}>
                        {r.market}
                      </td>
                      <td className="px-3 py-2 text-right">{fmtPrice(r.lastClose)}</td>
                      <td
                        className="px-3 py-2 text-right"
                        style={{ color: scoreColor(r.dayChangePct) }}
                      >
                        {fmtPct(r.dayChangePct)}
                      </td>
                      <td className="px-3 py-2 text-right">
                        {d.score === null ? (
                          <span style={{ color: "var(--faint)" }}>—</span>
                        ) : (
                          <span style={{ color: scoreColor(d.score) }}>{fmtScore(d.score)}</span>
                        )}
                      </td>
                      <td className="px-3 py-2" style={{ color: "var(--dim)" }}>
                        {d.score === null ? "unscored" : verdict(d.score)}
                      </td>
                      <td className="max-w-72 px-3 py-2">
                        {d.driver ? (
                          <span className="flex items-baseline gap-1.5">
                            <span style={{ color: "var(--text)" }}>{d.driver.name}</span>
                            <span
                              className="truncate text-[0.7rem]"
                              style={{ color: "var(--faint)" }}
                              title={d.driver.note}
                            >
                              {d.driver.note}
                            </span>
                          </span>
                        ) : (
                          <span style={{ color: "var(--faint)" }}>—</span>
                        )}
                      </td>
                      <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                        {ago(r.latestBarTs)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
