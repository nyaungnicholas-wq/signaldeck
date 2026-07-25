"use client";

// SIGNAL DETAIL REPORT — one page per clicked signal (?kind=trend21|
// liquidity21|vol21|vol63|prediction|composite|breakout|anomaly|overview).
// Sections, in reading order: the signal + WHY it fired (raw model inputs),
// the price chart with the platform's overlay markers, this signal's own
// walk-forward history on THIS symbol, and plain trade context + the symbol's
// full current signal stack. House honesty rules apply: accuracies shown are
// the measured per-band numbers, per-symbol histories are labeled small-sample,
// and the live directional verdict renders wherever P(up) numbers appear.

import { use, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  api,
  chartOverlays,
  signalReport,
  type Bar,
  type ChartOverlayMarker,
  type Market,
  type SignalReport,
} from "@/lib/api";
import { ago, fmtPrice, fmtTs } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import CandleChart from "@/components/symbol/CandleChart";

const KIND_LABEL: Record<string, string> = {
  trend21: "TREND REGIME (21d)",
  liquidity21: "LIQUIDITY REGIME (21d)",
  vol21: "VOLATILITY REGIME (monthly)",
  vol63: "VOLATILITY REGIME (quarterly)",
  prediction: "CALIBRATED PREDICTION",
  composite: "COMPOSITE SIGNAL SCORE",
  breakout: "BREAKOUT",
  anomaly: "UNUSUAL ACTIVITY",
  overview: "SIGNAL OVERVIEW",
};

function pct(x: number | undefined, dec = 1): string {
  return x == null ? "—" : `${(x * 100).toFixed(dec)}%`;
}

function num(x: number | undefined, dec = 2): string {
  if (x == null) return "—";
  if (Math.abs(x) >= 1e9) return `${(x / 1e9).toFixed(2)}B`;
  if (Math.abs(x) >= 1e6) return `${(x / 1e6).toFixed(2)}M`;
  return x.toFixed(dec);
}

