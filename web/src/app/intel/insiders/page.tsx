"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { insiders, pollMs, POLL_SLOW, type InsiderTrade } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { PageHero, StatTile, Reveal, MiniBar } from "@/components/ui/Kit";
import GoalBanner from "@/components/home/GoalBanner";
import ShowAllBar from "@/components/ShowAllBar";
import SortHeader from "@/components/SortHeader";
import { listLimitFor, useGoal } from "@/lib/goal";

type SortKey = "value" | "date" | "symbol";
type SortDir = "asc" | "desc";
type CodeFilter = "all" | "P" | "S";

function codeColor(t: InsiderTrade): string {
  if (t.code === "P") return "var(--bid)";
  if (t.code === "S") return "var(--ask)";
  return "var(--dim)";
}

function fmtUSD(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  if (Math.abs(v) >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (Math.abs(v) >= 1e6) return `$${(v / 1e6).toFixed(2)}M`;
  if (Math.abs(v) >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

// SortHeader now lives in @/components/SortHeader — three tables needed it.

export default function InsidersPage() {
  const [rows, setRows] = useState<InsiderTrade[] | null>(null);
  const [note, setNote] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [code, setCode] = useState<CodeFilter>("all");
  const { symbol } = useIntelSymbol();
  const [retryTick, setRetryTick] = useState(0);
  const [sortKey, setSortKey] = useState<SortKey>("value");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const goal = useGoal();
  // Per-visit override of the goal's default length, not a stored preference.
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    let alive = true;
    const load = () =>
      insiders(symbol || undefined, code === "all" ? undefined : code, 200)
        .then((r) => {
          if (!alive) return;
          setRows(r.trades ?? []);
          setNote(r.note);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (symbol && msg.includes("404")) {
            setRows([]);
            setErr(null);
            return;
          }
          setErr(msg);
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [code, symbol, retryTick]);

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;
  const list = useMemo(() => rows ?? [], [rows]);
  const maxValue = useMemo(
    () => list.reduce((m, t) => (Number.isFinite(t.value) ? Math.max(m, Math.abs(t.value)) : m), 0),
    [list],
  );
  const stats = useMemo(() => {
    if (!list.length) return { count: 0, buys: 0, sells: 0, biggest: 0, newest: "" };
    const buys = list.filter((t) => t.code === "P").length;
    const sells = list.filter((t) => t.code === "S").length;
    const biggest = list.reduce((m, t) => (Number.isFinite(t.value) ? Math.max(m, Math.abs(t.value)) : m), 0);
    const newest = list.reduce((m, t) => (t.filedTs > m ? t.filedTs : m), 0);
    return { count: list.length, buys, sells, biggest, newest: ago(newest) };
  }, [list]);

  const sortedList = useMemo(() => {
    const arr = [...list];
    arr.sort((a, b) => {
      let cmp = 0;
      if (sortKey === "value") cmp = (Math.abs(a.value) || 0) - (Math.abs(b.value) || 0);
      else if (sortKey === "date") cmp = a.filedTs - b.filedTs;
      else cmp = (a.symbol || "").localeCompare(b.symbol || "");
      return sortDir === "asc" ? cmp : -cmp;
    });
    return arr;
  }, [list, sortKey, sortDir]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else { setSortKey(key); setSortDir("desc"); }
  };

  // Table rows, so the "compact" cap — 25 / 50 / all. Applied AFTER the sort,
  // so a trimmed table always shows the top of the reader's own ordering.
  const limit = listLimitFor(goal, "compact");
  const drawn = limit === null || expanded ? sortedList : sortedList.slice(0, limit);

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Insider Trades"
        subtitle="Executives trading their own stock - buys and sells, ranked by size and conviction."
        right={
          <div className="flex flex-wrap gap-2">
            <span className="chip">SEC Form 4 · filed ~2 business days after the trade</span>
            {err !== null && rows !== null && (
              <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
                poll failed — showing last data
              </span>
            )}
            {symbol && (
              <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                {symbol} — from the shared intel filter
              </span>
            )}
          </div>
        }
      />

      <Reveal className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Trades" value={stats.count} i={0} />
        <StatTile label="Buys" value={stats.buys} glow="up" i={1} />
        <StatTile label="Sells" value={stats.sells} glow="down" i={2} />
        <StatTile label="Largest" value={stats.biggest} prefix="$" decimals={0} sub={stats.newest && `newest ${stats.newest}`} i={3} />
      </Reveal>

      <GoalBanner
        note={
          limit === null
            ? "Every parsed trade, uncapped."
            : `The ${limit} largest trades by value. Sort or open the full table any time.`
        }
      />

      {loading && <Skeleton lines={6} label="loading insider trades" />}
      {hardError && (
        <ErrorState
          message={err ?? "insider data unavailable"}
          hint="Is the daemon running? Form 4s are parsed by the filings-poller (~2h sweeps)."
          retry={() => { setErr(null); setRetryTick((t) => t + 1); }}
        />
      )}

      {rows !== null && (
        <section className="panel hud-panel">
          <div className="panel-h flex-wrap gap-2">
            INSIDER TRANSACTIONS
            <span className="chip tnum">{list.length} shown</span>
            <span className="ml-auto flex items-center gap-1" role="tablist" aria-label="code filter">
              {(["all", "P", "S"] as CodeFilter[]).map((c) => (
                <button
                  key={c}
                  type="button"
                  role="tab"
                  aria-selected={c === code}
                  onClick={() => setCode(c)}
                  className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
                  title={c === "P" ? "open-market buys only" : c === "S" ? "open-market sells only" : "all codes"}
                  style={{ color: c === code ? "var(--accent)" : undefined, borderColor: c === code ? "var(--accent)" : undefined }}
                >
                  {c === "P" ? "buys" : c === "S" ? "sells" : "all"}
                </button>
              ))}
            </span>
          </div>
          <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {note} Value bars are scaled relative to the largest trade currently listed.
          </p>
          {list.length === 0 ? (
            symbol || code !== "all" ? (
              <EmptyState className="m-4" message="No parsed insider trades match this filter" detail="Clear the shared symbol filter or widen the code filter." />
            ) : (
              <EmptyState className="m-4" message="No insider trades parsed yet — SEC sweep in progress" detail="Form 4s are fetched and parsed as the filings-poller sweeps (~2h cadence)." />
            )
          ) : (
            <div className="table-wrap">
              <table className="v4-table w-full">
                <thead>
                  {/* Sorting used to be onClick on the <th> itself: a table
                      header is not focusable, so the sort was unreachable by
                      keyboard entirely, and with no aria-sort a screen reader
                      could not tell the table was sortable or which way it
                      was ordered. The control is now a real button and the
                      header carries the state. */}
                  <tr className="text-[0.75rem] uppercase tracking-wider" style={{ color: "var(--dim)" }}>
                    <SortHeader
                      label="Symbol"
                      active={sortKey === "symbol"}
                      dir={sortDir}
                      onSort={() => toggleSort("symbol")}
                    />
                    <th className="text-left">Type</th>
                    <th className="text-left">Insider</th>
                    <th className="text-right">Shares</th>
                    <SortHeader
                      label="Value"
                      active={sortKey === "value"}
                      dir={sortDir}
                      onSort={() => toggleSort("value")}
                      align="right"
                    />
                    <SortHeader
                      label="Filed"
                      active={sortKey === "date"}
                      dir={sortDir}
                      onSort={() => toggleSort("date")}
                      align="right"
                    />
                  </tr>
                </thead>
                <tbody>
                  {drawn.map((t, i) => (
                    <tr
                      key={t.accession}
                      className={`reveal-item ${t.code === "P" ? "border-l-2 border-l-[--bid]" : t.code === "S" ? "border-l-2 border-l-[--ask]" : ""}`}
                      style={{ "--i": Math.min(i, 12) } as React.CSSProperties}
                    >
                      <td><Link href={`/s/stocks/${encodeURIComponent(t.symbol ?? "")}`} className="mono font-bold hover:underline" style={{ color: "var(--text)" }}>{t.symbol}</Link></td>
                      <td><span className="chip" style={{ color: codeColor(t), borderColor: codeColor(t) }} title={t.codeLabel}>{t.codeLabel}</span></td>
                      <td className="truncate max-w-[200px] sm:max-w-[300px]" title={`${t.insider}${t.title ? ` (${t.title})` : ""}`}>
                        <span style={{ color: "var(--text)" }}>{t.insider}</span>
                        {t.title && <span style={{ color: "var(--faint)" }}> ({t.title})</span>}
                      </td>
                      <td className="tnum text-right">{t.shares > 0 ? t.shares.toLocaleString() : "—"}</td>
                      <td className="tnum text-right">
                        <MiniBar value={maxValue > 0 && Number.isFinite(t.value) ? Math.abs(t.value) : 0} max={maxValue} color={codeColor(t)} i={Math.min(i, 12)} />
                        <span className="ml-2">{fmtUSD(t.value)}</span>
                      </td>
                      <td className="tnum text-right" style={{ color: "var(--faint)" }}>{ago(t.filedTs)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <ShowAllBar
                shown={drawn.length}
                total={sortedList.length}
                limit={limit}
                expanded={expanded}
                onToggle={setExpanded}
                noun="trades"
              />
            </div>
          )}
        </section>
      )}
    </div>
  );
}