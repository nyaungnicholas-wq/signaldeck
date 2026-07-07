"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  HORIZONS,
  candidates as fetchCandidates,
  addCandidate,
  dismissCandidate,
  screenerRows,
  type CandidatesResponse,
  type Horizon,
  type Market,
  type RankedRow,
  type RegimeState,
  type ScoreComponent,
  type WatchRow,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice, fmtScore, scoreColor } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import VerdictCard from "@/components/VerdictCard";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import MoversPanel from "@/components/MoversPanel";
import Sparkline from "@/components/viz/Sparkline";
import Heatmap, { type HeatmapItem } from "@/components/viz/Heatmap";
import { regimeColor } from "@/components/regime/regime";
import PagePurpose from "@/components/PagePurpose";
import { useViewMode } from "@/components/Plain";

type Direction = "all" | "buy" | "sell";
type MarketFilter = "all" | Market;
/** Stage 5: TABLE stays the workhorse; HEATMAP is the same filtered set as
 *  color tiles (day % change) — a view toggle, not different data.
 *  Stage 4 (tables→charts): CARDS is the same filtered set as a grid of
 *  verdict cards (sparkline + honest tier badge). Default view follows the
 *  SIMPLE/PRO toggle — cards in simple, table in pro — until the user picks
 *  one explicitly. No view ever loses data: all three render the SAME rows. */
type View = "cards" | "table" | "heatmap";
type SortKey =
  | "symbol"
  | "market"
  | "spark"
  | "lastClose"
  | "dayChangePct"
  | "score"
  | "verdict"
  | "rank"
  | "regime"
  | "driver"
  | "latestBarTs";
type SortDir = "asc" | "desc";

const COLUMNS: { key: SortKey; label: string; numeric: boolean; title?: string; sortable?: boolean }[] = [
  { key: "symbol", label: "SYMBOL", numeric: false },
  { key: "market", label: "MARKET", numeric: false },
  // Stage 5: inline 30-day trend from the same stored daily closes as LAST —
  // no extra fetch, and <5 closes renders the honest dashed placeholder.
  { key: "spark", label: "TREND 30D", numeric: false, sortable: false, title: "Last ~30 stored daily closes (worker cadence, not live)" },
  { key: "lastClose", label: "LAST", numeric: true, title: "Last close price" },
  { key: "dayChangePct", label: "DAY %", numeric: true, title: "Change since previous close" },
  { key: "score", label: "SCORE", numeric: true, title: "Pressure score, −1 (sell) to +1 (buy)" },
  // Stage 2 (verdict cards): the REAL calibrated 1d P(up) as an honest
  // verdict card — sortable by that probability; "NO READ YET" rows sink.
  { key: "verdict", label: "VERDICT (1d)", numeric: false, title: "Calibrated 1d P(up) as a verdict card — near-50% reads NO CLEAR LEAN; no stored prediction reads NO READ YET (never a fabricated lean). Backtested calibration, not a live track record." },
  // Stage 5: cross-sectional relative-strength rank + current regime label.
  { key: "rank", label: "RANK", numeric: true, title: "Cross-sectional relative-strength rank (1 = strongest); — = not in the latest ranking pass" },
  { key: "regime", label: "REGIME", numeric: false, title: "Current detected regime (described from stored bars, no lookahead); — = not classified yet" },
  { key: "driver", label: "TOP DRIVER", numeric: false, title: "Component contributing most to the score" },
  { key: "latestBarTs", label: "LAST BAR", numeric: true, title: "Time of the most recent price bar" },
];