export default function SignalReportPage({
  params,
}: {
  params: Promise<{ market: string; symbol: string }>;
}) {
  const { market, symbol: rawSymbol } = use(params);
  const symbol = decodeURIComponent(rawSymbol);
  const kind = useSearchParams().get("kind") ?? "overview";

  const [report, setReport] = useState<SignalReport | null>(null);
  const [bars, setBars] = useState<Bar[] | null>(null);
  const [overlays, setOverlays] = useState<ChartOverlayMarker[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let dead = false;
    signalReport(symbol, market as Market, kind)
      .then((r) => {
        if (!dead) {
          setReport(r);
          setErr(null);
        }
      })
      .catch((e: unknown) => {
        if (!dead) setErr(e instanceof Error ? e.message : String(e));
      });
    api
      .bars(symbol, market as Market, "1d", 365)
      .then((b) => {
        if (!dead) setBars(b);
      })
      .catch(() => undefined);
    chartOverlays(symbol, market as Market)
      .then((o) => {
        if (!dead) setOverlays(o.markers ?? []);
      })
      .catch(() => undefined);
    return () => {
      dead = true;
    };
  }, [symbol, market, kind]);

  const sig = report?.signal as
    | {
        regime?: string;
        conviction?: number;
        historicalAccuracy?: number;
        tier?: string;
      }
    | undefined;

  return (
    <main className="mx-auto max-w-5xl space-y-4 px-4 py-6">
      <div className="flex flex-wrap items-baseline gap-3">
        <h1 className="mono text-lg font-semibold text-zinc-100">
          {symbol} · {KIND_LABEL[kind] ?? kind.toUpperCase()}
        </h1>
        <Link
          className="text-xs text-zinc-400 hover:text-zinc-200"
          href={`/s/${market}/${encodeURIComponent(symbol)}`}
        >
          full symbol page →
        </Link>
        {report ? (
          <span className="text-[11px] text-zinc-500">
            data as of {fmtTs(report.asOf)} ({ago(report.asOf)})
          </span>
        ) : null}
      </div>
      <PagePurpose
        id="signal-report"
        text="Everything behind one signal: the raw inputs that fired it, its measured accuracy band, its own walk-forward history on this symbol (small sample — judge it as one), the chart with the platform's markers, and plain trade context."
      />
      {err ? <ErrorState message={err} /> : null}
      {!report && !err ? <Skeleton lines={14} /> : null}

      {report ? (
        <>
          {/* the signal + why it fired */}
          {sig?.regime ? (
            <section className="panel space-y-2 p-4">
              <div className="flex flex-wrap items-baseline gap-3">
                <span className="mono text-base font-bold text-emerald-300">
                  {sig.regime}
                </span>
                {sig.conviction != null ? (
                  <span className="text-xs text-zinc-300">
                    conviction {pct(sig.conviction)}
                  </span>
                ) : null}
                {sig.historicalAccuracy != null ? (
                  <span className="text-xs text-zinc-100">
                    measured accuracy at this band: {pct(sig.historicalAccuracy)}
                  </span>
                ) : null}
                {sig.tier ? (
                  <span className="chip px-2 py-[1px] text-[11px]">{sig.tier}</span>
                ) : null}
              </div>
              {report.whyFired?.length ? (
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-zinc-500">
                      <th className="py-1 pr-2 font-normal">input</th>
                      <th className="py-1 pr-2 font-normal">value</th>
                      <th className="py-1 font-normal">meaning</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.whyFired.map((i) => (
                      <tr key={i.name} className="border-t border-zinc-900">
                        <td className="mono py-1 pr-2 text-zinc-300">{i.name}</td>
                        <td className="tnum py-1 pr-2 text-zinc-100">{num(i.value, 4)}</td>
                        <td className="py-1 text-zinc-500">{i.note}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : null}
            </section>
          ) : null}

          {/* prediction / composite view */}
          {(kind === "prediction" || kind === "composite" || kind === "overview") &&
          report.predictionNow ? (
            <section className="panel space-y-1 p-4">
              <div className="text-xs font-semibold tracking-wide text-zinc-300">
                CALIBRATED PREDICTION (1d)
              </div>
              <div className="flex flex-wrap gap-4 text-xs text-zinc-200">
                <span>P(up) raw {pct(report.predictionNow.rawProb)}</span>
                <span>calibrated {pct(report.predictionNow.calProb)}</span>
                <span>n used {report.predictionNow.nUsed}</span>
                {report.composite ? (
                  <span>
                    composite score {report.composite.score} (edge{" "}
                    {report.composite.edge.toFixed(3)})
                  </span>
                ) : null}
              </div>
              {report.liveDirectionalNote ? (
                <p className="text-[11px] leading-relaxed text-amber-200/80">
                  {report.liveDirectionalNote}
                </p>
              ) : null}
            </section>
          ) : null}

          {/* chart with platform markers */}
          {bars && bars.length > 0 ? (
            <section className="panel p-2">
              <CandleChart bars={bars} tf="1d" height={360} overlays={overlays} />
            </section>
          ) : null}

          {/* this signal's history on this symbol */}
          {report.history ? (
            <section className="panel space-y-2 p-4">
              <div className="flex flex-wrap items-baseline gap-3">
                <span className="text-xs font-semibold tracking-wide text-zinc-300">
                  THIS SIGNAL ON {symbol} — WALK-FORWARD HISTORY
                </span>
                {report.history.total != null && report.history.total > 0 ? (
                  <span className="text-xs text-zinc-100">
                    {report.history.correct}/{report.history.total} correct (
                    {pct(report.history.hitRate)})
                  </span>
                ) : null}
              </div>
              {report.history.note ? (
                <p className="text-[11px] text-zinc-500">{report.history.note}</p>
              ) : null}
              {report.history.instances?.length ? (
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-zinc-500">
                      <th className="py-1 pr-2 font-normal">date</th>
                      <th className="py-1 pr-2 font-normal">called</th>
                      <th className="py-1 pr-2 font-normal">conviction</th>
                      <th className="py-1 pr-2 font-normal">actual</th>
                      <th className="py-1 font-normal">result</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.history.instances.map((x) => (
                      <tr key={x.ts} className="border-t border-zinc-900">
                        <td className="tnum py-1 pr-2 text-zinc-400">{fmtTs(x.ts)}</td>
                        <td className="py-1 pr-2 text-zinc-200">{x.regime}</td>
                        <td className="tnum py-1 pr-2 text-zinc-300">{pct(x.conviction)}</td>
                        <td className="py-1 pr-2 text-zinc-200">{x.actual}</td>
                        <td className={`py-1 ${x.correct ? "text-emerald-400" : "text-red-400"}`}>
                          {x.correct ? "correct" : "wrong"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : null}
              {report.history.rows?.length ? (
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-zinc-500">
                      <th className="py-1 pr-2 font-normal">date</th>
                      <th className="py-1 pr-2 font-normal">P(up)</th>
                      <th className="py-1 pr-2 font-normal">actual</th>
                      <th className="py-1 pr-2 font-normal">fwd return</th>
                      <th className="py-1 font-normal">result</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.history.rows.map((x) => (
                      <tr key={`${x.ts}-${x.horizon}`} className="border-t border-zinc-900">
                        <td className="tnum py-1 pr-2 text-zinc-400">{fmtTs(x.ts)}</td>
                        <td className="tnum py-1 pr-2 text-zinc-300">{pct(x.prob)}</td>
                        <td className="py-1 pr-2 text-zinc-200">{x.up ? "up" : "down"}</td>
                        <td className="tnum py-1 pr-2 text-zinc-300">{pct(x.fwdReturn, 2)}</td>
                        <td className={`py-1 ${x.correct ? "text-emerald-400" : "text-red-400"}`}>
                          {x.correct ? "correct" : "wrong"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : null}
              {(report.history.breakouts ?? report.history.anomalies)?.length ? (
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-zinc-500">
                      <th className="py-1 pr-2 font-normal">date</th>
                      <th className="py-1 pr-2 font-normal">kind</th>
                      <th className="py-1 pr-2 font-normal">detail</th>
                      <th className="py-1 font-normal">next 5 sessions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(report.history.breakouts ?? report.history.anomalies ?? []).map((x) => (
                      <tr key={`${x.ts}-${x.kind}`} className="border-t border-zinc-900">
                        <td className="tnum py-1 pr-2 text-zinc-400">{fmtTs(x.ts)}</td>
                        <td className="py-1 pr-2 text-zinc-200">{x.kind}</td>
                        <td className="py-1 pr-2 text-zinc-500">{x.detail}</td>
                        <td
                          className={`tnum py-1 ${
                            !x.hasFwd
                              ? "text-zinc-500"
                              : x.fwd5Pct >= 0
                                ? "text-emerald-400"
                                : "text-red-400"
                          }`}
                        >
                          {x.hasFwd ? `${x.fwd5Pct.toFixed(2)}%` : "unresolved"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : null}
            </section>
          ) : null}

          {/* trade context */}
          {report.tradeContext ? (
            <section className="panel space-y-2 p-4">
              <div className="text-xs font-semibold tracking-wide text-zinc-300">
                TRADE CONTEXT
              </div>
              <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs sm:grid-cols-4">
                {(
                  [
                    ["last close", fmtPrice(report.tradeContext.lastClose)],
                    ["52w high", fmtPrice(report.tradeContext.high52w)],
                    ["52w low", fmtPrice(report.tradeContext.low52w)],
                    ["off 52w high", `${report.tradeContext.offHigh52Pct?.toFixed(1) ?? "—"}%`],
                    ["SMA20", fmtPrice(report.tradeContext.sma20)],
                    ["SMA50", fmtPrice(report.tradeContext.sma50)],
                    ["SMA200", fmtPrice(report.tradeContext.sma200)],
                    ["ATR(14)", `${report.tradeContext.atr14Pct?.toFixed(2) ?? "—"}%`],
                    ["20d swing high", fmtPrice(report.tradeContext.swingHigh20d)],
                    ["20d swing low", fmtPrice(report.tradeContext.swingLow20d)],
                    ["avg $ vol (21d)", num(report.tradeContext.avgDollarVol21, 0)],
                  ] as const
                ).map(([k, v]) => (
                  <div key={k} className="flex justify-between gap-2">
                    <span className="text-zinc-500">{k}</span>
                    <span className="tnum text-zinc-200">{v}</span>
                  </div>
                ))}
              </div>
            </section>
          ) : null}

          {/* the symbol's full current signal stack */}
          <section className="panel space-y-2 p-4">
            <div className="text-xs font-semibold tracking-wide text-zinc-300">
              FULL SIGNAL STACK ON {symbol}
            </div>
            <div className="flex flex-wrap gap-2 text-[11px]">
              {(report.regimeStack ?? []).map((f) => (
                <Link
                  key={f.kind}
                  href={`/signals/report/${market}/${encodeURIComponent(symbol)}?kind=${f.kind}`}
                  className="chip px-2 py-[2px] hover:text-[var(--accent)]"
                >
                  {f.kind}: {f.regime} ({pct(f.historicalAccuracy, 1)} band)
                </Link>
              ))}
              {report.vol63 ? (
                <Link
                  href={`/signals/report/${market}/${encodeURIComponent(symbol)}?kind=vol63`}
                  className="chip px-2 py-[2px] hover:text-[var(--accent)]"
                >
                  vol63: {report.vol63.regime} ({pct(report.vol63.historicalAccuracy, 1)} band)
                </Link>
              ) : null}
              {(report.recentBreakouts ?? []).slice(0, 4).map((b) => (
                <span key={b.ts} className="chip px-2 py-[2px] text-zinc-400">
                  breakout {b.kind} {ago(b.ts)}
                </span>
              ))}
            </div>
            {report.honesty ? (
              <p className="text-[11px] leading-relaxed text-zinc-500">{report.honesty}</p>
            ) : null}
          </section>
        </>
      ) : null}
    </main>
  );
}
