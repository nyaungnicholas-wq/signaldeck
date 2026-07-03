"use client";

// WATCHLIST — the "what should I look at" home page.
// Polls api.watchlist() and renders a sortable tile grid; add/unwatch inline.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, type Horizon, type Insight, type Market, type WatchRow } from "@/lib/api";
import { ago, fmtPct, fmtPrice, fmtScore, scoreColor, verdict } from "@/lib/format";
import ScoreGauge from "@/components/ScoreGauge";
import Spark from "@/components/Spark";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

type SortKey = "score" | "change" | "symbol";

const SORTS: { k: SortKey; label: string }[] = [
  { k: "score", label: "1d score" },
  { k: "change", label: "day change" },
  { k: "symbol", label: "symbol" },
];

const keyOf = (symbol: string, market: Market) => `${market}:${symbol.toUpperCase()}`;

/** Today's date key in America/New_York (the daily-briefing worker's clock). */
function nyToday(): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(new Date());
}

/** Pinned "Daily briefing" card — shown only when TODAY's briefing insight
 *  exists (kind=daily_briefing, matched on the NY day key in its data blob). */
function DailyBriefingCard() {
  const [briefing, setBriefing] = useState<Insight | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .insights(1, "daily_briefing")
        .then((ins) => {
          if (!alive) return;
          const today = nyToday();
          const hit = ins.find((i) => {
            try {
              return (JSON.parse(i.data) as { day?: string }).day === today;
            } catch {
              return false;
            }
          });
          setBriefing(hit ?? null);
        })
        .catch(() => alive && setBriefing(null)); // silent: card simply hides
    load();
    const t = setInterval(load, 60000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  if (!briefing) return null;
  return (
    <section
      aria-label="daily briefing"
      className="panel px-4 py-3 sm:px-5"
      style={{ borderColor: "var(--accent)" }}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span
          className="chip px-2 py-[2px] text-[0.72rem] tracking-wider"
          style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          DAILY BRIEFING
        </span>
        <span className="text-[0.85rem] font-bold tracking-wide">{briefing.headline}</span>
        <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ago(briefing.ts)}
        </span>
      </div>
      <p className="mt-2 text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        {briefing.body}
      </p>
      <Link
        href="/insights"
        className="mt-2 inline-flex min-h-[40px] cursor-pointer items-center text-[0.75rem] tracking-wider transition-colors duration-150 hover:text-[var(--accent)]"
        style={{ color: "var(--faint)" }}
      >
        all insights →
      </Link>
    </section>
  );
}

function scoreFor(r: WatchRow, h: Horizon): number | null {
  const s = r.scores?.[h]?.score;
  return typeof s === "number" && Number.isFinite(s) ? s : null;
}

