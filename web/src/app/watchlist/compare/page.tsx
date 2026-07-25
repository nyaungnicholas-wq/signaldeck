"use client";

// COMPARE — two symbols side by side from one signalReport("overview") each:
// aligned trade-context rows, the validated regime stacks with measured
// accuracy bands, the EXPERIMENTAL calibrated P(up) with its caveat rendered
// verbatim, and recent breakout/anomaly activity. Deep-linkable via
// ?a=NVDA&b=AMD (+ am/bm for market). useSearchParams lives in a client
// component wrapped in Suspense, as Next 16 requires for static builds.

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { signalReport, type Market, type SignalReport } from "@/lib/api";
import { ago, fmtPrice } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import { usePeek } from "@/components/CompanyPeek";

function pct(x: number | undefined, dec = 1): string {
  return x == null ? "—" : `${(x * 100).toFixed(dec)}%`;
}

function num(x: number | undefined, dec = 2): string {
  if (x == null || !isFinite(x)) return "—";
  if (Math.abs(x) >= 1e9) return `${(x / 1e9).toFixed(2)}B`;
  if (Math.abs(x) >= 1e6) return `${(x / 1e6).toFixed(2)}M`;
  return x.toFixed(dec);
}

// Aligned trade-context rows: same fixed list for both columns, so every
// metric sits on one line across A and B.
const CONTEXT_ROWS: { label: string; value: (c: Record<string, number>) => string }[] = [
  { label: "last close", value: (c) => fmtPrice(c.lastClose) },
  { label: "52w range", value: (c) => `${fmtPrice(c.low52w)} – ${fmtPrice(c.high52w)}` },
  { label: "off 52w high", value: (c) => (c.offHigh52Pct == null ? "—" : `${c.offHigh52Pct.toFixed(1)}%`) },
  { label: "ATR(14)", value: (c) => (c.atr14Pct == null ? "—" : `${c.atr14Pct.toFixed(2)}%`) },
  { label: "SMA50", value: (c) => fmtPrice(c.sma50) },
  { label: "SMA200", value: (c) => fmtPrice(c.sma200) },
  { label: "avg $ vol (21d)", value: (c) => num(c.avgDollarVol21, 0) },
];

const MARKETS: Market[] = ["stocks", "crypto"];

function parseMarket(v: string | null): Market {
  return v === "crypto" ? "crypto" : "stocks";
}

interface Side {
  symbol: string;
  market: Market;
}

function SideHeader({ side }: { side: Side }) {
  const peek = usePeek();
  const sym = encodeURIComponent(side.symbol);
  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <button
        type="button"
        onClick={() => peek.open(side.symbol, side.market)}
        className="mono cursor-pointer text-base font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
        title={`peek ${side.symbol}`}
      >
        {side.symbol}
      </button>
      <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {side.market}
      </span>
      <Link
        href={`/signals/report/${side.market}/${sym}?kind=overview`}
        className="cursor-pointer text-[0.75rem] text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
      >
        report →
      </Link>
      <Link
        href={`/s/${side.market}/${sym}`}
        className="cursor-pointer text-[0.75rem] text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
      >
        full page →
      </Link>
    </div>
  );
}

function StackCell({ report, side }: { report: SignalReport; side: Side }) {
  const sym = encodeURIComponent(side.symbol);
  const stack = report.regimeStack ?? [];
  return (
    <div className="flex flex-wrap content-start gap-1.5 text-[0.75rem]">
      {stack.map((f) => (
        <Link
          key={f.kind}
          href={`/signals/report/${side.market}/${sym}?kind=${f.kind}`}
          className="chip cursor-pointer px-2 py-[2px] transition-colors duration-150 hover:text-[var(--accent)]"
        >
          {f.kind}: {f.regime} ({pct(f.historicalAccuracy)} band)
        </Link>
      ))}
      {report.vol63 ? (
        <Link
          href={`/signals/report/${side.market}/${sym}?kind=vol63`}
          className="chip cursor-pointer px-2 py-[2px] transition-colors duration-150 hover:text-[var(--accent)]"
        >
          vol63: {report.vol63.regime} ({pct(report.vol63.historicalAccuracy)} band)
        </Link>
      ) : null}
      {stack.length === 0 && !report.vol63 ? (
        <span style={{ color: "var(--faint)" }}>no validated forecasts on this symbol yet</span>
      ) : null}
    </div>
  );
}

