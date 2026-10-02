"use client";

// The watchlist as a MEMBER sees it. The daemon strips every vendor-derived
// field from a member's rows (closes, sparks, day change, scores), so each row
// carries SignalDeck's own validated regime reads instead, labelled with the
// accuracy they measured in backtests. Adds go through /api/watch, which only
// accepts symbols SignalDeck already tracks and never starts ingestion.

import Link from "next/link";
import { useEffect, useState } from "react";
import {
  api,
  companiesList,
  structuralRegimes,
  volRegime,
  type CompanyDirRow,
  type MemberWatchRow,
  type StructRegimeForecast,
  type VolRegimeForecast,
} from "@/lib/api";
import HypotheticalNote from "@/components/HypotheticalNote";
import HelpTip from "@/components/HelpTip";
import { regimeCaveats } from "@/lib/regimeCaveat";

type Chip = { label: string; regime: string; accuracy: number };

const GOOD = new Set(["up", "uptrend", "calm", "low"]);
const BAD = new Set(["down", "downtrend", "elevated", "high"]);

function chipColor(regime: string): string {
  if (GOOD.has(regime)) return "var(--bid)";
  if (BAD.has(regime)) return "var(--ask)";
  return "var(--dim)";
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export default function MemberWatchlist() {
  const [rows, setRows] = useState<MemberWatchRow[] | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [regimes, setRegimes] = useState<StructRegimeForecast[]>([]);
  const [vols, setVols] = useState<VolRegimeForecast[]>([]);
  const [reloadTick, setReloadTick] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [rowErr, setRowErr] = useState<{ key: string; msg: string } | null>(null);

  const [query, setQuery] = useState("");
  const [results, setResults] = useState<{ q: string; rows: CompanyDirRow[] } | null>(null);
  const [addErr, setAddErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      const [w, v] = await Promise.allSettled([api.memberWatchlist(), volRegime()]);
      if (!alive) return;
      if (w.status === "fulfilled") {
        setRows(w.value ?? []);
        setLoadErr(null);
      } else {
        setLoadErr(errText(w.reason));
      }
      if (v.status === "fulfilled") setVols(v.value.forecasts ?? []);
      // Only the watched symbols' regime rows (REGIMES-SIZE): the member read
      // of /api/regimes is sliced, so it is asked for once the list is known.
      const syms = w.status === "fulfilled" ? [...new Set((w.value ?? []).map((x) => x.symbol))] : [];
      if (syms.length === 0) return;
      try {
        const r = await structuralRegimes({ symbols: syms });
        if (alive) setRegimes(Object.values(r.forecasts ?? {}).flat());
      } catch {
        // As before: a failed regimes read leaves the chips as they were.
      }
    };
    void load();
    return () => {
      alive = false;
    };
  }, [reloadTick]);

  // Debounced directory search. Results are keyed by their query, so a stale
  // answer for an older query is never shown against a newer one. The source is
  // /api/companies, the SEC EDGAR registrant table joined to tracked STOCK
  // symbols, and adds go in as market "stocks": crypto can never be offered here.
  const q = query.trim();
  useEffect(() => {
    if (q.length < 1) return;
    let alive = true;
    const t = setTimeout(() => {
      companiesList({ q, limit: 8 }).then(
        (res) => {
          if (alive) setResults({ q, rows: res.companies ?? [] });
        },
        (e: unknown) => {
          if (alive) setAddErr(errText(e));
        },
      );
    }, 250);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [q]);
  const shown = results && results.q === q && q.length > 0 ? results.rows : [];

  const chipsFor = (row: MemberWatchRow): Chip[] => {
    const out: Chip[] = regimes
      .filter((f) => f.symbol === row.symbol && f.market === row.market)
      .map((f) => ({ label: f.kind, regime: f.regime, accuracy: f.historicalAccuracy }));
    const v = vols.find((f) => f.symbol === row.symbol && f.market === row.market);
    if (v) out.push({ label: "vol63", regime: v.regime, accuracy: v.historicalAccuracy });
    return out;
  };

  // The caveats of the rows behind the chips on show: every watched symbol's
  // regime rows, and the vol63 row of each watched symbol.
  const caveats = regimeCaveats([
    ...regimes,
    ...vols.filter((v) => (rows ?? []).some((r) => r.symbol === v.symbol && r.market === v.market)),
  ]);

  const watch = async (ticker: string) => {
    setBusy(`add:${ticker}`);
    setAddErr(null);
    try {
      await api.watch(ticker, "stocks");
      setQuery("");
      setResults(null);
      setReloadTick((t) => t + 1);
    } catch (e) {
      setAddErr(errText(e));
    } finally {
      setBusy(null);
    }
  };

  const unwatch = async (row: MemberWatchRow) => {
    const key = `${row.symbol}|${row.market}`;
    setBusy(`rm:${key}`);
    setRowErr(null);
    try {
      await api.unwatch(row.symbol, row.market);
      setReloadTick((t) => t + 1);
    } catch (e) {
      setRowErr({ key, msg: errText(e) });
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="panel flex flex-col gap-4 px-4 py-4" aria-labelledby="member-watchlist-h">
      <header className="flex flex-wrap items-baseline justify-between gap-2">
        <h1 id="member-watchlist-h" className="m-0 text-lg font-bold">
          Your watchlist
        </h1>
        {rows && (
          <span className="text-[0.8rem]" style={{ color: "var(--dim)" }}>
            {rows.length} {rows.length === 1 ? "symbol" : "symbols"}
          </span>
        )}
      </header>

      <div className="flex flex-col gap-2">
        <label htmlFor="member-watch-search" className="text-[0.75rem] tracking-wide" style={{ color: "var(--dim)" }}>
          Add a US stock
        </label>
        <input
          id="member-watch-search"
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Ticker or company name"
          autoComplete="off"
          className="chip min-h-[40px] w-full max-w-[28rem] px-3"
          style={{ color: "var(--text)" }}
        />
        {addErr && (
          <p role="alert" className="m-0 text-[0.8rem]" style={{ color: "var(--ask)" }}>
            {addErr}
          </p>
        )}
        {shown.length > 0 && (
          <ul className="m-0 flex max-w-[40rem] list-none flex-col gap-1 p-0">
            {shown.map((c) => (
              <li key={c.ticker} className="flex items-center gap-3 border-b py-1" style={{ borderColor: "var(--border)" }}>
                <span className="mono w-20 shrink-0 font-bold">{c.ticker}</span>
                <span className="min-w-0 flex-1 truncate text-[0.85rem]">{c.name}</span>
                <span className="hidden text-[0.75rem] sm:inline" style={{ color: "var(--faint)" }}>
                  {c.exchange || "—"}
                </span>
                {c.tracked ? (
                  <button
                    type="button"
                    onClick={() => void watch(c.ticker)}
                    disabled={busy === `add:${c.ticker}`}
                    className="chip min-h-[36px] cursor-pointer px-3 text-[0.8rem]"
                    style={{ color: "var(--accent)" }}
                  >
                    Watch
                  </button>
                ) : (
                  <button type="button" disabled className="chip min-h-[36px] px-3 text-[0.8rem]" style={{ color: "var(--faint)" }}>
                    Not tracked
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>

      {loadErr ? (
        <div className="flex flex-wrap items-center gap-3">
          <p role="alert" className="m-0 text-[0.85rem]" style={{ color: "var(--ask)" }}>
            {loadErr}
          </p>
          <button type="button" onClick={() => setReloadTick((t) => t + 1)} className="chip min-h-[36px] cursor-pointer px-3">
            Retry
          </button>
        </div>
      ) : rows === null ? (
        <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
          Loading your watchlist…
        </p>
      ) : rows.length === 0 ? (
        <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
          Nothing on your watchlist yet. Search above to add a symbol SignalDeck tracks.
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {rows.map((row) => {
            const key = `${row.symbol}|${row.market}`;
            const chips = chipsFor(row);
            return (
              <li key={key} className="flex flex-col gap-2 border-b pb-3" style={{ borderColor: "var(--border)" }}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <Link href={`/s/${row.market}/${encodeURIComponent(row.symbol)}`} className="flex min-w-0 items-baseline gap-2 hover:underline">
                    <span className="mono font-bold">{row.symbol}</span>
                    <span className="truncate text-[0.85rem]" style={{ color: "var(--dim)" }}>
                      {row.name}
                    </span>
                  </Link>
                  <div className="flex items-center gap-3">
                    <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      {row.latestBarTs > 0
                        ? `Data through ${new Date(row.latestBarTs * 1000).toISOString().slice(0, 10)}`
                        : "No data yet"}
                    </span>
                    <button
                      type="button"
                      onClick={() => void unwatch(row)}
                      disabled={busy === `rm:${key}`}
                      className="chip min-h-[36px] cursor-pointer px-3 text-[0.8rem]"
                    >
                      Unwatch
                    </button>
                  </div>
                </div>
                {chips.length > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {chips.map((c) => (
                      <span key={c.label} className="chip px-2 py-1 text-[0.75rem]" style={{ color: chipColor(c.regime) }}>
                        {c.label} {c.regime} · {Math.round(c.accuracy * 100)}% backtest
                      </span>
                    ))}
                  </div>
                ) : (
                  <span className="text-[0.8rem]" style={{ color: "var(--faint)" }}>
                    No validated read for this symbol yet
                  </span>
                )}
                {rowErr?.key === key && (
                  <p role="alert" className="m-0 text-[0.8rem]" style={{ color: "var(--ask)" }}>
                    {rowErr.msg}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Readings are SignalDeck&rsquo;s validated regime forecasts with their backtest accuracy; how they fare live is
        graded on the{" "}
        <Link href="/accuracy" className="underline">
          record
        </Link>
        . Prices are not shown: the market data is licensed and not redistributed. Not financial advice.{" "}
        {/* The chips' accuracies, qualified in the daemon's own words (verbatim). */}
        {caveats.length > 0 ? (
          <HelpTip label="what these accuracies are">
            {caveats.map((c) => (
              <span key={c} className="mb-2 block last:mb-0">
                {c}
              </span>
            ))}
          </HelpTip>
        ) : null}
      </p>
      <HypotheticalNote short />
    </section>
  );
}
