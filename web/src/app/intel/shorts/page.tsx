"use client";

import { useEffect, useState, useMemo } from "react";
import Link from "next/link";
import {
  pollMs,
  POLL_SLOW,
  shortsExtremes,
  shortsSymbol,
  type ShortsExtreme,
  type ShortsExtremesResponse,
  type ShortsSymbolResponse,
  type ShortVolumePoint,
} from "@/lib/api";
import HelpTip from "@/components/HelpTip";
import SortHeader from "@/components/SortHeader";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { Reveal, PageHero, StatTile, MiniBar } from "@/components/ui/Kit";

function fmtVol(v: number): string {
  if (!isFinite(v)) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

function fmtPct(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

type SortKey = "symbol" | "day" | "shortPct" | "shortVol" | "totalVol";

function SortIcon({ active, asc }: { active: boolean; asc?: boolean }) {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" className="inline ml-1" style={{ opacity: active ? 1 : 0.3 }}>
      <polygon points={asc ? "5,1 9,6 1,6" : "5,9 9,4 1,4"} fill="currentColor" />
    </svg>
  );
}

function SortBtn({ label, sortKey, current, asc, onClick }: { label: string; sortKey: SortKey; current: SortKey; asc: boolean; onClick: () => void }) {
  return (
    <button onClick={onClick} className="chip text-[0.7rem] cursor-pointer border px-2 py-0.5" style={{ color: current === sortKey ? "var(--hud)" : "var(--dim)", borderColor: current === sortKey ? "var(--hud)" : "var(--border)" }}>
      {label}
      <SortIcon active={current === sortKey} asc={asc} />
    </button>
  );
}

export default function ShortsPage() {
  const { symbol } = useIntelSymbol();
  const [extremes, setExtremes] = useState<ShortsExtremesResponse | null>(null);
  const [series, setSeries] = useState<ShortsSymbolResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [sortKey, setSortKey] = useState<SortKey>("shortPct");
  const [sortAsc, setSortAsc] = useState(false);

  useEffect(() => {
    let alive = true;
    const load = () => {
      if (symbol) {
        shortsSymbol(symbol, 30).then((r) => { if (!alive) return; setSeries(r); setErr(null); }).catch((e: unknown) => { if (!alive) return; setSeries(null); setErr(e instanceof Error ? e.message : String(e)); });
      } else {
        shortsExtremes(20).then((r) => { if (!alive) return; setExtremes(r); setErr(null); }).catch((e: unknown) => { if (!alive) return; setExtremes(null); setErr(e instanceof Error ? e.message : String(e)); });
      }
    };
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => { alive = false; stop(); };
  }, [symbol, retryTick]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortAsc(!sortAsc);
    else { setSortKey(key); setSortAsc(key === "symbol" || key === "day"); }
  };

  const data = symbol ? series : extremes;
  const loading = data === null && err === null;
  const hardError = data === null && err !== null;
  const caveat = data?.caveat ?? "";
  const note = data?.note ?? "";

  const sortedExtremes = useMemo(() => {
    if (!extremes?.extremes) return [];
    const arr = [...extremes.extremes];
    arr.sort((a, b) => {
      let cmp = 0;
      if (sortKey === "symbol") cmp = a.symbol.localeCompare(b.symbol);
      else if (sortKey === "day") cmp = a.day.localeCompare(b.day);
      else if (sortKey === "shortPct") cmp = (a.shortPct ?? 0) - (b.shortPct ?? 0);
      else if (sortKey === "shortVol") cmp = (a.shortVol ?? 0) - (b.shortVol ?? 0);
      else cmp = (a.totalVol ?? 0) - (b.totalVol ?? 0);
      return sortAsc ? cmp : -cmp;
    });
    return arr;
  }, [extremes, sortKey, sortAsc]);

  const maxRatio = useMemo(() => {
    if (!sortedExtremes.length) return 1;
    return Math.max(...sortedExtremes.map((e) => e.shortPct ?? 0), 0.01);
  }, [sortedExtremes]);

  const maxShortVol = useMemo(() => {
    if (!sortedExtremes.length) return 1;
    return Math.max(...sortedExtremes.map((e) => e.shortVol ?? 0), 1);
  }, [sortedExtremes]);

  const heroStats = useMemo(() => {
    if (symbol && series?.series?.length) {
      const s = series.series;
      const avgRatio = s.reduce((acc, p) => acc + (p.shortPct ?? 0), 0) / s.length;
      const totalShort = s.reduce((acc, p) => acc + (p.shortVol ?? 0), 0);
      return [
        { label: "Days Tracked", value: s.length },
        { label: "Avg Ratio", value: avgRatio * 100, decimals: 1, suffix: "%" },
        { label: "Total Short Vol", value: totalShort, decimals: 0, sub: fmtVol(totalShort) },
        series?.latestZ != null ? { label: "Latest Z-Score", value: series.latestZ, decimals: 2, glow: "hud" as const } : null,
      ].filter((x) => x !== null);
    }
    if (extremes?.extremes?.length) {
      const e = extremes.extremes;
      // `e` is shortsExtremes(20) — the 20 HIGHEST short-ratio symbols, not the
      // market. Summing them and calling it "Total Short Vol" is the recurring
      // partial-sum-labelled-total defect; the mean over the 20 most extreme
      // rows is likewise biased far above the market average. Both are labelled
      // for the subset they actually cover.
      const avgRatio = e.reduce((acc, x) => acc + (x.shortPct ?? 0), 0) / e.length;
      const totalShort = e.reduce((acc, x) => acc + (x.shortVol ?? 0), 0);
      return [
        { label: "Symbols", value: e.length },
        { label: `Avg Ratio (top ${e.length})`, value: avgRatio * 100, decimals: 1, suffix: "%" },
        { label: "Highest Ratio", value: (e[0]?.shortPct ?? 0) * 100, decimals: 1, suffix: "%", glow: "accent" as const },
        { label: `Short Vol (top ${e.length})`, value: totalShort, decimals: 0, sub: fmtVol(totalShort) },
      ];
    }
    return [];
  }, [symbol, series, extremes]);

  const chipStyle = { color: "var(--warn)", borderColor: "var(--warn)" };

  const controls = (
    <>
      <span className="chip" style={chipStyle}>NOT short interest — volume ratio only</span>
      {err !== null && data !== null && <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>poll failed</span>}
    </>
  );

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Short Interest"
        subtitle="Who is betting against what — short interest and squeeze pressure, ranked."
        right={controls}
      />

      {loading && <Skeleton lines={6} label="loading short sale volume" />}
      {hardError && (
        <ErrorState
          message={err ?? "short volume data unavailable"}
          hint={symbol ? "Only tracked stocks have Reg SHO rows — clear the shared symbol filter or check the daemon." : "Is the daemon running? The finra-shorts worker ingests FINRA's daily file after ~6:30pm ET."}
          retry={() => { setErr(null); setRetryTick((t) => t + 1); }}
        />
      )}

      {heroStats.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {heroStats.map((s, i) => (
            <StatTile key={s.label} label={s.label} value={s.value} decimals={s.decimals} suffix={s.suffix} sub={s.sub} glow={s.glow} i={i} />
          ))}
        </div>
      )}

      {data !== null && (
        <section className="hud-panel">
          <div className="panel-h flex flex-wrap items-center gap-2">
            <span>{symbol ? `DAILY SHORT SALE VOLUME — ${symbol}` : "HIGHEST SHORT VOLUME RATIOS"}</span>
            {extremes?.day && !symbol && <span className="chip tnum">{extremes.day}</span>}
            {symbol && series?.latestZ != null && (
              <span className="flex items-center gap-1">
                <span className="chip tnum">z {series.latestZ.toFixed(2)}</span>
                <HelpTip label="what this z-score means">{series.zNote}</HelpTip>
              </span>
            )}
          </div>

          <p className="px-4 py-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {caveat}. {note}
            {!symbol && extremes ? ` ${extremes.floorNote} (minTotalVol ${fmtVol(extremes.minTotalVol)}).` : ""}
          </p>

          {symbol ? (
            !series?.series?.length ? (
              <EmptyState className="m-4" message={`No Reg SHO rows for ${symbol}`} detail="Rows accrue one trading day at a time." />
            ) : (
              <Reveal>
                <div className="table-wrap">
                  <table className="v4-table w-full text-[0.75rem]">
                    <thead>
                      <tr className="text-left" style={{ color: "var(--dim)" }}>
                        <th className="px-4 py-2 font-normal">DAY</th>
                        <th className="px-4 py-2 font-normal">RATIO</th>
                        <th className="px-4 py-2 font-normal">SHORT VOL</th>
                        <th className="px-4 py-2 font-normal">EXEMPT</th>
                        <th className="px-4 py-2 font-normal">TOTAL VOL</th>
                      </tr>
                    </thead>
                    <tbody>
                      {[...series.series].reverse().map((p: ShortVolumePoint, i) => (
                        <tr key={p.day} className="reveal-item" style={{ "--i": Math.min(i, 12) } as React.CSSProperties}>
                          <td className="px-4 py-2 tnum">{p.day}</td>
                          <td className="px-4 py-2">
                            <div className="flex items-center gap-2">
                              <span className="tnum" style={{ width: "3.5rem" }}>{fmtPct(p.shortPct)}</span>
                              <MiniBar value={p.shortPct ?? 0} max={1} color="var(--accent)" height={5} />
                            </div>
                          </td>
                          <td className="px-4 py-2 tnum">{fmtVol(p.shortVol)}</td>
                          <td className="px-4 py-2 tnum">{fmtVol(p.shortExempt)}</td>
                          <td className="px-4 py-2 tnum">{fmtVol(p.totalVol)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Reveal>
            )
          ) : !extremes?.extremes?.length ? (
            <EmptyState className="m-4" message="No Reg SHO data stored yet" detail={extremes?.emptyNote ?? "The finra-shorts worker ingests after ~6:30pm ET."} />
          ) : (
            <>
              <div className="px-4 pb-2 flex flex-wrap gap-1.5">
                {(["shortPct", "shortVol", "totalVol", "day", "symbol"] as SortKey[]).map((k) => (
                  <SortBtn key={k} label={{ shortPct: "Ratio", shortVol: "Short Vol", totalVol: "Total Vol", day: "Date", symbol: "Symbol" }[k]} sortKey={k} current={sortKey} asc={sortAsc} onClick={() => toggleSort(k)} />
                ))}
              </div>
              <Reveal>
                <div className="table-wrap">
                  <table className="v4-table w-full text-[0.75rem]">
                    <thead>
                      {/* These were onClick on the <th> itself: sortable with
                          a mouse, unreachable by keyboard, and never announced.
                          SortHeader puts aria-sort on the cell and a real
                          button inside it. */}
                      <tr className="text-left" style={{ color: "var(--dim)" }}>
                        <SortHeader label="Symbol" active={sortKey === "symbol"} dir={sortAsc ? "asc" : "desc"} onSort={() => toggleSort("symbol")} className="px-4 py-2 font-normal" />
                        <SortHeader label="Day" active={sortKey === "day"} dir={sortAsc ? "asc" : "desc"} onSort={() => toggleSort("day")} className="px-4 py-2 font-normal" />
                        <SortHeader label="Ratio" active={sortKey === "shortPct"} dir={sortAsc ? "asc" : "desc"} onSort={() => toggleSort("shortPct")} className="px-4 py-2 font-normal" />
                        <th className="px-4 py-2 font-normal">LAST 30D</th>
                        <SortHeader label="Short vol" active={sortKey === "shortVol"} dir={sortAsc ? "asc" : "desc"} onSort={() => toggleSort("shortVol")} className="px-4 py-2 font-normal" />
                        <SortHeader label="Total vol" active={sortKey === "totalVol"} dir={sortAsc ? "asc" : "desc"} onSort={() => toggleSort("totalVol")} className="px-4 py-2 font-normal" />
                      </tr>
                    </thead>
                    <tbody>
                      {sortedExtremes.map((e: ShortsExtreme, i) => (
                        <tr key={e.symbolId} className="reveal-item" style={{ "--i": Math.min(i, 12) } as React.CSSProperties}>
                          <td className="px-4 py-2">
                            <Link href={`/s/stocks/${encodeURIComponent(e.symbol)}`} className="mono font-bold hover:underline" style={{ color: "var(--accent)" }}>{e.symbol}</Link>
                          </td>
                          <td className="px-4 py-2 tnum">{e.day}</td>
                          <td className="px-4 py-2">
                            <div className="flex items-center gap-2">
                              <span className="tnum" style={{ width: "3.5rem" }}>{fmtPct(e.shortPct)}</span>
                              <MiniBar value={e.shortPct ?? 0} max={maxRatio} color="var(--accent)" height={5} />
                            </div>
                          </td>
                          <td className="px-4 py-2">
                            {e.spark?.length >= 5 ? (
                              <svg width="110" height="22" role="img" aria-label="30d ratio trend">
                                <polyline
                                  points={(() => { const pts = e.spark.filter((v) => Number.isFinite(v)); const min = Math.min(...pts); const max = Math.max(...pts); const span = max - min || 1; return pts.map((v, j) => `${(j / (pts.length - 1)) * 110},${20 - ((v - min) / span) * 16 - 2}`).join(" "); })()}
                                  fill="none" stroke="var(--accent)" strokeWidth="1.5" className="draw-path" />
                              </svg>
                            ) : (
                              <span className="text-[0.65rem]" style={{ color: "var(--faint)" }}>—</span>
                            )}
                          </td>
                          <td className="px-4 py-2">
                            <div className="flex items-center gap-2">
                              <span className="tnum">{fmtVol(e.shortVol)}</span>
                              <MiniBar value={e.shortVol ?? 0} max={maxShortVol} color="var(--hud)" height={5} />
                            </div>
                          </td>
                          <td className="px-4 py-2 tnum">{fmtVol(e.totalVol)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Reveal>
            </>
          )}
        </section>
      )}
    </div>
  );
}
