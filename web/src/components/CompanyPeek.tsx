"use client";

// COMPANY PEEK — the 2026-07-18 slide-over: click a ticker on any market
// surface and see what's going on inside the company WITHOUT leaving the
// page. One data call (GET /api/signal-report?kind=overview) + one bars call
// for the mini chart. Glassmorphism panel sliding from the right; Esc /
// backdrop closes; body scroll locked while open. "Full page →" and
// "detail report →" are the deep-dive exits.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from "react";
import Link from "next/link";
import {
  api,
  signalReport,
  type Bar,
  type Market,
  type SignalReport,
  type SignalReportWithEarnings,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice } from "@/lib/format";
import Sparkline from "@/components/viz/Sparkline";
import Skeleton from "@/components/Skeleton";

interface PeekTarget {
  symbol: string;
  market: Market;
}

const PeekContext = createContext<{ open: (symbol: string, market: Market) => void }>({
  open: () => undefined,
});

/** Any component can call usePeek().open("NVDA","stocks"). */
export function usePeek() {
  return useContext(PeekContext);
}

function pct(x: number | undefined, dec = 1): string {
  return x == null ? "—" : `${(x * 100).toFixed(dec)}%`;
}

function money(x: number | undefined): string {
  if (x == null || !isFinite(x)) return "—";
  if (x >= 1e9) return `$${(x / 1e9).toFixed(2)}B`;
  if (x >= 1e6) return `$${(x / 1e6).toFixed(1)}M`;
  return `$${x.toFixed(0)}`;
}

