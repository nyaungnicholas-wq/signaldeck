"use client";

import { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  institutionsByManager,
  institutionsBySymbol,
  institutionsOverview,
  type InstHolding,
  type InstitutionsOverview,
} from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { Reveal, StatTile, PageHero, MiniBar } from "@/components/ui/Kit";

function fmtUSD(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  if (Math.abs(v) >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (Math.abs(v) >= 1e6) return `$${(v / 1e6).toFixed(2)}M`;
  if (Math.abs(v) >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

function fmtShares(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  return v.toLocaleString();
}

function SortArrow({ active, asc }: { active: boolean; asc?: boolean }) {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" className="inline-block ml-1 opacity-50">
      <path
        d={active ? (asc ? "M6 2 L10 7 L2 7 Z" : "M6 10 L2 5 L10 5 Z") : "M6 3 L9 7 L3 7 Z"}
        fill="currentColor"
        className={active ? "text-[var(--hud)]" : "text-[var(--dim)]"}
      />
    </svg>
  );
}

type SortKey = "value" | "shares" | "symbol" | "manager";

function getSortComparator(key: SortKey, asc: boolean) {
  return (a: InstHolding, b: InstHolding) => {
    let cmp = 0;
    if (key === "value") cmp = a.value - b.value;
    else if (key === "shares") cmp = a.shares - b.shares;
    else if (key === "symbol") cmp = (a.symbol ?? "zzz").localeCompare(b.symbol ?? "zzz");
    else if (key === "manager") cmp = a.manager.localeCompare(b.manager);
    return asc ? cmp : -cmp;
  };
}

function ManagerHoldings({ manager }: { manager: string }) {
  const [rows, setRows] = useState<InstHolding[] | null>(null);
  const [note, setNote] = useState("");
  const [cik, setCik] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [sortKey, setSortKey] = useState<SortKey>("value");
  const [sortAsc, setSortAsc] = useState(false);

  useEffect(() => {
    let alive = true;
    institutionsByManager(manager, 200)
      .then((r) => {
        if (!alive) return;
        setRows(r.holdings ?? []);
        setNote(r.note);
        setCik(r.cik);
        setErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setErr(e instanceof Error ? e.message : String(e));
      });
    return () => { alive = false; };
  }, [manager, retryTick]);

  const list = useMemo(() => rows ?? [], [rows]);
  const managerName = list.length > 0 ? list[0].manager : manager;
  const period = list.length > 0 ? list[0].period : "";
  const sortedList = useMemo(() => [...list].sort(getSortComparator(sortKey, sortAsc)), [list, sortKey, sortAsc]);
  const maxValue = useMemo(() => Math.max(...list.map(h => h.value)), [list]);
  const maxShares = useMemo(() => Math.max(...list.map(h => h.shares)), [list]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortAsc(!sortAsc);
    else { setSortKey(key); setSortAsc(key === "symbol" || key === "manager"); }
  };

  if (rows === null && err !== null) {
    return (
      <ErrorState message={err} hint="Is the daemon running? 13F books are stored by the 13f-poller (24h cadence)." retry={() => { setErr(null); setRetryTick(t => t + 1); }} />
    );
  }
  if (rows === null) return <Skeleton lines={6} label="loading manager holdings" />;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title={managerName.toUpperCase()}
        subtitle={`Latest 13F book — ${list.length} positions`}
        right={
          <Link href="/intel/institutions" className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]">
            ← all managers
          </Link>
        }
      />
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Positions" value={list.length} glow="hud" i={0} />
        <StatTile label="Total Value" value={fmtUSD(list.reduce((s,h) => s + h.value, 0))} glow="accent" i={1} />
        <StatTile label="Largest Position" value={fmtUSD(maxValue)} sub={`${list.find(h => h.value === maxValue)?.symbol ?? "?"}`} glow="up" i={2} />
        <StatTile label="Unmatched" value={list.filter(h => !h.symbol).length} glow="down" i={3} />
      </div>
      <section className="panel">
        <div className="panel-h flex-wrap gap-2">
          <span className="chip tnum">quarter end {period}</span>
          <span className="chip tnum">CIK {cik}</span>
          <span className="chip tnum">{list.length} positions</span>
        </div>
        <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {note} Rows marked &ldquo;unmatched&rdquo; mean the filed issuer name couldn&rsquo;t be matched to a tracked ticker — no symbol is ever guessed.
        </p>
        {list.length === 0 ? (
          <EmptyState className="m-4" message="No holdings stored for this manager yet" detail="The 13f-poller rotates through the curated list daily and stores each manager's latest 13F-HR once per report period." />
        ) : (
          <div className="table-wrap">
            <table className="v4-table w-full text-[0.75rem]">
              <thead>
                <tr className="text-left tracking-wider" style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                  <th className="px-4 py-2 font-normal">ISSUER (AS FILED)</th>
                  <th className="px-2 py-2 font-normal cursor-pointer" onClick={() => toggleSort("symbol")}>SYMBOL <SortArrow active={sortKey === "symbol"} asc={sortAsc} /></th>
                  <th className="px-2 py-2 font-normal">CUSIP</th>
                  <th className="px-2 py-2 text-right font-normal cursor-pointer" onClick={() => toggleSort("shares")}>SHARES <SortArrow active={sortKey === "shares"} asc={sortAsc} /></th>
                  <th className="px-4 py-2 text-right font-normal cursor-pointer" onClick={() => toggleSort("value")}>VALUE <SortArrow active={sortKey === "value"} asc={sortAsc} /></th>
                </tr>
              </thead>
              <tbody>
                {sortedList.map((h, i) => (
                  <tr key={h.cusip} className="reveal-item" style={{ "--i": Math.min(i, 11), borderBottom: "1px solid var(--border)" } as React.CSSProperties}>
                    <td className="px-4 py-2 truncate max-w-[200px]" title={h.name} style={{ color: "var(--text)" }}>{h.name}</td>
                    <td className="px-2 py-2">
                      {h.symbol ? (
                        <Link href={`/s/stocks/${encodeURIComponent(h.symbol)}`} className="mono font-bold text-[var(--text)] transition-colors duration-150 hover:text-[var(--accent)]">
                          {h.symbol}
                        </Link>
                      ) : (
                        <span title="issuer name not matched to a tracked symbol" style={{ color: "var(--faint)" }}>unmatched</span>
                      )}
                    </td>
                    <td className="tnum px-2 py-2" style={{ color: "var(--dim)" }}>{h.cusip}</td>
                    <td className="px-2 py-2 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <span className="tnum" style={{ color: "var(--dim)" }}>{fmtShares(h.shares)}</span>
                        <div className="w-16"><MiniBar value={h.shares} max={maxShares} color="var(--hud)" i={i} /></div>
                      </div>
                    </td>
                    <td className="px-4 py-2 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <span className="tnum" style={{ color: "var(--text)" }}>{fmtUSD(h.value)}</span>
                        <div className="w-16"><MiniBar value={h.value} max={maxValue} color="var(--accent)" i={i} /></div>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function ManagersOverview() {
  const [resp, setResp] = useState<InstitutionsOverview | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    institutionsOverview()
      .then((r) => { if (!alive) return; setResp(r); setErr(null); })
      .catch((e: unknown) => { if (!alive) return; setErr(e instanceof Error ? e.message : String(e)); });
    return () => { alive = false; };
  }, [retryTick]);

  if (resp === null && err !== null) {
    return <ErrorState message={err} hint="Is the daemon running? The 13f-poller needs nothing but SEC EDGAR." retry={() => { setErr(null); setRetryTick(t => t + 1); }} />;
  }
  if (resp === null) return <Skeleton lines={6} label="loading institutional managers" />;

  const stored = resp.managers ?? [];
  const storedCiks = new Set(stored.map((m) => m.cik));
  const pending = resp.curated.filter((c) => !storedCiks.has(String(c.cik)));
  const totalValue = stored.reduce((sum, m) => sum + (m.totalValue || 0), 0);
  const newestPeriod = stored.length > 0 ? stored[0].period : "";
  const storedCount = stored.length;

  return (
    <div className="page-enter space-y-4">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Stored Books" value={storedCount} glow="hud" i={0} />
        <StatTile label="Curated Managers" value={resp.curated.length} glow="accent" i={1} />
        <StatTile label="Total Value" value={fmtUSD(totalValue)} glow="up" i={2} />
        <StatTile label="Pending Sweep" value={pending.length} sub={newestPeriod ? `newest: ${newestPeriod}` : ""} glow="down" i={3} />
      </div>
      <Reveal className="flex flex-col gap-4">
        <section className="panel hud-panel">
          <div className="panel-h flex-wrap gap-2">
            STORED 13F BOOKS
            <span className="chip tnum">{storedCount} of {resp.curated.length}</span>
          </div>
          <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>{resp.note}</p>
          {stored.length === 0 ? (
            <EmptyState className="m-4" message="No 13F books stored yet — SEC sweep in progress" detail="The 13f-poller rotates through the curated managers (rate-limited per SEC policy, runs at daemon boot and daily) — books appear within ~2h of a completed sweep." />
          ) : (
            <ul style={{ borderTop: "1px solid var(--border)" }}>
              {stored.map((m, i) => (
                <li key={m.cik} className="reveal-item" style={{ "--i": Math.min(i, 11), borderBottom: "1px solid var(--border)" } as React.CSSProperties}>
                  <Link href={`/intel/institutions?manager=${encodeURIComponent(m.cik)}`} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 transition-colors duration-150 hover:bg-[var(--panel2)]">
                    <span className="font-bold" style={{ color: "var(--text)" }}>{m.manager}</span>
                    <span className="chip tnum">quarter end {m.period}</span>
                    <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>{m.positions} positions</span>
                    <span className="tnum ml-auto" style={{ color: "var(--text)" }}>{fmtUSD(m.totalValue)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
        {pending.length > 0 && (
          <section className="panel">
            <div className="panel-h flex-wrap gap-2">
              CURATED — NOT YET STORED
              <span className="chip tnum">{pending.length}</span>
            </div>
            <div className="flex flex-wrap gap-2 px-4 py-3">
              {pending.map((c) => (
                <Link key={c.cik} href={`/intel/institutions?manager=${encodeURIComponent(String(c.cik))}`} className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]">
                  {c.name}
                </Link>
              ))}
            </div>
          </section>
        )}
      </Reveal>
    </div>
  );
}

function SymbolHolders({ symbol }: { symbol: string }) {
  const [rows, setRows] = useState<InstHolding[] | null>(null);
  const [note, setNote] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [sortKey, setSortKey] = useState<SortKey>("value");
  const [sortAsc, setSortAsc] = useState(false);

  useEffect(() => {
    let alive = true;
    institutionsBySymbol(symbol, 100)
      .then((r) => {
        if (!alive) return;
        setRows(r.holdings ?? []);
        setNote(r.note);
        setErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        const msg = e instanceof Error ? e.message : String(e);
        if (msg.includes("404")) { setRows([]); setErr(null); return; }
        setErr(msg);
      });
    return () => { alive = false; };
  }, [symbol, retryTick]);

  const list = useMemo(() => rows ?? [], [rows]);
  const sortedList = useMemo(() => [...list].sort(getSortComparator(sortKey, sortAsc)), [list, sortKey, sortAsc]);
  const maxValue = useMemo(() => Math.max(...list.map(h => h.value)), [list]);
  const maxShares = useMemo(() => Math.max(...list.map(h => h.shares)), [list]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortAsc(!sortAsc);
    else { setSortKey(key); setSortAsc(key === "manager"); }
  };

  if (rows === null && err !== null) {
    return <ErrorState message={err} hint="Is the daemon running? 13F books are stored by the 13f-poller." retry={() => { setErr(null); setRetryTick(t => t + 1); }} />;
  }
  if (rows === null) return <Skeleton lines={4} label={`loading ${symbol} holders`} />;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title={`WHO HOLDS ${symbol}`}
        subtitle={`${rows.length} curated managers`}
        right={<span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>from the shared intel filter</span>}
      />
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Managers" value={rows.length} glow="hud" i={0} />
        <StatTile label="Total Shares" value={fmtShares(rows.reduce((s,h) => s + h.shares, 0))} glow="accent" i={1} />
        <StatTile label="Total Value" value={fmtUSD(rows.reduce((s,h) => s + h.value, 0))} glow="up" i={2} />
        <StatTile label="Largest Holder" value={rows.length > 0 ? rows.sort((a,b) => b.value - a.value)[0].manager : "?"} sub={fmtUSD(maxValue)} glow="down" i={3} />
      </div>
      <section className="panel hud-panel">
        {note && <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>{note}</p>}
        {rows.length === 0 ? (
          <EmptyState className="m-4" message={`No stored 13F position in ${symbol}`} detail="Either no curated manager reported it last quarter, that ticker isn't tracked, or the 13f-poller hasn't swept the relevant books yet." />
        ) : (
          <div className="table-wrap">
            <table className="v4-table w-full text-[0.75rem]">
              <thead>
                <tr className="text-left tracking-wider" style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                  <th className="px-4 py-2 font-normal cursor-pointer" onClick={() => toggleSort("manager")}>MANAGER <SortArrow active={sortKey === "manager"} asc={sortAsc} /></th>
                  <th className="px-2 py-2 font-normal">PERIOD</th>
                  <th className="px-2 py-2 text-right font-normal cursor-pointer" onClick={() => toggleSort("shares")}>SHARES <SortArrow active={sortKey === "shares"} asc={sortAsc} /></th>
                  <th className="px-4 py-2 text-right font-normal cursor-pointer" onClick={() => toggleSort("value")}>VALUE <SortArrow active={sortKey === "value"} asc={sortAsc} /></th>
                </tr>
              </thead>
              <tbody>
                {sortedList.map((h, i) => (
                  <tr key={`${h.manager}:${h.cusip}`} className="reveal-item" style={{ "--i": Math.min(i, 11), borderBottom: "1px solid var(--border)" } as React.CSSProperties}>
                    <td className="px-4 py-2 font-bold" style={{ color: "var(--text)" }}>{h.manager}</td>
                    <td className="tnum px-2 py-2" style={{ color: "var(--faint)" }}>{h.period}</td>
                    <td className="px-2 py-2 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <span className="tnum" style={{ color: "var(--dim)" }}>{fmtShares(h.shares)}</span>
                        <div className="w-16"><MiniBar value={h.shares} max={maxShares} color="var(--hud)" i={i} /></div>
                      </div>
                    </td>
                    <td className="px-4 py-2 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <span className="tnum" style={{ color: "var(--text)" }}>{fmtUSD(h.value)}</span>
                        <div className="w-16"><MiniBar value={h.value} max={maxValue} color="var(--accent)" i={i} /></div>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function InstitutionsInner() {
  const params = useSearchParams();
  const manager = params.get("manager") ?? "";
  const { symbol } = useIntelSymbol();

  return (
    <div className="flex flex-col gap-4">
      <PageHero
        title="Institutions"
        subtitle="Where the big money moved — 13F position changes sorted by impact."
        right={<span className="chip">SEC 13F-HR · quarterly, filed up to 45 days after quarter end</span>}
      />
      {symbol ? (
        <SymbolHolders symbol={symbol} />
      ) : manager ? (
        <ManagerHoldings manager={manager} />
      ) : (
        <ManagersOverview />
      )}
    </div>
  );
}

export default function InstitutionsPage() {
  return (
    <Suspense fallback={<Skeleton lines={6} label="loading institutions" />}>
      <InstitutionsInner />
    </Suspense>
  );
}