interface Derived {
  row: WatchRow;
  /** score for the selected horizon; null when the daemon hasn't scored it yet */
  score: number | null;
  driver: ScoreComponent | null;
  /** relative-strength rank; null = not in the latest ranking pass (honest) */
  rank: number | null;
  /** current regime state; null = not classified yet (honest) */
  regime: RegimeState | null;
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
    case "spark":
      return null; // not sortable (visual column)
    case "lastClose":
      return isFinite(d.row.lastClose) ? d.row.lastClose : null;
    case "dayChangePct":
      return isFinite(d.row.dayChangePct) ? d.row.dayChangePct : null;
    case "score":
      return d.score;
    case "verdict": {
      // Stage 2: sort by the REAL calibrated 1d P(up); rows without a stored
      // prediction return null and sink to the bottom (honest absence).
      const p = d.row.calProb1d;
      return typeof p === "number" && isFinite(p) ? p : null;
    }
    case "rank":
      // rank 1 is best — negate so "desc" (the numeric default) puts #1 on top.
      return d.rank === null ? null : -d.rank;
    case "regime":
      return d.regime ? d.regime.label : null;
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

// ── DISCOVER panel (universe-discovery wave) ─────────────────────────────

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
 * Add / Dismiss. Hidden entirely when logged out (the mutations are
 * session-scoped, so an anonymous panel would be all dead buttons).
 */
function DiscoverPanel() {
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
    const t = setInterval(load, pollMs() * 6); // discovery moves slowly; poll gently
    return () => {
      alive = false;
      clearInterval(t);
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
          <span
            className="tnum"
            style={{ color: atCap ? "var(--bad)" : "var(--faint)" }}
            title="Active symbols vs the SIGNALDECK_SYMBOL_CAP budget"
          >
            {data.active}/{data.cap} symbols active
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

      {data !== null && data.candidates.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-[0.8rem]">
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
                        title={
                          atCap
                            ? `Symbol cap reached (${data.active}/${data.cap}) — dismiss or unsubscribe first`
                            : `Subscribe ${c.symbol} and add it to your watchlist`
                        }
                        onClick={() => act(c.symbol, c.market, () => addCandidate(c.symbol, c.market))}
                        className="chip min-h-[44px] min-w-[44px] cursor-pointer px-4 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                        style={{ color: "var(--good)", borderColor: "var(--good)" }}
                      >
                        {busy === c.symbol ? "…" : "Add"}
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

export default function ScreenerPage() {
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Stage 5: rank + regime context (best-effort — the table renders without
  // them; missing values are honest "—", never invented).
  const [ranking, setRanking] = useState<RankedRow[] | null>(null);
  const [regimes, setRegimes] = useState<RegimeState[] | null>(null);

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
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () => {
      // Whole-universe PUBLIC read (Stage 5) — the screener is the universe
      // table, not the per-user watchlist, so it renders logged out too.
      screenerRows()
        .then((r) => {
          if (!alive) return;
          setRows(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
      // Rank + regime enrichers fail soft: their columns show "—" instead of
      // taking the whole table down.
      api
        .ranking()
        .then((r) => alive && setRanking(r ?? []))
        .catch(() => {});
      api
        .regime()
        .then((r) => alive && setRegimes(r.states ?? []))
        .catch(() => {});
    };
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  const derived = useMemo<Derived[]>(() => {
    if (!rows) return [];
    const rankBy = new Map<string, number>();
    for (const r of ranking ?? []) rankBy.set(`${r.market}:${r.symbol}`, r.rank);
    const regimeBy = new Map<string, RegimeState>();
    for (const s of regimes ?? []) regimeBy.set(`${s.market}:${s.symbol}`, s);
    return rows.map((row) => {
      const s = row.scores?.[horizon];
      const score = s && isFinite(s.score) ? s.score : null;
      const key = `${row.market}:${row.symbol}`;
      return {
        row,
        score,
        driver: topDriver(s?.components),
        rank: rankBy.get(key) ?? null,
        regime: regimeBy.get(key) ?? null,
      };
    });
  }, [rows, horizon, ranking, regimes]);

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

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="markets-screener"
        text="Which stocks look strongest right now? Ranks every tracked symbol by its pressure score — stored daily data on worker cadence, not live quotes, and never advice."
      />

      {/* Signal8 wave Stage 4: gainers/losers over the daily universe with
          the best-effort mcap filter (unknown mcap = excluded + counted,
          never guessed). */}
      <MoversPanel limit={10} />

      {/* discovery panel (hidden when logged out) */}
      <DiscoverPanel />

      {/* filter bar */}
      <section className="panel">
        <div className="panel-h">FILTERS</div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3 text-[0.78rem]">
          <label className="flex items-center gap-2">
            <span style={{ color: "var(--faint)" }}>horizon</span>
            <select
              value={horizon}
              onChange={(e) => setHorizon(e.target.value as Horizon)}
              className="cursor-pointer rounded border px-2 py-1 text-[0.78rem]"
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
            className="min-w-40 flex-1 rounded border px-2.5 py-1.5 text-[0.78rem]"
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
        <div className="panel-h flex-wrap gap-2">
          RESULTS
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
                onClick={() => setView(v)}
                className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
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

        {loading && <Skeleton lines={4} label="loading screener" className="m-4" />}

        {hardError && (
          <ErrorState
            message={err ?? "request failed"}
            hint="Is the daemon running? Start it with signaldeckd and this page will recover."
            retry={() => {
              setErr(null);
              setRetryTick((t) => t + 1);
            }}
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

        {/* Stage 4 (tables→charts): CARDS view — the same filtered rows as a
            grid of verdict cards: symbol + price + day %, the 30d sparkline,
            and the Stage-2 VerdictCard (REAL calibrated 1d prob or the honest
            NO READ YET, tier badge always visible). Nothing the table shows
            is lost — switch views any time. */}
        {effView === "cards" && filtered.length > 0 && (
          <div className="grid grid-cols-1 gap-2 px-4 py-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {filtered.map((d) => {
              const r = d.row;
              return (
                <div
                  key={`${r.market}:${r.symbol}`}
                  className="flex flex-col gap-2 rounded-lg border p-3 transition-colors duration-150 hover:bg-[var(--panel)]"
                  style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
                >
                  <div className="flex items-baseline justify-between gap-2">
                    <Link
                      href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                      className="min-w-0 cursor-pointer truncate font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                      title={r.name || r.symbol}
                    >
                      {r.symbol}
                      <span className="ml-1.5 text-[0.72rem] font-normal" style={{ color: "var(--faint)" }}>
                        {r.market}
                      </span>
                    </Link>
                    <span className="tnum shrink-0 text-[0.78rem]">
                      {fmtPrice(r.lastClose)}{" "}
                      <span style={{ color: scoreColor(r.dayChangePct) }} title="change since previous stored close">
                        {fmtPct(r.dayChangePct)}
                      </span>
                    </span>
                  </div>
                  {/* same stored daily closes as the table's TREND 30D column */}
                  <Sparkline closes={r.spark} width={190} height={34} area={false} />
                  <VerdictCard
                    size="sm"
                    symbol={r.symbol}
                    market={r.market}
                    calProb={r.calProb1d ?? null}
                    nUsed={r.nUsed1d ?? 0}
                    tier={r.tier1d ?? ""}
                    tierProgress={{ nSamples: r.nSamples1d ?? 0, threshold: r.tierThreshold ?? 0 }}
                  />
                </div>
              );
            })}
          </div>
        )}

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
              <p className="mt-2 text-[0.68rem]" style={{ color: "var(--faint)" }}>
                {filtered.filter((d) => d.row.latestBarTs === 0).length} filtered symbols have no
                stored daily bar yet and are omitted from the grid — bars accrue on worker cadence.
              </p>
            )}
          </div>
        )}

        {effView === "table" && filtered.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-[0.8rem]">
              <thead>
                <tr style={{ borderBottom: "1px solid var(--border)" }}>
                  {COLUMNS.map((c) => (
                    <th
                      key={c.key}
                      scope="col"
                      aria-sort={
                        c.sortable === false
                          ? undefined
                          : sortKey === c.key
                            ? sortDir === "asc"
                              ? "ascending"
                              : "descending"
                            : "none"
                      }
                      className={`px-3 py-2 text-[0.75rem] font-medium tracking-wide ${
                        c.numeric ? "text-right" : "text-left"
                      }`}
                      style={{ color: "var(--faint)" }}
                    >
                      {c.sortable === false ? (
                        <span title={c.title} className="tracking-wide">
                          {c.label}
                        </span>
                      ) : (
                        <button
                          type="button"
                          title={c.title}
                          onClick={() => onSort(c.key, c.numeric)}
                          className={`cursor-pointer tracking-wide transition-colors duration-150 hover:text-[var(--dim)] ${
                            c.numeric ? "text-right" : "text-left"
                          }`}
                          style={{ color: sortKey === c.key ? "var(--accent)" : undefined }}
                        >
                          {c.label}
                          {sortKey === c.key ? (sortDir === "asc" ? " ▲" : " ▼") : ""}
                        </button>
                      )}
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
                      {/* Stage 5: 30d trend from the row's own stored closes */}
                      <td className="px-3 py-1">
                        <Sparkline closes={r.spark} width={96} height={26} area={false} />
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
                      {/* Stage 2: calibrated 1d verdict card — real prob or
                          the honest NO READ YET, tier badge always visible. */}
                      <td className="px-3 py-2">
                        <VerdictCard
                          size="sm"
                          symbol={r.symbol}
                          market={r.market}
                          calProb={r.calProb1d ?? null}
                          nUsed={r.nUsed1d ?? 0}
                          tier={r.tier1d ?? ""}
                          tierProgress={{ nSamples: r.nSamples1d ?? 0, threshold: r.tierThreshold ?? 0 }}
                        />
                      </td>
                      {/* Stage 5: relative-strength rank (1 = strongest) */}
                      <td className="px-3 py-2 text-right">
                        {d.rank === null ? (
                          <span style={{ color: "var(--faint)" }} title="not in the latest ranking pass">
                            —
                          </span>
                        ) : (
                          <span style={{ color: d.rank <= 10 ? "var(--accent)" : "var(--dim)" }}>
                            #{d.rank}
                          </span>
                        )}
                      </td>
                      {/* Stage 5: current regime chip (descriptive, no lookahead) */}
                      <td className="px-3 py-2">
                        {d.regime === null ? (
                          <span style={{ color: "var(--faint)" }} title="not classified yet — the regime worker fills this in from stored bars">
                            —
                          </span>
                        ) : (
                          <span
                            className="chip px-2 py-[1px] text-[0.68rem] tracking-wider"
                            title={`${d.regime.label} — ${d.regime.note || "described from stored bars, no lookahead"}`}
                            style={{ color: regimeColor(d.regime.label), borderColor: regimeColor(d.regime.label) }}
                          >
                            {d.regime.label}
                          </span>
                        )}
                      </td>
                      <td className="max-w-72 px-3 py-2">
                        {d.driver ? (
                          <span className="flex items-baseline gap-1.5">
                            <span style={{ color: "var(--text)" }}>{d.driver.name}</span>
                            <span
                              className="truncate text-[0.78rem]"
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