function changeColor(v: number): string {
  if (!Number.isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

export default function WatchlistPage() {
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sort, setSort] = useState<SortKey>("score");
  const [tick, setTick] = useState(0); // bump to refetch immediately
  const [pending, setPending] = useState<Set<string>>(() => new Set());

  // add-symbol form
  const [sym, setSym] = useState("");
  const [mkt, setMkt] = useState<Market>("stocks");
  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .watchlist()
        .then((data) => {
          if (!alive) return;
          setRows(data);
          setError(null);
          // clear "backfilling…" once the daemon has history for the symbol
          setPending((prev) => {
            if (prev.size === 0) return prev;
            const next = new Set(prev);
            for (const r of data) {
              if (next.has(keyOf(r.symbol, r.market)) && (r.spark?.length ?? 0) >= 2) {
                next.delete(keyOf(r.symbol, r.market));
              }
            }
            return next.size === prev.size ? prev : next;
          });
        })
        .catch((e) => {
          if (!alive) return;
          setError(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [tick]);

  const sorted = useMemo(() => {
    const arr = [...(rows ?? [])];
    if (sort === "score") {
      arr.sort(
        (a, b) =>
          (scoreFor(b, "1d") ?? Number.NEGATIVE_INFINITY) -
          (scoreFor(a, "1d") ?? Number.NEGATIVE_INFINITY),
      );
    } else if (sort === "change") {
      arr.sort(
        (a, b) =>
          (Number.isFinite(b.dayChangePct) ? b.dayChangePct : Number.NEGATIVE_INFINITY) -
          (Number.isFinite(a.dayChangePct) ? a.dayChangePct : Number.NEGATIVE_INFINITY),
      );
    } else {
      arr.sort((a, b) => a.symbol.localeCompare(b.symbol));
    }
    return arr;
  }, [rows, sort]);

  const stats = useMemo(() => {
    let pos = 0;
    let neg = 0;
    let scored = 0;
    for (const r of rows ?? []) {
      const s = scoreFor(r, "1d");
      if (s === null) continue;
      scored++;
      if (s > 0) pos++;
      else if (s < 0) neg++;
    }
    const regime =
      scored === 0 ? "unscored" : pos > neg ? "bullish" : neg > pos ? "bearish" : "mixed";
    const regimeColor =
      regime === "bullish" ? "var(--bid)" : regime === "bearish" ? "var(--ask)" : "var(--dim)";
    return { pos, scored, regime, regimeColor };
  }, [rows]);

  async function onAdd(e: React.FormEvent) {
    e.preventDefault();
    const s = sym.trim().toUpperCase();
    if (!s || adding) return;
    setAdding(true);
    setAddError(null);
    try {
      const info = await api.subscribe(s, mkt);
      setPending((prev) => {
        const next = new Set(prev);
        next.add(keyOf(info.symbol || s, info.market || mkt));
        return next;
      });
      setSym("");
      setTick((t) => t + 1);
    } catch (err) {
      // 422 = validation failed; the API error text is shown verbatim
      setAddError(err instanceof Error ? err.message : String(err));
    } finally {
      setAdding(false);
    }
  }

  function onUnwatch(e: React.MouseEvent, r: WatchRow) {
    e.preventDefault();
    e.stopPropagation();
    if (!window.confirm(`Stop watching ${r.symbol} (${r.market})?`)) return;
    setActionError(null);
    api
      .unsubscribe(r.symbol, r.market)
      .then(() => setTick((t) => t + 1))
      .catch((err) => setActionError(err instanceof Error ? err.message : String(err)));
  }

  return (
    <div className="flex flex-col gap-3">
      <style>{`
        .wl-tile { transition: border-color .2s ease; }
        .wl-tile:hover { border-color: var(--accent); }
        .wl-unwatch:hover { color: var(--bad) !important; }
        .wl-input {
          background: var(--panel2);
          border: 1px solid var(--border);
          border-radius: 6px;
          padding: 6px 10px;
          min-height: 40px;
          font-size: .72rem;
          color: var(--text);
          font-family: inherit;
        }
        .wl-input::placeholder { color: var(--faint); }
        .wl-add:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
        .wl-add:disabled { color: var(--faint); cursor: default; }
      `}</style>

      {/* pinned daily briefing (renders only when today's briefing exists) */}
      <DailyBriefingCard />

      {/* header row: title + contextual chips + add-symbol box */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">WATCHLIST</h1>
        <span className="chip tnum">{rows === null ? "…" : rows.length} tracked</span>
        <span className="chip tnum">
          <span style={{ color: stats.pos > 0 ? "var(--bid)" : "var(--dim)" }}>{stats.pos}</span>{" "}
          positive 1d
        </span>
        <span className="chip">
          regime <span style={{ color: stats.regimeColor, fontWeight: 700 }}>{stats.regime}</span>
        </span>
        {error && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            refresh failed — retrying
          </span>
        )}
        <form className="ml-auto flex flex-wrap items-center gap-2" onSubmit={onAdd}>
          <input
            className="wl-input tnum w-40 uppercase"
            value={sym}
            onChange={(e) => {
              setSym(e.target.value);
              if (addError) setAddError(null);
            }}
            placeholder={mkt === "crypto" ? "e.g. BTC/USD" : "e.g. AAPL"}
            aria-label="symbol to add"
            spellCheck={false}
            autoComplete="off"
          />
          <select
            className="wl-input cursor-pointer"
            value={mkt}
            onChange={(e) => setMkt(e.target.value as Market)}
            aria-label="market"
          >
            <option value="stocks">stocks</option>
            <option value="crypto">crypto</option>
          </select>
          <button
            type="submit"
            disabled={adding || sym.trim() === ""}
            className="wl-input wl-add cursor-pointer transition-colors duration-150"
            style={{ color: "var(--dim)" }}
          >
            {adding ? "adding…" : "add"}
          </button>
        </form>
      </div>

      {(addError || actionError) && (
        <div role="alert" className="text-[0.78rem]" style={{ color: "var(--bad)" }}>
          {addError ?? actionError}
        </div>
      )}

      {/* sort control */}
      <div className="flex flex-wrap items-center gap-2 text-[0.78rem]">
        <span style={{ color: "var(--faint)", letterSpacing: "0.12em" }}>SORT</span>
        {SORTS.map((s) => (
          <button
            key={s.k}
            type="button"
            aria-pressed={sort === s.k}
            onClick={() => setSort(s.k)}
            className="chip min-h-[40px] cursor-pointer transition-colors duration-150"
            style={
              sort === s.k ? { color: "var(--text)", borderColor: "var(--accent)" } : undefined
            }
          >
            {s.label}
          </button>
        ))}
      </div>

      {/* body states */}
      {rows === null && !error && <Skeleton lines={4} label="loading watchlist" />}

      {rows === null && error && (
        <ErrorState
          message={error}
          hint="The SignalDeck daemon looks offline — start signaldeckd (:8322) and this page will recover on its own."
          retry={() => {
            setError(null);
            setTick((t) => t + 1);
          }}
        />
      )}

      {rows !== null && rows.length === 0 && (
        <EmptyState
          message="No symbols tracked yet"
          detail="Add one above — e.g. AAPL (stocks) or BTC/USD (crypto). The daemon backfills history and scores automatically."
        />
      )}

      {rows !== null && rows.length > 0 && (
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          {sorted.map((r) => {
            const s1d = scoreFor(r, "1d");
            const backfilling = pending.has(keyOf(r.symbol, r.market));
            return (
              <Link
                key={keyOf(r.symbol, r.market)}
                href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                className="panel wl-tile block cursor-pointer"
                aria-label={`open ${r.symbol} (${r.market}) details`}
              >
                {/* symbol row */}
                <div className="flex items-center gap-2 px-4 pt-3">
                  <span className="text-[0.92rem] font-bold tracking-wide">{r.symbol}</span>
                  <span className="chip px-2 py-[2px] text-[0.75rem]">{r.market}</span>
                  {backfilling && (
                    <span
                      className="chip px-2 py-[2px] text-[0.75rem]"
                      style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
                    >
                      backfilling…
                    </span>
                  )}
                  <button
                    type="button"
                    onClick={(e) => onUnwatch(e, r)}
                    aria-label={`stop watching ${r.symbol}`}
                    className="wl-unwatch ml-auto cursor-pointer text-[0.75rem] tracking-wider transition-colors duration-150"
                    style={{ color: "var(--faint)" }}
                  >
                    unwatch
                  </button>
                </div>

                {/* price + spark */}
                <div className="flex items-end justify-between gap-3 px-4 pt-2">
                  <div>
                    <div className="tnum text-[1.05rem] font-bold">{fmtPrice(r.lastClose)}</div>
                    <div
                      className="tnum text-[0.78rem]"
                      style={{ color: changeColor(r.dayChangePct) }}
                    >
                      {fmtPct(r.dayChangePct)} today
                    </div>
                  </div>
                  <Spark values={r.spark ?? []} width={130} height={38} />
                </div>

                {/* scores */}
                <div className="px-4 pb-3 pt-3">
                  {s1d !== null ? (
                    <>
                      <ScoreGauge score={s1d} label={`${r.symbol} 1d pressure`} compact />
                      <div className="mt-1.5 flex items-baseline justify-between gap-2 text-[0.78rem]">
                        <span style={{ color: "var(--faint)" }}>1d</span>
                        <span className="tnum" style={{ color: scoreColor(s1d) }}>
                          {fmtScore(s1d)} · {verdict(s1d)}
                        </span>
                      </div>
                    </>
                  ) : (
                    <div className="py-1 text-[0.78rem]" style={{ color: "var(--faint)" }}>
                      1d score pending — needs more history
                    </div>
                  )}
                  <div className="mt-2 flex items-center gap-1.5 text-[0.75rem]">
                    {(["1h", "1w"] as const).map((h) => {
                      const s = scoreFor(r, h);
                      return (
                        <span key={h} className="chip tnum px-2 py-[2px] text-[0.75rem]">
                          {h}{" "}
                          <span style={s !== null ? { color: scoreColor(s) } : undefined}>
                            {s !== null ? fmtScore(s) : "—"}
                          </span>
                        </span>
                      );
                    })}
                    <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
                      bar {ago(r.latestBarTs)}
                    </span>
                  </div>
                </div>
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}