export default function CompanyPeekProvider({ children }: { children: React.ReactNode }) {
  const [target, setTarget] = useState<PeekTarget | null>(null);
  const [report, setReport] = useState<SignalReport | null>(null);
  const [bars, setBars] = useState<Bar[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const open = useCallback((symbol: string, market: Market) => {
    setTarget({ symbol, market });
    setReport(null);
    setBars(null);
    setErr(null);
  }, []);
  const close = useCallback(() => setTarget(null), []);

  useEffect(() => {
    if (!target) return;
    let dead = false;
    signalReport(target.symbol, target.market, "overview")
      .then((r) => !dead && setReport(r))
      .catch((e: unknown) => !dead && setErr(e instanceof Error ? e.message : String(e)));
    api
      .bars(target.symbol, target.market, "1d", 90)
      .then((b) => !dead && setBars(b))
      .catch(() => undefined);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    window.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      dead = true;
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [target, close]);

  const tc = report?.tradeContext;
  // Credibility wave: the optional appended earningsWindow label. SignalReport
  // is defined in a frozen api.ts block, so the appended intersection type is
  // applied here at the call site.
  const ew = (report as SignalReportWithEarnings | null)?.earningsWindow;
  const closes = (bars ?? []).map((b) => b.c);
  const dayChg =
    closes.length > 1 ? ((closes[closes.length - 1] / closes[closes.length - 2] - 1) * 100) : null;

  return (
    <PeekContext.Provider value={{ open }}>
      {children}
      {target ? (
        <div
          className="fixed inset-0 z-50"
          role="dialog"
          aria-modal="true"
          aria-label={`${target.symbol} company peek`}
        >
          {/* backdrop */}
          <button
            aria-label="close"
            onClick={close}
            className="absolute inset-0 cursor-pointer"
            style={{ background: "rgba(3,6,12,0.55)", backdropFilter: "blur(2px)" }}
          />
          {/* slide-over */}
          <aside
            className="absolute right-0 top-0 flex h-full w-full max-w-md flex-col gap-3 overflow-y-auto p-4"
            style={{
              background: "rgba(10,16,26,0.82)",
              backdropFilter: "blur(22px) saturate(150%)",
              WebkitBackdropFilter: "blur(22px) saturate(150%)",
              borderLeft: "1px solid rgba(255,255,255,0.10)",
              boxShadow: "-24px 0 60px -20px rgba(0,0,0,0.7)",
              animation: "peek-in 200ms ease",
            }}
          >
            <div className="flex items-center gap-3">
              <span className="mono text-lg font-extrabold tracking-wide">{target.symbol}</span>
              {tc ? (
                <span className="tnum text-base">{fmtPrice(tc.lastClose)}</span>
              ) : null}
              {dayChg != null ? (
                <span
                  className="tnum text-sm"
                  style={{ color: dayChg >= 0 ? "var(--bid)" : "var(--ask)" }}
                >
                  {fmtPct(dayChg)}
                </span>
              ) : null}
              {report ? (
                <span className="text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  {ago(report.asOf)}
                </span>
              ) : null}
              {ew?.withinWindow ? (
                <span
                  className="chip px-2 py-[2px] text-[0.7rem]"
                  style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
                  title={
                    ew.note ??
                    "estimated earnings inside the next 7 days — a label, never a suppression"
                  }
                >
                  earnings ~{ew.daysUntil}d
                </span>
              ) : null}
              <button
                onClick={close}
                aria-label="close panel"
                className="ml-auto inline-flex h-9 w-9 cursor-pointer items-center justify-center rounded-full text-lg hover:bg-[rgba(255,255,255,0.08)]"
              >
                ×
              </button>
            </div>

            <div className="flex gap-2">
              <Link
                href={`/s/${target.market}/${encodeURIComponent(target.symbol)}`}
                onClick={close}
                className="chip cursor-pointer px-3 py-1 font-medium hover:text-[var(--accent)]"
              >
                full page →
              </Link>
              <Link
                href={`/signals/report/${target.market}/${encodeURIComponent(target.symbol)}?kind=overview`}
                onClick={close}
                className="chip cursor-pointer px-3 py-1 font-medium hover:text-[var(--accent)]"
              >
                detail report →
              </Link>
            </div>

            {err ? (
              <p className="text-[0.75rem]" style={{ color: "var(--bad)" }}>
                {err}
              </p>
            ) : null}
            {!report && !err ? <Skeleton lines={10} /> : null}

            {closes.length > 1 ? (
              <div className="panel p-3">
                <Sparkline closes={closes} width={380} height={64} />
                <p className="mt-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  last {closes.length} stored daily closes
                </p>
              </div>
            ) : null}

            {tc ? (
              <div className="panel p-3">
                <div className="mb-1 text-[0.7rem] font-semibold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                  TRADE CONTEXT
                </div>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-[0.75rem]">
                  {(
                    [
                      ["52w high", fmtPrice(tc.high52w)],
                      ["52w low", fmtPrice(tc.low52w)],
                      ["off high", `${tc.offHigh52Pct?.toFixed(1) ?? "—"}%`],
                      ["ATR(14)", `${tc.atr14Pct?.toFixed(2) ?? "—"}%`],
                      ["SMA50", fmtPrice(tc.sma50)],
                      ["SMA200", fmtPrice(tc.sma200)],
                      ["avg $ vol", money(tc.avgDollarVol21)],
                      ["20d range", `${fmtPrice(tc.swingLow20d)}–${fmtPrice(tc.swingHigh20d)}`],
                    ] as const
                  ).map(([k, v]) => (
                    <div key={k} className="flex justify-between gap-2">
                      <span style={{ color: "var(--faint)" }}>{k}</span>
                      <span className="tnum">{v}</span>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}

            {report?.regimeStack?.length || report?.vol63 ? (
              <div className="panel p-3">
                <div className="mb-1 text-[0.7rem] font-semibold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                  VALIDATED SIGNALS
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {(report?.regimeStack ?? []).map((f) => (
                    <Link
                      key={f.kind}
                      onClick={close}
                      href={`/signals/report/${target.market}/${encodeURIComponent(target.symbol)}?kind=${f.kind}`}
                      className="chip cursor-pointer px-2 py-[2px] text-[0.7rem] hover:text-[var(--accent)]"
                    >
                      {f.kind}: {f.regime} · {pct(f.historicalAccuracy)}
                    </Link>
                  ))}
                  {report?.vol63 ? (
                    <Link
                      onClick={close}
                      href={`/signals/report/${target.market}/${encodeURIComponent(target.symbol)}?kind=vol63`}
                      className="chip cursor-pointer px-2 py-[2px] text-[0.7rem] hover:text-[var(--accent)]"
                    >
                      vol63: {report.vol63.regime} · {pct(report.vol63.historicalAccuracy)}
                    </Link>
                  ) : null}
                </div>
              </div>
            ) : null}

            {report?.predictionNow ? (
              <div className="panel p-3">
                <div className="mb-1 text-[0.7rem] font-semibold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                  EXPERIMENTAL P(UP)
                </div>
                <div className="flex gap-4 text-[0.75rem]">
                  <span className="tnum">cal {pct(report.predictionNow.calProb)}</span>
                  <span className="tnum">raw {pct(report.predictionNow.rawProb)}</span>
                  {report.composite ? (
                    <span className="tnum">score {report.composite.score}/10</span>
                  ) : null}
                </div>
                {report.liveDirectionalNote ? (
                  <p className="mt-1 text-[0.68rem] leading-snug" style={{ color: "var(--warn)" }}>
                    {report.liveDirectionalNote}
                  </p>
                ) : null}
              </div>
            ) : null}

            {report?.recentBreakouts?.length || report?.recentAnomalies?.length ? (
              <div className="panel p-3">
                <div className="mb-1 text-[0.7rem] font-semibold tracking-[0.14em]" style={{ color: "var(--dim)" }}>
                  RECENT ACTIVITY
                </div>
                <ul className="space-y-1 text-[0.72rem]">
                  {(report?.recentBreakouts ?? []).slice(0, 4).map((b) => (
                    <li key={`b-${b.ts}-${b.kind}`} className="flex justify-between gap-2">
                      <span>breakout · {b.kind}</span>
                      <span className="tnum" style={{ color: "var(--faint)" }}>
                        {ago(b.ts)}
                        {b.hasFwd ? ` · +5d ${b.fwd5Pct.toFixed(1)}%` : ""}
                      </span>
                    </li>
                  ))}
                  {(report?.recentAnomalies ?? []).slice(0, 4).map((a) => (
                    <li key={`a-${a.ts}-${a.kind}`} className="flex justify-between gap-2">
                      <span>{a.kind.replace("anomaly_", "unusual ")}</span>
                      <span className="tnum" style={{ color: "var(--faint)" }}>
                        z {a.z.toFixed(1)} · {ago(a.ts)}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </aside>
          <style>{`@keyframes peek-in { from { transform: translateX(28px); opacity: 0 } to { transform: none; opacity: 1 } }
@media (prefers-reduced-motion: reduce) { aside { animation: none !important } }`}</style>
        </div>
      ) : null}
    </PeekContext.Provider>
  );
}
