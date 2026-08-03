"use client";
import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { signalReport, type Market, type SignalReport } from "@/lib/api";
import { ago, fmtPrice } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import { Reveal, AnimatedNumber, Spark, PageHero, MiniBar } from "@/components/ui/Kit";

const CONTEXT_ROWS: { label: string; key: string; higherBetter?: boolean; fmt: (c: Record<string, number>) => [number, string] }[] = [
  { label: "Last Close", key: "lastClose", higherBetter: true, fmt: (c) => [c.lastClose ?? NaN, fmtPrice(c.lastClose)] },
  { label: "52W Low", key: "low52w", higherBetter: true, fmt: (c) => [c.low52w ?? NaN, fmtPrice(c.low52w)] },
  { label: "52W High", key: "high52w", higherBetter: true, fmt: (c) => [c.high52w ?? NaN, fmtPrice(c.high52w)] },
  { label: "Off 52W High", key: "offHigh52Pct", higherBetter: false, fmt: (c) => [c.offHigh52Pct ?? NaN, c.offHigh52Pct == null ? "—" : `${c.offHigh52Pct.toFixed(1)}%`] },
  { label: "ATR(14) %", key: "atr14Pct", higherBetter: undefined, fmt: (c) => [c.atr14Pct ?? NaN, c.atr14Pct == null ? "—" : `${c.atr14Pct.toFixed(2)}%`] },
  { label: "SMA50", key: "sma50", higherBetter: true, fmt: (c) => [c.sma50 ?? NaN, fmtPrice(c.sma50)] },
  { label: "SMA200", key: "sma200", higherBetter: true, fmt: (c) => [c.sma200 ?? NaN, fmtPrice(c.sma200)] },
  { label: "Avg $ Vol (21d)", key: "avgDollarVol21", higherBetter: true, fmt: (c) => [c.avgDollarVol21 ?? NaN, c.avgDollarVol21 == null ? "—" : c.avgDollarVol21.toLocaleString(undefined, { maximumFractionDigits: 0 })] },
];
const COLORS = ["var(--hud)", "var(--accent)", "var(--crossed)", "var(--bid)"];

function TugWarRow({ label, aVal, aFmt, bVal, bFmt, higherBetter, idx }: { label: string; aVal: number; aFmt: string; bVal: number; bFmt: string; higherBetter?: boolean; idx: number }) {
  const aNum = Number.isFinite(aVal) ? aVal : 0;
  const bNum = Number.isFinite(bVal) ? bVal : 0;
  const max = Math.max(aNum, bNum, 0.00001);
  let aColor = "var(--dim)", bColor = "var(--dim)";
  if (higherBetter === true) { aColor = aNum > bNum ? "var(--bid)" : aNum < bNum ? "var(--ask)" : "var(--hud)"; bColor = bNum > aNum ? "var(--bid)" : bNum < aNum ? "var(--ask)" : "var(--hud)"; }
  else if (higherBetter === false) { aColor = aNum < bNum ? "var(--bid)" : aNum > bNum ? "var(--ask)" : "var(--hud)"; bColor = bNum < aNum ? "var(--bid)" : bNum > aNum ? "var(--ask)" : "var(--hud)"; }
  return (
    <div className="reveal-item" style={{ "--i": idx } as React.CSSProperties}>
      <div className="grid grid-cols-[minmax(64px,1fr)_minmax(120px,1.5fr)_minmax(96px,auto)_minmax(120px,1.5fr)_minmax(64px,1fr)] items-center gap-2 px-4 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
        <span className="tnum text-right" style={{ color: aColor }}>{aNum > 0 ? aFmt : "—"}</span>
        <MiniBar value={aNum} max={max} color={aColor} i={idx} />
        <div className="text-center px-2 text-[0.75rem] font-medium truncate" style={{ color: "var(--text)" }}>{label}</div>
        <MiniBar value={bNum} max={max} color={bColor} i={idx} />
        <span className="tnum" style={{ color: bColor }}>{bNum > 0 ? bFmt : "—"}</span>
      </div>
    </div>
  );
}

