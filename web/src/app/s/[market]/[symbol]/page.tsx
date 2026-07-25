"use client";

// Symbol deep-dive: candlestick chart, pressure decomposition, expectancy
// ("what usually happens next"), microstructure (crypto), insights, coverage.
//
// WHY-IT'S-MOVING wave (2026-07-24) — the story order now follows the MEASURED
// evidence rather than the old verdict-first flow:
//
//   1 · IDENTITY & PRICE   name/sector/exchange + the chart
//   2 · VALIDATED SIGNALS  the regime stack (vol leads — it is the replicated,
//                          significant read) with banded accuracy + tradeability
//   3 · WHY IT'S MOVING    /api/explain attribution, this symbol's headlines,
//                          recent breakouts + unusual activity, then the score
//                          components / expectancy / agent / AI insights
//   4 · EXPERIMENTAL P(UP) the directional read, DEMOTED: it measured 48.08%
//                          directional accuracy against a 54.50% majority-class
//                          null over 12,931 independent symbol-days, its whole
//                          CI sits below the null, and raising conviction makes
//                          it WORSE. It stays visible and stays labeled.
//   5 · THE DETAILS        raw records: financials, filings, short volume AND
//                          real short interest, retail sentiment/attention,
//                          congress, microstructure, coverage.
//
// In SIMPLE view-mode the DETAILS section starts collapsed ("show the
// numbers"); PRO expanded.

import { use, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  api,
  candlePatterns,
  chartOverlays,
  pollMs,
  POLL_DEFAULT,
  POLL_FAST,
  POLL_SLOW,
  symbolAgent,
  trend,
  type Bar,
  type BreakoutRow,
  type CandlePattern,
  type ChartOverlayMarker,
  type CompanyProfile,
  type Horizon,
  type Market,
  type PatternBar,
  type Prediction,
  type SymbolAgent,
  type SymbolDetail,
  type Trendline,
} from "@/lib/api";
import VerdictCard from "@/components/VerdictCard";
import { ago, fmtPct, fmtPrice, fmtScore, scoreColor, verdict } from "@/lib/format";
import CandleChart, { type Tf } from "@/components/symbol/CandleChart";
import IndicatorMenu from "@/components/symbol/IndicatorMenu";
import PatternPopup from "@/components/symbol/PatternPopup";
import { type IndicatorId } from "@/components/symbol/indicators";
import PressurePanel from "@/components/symbol/PressurePanel";
import ExpectancyPanel from "@/components/symbol/ExpectancyPanel";
import MicroPanel from "@/components/symbol/MicroPanel";
import InsightsPanel from "@/components/symbol/InsightsPanel";
import SymbolAgentPanel from "@/components/symbol/SymbolAgentPanel";
import CoveragePanel from "@/components/symbol/CoveragePanel";
import FilingsIntelPanel from "@/components/symbol/FilingsIntelPanel";
import ShortVolumePanel from "@/components/symbol/ShortVolumePanel";
import FinancialsPanel from "@/components/symbol/FinancialsPanel";
import CongressChip from "@/components/symbol/CongressChip";
import UnusualActivityPanel from "@/components/UnusualActivityPanel";
import HelpTip from "@/components/HelpTip";
import PagePurpose from "@/components/PagePurpose";
import StorySection from "@/components/StorySection";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

const TF_LIMIT: Record<Tf, number> = { "1d": 365, "1h": 168, "1m": 390 };
const TFS: Tf[] = ["1d", "1h", "1m"];

