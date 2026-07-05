"use client";

// Symbol deep-dive: candlestick chart, pressure decomposition, expectancy
// ("what usually happens next"), microstructure (crypto), insights, coverage.

import { use, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  api,
  chartOverlays,
  pollMs,
  type Bar,
  type ChartOverlayMarker,
  type Horizon,
  type Market,
  type SymbolDetail,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice, fmtScore, scoreColor, verdict } from "@/lib/format";
import CandleChart, { type Tf } from "@/components/symbol/CandleChart";
import PressurePanel from "@/components/symbol/PressurePanel";
import ExpectancyPanel from "@/components/symbol/ExpectancyPanel";
import MicroPanel from "@/components/symbol/MicroPanel";
import InsightsPanel from "@/components/symbol/InsightsPanel";
import SymbolAgentPanel from "@/components/symbol/SymbolAgentPanel";
import CoveragePanel from "@/components/symbol/CoveragePanel";
import FilingsIntelPanel from "@/components/symbol/FilingsIntelPanel";
import FinancialsPanel from "@/components/symbol/FinancialsPanel";
import CongressChip from "@/components/symbol/CongressChip";
import UnusualActivityPanel from "@/components/UnusualActivityPanel";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

const TF_LIMIT: Record<Tf, number> = { "1d": 365, "1h": 168, "1m": 390 };
const TFS: Tf[] = ["1d", "1h", "1m"];

function isMarket(m: string): m is Market {
  return m === "crypto" || m === "stocks";
}