function CompareInner() {
  const sp = useSearchParams();
  const aSymbol = (sp.get("a") ?? "NVDA").toUpperCase();
  const aMarket = (sp.get("am") === "crypto" ? "crypto" : "stocks") as Market;
  const bSymbol = (sp.get("b") ?? "AMD").toUpperCase();
  const bMarket = (sp.get("bm") === "crypto" ? "crypto" : "stocks") as Market;
  const [draftA, setDraftA] = useState(aSymbol);
  const [draftB, setDraftB] = useState(bSymbol);
  const [draftAm, setDraftAm] = useState<Market>(aMarket);
  const [draftBm, setDraftBm] = useState<Market>(bMarket);
  const [repA, setRepA] = useState<SignalReport | null>(null);
  const [repB, setRepB] = useState<SignalReport | null>(null);
  const [errA, setErrA] = useState<string | null>(null);
  const [errB, setErrB] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const retry = () => setTick(t => t + 1);
  const navigate = (na: { symbol: string; market: Market }, nb: { symbol: string; market: Market }) => {
    const q = new URLSearchParams({ a: na.symbol, b: nb.symbol });
    if (na.market !== "stocks") q.set("am", na.market);
    if (nb.market !== "stocks") q.set("bm", nb.market);
    window.history.replaceState(null, "", `/watchlist/compare?${q.toString()}`);
  };
  const urlKey = `${aMarket}:${aSymbol}|${bMarket}:${bSymbol}`;
  const [prevUrlKey, setPrevUrlKey] = useState(urlKey);
  if (urlKey !== prevUrlKey) { setPrevUrlKey(urlKey); setDraftA(aSymbol); setDraftB(bSymbol); setDraftAm(aMarket); setDraftBm(bMarket); }
  const fetchKey = `${urlKey}#${tick}`;
  const [prevFetchKey, setPrevFetchKey] = useState(fetchKey);
  if (fetchKey !== prevFetchKey) { setPrevFetchKey(fetchKey); setRepA(null); setRepB(null); }
  useEffect(() => {
    let dead = false;
    signalReport(aSymbol, aMarket, "overview").then(r => !dead && (setRepA(r), setErrA(null))).catch(e => !dead && setErrA(e instanceof Error ? e.message : String(e)));
    signalReport(bSymbol, bMarket, "overview").then(r => !dead && (setRepB(r), setErrB(null))).catch(e => !dead && setErrB(e instanceof Error ? e.message : String(e)));
    return () => { dead = true; };
  }, [aSymbol, aMarket, bSymbol, bMarket, tick]);
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const na = draftA.trim().toUpperCase();
    const nb = draftB.trim().toUpperCase();
    if (!na || !nb) return;
    navigate({ symbol: na, market: draftAm }, { symbol: nb, market: draftBm });
  };
  const bothLoaded = repA && repB;
  const aHeadline = repA?.tradeContext?.lastClose ?? NaN;
  const bHeadline = repB?.tradeContext?.lastClose ?? NaN;
  const aSpark = repA?.tradeContext ? [repA.tradeContext.low52w, repA.tradeContext.sma50, repA.tradeContext.sma200, repA.tradeContext.lastClose, repA.tradeContext.high52w].filter(v => v != null) as number[] : [];
  const bSpark = repB?.tradeContext ? [repB.tradeContext.low52w, repB.tradeContext.sma50, repB.tradeContext.sma200, repB.tradeContext.lastClose, repB.tradeContext.high52w].filter(v => v != null) as number[] : [];
  const symbolChips = (
    <form onSubmit={submit} className="flex flex-wrap gap-2">
      {([
        { label: "A", val: draftA, setVal: setDraftA, mkt: draftAm, setMkt: setDraftAm },
        { label: "B", val: draftB, setVal: setDraftB, mkt: draftBm, setMkt: setDraftBm },
      ] as { label: string; val: string; setVal: (v: string) => void; mkt: Market; setMkt: (m: Market) => void }[]).map(({ label, val, setVal, mkt, setMkt }) => (
        <div key={label} className="flex items-center gap-1">
          <span className="mono text-xs" style={{ color: "var(--faint)" }}>{label}</span>
          <input value={val} onChange={e => setVal(e.target.value)} aria-label={`symbol ${label}`} spellCheck={false} autoCapitalize="characters" className="mono w-20 rounded border bg-transparent px-2 py-1 text-sm uppercase outline-none focus:border-[var(--accent)]" style={{ borderColor: "var(--border)" }} />
          <select value={mkt} onChange={e => setMkt(e.target.value as Market)} className="rounded border bg-transparent px-1 py-1 text-xs" style={{ borderColor: "var(--border)", color: "var(--dim)" }}>
            <option value="stocks">stocks</option>
            <option value="crypto">crypto</option>
          </select>
        </div>
      ))}
      <button type="submit" className="chip cursor-pointer px-3 py-1 text-xs tracking-wider transition-colors hover:border-[var(--accent)] hover:text-[var(--accent)]">compare</button>
      <button type="button" onClick={() => navigate({ symbol: bSymbol, market: bMarket }, { symbol: aSymbol, market: aMarket })} className="chip cursor-pointer px-3 py-1 text-xs tracking-wider transition-colors hover:border-[var(--accent)] hover:text-[var(--accent)]">⇄ swap</button>
    </form>
  );
  return (
    <div className="page-enter space-y-4">
      <PageHero title="Compare" subtitle="Symbols side by side — same metrics, same scale, so differences jump out." right={symbolChips} />
      {errA && <ErrorState message={`${aSymbol}: ${errA}`} retry={retry} />}
      {errB && <ErrorState message={`${bSymbol}: ${errB}`} retry={retry} />}
      {!bothLoaded && !errA && !errB && <Skeleton lines={12} />}
      {bothLoaded && (
        <>
          <section className="hud-panel relative p-6">
            <div className="grid grid-cols-2 gap-4">
              {[{ sym: aSymbol, mkt: aMarket, val: aHeadline, spark: aSpark, color: COLORS[0], asOf: repA!.asOf }, { sym: bSymbol, mkt: bMarket, val: bHeadline, spark: bSpark, color: COLORS[1], asOf: repB!.asOf }].map((side, i) => (
                <div key={i} className="flex flex-col items-center gap-2">
                  <span className="mono text-sm font-bold" style={{ color: side.color }}>{side.sym}</span>
                  <AnimatedNumber value={side.val} decimals={2} prefix="$" className={`num-hero text-4xl ${Number.isFinite(side.val) && side.val > 0 ? (i === (aHeadline > bHeadline ? 0 : 1) ? 'glow-up' : 'glow-down') : ''}`} />
                  <div className="w-32 h-8"><Spark data={side.spark} width={128} height={32} color={side.color} /></div>
                  <span className="text-xs" style={{ color: "var(--faint)" }}>as of {ago(side.asOf)}</span>
                </div>
              ))}
            </div>
          </section>
          <Reveal className="space-y-1">
            {CONTEXT_ROWS.map((row, i) => {
              const aVal = repA?.tradeContext ? row.fmt(repA.tradeContext)[0] : NaN;
              const bVal = repB?.tradeContext ? row.fmt(repB.tradeContext)[0] : NaN;
              return <TugWarRow key={row.key} label={row.label} aVal={aVal} aFmt={repA?.tradeContext ? row.fmt(repA.tradeContext)[1] : "—"} bVal={bVal} bFmt={repB?.tradeContext ? row.fmt(repB.tradeContext)[1] : "—"} higherBetter={row.higherBetter} idx={i} />;
            })}
          </Reveal>
          <section className="panel p-4">
            <div className="panel-h">REGIME STACK & PREDICTIONS</div>
            <div className="grid grid-cols-2 gap-4 mt-2">
              {[{ rep: repA, sym: aSymbol, mkt: aMarket }, { rep: repB, sym: bSymbol, mkt: bMarket }].map((side, i) => (
                <div key={i} className="space-y-2">
                  <div className="text-sm font-bold mono" style={{ color: COLORS[i] }}>{side.sym}</div>
                  {side.rep.regimeStack?.map(f => (
                    <div key={f.kind} className="chip text-xs px-2 py-0.5">{f.kind}: {f.regime}</div>
                  ))}
                  {side.rep.predictionNow && (
                    <div className="text-xs tnum">
                      P(up) {((side.rep.predictionNow.calProb ?? 0) * 100).toFixed(1)}%
                    </div>
                  )}
                </div>
              ))}
            </div>
          </section>
          <section className="panel overflow-x-auto">
            <div className="panel-h">TRADE CONTEXT DETAIL</div>
            <table className="v4-table w-full text-xs">
              <thead><tr className="text-left text-[var(--faint)]"><th className="px-4 py-2">metric</th><th className="px-4 py-2 mono">{aSymbol}</th><th className="px-4 py-2 mono">{bSymbol}</th></tr></thead>
              <tbody>
                {CONTEXT_ROWS.map((r, i) => (
                  <tr key={r.key} className="reveal-item border-t" style={{ "--i": i, borderColor: "var(--border)" } as React.CSSProperties}>
                    <td className="px-4 py-2" style={{ color: "var(--dim)" }}>{r.label}</td>
                    <td className="tnum px-4 py-2">{repA?.tradeContext ? r.fmt(repA.tradeContext)[1] : "—"}</td>
                    <td className="tnum px-4 py-2">{repB?.tradeContext ? r.fmt(repB.tradeContext)[1] : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        </>
      )}
    </div>
  );
}

export default function ComparePage() {
  return (
    <main className="mx-auto max-w-6xl px-4 py-6">
      <Suspense fallback={<Skeleton lines={12} />}>
        <CompareInner />
      </Suspense>
    </main>
  );
}
