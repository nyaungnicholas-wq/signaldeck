"use client";

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
import CandleChart from "@/components/symbol/CandleChart";
import { PageHero, StatTile, Gauge, Reveal, MiniBar } from "@/components/ui/Kit";
import HelpTip from "@/components/HelpTip";

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

  // This hand-written structural cast is the second half of why evidenceCaveat
  // had zero render sites in web/src: even once lib/api.ts declares the field,
  // a narrowing cast that omits it puts it back out of reach. Any honesty field
  // the daemon ships beside historicalAccuracy belongs in this list — dropping
  // one here silently turns a disclosed number into an undisclosed one.
  const sig = report?.signal as
    | {
        regime?: string;
        conviction?: number;
        historicalAccuracy?: number;
        tier?: string;
        evidence?: string;
        firstGradableOn?: string;
        evidenceCaveat?: string;
      }
    | undefined;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title={`${symbol} Signal Report`}
        subtitle="Why this signal fired, what the inputs were, and how this exact signal has done on this symbol before."
        right={
          <Link
            className="text-xs text-[color:var(--dim)] hover:text-[color:var(--text)]"
            href={`/s/${market}/${encodeURIComponent(symbol)}`}
          >
            full symbol page →
          </Link>
        }
      />

      {report && (
        <span className="text-[0.75rem] text-[color:var(--faint)]">
          data as of {fmtTs(report.asOf)} ({ago(report.asOf)})
        </span>
      )}

      {err ? <ErrorState message={err} /> : null}
      {!report && !err ? <Skeleton lines={14} /> : null}

      {report && (
        <>
          <section className="hud-panel p-4">
            <div className="flex flex-wrap items-center gap-6">
              {sig?.regime && (
                <div className="flex-1 min-w-[200px]">
                  <div className="text-[0.75rem] text-[color:var(--dim)] uppercase tracking-wider mb-1">
                    Signal Direction
                  </div>
                  <div className="num-hero text-3xl font-bold" style={{ color: sig.conviction != null ? (sig.conviction >= 0.5 ? 'var(--bid)' : 'var(--ask)') : 'var(--text)' }}>
                    {sig.regime}
                  </div>
                </div>
              )}

              {sig?.conviction != null && (
                <div className="flex-1 min-w-[150px] flex justify-center">
                  <Gauge
                    value={sig.conviction}
                    min={0}
                    max={1}
                    label="Conviction"
                    color="var(--hud)"
                    size={120}
                  />
                </div>
              )}

              <div className="flex-1 min-w-[200px] grid grid-cols-2 gap-2">
                {/* NOT "MEASURED": historicalAccuracy is a walk-forward BACKTEST
                    lookup, frozen and hash-chained before any of this predictor's
                    forecasts resolved. The daemon says so on every payload via
                    evidenceCaveat (structregime.go:275-278) and that string used
                    to be dropped at the type layer, so this tile called a backtest
                    a measurement. The caveat is rendered verbatim below. */}
                {sig?.historicalAccuracy != null && (
                  <StatTile
                    label="BACKTEST ACCURACY"
                    value={sig.historicalAccuracy * 100}
                    decimals={1}
                    suffix="%"
                    glow="up"
                    i={0}
                  />
                )}
                {sig?.tier && (
                  <StatTile
                    label="TIER"
                    value={sig.tier}
                    i={1}
                  />
                )}
                {report.predictionNow && (
                  <>
                    <StatTile
                      label="P(UP) RAW"
                      value={report.predictionNow.rawProb * 100}
                      decimals={1}
                      suffix="%"
                      i={2}
                    />
                    <StatTile
                      label="CALIBRATED"
                      value={report.predictionNow.calProb * 100}
                      decimals={1}
                      suffix="%"
                      i={3}
                    />
                  </>
                )}
              </div>
            </div>
            {/* Rendered VERBATIM — a paraphrased caveat is a broken caveat. The
                daemon ships this sentence precisely so no reader has to infer
                what kind of number sits above it. Absent ⇒ render nothing. */}
            {sig?.evidenceCaveat && (
              <p className="mt-3 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                The accuracy above is a backtest claim, not a live track record.{" "}
                <HelpTip label="the full caveat">{sig.evidenceCaveat}</HelpTip>
                {sig.firstGradableOn ? ` First gradable on ${sig.firstGradableOn}.` : ""}
              </p>
            )}
          </section>

          {sig?.regime && report.whyFired?.length ? (
            <section className="panel space-y-2 p-4">
              <div className="panel-h text-[0.75rem] uppercase tracking-wider text-[color:var(--dim)]">
                Why This Signal Fired
              </div>
              <Reveal>
                <div className="table-wrap">
                <table className="w-full text-[0.75rem]">
                  <thead>
                    <tr className="text-left text-[color:var(--faint)]">
                      <th className="py-1 pr-2 font-normal">input</th>
                      <th className="py-1 pr-2 font-normal">value</th>
                      <th className="py-1 font-normal">meaning</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.whyFired.map((i) => (
                      <tr key={i.name} className="border-t border-[color:var(--faint)] reveal-item" style={{ "--i": 0 } as React.CSSProperties}>
                        <td className="mono py-1 pr-2 text-[color:var(--text)]">{i.name}</td>
                        <td className="tnum py-1 pr-2 text-[color:var(--text)]">{num(i.value, 4)}</td>
                        <td className="py-1 text-[color:var(--dim)]">{i.note}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                </div>
              </Reveal>
            </section>
          ) : null}

          {(kind === "prediction" || kind === "composite" || kind === "overview") &&
          report.predictionNow ? (
            <section className="panel space-y-2 p-4">
              <div className="panel-h text-[0.75rem] uppercase tracking-wider text-[color:var(--dim)]">
                Calibrated Prediction (1d)
              </div>
              <div className="flex flex-wrap gap-4 text-[0.75rem]">
                <span className="tnum text-[color:var(--text)]">n used {report.predictionNow.nUsed}</span>
                {report.composite && (
                  <span className="tnum text-[color:var(--text)]">
                    composite score {report.composite.score} (edge{" "}
                    {report.composite.edge.toFixed(3)})
                  </span>
                )}
              </div>
              {report.liveDirectionalNote && (
                <p className="text-[0.75rem] leading-relaxed text-amber-200/80">
                  {report.liveDirectionalNote}
                </p>
              )}
            </section>
          ) : null}

          {bars && bars.length > 0 ? (
            <section className="panel p-2">
              <CandleChart bars={bars} tf="1d" height={360} overlays={overlays} />
            </section>
          ) : null}

          {report.history ? (
            <section className="panel space-y-2 p-4">
              <div className="panel-h flex flex-wrap items-baseline gap-3">
                <span className="text-[0.75rem] uppercase tracking-wider text-[color:var(--dim)]">
                  Walk-Forward History on {symbol}
                </span>
                {report.history.total != null && report.history.total > 0 && (
                  <div className="flex gap-2">
                    {/* total>0 does not imply hitRate was computed: ReportHistory
                        types them independently. `?? 0` here published "right 0.0%
                        of the time" for a signal that simply has no rate yet.
                        StatTile renders null as an em-dash — see Kit.tsx. */}
                    <StatTile
                      label="HIT RATE"
                      value={report.history.hitRate != null ? report.history.hitRate * 100 : null}
                      decimals={1}
                      suffix="%"
                      i={0}
                    />
                    <StatTile
                      label="SAMPLES"
                      value={report.history.total}
                      i={1}
                    />
                  </div>
                )}
              </div>
              {report.history.note && (
                <p className="text-[0.75rem] text-[color:var(--dim)]">{report.history.note}</p>
              )}
              <Reveal>
                {report.history.instances?.length ? (
                  <div className="table-wrap">
                  <table className="w-full text-[0.75rem] v4-table">
                    <thead>
                      <tr className="text-left text-[color:var(--faint)]">
                        <th className="py-1 pr-2 font-normal">date</th>
                        <th className="py-1 pr-2 font-normal">called</th>
                        <th className="py-1 pr-2 font-normal">conviction</th>
                        <th className="py-1 pr-2 font-normal">actual</th>
                        <th className="py-1 font-normal">result</th>
                      </tr>
                    </thead>
                    <tbody>
                      {report.history.instances.map((x) => (
                        <tr key={x.ts} className={`border-t border-[color:var(--faint)] reveal-item ${x.correct ? 'bg-[color:var(--bid)]/5' : 'bg-[color:var(--ask)]/5'}`} style={{ "--i": 0 } as React.CSSProperties}>
                          <td className="tnum py-1 pr-2 text-[color:var(--text)]">{fmtTs(x.ts)}</td>
                          <td className="py-1 pr-2 text-[color:var(--text)]">{x.regime}</td>
                          <td className="py-1 pr-2">
                            <div className="flex items-center gap-2">
                              <span className="tnum text-[color:var(--text)]">{pct(x.conviction)}</span>
                              {x.conviction != null && (
                                <MiniBar value={x.conviction} max={1} color="var(--hud)" height={4} />
                              )}
                            </div>
                          </td>
                          <td className="py-1 pr-2 text-[color:var(--text)]">{x.actual}</td>
                          <td className={`py-1 ${x.correct ? "text-[color:var(--bid)]" : "text-[color:var(--ask)]"}`}>
                            {x.correct ? "correct" : "wrong"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  </div>
                ) : null}

                {report.history.rows?.length ? (
                  <div className="table-wrap">
                  <table className="w-full text-[0.75rem] v4-table mt-4">
                    <thead>
                      <tr className="text-left text-[color:var(--faint)]">
                        <th className="py-1 pr-2 font-normal">date</th>
                        <th className="py-1 pr-2 font-normal">P(up)</th>
                        <th className="py-1 pr-2 font-normal">actual</th>
                        <th className="py-1 pr-2 font-normal">fwd return</th>
                        <th className="py-1 font-normal">result</th>
                      </tr>
                    </thead>
                    <tbody>
                      {report.history.rows.map((x) => (
                        <tr key={`${x.ts}-${x.horizon}`} className={`border-t border-[color:var(--faint)] reveal-item ${x.correct ? 'bg-[color:var(--bid)]/5' : 'bg-[color:var(--ask)]/5'}`} style={{ "--i": 0 } as React.CSSProperties}>
                          <td className="tnum py-1 pr-2 text-[color:var(--text)]">{fmtTs(x.ts)}</td>
                          <td className="py-1 pr-2">
                            <div className="flex items-center gap-2">
                              <span className="tnum text-[color:var(--text)]">{pct(x.prob)}</span>
                              {x.prob != null && (
                                <MiniBar value={x.prob} max={1} color="var(--hud)" height={4} />
                              )}
                            </div>
                          </td>
                          <td className="py-1 pr-2 text-[color:var(--text)]">{x.up ? "up" : "down"}</td>
                          <td className="tnum py-1 pr-2 text-[color:var(--text)]">{pct(x.fwdReturn, 2)}</td>
                          <td className={`py-1 ${x.correct ? "text-[color:var(--bid)]" : "text-[color:var(--ask)]"}`}>
                            {x.correct ? "correct" : "wrong"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  </div>
                ) : null}

                {(report.history.breakouts ?? report.history.anomalies)?.length ? (
                  <div className="table-wrap">
                  <table className="w-full text-[0.75rem] v4-table mt-4">
                    <thead>
                      <tr className="text-left text-[color:var(--faint)]">
                        <th className="py-1 pr-2 font-normal">date</th>
                        <th className="py-1 pr-2 font-normal">kind</th>
                        <th className="py-1 pr-2 font-normal">detail</th>
                        <th className="py-1 font-normal">next 5 sessions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(report.history.breakouts ?? report.history.anomalies ?? []).map((x) => (
                        <tr key={`${x.ts}-${x.kind}`} className={`border-t border-[color:var(--faint)] reveal-item`} style={{ "--i": 0 } as React.CSSProperties}>
                          <td className="tnum py-1 pr-2 text-[color:var(--text)]">{fmtTs(x.ts)}</td>
                          <td className="py-1 pr-2 text-[color:var(--text)]">{x.kind}</td>
                          <td className="py-1 pr-2 text-[color:var(--dim)]">{x.detail}</td>
                          <td
                            className={`tnum py-1 ${
                              !x.hasFwd
                                ? "text-[color:var(--dim)]"
                                : x.fwd5Pct >= 0
                                  ? "text-[color:var(--bid)]"
                                  : "text-[color:var(--ask)]"
                            }`}
                          >
                            {x.hasFwd ? `${x.fwd5Pct.toFixed(2)}%` : "unresolved"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  </div>
                ) : null}
              </Reveal>
            </section>
          ) : null}

          {report.tradeContext ? (
            <section className="panel space-y-2 p-4">
              <div className="panel-h text-[0.75rem] uppercase tracking-wider text-[color:var(--dim)]">
                Trade Context
              </div>
              <Reveal>
                <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-[0.75rem] sm:grid-cols-4">
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
                    <div key={k} className="flex justify-between gap-2 reveal-item" style={{ "--i": 0 } as React.CSSProperties}>
                      <span className="text-[color:var(--dim)]">{k}</span>
                      <span className="tnum text-[color:var(--text)]">{v}</span>
                    </div>
                  ))}
                </div>
              </Reveal>
            </section>
          ) : null}

          <section className="panel space-y-2 p-4">
            <div className="panel-h text-[0.75rem] uppercase tracking-wider text-[color:var(--dim)]">
              Full Signal Stack on {symbol}
            </div>
            <Reveal>
              <div className="flex flex-wrap gap-2 text-[0.75rem]">
                {(report.regimeStack ?? []).map((f) => (
                  <Link
                    key={f.kind}
                    href={`/signals/report/${market}/${encodeURIComponent(symbol)}?kind=${f.kind}`}
                    className="panel px-2 py-[2px] hover:text-[color:var(--accent)] reveal-item"
                    style={{ "--i": 0 } as React.CSSProperties}
                  >
                    {f.kind}: {f.regime} ({pct(f.historicalAccuracy, 1)} band)
                  </Link>
                ))}
                {report.vol63 && (
                  <Link
                    href={`/signals/report/${market}/${encodeURIComponent(symbol)}?kind=vol63`}
                    className="panel px-2 py-[2px] hover:text-[color:var(--accent)] reveal-item"
                    style={{ "--i": 1 } as React.CSSProperties}
                  >
                    vol63: {report.vol63.regime} ({pct(report.vol63.historicalAccuracy, 1)} band)
                  </Link>
                )}
                {(report.recentBreakouts ?? []).slice(0, 4).map((b) => (
                  <span key={b.ts} className="panel px-2 py-[2px] text-[color:var(--dim)] reveal-item" style={{ "--i": 2 } as React.CSSProperties}>
                    breakout {b.kind} {ago(b.ts)}
                  </span>
                ))}
              </div>
            </Reveal>
            {report.honesty && (
              <p className="text-[0.75rem] leading-relaxed text-[color:var(--dim)]">{report.honesty}</p>
            )}
          </section>
        </>
      )}
    </div>
  );
}