function ActivityCell({ report }: { report: SignalReport }) {
  const breakouts = (report.recentBreakouts ?? []).slice(0, 3);
  const anomalies = (report.recentAnomalies ?? []).slice(0, 3);
  if (breakouts.length === 0 && anomalies.length === 0) {
    return (
      <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        no recent breakouts or anomalies recorded
      </p>
    );
  }
  return (
    <ul className="m-0 list-none space-y-1 p-0 text-[0.75rem]">
      {breakouts.map((b) => (
        <li key={`b-${b.ts}-${b.kind}`} className="flex flex-wrap gap-x-2">
          <span className="chip px-2 py-[1px]" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
            breakout
          </span>
          <span style={{ color: "var(--dim)" }}>
            {b.kind}
            {b.detail ? ` — ${b.detail}` : ""}
          </span>
          <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
            {ago(b.ts)}
          </span>
        </li>
      ))}
      {anomalies.map((a) => (
        <li key={`a-${a.ts}-${a.kind}`} className="flex flex-wrap gap-x-2">
          <span
            className="chip tnum px-2 py-[1px]"
            style={{ color: "var(--crossed)", borderColor: "var(--crossed)" }}
            title="descriptive z-score vs the symbol's own baseline — NOT a prediction"
          >
            anomaly · z={a.z.toFixed(1)}
          </span>
          <span style={{ color: "var(--dim)" }}>
            {a.kind}
            {a.detail ? ` — ${a.detail}` : ""}
          </span>
          <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
            {ago(a.ts)}
          </span>
        </li>
      ))}
    </ul>
  );
}

function PredictionCell({ report }: { report: SignalReport }) {
  const p = report.predictionNow;
  if (!p) {
    return (
      <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        no stored prediction for this symbol yet — honest absence, not a 50%.
      </p>
    );
  }
  return (
    <div className="space-y-1 text-[0.75rem]">
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        <span
          className="chip px-2 py-[1px] tracking-wider"
          style={{ color: "var(--crossed)", borderColor: "var(--crossed)" }}
        >
          experimental
        </span>
        <span className="tnum">P(up) calibrated {pct(p.calProb)}</span>
        <span className="tnum" style={{ color: "var(--dim)" }}>
          raw {pct(p.rawProb)}
        </span>
        <span className="tnum" style={{ color: "var(--faint)" }}>
          n used {p.nUsed}
        </span>
      </div>
      {report.liveDirectionalNote ? (
        <p className="m-0 leading-relaxed" style={{ color: "var(--dim)" }}>
          {report.liveDirectionalNote}
        </p>
      ) : null}
    </div>
  );
}