// Chart indicator selection persists across sessions. First visit defaults to
// Volume on (preserving the chart's long-standing always-on volume strip, now a
// toggleable indicator in its own pane).
const INDICATORS_KEY = "sd-chart-indicators";
const DEFAULT_INDICATORS: IndicatorId[] = ["volume"];
const VALID_INDICATORS = new Set<IndicatorId>([
  "ma", "bbands", "rsi", "macd", "volume", "stoch", "adx", "atr", "cci", "willr",
  "vwap", "fib", "pivots", "sr", "ichimoku", "supertrend", "psar", "keltner",
]);

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

  // ── Chart analytics: indicators (client-side), patterns + trendlines (API) ──
  // Indicator selection is hydrated from localStorage after first paint (SSR-safe
  // default matches DEFAULT_INDICATORS), then persisted on every change.
  const [indicators, setIndicators] = useState<IndicatorId[]>(DEFAULT_INDICATORS);
  // Hydrate from localStorage AFTER first paint (SSR-safe), and subscribe to the
  // `storage` event so a change in another tab syncs here — a genuine external
  // subscription, the same shape as useViewMode (no bare setState in an effect).
  useEffect(() => {
    const read = () => {
      try {
        const raw = localStorage.getItem(INDICATORS_KEY);
        if (raw === null) return; // keep the current selection
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) {
          setIndicators(parsed.filter((v): v is IndicatorId => VALID_INDICATORS.has(v as IndicatorId)));
        }
      } catch {
        /* corrupt value — keep the current selection */
      }
    };
    read();
    window.addEventListener("storage", read);
    return () => window.removeEventListener("storage", read);
  }, []);
  const updateIndicators = (ids: IndicatorId[]) => {
    setIndicators(ids);
    try {
      localStorage.setItem(INDICATORS_KEY, JSON.stringify(ids));
    } catch {
      /* storage full / disabled — selection still works this session */
    }
  };

  const [showPatterns, setShowPatterns] = useState(true);
  const [showTrend, setShowTrend] = useState(true);

  // Patterns are per-timeframe; keyed so a symbol/tf switch never shows stale
  // markers. Failures are silent — the chart renders fine without them.
  const [patternsState, setPatternsState] =
    useState<{ key: string; bars: PatternBar[]; note: string } | null>(null);
  const patternsKey = `${symbol}|${market}|${tf}`;
  const patternPayload =
    showPatterns && patternsState && patternsState.key === patternsKey ? patternsState : null;

  const [trendState, setTrendState] =
    useState<{ key: string; lines: Trendline[]; classification: string; slope: number; channel: boolean; note: string } | null>(null);
  const trendPayload =
    showTrend && trendState && trendState.key === patternsKey ? trendState : null;

  // The clicked candle's patterns (keyed to bars so a tf/symbol switch hides it).
  const [selected, setSelected] = useState<{ key: string; ts: number; patterns: CandlePattern[] } | null>(null);
  const selectedShown = selected && selected.key === patternsKey ? selected : null;

  // Stage 2 (verdict cards): the hero verdict's inputs — the REAL calibrated
  // 1d prediction + the symbol-agent evidence tier. Keyed by symbol so a
  // symbol switch never flashes another symbol's verdict; failures are soft
  // (the card renders the honest "NO READ YET", never a fabricated lean).
  const heroKey = `${symbol}|${market}`;
  const [heroPreds, setHeroPreds] = useState<{ key: string; p: Record<string, Prediction> } | null>(null);
  const [heroAgent, setHeroAgent] = useState<{ key: string; a: SymbolAgent } | null>(null);
  useEffect(() => {
    if (!marketOk) return;
    let alive = true;
    const key = `${symbol}|${market}`;
    const load = () => {
      api
        .predictions(symbol, market)
        .then((p) => alive && setHeroPreds({ key, p }))
        .catch(() => {});
      symbolAgent(symbol, market, "1d")
        .then((a) => alive && setHeroAgent({ key, a }))
        .catch(() => {});
    };
    load();
    // POLL_DEFAULT: predictions and the agent tier move on model cadence,
    // not per-second.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, marketOk, retryTick]);

  // Poll the detail payload on the FAST tier — it drives the price chip and
  // the WHY panels on an actively-watched page.
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
    const stop = pollMs(load, POLL_FAST);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, marketOk, retryTick]);

  // Bars: refetch on timeframe change + a DEFAULT-tier background refresh.
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
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, tf, marketOk]);

  // Overlays: fetch once per symbol + a SLOW-tier background refresh. Failures
  // are silent (overlays are an enhancement; the chart renders without them).
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
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, marketOk, showOverlays]);

  // Patterns: fetch per symbol+market+tf when enabled; SLOW background refresh.
  // Go nil slices arrive as JSON null, hence `?? []`. Silent on failure.
  useEffect(() => {
    if (!marketOk || !showPatterns) return;
    let alive = true;
    const key = `${symbol}|${market}|${tf}`;
    const load = () =>
      candlePatterns(symbol, market, tf)
        .then((p) => {
          if (!alive) return;
          setPatternsState({ key, bars: p.bars ?? [], note: p.note ?? "" });
        })
        .catch(() => {
          /* feature silently absent (404 / daemon not yet shipping it) */
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, tf, marketOk, showPatterns]);

  // Trend: fetch per symbol+market+tf when enabled; SLOW background refresh.
  useEffect(() => {
    if (!marketOk || !showTrend) return;
    let alive = true;
    const key = `${symbol}|${market}|${tf}`;
    const load = () =>
      trend(symbol, market, tf)
        .then((t) => {
          if (!alive) return;
          setTrendState({
            key,
            lines: t.trendlines ?? [],
            classification: t.classification,
            slope: t.slopePctPerBar,
            channel: t.channel,
            note: t.note ?? "",
          });
        })
        .catch(() => {
          /* feature silently absent */
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, tf, marketOk, showTrend]);

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
      <section className="panel p-6 text-[0.75rem]">
        <p style={{ color: "var(--bad)" }}>
          unknown market &ldquo;{p.market}&rdquo; — expected /s/crypto/… or /s/stocks/…
        </p>
        <p className="mt-2">
          <Link
            href="/"
            className="cursor-pointer text-[var(--dim)] underline transition-colors duration-150 hover:text-[var(--text)]"
          >
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
        <h1 className="mono text-lg font-extrabold tracking-[0.08em]" style={{ color: "var(--text)" }}>
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

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="symbol"
        text={`What does SignalDeck currently make of ${symbol}? The verdict first, the reasons behind it second, the raw records last. Every read carries its evidence tier — 'no read yet' is a real answer here.`}
      />

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

      {/* ── STAGE 3 STORY, SECTION 1 ── */}
      <StorySection
        n={1}
        title="THE VERDICT"
        sub="the model's honest current read — plus the price itself"
      >
      {/* Stage 2: the hero VERDICT card — the one honest read-out. Real
          calibrated 1d P(up) or "NO READ YET"; the evidence-tier badge
          ("still learning 12/40 — using global model") is always visible. */}
      <VerdictCard
        size="lg"
        symbol={symbol}
        market={market}
        horizon="1d"
        calProb={
          heroPreds?.key === heroKey && typeof heroPreds.p?.["1d"]?.calProb === "number"
            ? heroPreds.p["1d"].calProb
            : null
        }
        nUsed={heroPreds?.key === heroKey ? (heroPreds.p?.["1d"]?.nUsed ?? 0) : 0}
        tier={heroAgent?.key === heroKey ? heroAgent.a.tier : ""}
        tierProgress={{
          nSamples: heroAgent?.key === heroKey ? (heroAgent.a.nSamples ?? 0) : 0,
          threshold: heroAgent?.key === heroKey ? (heroAgent.a.threshold ?? 0) : 0,
        }}
        sparkCloses={
          tf === "1d" && bars && bars.length > 0
            ? [...bars].sort((a, b) => a.ts - b.ts).slice(-30).map((b) => b.c)
            : undefined
        }
      />

      {/* Chart */}
      <section className="panel">
        <div className="panel-h flex-wrap gap-2">
          <span>PRICE · {symbol}</span>

          {/* Client-side technical indicators (RSI/MACD/BB/EMA/…): grouped
              toggle menu, computed in-browser from the bars already loaded. */}
          <IndicatorMenu value={indicators} onChange={updateIndicators} />

          {/* Patterns: dot markers + click-a-candle detail. */}
          <button
            type="button"
            onClick={() => setShowPatterns((v) => !v)}
            aria-pressed={showPatterns}
            className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
            style={{
              color: showPatterns ? "var(--accent)" : "var(--dim)",
              borderColor: showPatterns ? "var(--accent)" : "var(--border)",
            }}
          >
            patterns {showPatterns ? "on" : "off"}
          </button>

          {/* Trendlines + trend classification. */}
          <button
            type="button"
            onClick={() => setShowTrend((v) => !v)}
            aria-pressed={showTrend}
            className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
            style={{
              color: showTrend ? "var(--accent)" : "var(--dim)",
              borderColor: showTrend ? "var(--accent)" : "var(--border)",
            }}
          >
            trend {showTrend ? "on" : "off"}
          </button>

          {/* Stage 7: signal-overlay toggle (score extremes / regime / breakout). */}
          <button
            type="button"
            onClick={() => setShowOverlays((v) => !v)}
            aria-pressed={showOverlays}
            className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
            style={{
              color: showOverlays ? "var(--accent)" : "var(--dim)",
              borderColor: showOverlays ? "var(--accent)" : "var(--border)",
            }}
          >
            signals {showOverlays ? "on" : "off"}
          </button>
          <HelpTip label="what the chart tools show">
            Indicators are computed in your browser from the loaded bars. Patterns
            marks candles with recognized shapes (click one for its measured
            edge). Trend draws support/resistance lines. Signals marks past score
            extremes, regime changes and breakouts. All are context, not forecasts.
          </HelpTip>

          {/* Trend classification chip (rendered when the trend read is on). */}
          {trendPayload && (
            <span
              className="chip tnum uppercase tracking-wider"
              style={{
                color:
                  trendPayload.classification === "uptrend"
                    ? "var(--bid)"
                    : trendPayload.classification === "downtrend"
                      ? "var(--ask)"
                      : "var(--dim)",
                borderColor:
                  trendPayload.classification === "uptrend"
                    ? "var(--bid)"
                    : trendPayload.classification === "downtrend"
                      ? "var(--ask)"
                      : "var(--border)",
              }}
              title={`slope ${trendPayload.slope >= 0 ? "+" : ""}${trendPayload.slope.toFixed(3)}% per bar${trendPayload.channel ? " · channel" : ""}`}
            >
              {trendPayload.classification}
              <span className="ml-1.5" style={{ color: "var(--faint)" }}>
                {trendPayload.slope >= 0 ? "+" : ""}
                {trendPayload.slope.toFixed(2)}%/bar
                {trendPayload.channel ? " · channel" : ""}
              </span>
            </span>
          )}

          <span className="ml-auto flex items-center gap-1" role="tablist" aria-label="timeframe">
            {TFS.map((t) => (
              <button
                key={t}
                type="button"
                role="tab"
                aria-selected={t === tf}
                onClick={() => setTf(t)}
                className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
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
            <div className="flex h-[420px] items-center justify-center text-[0.75rem]" style={{ color: "var(--bad)" }}>
              {barsErr} — is the daemon running?
            </div>
          ) : bars === null ? (
            <div className="flex h-[420px] items-center justify-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
              loading…
            </div>
          ) : bars.length === 0 ? (
            <div className="flex h-[420px] items-center justify-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
              no {tf} bars stored yet — backfill runs shortly after subscribing.
            </div>
          ) : (
            <CandleChart
              bars={bars}
              tf={tf}
              height={420}
              overlays={overlays}
              indicators={indicators}
              patternBars={patternPayload?.bars}
              onCandleClick={(ts, patterns) =>
                setSelected(patterns.length ? { key: patternsKey, ts, patterns } : null)
              }
              trendlines={trendPayload?.lines}
              showTrend={showTrend}
            />
          )}

          {/* Click-a-candle pattern detail (only for a candle that has one). */}
          {selectedShown && (
            <PatternPopup
              ts={selectedShown.ts}
              patterns={selectedShown.patterns}
              note={patternPayload?.note}
              onClose={() => setSelected(null)}
            />
          )}

          {/* Chart legend — indicators/patterns/trend hints + overlay markers. */}
          {(showOverlays && overlays && overlays.length > 0) ||
          (showPatterns && patternPayload && patternPayload.bars.length > 0) ||
          (showTrend && trendPayload && trendPayload.lines.length > 0) ? (
            <div
              className="flex flex-wrap items-center gap-x-4 gap-y-1 px-2 pb-1 pt-2 text-[0.75rem]"
              style={{ color: "var(--faint)" }}
            >
              {showPatterns && patternPayload && patternPayload.bars.length > 0 && (
                <span className="flex items-center gap-1">
                  <span style={{ color: "var(--bid)" }}>●</span> pattern (click a candle)
                </span>
              )}
              {showTrend && trendPayload && trendPayload.lines.length > 0 && (
                <span className="flex items-center gap-1">
                  <span style={{ color: "var(--bid)" }}>╱</span> support ·{" "}
                  <span style={{ color: "var(--ask)" }}>╲</span> resistance
                </span>
              )}
              {showOverlays && overlays && overlays.length > 0 && (
                <>
                  <span className="flex items-center gap-1">
                    <span style={{ color: "var(--bid)" }}>▲▼</span> score extreme
                  </span>
                  <span className="flex items-center gap-1">
                    <span style={{ color: "var(--accent)" }}>●</span> regime change
                  </span>
                  <span className="flex items-center gap-1">
                    <span style={{ color: "var(--bid)" }}>■</span> breakout
                  </span>
                </>
              )}
            </div>
          ) : null}

          {/* Trend note — rendered verbatim per the honesty doctrine. */}
          {showTrend && trendPayload && trendPayload.note.trim() && (
            <p className="px-2 pb-1 pt-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              {trendPayload.note}
            </p>
          )}
        </div>
      </section>
      </StorySection>

      {detail && (
        <>
          {/* ── STAGE 3 STORY, SECTION 2 ── */}
          <StorySection
            n={2}
            title="WHY"
            sub="what is pushing the read: score components, what usually follows, this symbol's own agent"
          >
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

          <InsightsPanel insights={detail.insights} />
          </StorySection>

          {/* ── STAGE 3 STORY, SECTION 3 (SIMPLE mode starts folded) ── */}
          <StorySection
            n={3}
            title="THE DETAILS"
            sub="raw records: unusual activity, financials, SEC filings, shorts, coverage"
            collapsible
          >
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

          {/* Stage 5 FINRA Reg SHO: daily short sale VOLUME ratio (free FINRA
              files, universe-scoped) with the NOT-short-interest caveat
              rendered verbatim. Stocks only (crypto has no Reg SHO data). */}
          {market === "stocks" && <ShortVolumePanel symbol={symbol} />}

          {/* Signal8 wave Stage 2: congressional-activity chip — renders only
              when this ticker has disclosed trades in the last 90d (the legal
              30-45d disclosure lag is stated on the chip itself). */}
          {market === "stocks" && (
            <div className="px-1">
              <CongressChip symbol={symbol} />
            </div>
          )}

          {market === "crypto" && detail.latestSnap && (
            <MicroPanel symbol={symbol} market={market} snap={detail.latestSnap} />
          )}

          <CoveragePanel symbol={symbol} market={market} coverage={detail.coverage ?? {}} />
          </StorySection>
        </>
      )}
    </div>
  );
}