export default function SymbolPage({
  params,
}: {
  params: Promise<{ market: string; symbol: string }>;
}) {
  const p = use(params);
  const symbol = decodeURIComponent(p.symbol);
  const marketOk = isMarket(p.market);
  const market: Market = isMarket(p.market) ? p.market : "crypto";

  const [detail, setDetail] = useState<SymbolDetail | null>(null);
  const [detailErr, setDetailErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [updatedAt, setUpdatedAt] = useState<number>(0);
  const [tf, setTf] = useState<Tf>("1d");
  // Bars are keyed by "symbol|tf" so switching timeframes shows a loading
  // state without a synchronous setState inside the effect.
  const [barsState, setBarsState] = useState<{ key: string; list: Bar[] } | null>(null);
  const [barsErrState, setBarsErrState] = useState<{ key: string; msg: string } | null>(null);
  const [horizon, setHorizon] = useState<Horizon>("1d");
  const barsKey = `${symbol}|${market}|${tf}`;
  const bars = barsState && barsState.key === barsKey ? barsState.list : null;
  const barsErr = barsErrState && barsErrState.key === barsKey ? barsErrState.msg : null;

  // Stage 7: chart overlays (score extremes, regime changes, breakouts). Toggled
  // on by default; keyed by symbol so switching symbols refetches.
  const [showOverlays, setShowOverlays] = useState(true);
  const [overlaysState, setOverlaysState] = useState<{ key: string; list: ChartOverlayMarker[] } | null>(null);
  const overlaysKey = `${symbol}|${market}`;
  const overlays =
    showOverlays && overlaysState && overlaysState.key === overlaysKey ? overlaysState.list : undefined;

  // Poll the detail payload every 5s.
  useEffect(() => {
    if (!marketOk) return;
    let alive = true;
    const load = () =>
      api
        .symbol(symbol, market)
        .then((d) => {
          if (!alive) return;
          setDetail(d);
          setDetailErr(null);
          setUpdatedAt(Math.floor(Date.now() / 1000));
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setDetailErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol, market, marketOk, retryTick]);

  // Bars: refetch on timeframe change + a slow 60s background refresh.
  useEffect(() => {
    if (!marketOk) return;
    let alive = true;
    const key = `${symbol}|${market}|${tf}`;
    const load = () =>
      api
        .bars(symbol, market, tf, TF_LIMIT[tf])
        .then((b) => {
          if (!alive) return;
          setBarsState({ key, list: b ?? [] });
          setBarsErrState(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setBarsErrState({ key, msg: e instanceof Error ? e.message : String(e) });
        });
    load();
    const t = setInterval(load, 60_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol, market, tf, marketOk]);

  // Overlays: fetch once per symbol + a slow background refresh. Failures are
  // silent (overlays are an enhancement; the chart still renders without them).
  useEffect(() => {
    if (!marketOk || !showOverlays) return;
    let alive = true;
    const key = `${symbol}|${market}`;
    const load = () =>
      chartOverlays(symbol, market)
        .then((o) => {
          if (!alive) return;
          setOverlaysState({ key, list: o.markers ?? [] });
        })
        .catch(() => {
          /* overlays are best-effort; keep the chart clean on error */
        });
    load();
    const t = setInterval(load, 120_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [symbol, market, marketOk, showOverlays]);

  const lastBar = useMemo(
    () => (bars && bars.length ? bars.reduce((a, b) => (b.ts > a.ts ? b : a)) : null),
    [bars]
  );
  const changePct = useMemo(() => {
    if (!bars || bars.length < 2 || !lastBar) return null;
    const sorted = [...bars].sort((a, b) => a.ts - b.ts);
    const prev = sorted[sorted.length - 2];
    return prev && prev.c ? ((lastBar.c - prev.c) / prev.c) * 100 : null;
  }, [bars, lastBar]);

  if (!marketOk) {
    return (
      <section className="panel p-6 text-[0.8rem]">
        <p style={{ color: "var(--bad)" }}>
          unknown market &ldquo;{p.market}&rdquo; — expected /s/crypto/… or /s/stocks/…
        </p>
        <p className="mt-2">
          <Link href="/" className="cursor-pointer underline" style={{ color: "var(--dim)" }}>
            back to watchlist
          </Link>
        </p>
      </section>
    );
  }

  const score1d = detail?.scores?.["1d"];

  return (
    <div className="flex flex-col gap-4">
      {/* Header row: title + contextual chips */}
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-lg font-extrabold tracking-[0.08em]" style={{ color: "var(--text)" }}>
          {symbol}
        </h1>
        <span className="chip uppercase tracking-wider">{market}</span>
        {detail?.symbol?.name && <span className="chip">{detail.symbol.name}</span>}
        {lastBar && (
          <span className="chip tnum" style={{ color: "var(--text)" }}>
            {fmtPrice(lastBar.c)}
            {changePct !== null && (
              <span className="ml-1.5" style={{ color: changePct >= 0 ? "var(--bid)" : "var(--ask)" }}>
                {fmtPct(changePct)}
              </span>
            )}
          </span>
        )}
        {score1d && (
          <span className="chip tnum" style={{ color: scoreColor(score1d.score) }}>
            1d {fmtScore(score1d.score)} · {verdict(score1d.score)}
          </span>
        )}
        {detail && (
          <span className="ml-auto flex items-center gap-2 text-[0.75rem] tnum" style={{ color: "var(--faint)" }}>
            {detailErr && <span style={{ color: "var(--bad)" }}>reconnecting…</span>}
            updated {ago(updatedAt)}
          </span>
        )}
      </div>

      {detailErr && !detail && (
        <ErrorState
          message={detailErr}
          retry={() => {
            setDetailErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}
      {!detailErr && !detail && <Skeleton lines={5} label={`loading ${symbol}`} />}

      {/* Chart */}
      <section className="panel">
        <div className="panel-h flex-wrap gap-2">
          <span>PRICE · {symbol}</span>
          {/* Stage 7: signal-overlay toggle (score extremes / regime / breakout). */}
          <button
            type="button"
            onClick={() => setShowOverlays((v) => !v)}
            aria-pressed={showOverlays}
            className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
            title="Overlay score extremes, regime changes, and breakouts on the chart"
            style={{
              color: showOverlays ? "var(--accent)" : "var(--dim)",
              borderColor: showOverlays ? "var(--accent)" : "var(--border)",
            }}
          >
            signals {showOverlays ? "on" : "off"}
          </button>
          <span className="ml-auto flex items-center gap-1" role="tablist" aria-label="timeframe">
            {TFS.map((t) => (
              <button
                key={t}
                type="button"
                role="tab"
                aria-selected={t === tf}
                onClick={() => setTf(t)}
                className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
                title={`Show ${t} bars`}
                style={{
                  color: t === tf ? "var(--accent)" : "var(--dim)",
                  borderColor: t === tf ? "var(--accent)" : "var(--border)",
                }}
              >
                {t}
              </button>
            ))}
          </span>
        </div>
        <div className="p-2">
          {barsErr ? (
            <div className="flex h-[420px] items-center justify-center text-[0.78rem]" style={{ color: "var(--bad)" }}>
              {barsErr} — is the daemon running?
            </div>
          ) : bars === null ? (
            <div className="flex h-[420px] items-center justify-center text-[0.78rem]" style={{ color: "var(--faint)" }}>
              loading…
            </div>
          ) : bars.length === 0 ? (
            <div className="flex h-[420px] items-center justify-center text-[0.78rem]" style={{ color: "var(--faint)" }}>
              no {tf} bars stored yet — backfill runs shortly after subscribing.
            </div>
          ) : (
            <CandleChart bars={bars} tf={tf} height={420} overlays={overlays} />
          )}
          {/* Overlay legend — only when signals are on and there are markers. */}
          {showOverlays && overlays && overlays.length > 0 && (
            <div
              className="flex flex-wrap items-center gap-x-4 gap-y-1 px-2 pb-1 pt-2 text-[0.68rem]"
              style={{ color: "var(--faint)" }}
            >
              <span className="flex items-center gap-1">
                <span style={{ color: "var(--bid)" }}>▲▼</span> score extreme
              </span>
              <span className="flex items-center gap-1">
                <span style={{ color: "var(--accent)" }}>●</span> regime change
              </span>
              <span className="flex items-center gap-1">
                <span style={{ color: "var(--bid)" }}>■</span> breakout
              </span>
              <span className="ml-auto tnum">{overlays.length} markers</span>
            </div>
          )}
        </div>
      </section>

      {detail && (
        <>
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <PressurePanel scores={detail.scores ?? {}} horizon={horizon} onHorizon={setHorizon} />
            <ExpectancyPanel
              expectancy={detail.expectancy ?? {}}
              stateKeys={detail.stateKeys ?? {}}
              horizon={horizon}
              onHorizon={setHorizon}
            />
          </div>

          {/* THIS SYMBOL'S AGENT — the model learned from THIS symbol's own
              resolved outcomes (personality + per-signal skill + honest tier). */}
          <SymbolAgentPanel symbol={symbol} market={market} />

          {/* Signal8 wave Stage 3: this symbol's unusual-activity history —
              imbalance / volatility / volume z-scores vs its OWN baseline
              (descriptive, never predictions; stock imbalance = labeled
              volume-side proxy). */}
          <UnusualActivityPanel symbol={symbol} market={market} limit={8} />

          {/* Signal8 wave Stage 5: FINANCIALS — headline EDGAR company-facts
              (Revenues/EPS/shares/float) + small history sparklines; honest
              "EDGAR sweep pending" until the daily sweep covers this symbol.
              Stocks only (crypto has no SEC filings). */}
          {market === "stocks" && <FinancialsPanel symbol={symbol} />}

          {/* Signal8 wave: SEC filings intelligence — stocks only (crypto has
              no SEC filings). Insider activity + 13F holders + dilution badge,
              all with honest legal-lag labels. */}
          {market === "stocks" && <FilingsIntelPanel symbol={symbol} />}

          {/* Signal8 wave Stage 2: congressional-activity chip — renders only
              when this ticker has disclosed trades in the last 90d (the legal
              30-45d disclosure lag is stated on the chip itself). */}
          {market === "stocks" && (
            <div className="px-1">
              <CongressChip symbol={symbol} />
            </div>
          )}

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {market === "crypto" && detail.latestSnap && (
              <MicroPanel symbol={symbol} market={market} snap={detail.latestSnap} />
            )}
            <div className={market === "crypto" && detail.latestSnap ? "" : "lg:col-span-2"}>
              <InsightsPanel insights={detail.insights} />
            </div>
          </div>

          <CoveragePanel symbol={symbol} market={market} coverage={detail.coverage ?? {}} />
        </>
      )}
    </div>
  );
}