function CompareInner() {
  const sp = useSearchParams();

  const a: Side = { symbol: (sp.get("a") ?? "NVDA").toUpperCase(), market: parseMarket(sp.get("am")) };
  const b: Side = { symbol: (sp.get("b") ?? "AMD").toUpperCase(), market: parseMarket(sp.get("bm")) };

  // input drafts (applied on submit → URL, which drives the fetches)
  const [draftA, setDraftA] = useState(a.symbol);
  const [draftB, setDraftB] = useState(b.symbol);
  const [draftAm, setDraftAm] = useState<Market>(a.market);
  const [draftBm, setDraftBm] = useState<Market>(b.market);

  const [repA, setRepA] = useState<SignalReport | null>(null);
  const [repB, setRepB] = useState<SignalReport | null>(null);
  const [errA, setErrA] = useState<string | null>(null);
  const [errB, setErrB] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const retry = () => setTick((t) => t + 1);

  // Write the CANONICAL path (never the legacy /compare that next.config 307s
  // here) through the native History API rather than router.replace().
  // Measured on 16.2.10: router.replace/push to this route with only the query
  // changed is silently dropped once the route has settled — the swap button
  // and the compare submit did nothing at all — while replaceState lands every
  // time. It is the documented way to update search params and keeps
  // useSearchParams in sync, which is what re-runs the fetches below.
  const navigate = (na: Side, nb: Side) => {
    const q = new URLSearchParams({ a: na.symbol, b: nb.symbol });
    if (na.market !== "stocks") q.set("am", na.market);
    if (nb.market !== "stocks") q.set("bm", nb.market);
    window.history.replaceState(null, "", `/watchlist/compare?${q.toString()}`);
  };

  // Both syncs happen during render (the prev-key pattern, cf. viz/BigCandle)
  // rather than in an effect: the URL is a prop-like input here, and adjusting
  // state from it synchronously avoids a cascading second render.
  //   urlKey  → keep the drafts in step when the URL changes (swap, back button)
  //   fetchKey → drop the stale reports so the skeletons show while refetching
  //              (retry bumps `tick`, which must reset them too)
  const urlKey = `${a.market}:${a.symbol}|${b.market}:${b.symbol}`;
  const [prevUrlKey, setPrevUrlKey] = useState(urlKey);
  if (urlKey !== prevUrlKey) {
    setPrevUrlKey(urlKey);
    setDraftA(a.symbol);
    setDraftB(b.symbol);
    setDraftAm(a.market);
    setDraftBm(b.market);
  }

  const fetchKey = `${urlKey}#${tick}`;
  const [prevFetchKey, setPrevFetchKey] = useState(fetchKey);
  if (fetchKey !== prevFetchKey) {
    setPrevFetchKey(fetchKey);
    setRepA(null);
    setRepB(null);
  }

  useEffect(() => {
    let dead = false;
    signalReport(a.symbol, a.market, "overview")
      .then((r) => !dead && (setRepA(r), setErrA(null)))
      .catch((e: unknown) => !dead && setErrA(e instanceof Error ? e.message : String(e)));
    signalReport(b.symbol, b.market, "overview")
      .then((r) => !dead && (setRepB(r), setErrB(null)))
      .catch((e: unknown) => !dead && setErrB(e instanceof Error ? e.message : String(e)));
    return () => {
      dead = true;
    };
  }, [a.symbol, a.market, b.symbol, b.market, tick]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const na = draftA.trim().toUpperCase();
    const nb = draftB.trim().toUpperCase();
    if (!na || !nb) return;
    navigate({ symbol: na, market: draftAm }, { symbol: nb, market: draftBm });
  };

  const bothLoaded = repA && repB;

  return (
    <main className="mx-auto max-w-6xl space-y-4 px-4 py-6">
      <h1 className="mono text-lg font-semibold">COMPARE</h1>
      <PagePurpose
        id="compare"
        text="two symbols side by side from stored signal data: aligned trade context, each one's validated regime stack with measured accuracy bands, the experimental calibrated P(up) with its caveat, and recent activity. Nothing here is a recommendation."
      />

      {/* symbol pickers + swap */}
      <form onSubmit={submit} className="panel flex flex-wrap items-center gap-2 px-4 py-3">
        {(
          [
            ["A", draftA, setDraftA, draftAm, setDraftAm],
            ["B", draftB, setDraftB, draftBm, setDraftBm],
          ] as const
        ).map(([label, val, setVal, mkt, setMkt]) => (
          <label key={label} className="flex items-center gap-1.5 text-[0.75rem]">
            <span style={{ color: "var(--faint)" }}>{label}</span>
            <input
              value={val}
              onChange={(e) => setVal(e.target.value)}
              spellCheck={false}
              autoCapitalize="characters"
              aria-label={`symbol ${label}`}
              className="mono w-24 rounded border bg-transparent px-2 py-1.5 text-[0.85rem] uppercase outline-none focus:border-[var(--accent)]"
              style={{ borderColor: "var(--border)" }}
            />
            <select
              value={mkt}
              onChange={(e) => setMkt(e.target.value as Market)}
              aria-label={`market ${label}`}
              className="rounded border bg-transparent px-1 py-1.5 text-[0.75rem]"
              style={{ borderColor: "var(--border)", color: "var(--dim)" }}
            >
              {MARKETS.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </label>
        ))}
        <button
          type="submit"
          className="chip min-h-[36px] cursor-pointer px-4 text-[0.75rem] tracking-wider transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
        >
          compare
        </button>
        <button
          type="button"
          onClick={() => navigate(b, a)}
          className="chip min-h-[36px] cursor-pointer px-4 text-[0.75rem] tracking-wider transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
          title="swap A and B"
        >
          ⇄ swap
        </button>
      </form>

      {errA ? <ErrorState message={`${a.symbol}: ${errA}`} retry={retry} /> : null}
      {errB ? <ErrorState message={`${b.symbol}: ${errB}`} retry={retry} /> : null}
      {!bothLoaded && !errA && !errB ? <Skeleton lines={12} /> : null}

      {bothLoaded ? (
        <>
          {/* column headers */}
          <div className="grid gap-4 md:grid-cols-2">
            <section className="panel px-4 py-3">
              <SideHeader side={a} />
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                data as of {ago(repA.asOf)}
              </p>
            </section>
            <section className="panel px-4 py-3">
              <SideHeader side={b} />
              <p className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                data as of {ago(repB.asOf)}
              </p>
            </section>
          </div>

          {/* trade context — one table so rows align exactly */}
          <section className="panel" aria-label="trade context comparison">
            <div className="panel-h">TRADE CONTEXT</div>
            <div className="overflow-x-auto">
              <table className="w-full text-[0.75rem]">
                <thead>
                  <tr className="text-left" style={{ color: "var(--faint)" }}>
                    <th className="px-4 py-1 font-normal">metric</th>
                    <th className="mono py-1 pr-2 font-normal">{a.symbol}</th>
                    <th className="mono py-1 pr-4 font-normal">{b.symbol}</th>
                  </tr>
                </thead>
                <tbody>
                  {CONTEXT_ROWS.map((r) => (
                    <tr key={r.label} className="border-t" style={{ borderColor: "var(--border)" }}>
                      <td className="px-4 py-1.5" style={{ color: "var(--dim)" }}>
                        {r.label}
                      </td>
                      <td className="tnum py-1.5 pr-2">
                        {repA.tradeContext ? r.value(repA.tradeContext) : "—"}
                      </td>
                      <td className="tnum py-1.5 pr-4">
                        {repB.tradeContext ? r.value(repB.tradeContext) : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p
              className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
              style={{ borderColor: "var(--border)", color: "var(--faint)" }}
            >
              Stored daily closes on worker cadence, not live quotes. — means the daemon has no
              value for that metric, never a guess.
            </p>
          </section>

          {/* regime stacks */}
          <section className="panel" aria-label="validated regime stacks">
            <div className="panel-h">VALIDATED REGIME STACK</div>
            <div className="grid gap-4 px-4 py-3 md:grid-cols-2">
              <StackCell report={repA} side={a} />
              <StackCell report={repB} side={b} />
            </div>
            <p
              className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
              style={{ borderColor: "var(--border)", color: "var(--faint)" }}
            >
              Each chip shows the MEASURED walk-forward accuracy at that conviction band, served by
              the daemon in-payload — never invented client-side.
            </p>
          </section>

          {/* experimental P(up) */}
          <section className="panel" aria-label="experimental predictions">
            <div className="panel-h">EXPERIMENTAL P(UP) — 1d</div>
            <div className="grid gap-4 px-4 py-3 md:grid-cols-2">
              <PredictionCell report={repA} />
              <PredictionCell report={repB} />
            </div>
          </section>

          {/* recent activity */}
          <section className="panel" aria-label="recent activity">
            <div className="panel-h">RECENT ACTIVITY</div>
            <div className="grid gap-4 px-4 py-3 md:grid-cols-2">
              <ActivityCell report={repA} />
              <ActivityCell report={repB} />
            </div>
          </section>

          {(repA.honesty ?? repB.honesty) ? (
            <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              {repA.honesty ?? repB.honesty}
            </p>
          ) : null}
        </>
      ) : null}
    </main>
  );
}

export default function ComparePage() {
  return (
    <Suspense fallback={<Skeleton lines={12} />}>
      <CompareInner />
    </Suspense>
  );
}
