"use client";

// Candlestick chart (lightweight-charts v5). Dark-terminal theme, green/red
// bid-ask semantics. Owns the chart lifecycle plus three analysis layers, each
// pushed imperatively so a lint-clean render body never touches the canvas:
//
//   • INDICATORS — client-side TA (src/components/symbol/indicators.ts). Overlay
//     indicators draw on the price pane; oscillators each get their OWN v5 pane
//     via addSeries(def, opts, paneIndex) — passing the next free index makes the
//     library create the pane (see _getOrCreatePane). Everything is rebuilt in a
//     single synchronous effect tick (no visible flicker) whenever the enabled
//     set or the bars change; only ENABLED indicators are computed.
//   • PATTERNS — a dot marker under/over each candle that carries a pattern
//     (merged into the same createSeriesMarkers plugin as the score/regime
//     overlays), plus a click subscription that resolves the clicked bar to its
//     patterns and calls onCandleClick.
//   • TRENDLINES — each daemon trendline as a 2-point v5 line series.
//
// Every layer degrades to nothing on empty input — the base candles always draw.

import { useEffect, useMemo, useRef } from "react";
import {
  createChart,
  createSeriesMarkers,
  createTextWatermark,
  CandlestickSeries,
  HistogramSeries,
  LineSeries,
  LineStyle,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type ITextWatermarkPluginApi,
  type HistogramData,
  type LineData,
  type SeriesMarker,
  type SeriesType,
  type Time,
  type UTCTimestamp,
} from "lightweight-charts";
import type { Bar, CandlePattern, ChartOverlayMarker, PatternBar, Trendline } from "@/lib/api";
import {
  computeIndicator,
  indicatorMeta,
  type DashStyle,
  type HistPoint,
  type IndicatorId,
  type LinePoint,
  type PlotSpec,
} from "@/components/symbol/indicators";

const UP = "#34D399";
const DOWN = "#F87171";
const GRID = "#1e2637"; // --border

// Overlay marker palette (kept in the existing dark aesthetic).
const OVERLAY_BULL = "#34D399"; // bullish score / uptrend / up-breakout
const OVERLAY_BEAR = "#F87171"; // bearish score / downtrend / down-breakout
const OVERLAY_NEUTRAL = "#94a3b8"; // --dim — directionless (range/squeeze/vol-spike)

const PATTERN_BULL = "#34D399";
const PATTERN_BEAR = "#F87171";
const PATTERN_NEUTRAL = "#94a3b8";
const TREND_SUPPORT = "#34D399";
const TREND_RESISTANCE = "#F87171";

export type Tf = "1m" | "1h" | "1d";

function dashToStyle(s?: DashStyle): LineStyle {
  return s === "dashed" ? LineStyle.Dashed : s === "dotted" ? LineStyle.Dotted : LineStyle.Solid;
}

// toSeriesMarkers converts the daemon's overlay markers into lightweight-charts
// series markers. Score extremes are arrows below/above the bar; regime changes
// are circles; breakouts are squares. Direction (up) picks green/red; neutral
// events (directionless breakouts, range/squeeze regimes) render gray above bar.
function toSeriesMarkers(overlays: ChartOverlayMarker[]): SeriesMarker<Time>[] {
  return overlays.map((o): SeriesMarker<Time> => {
    const color = o.up
      ? OVERLAY_BULL
      : o.type === "score" || o.type === "regime"
        ? OVERLAY_BEAR
        : OVERLAY_NEUTRAL;
    if (o.type === "score") {
      return {
        time: o.ts as UTCTimestamp,
        position: o.up ? "belowBar" : "aboveBar",
        shape: o.up ? "arrowUp" : "arrowDown",
        color,
        text: o.label,
      };
    }
    if (o.type === "regime") {
      return {
        time: o.ts as UTCTimestamp,
        position: "aboveBar",
        shape: "circle",
        color: o.up ? OVERLAY_BULL : OVERLAY_NEUTRAL,
        text: o.label,
      };
    }
    return {
      time: o.ts as UTCTimestamp,
      position: "belowBar",
      shape: "square",
      color: o.up ? OVERLAY_BULL : OVERLAY_NEUTRAL,
      text: o.label,
    };
  });
}

// Net bias across the patterns on one bar: any bear wins over bull (honest —
// conflicting signals shouldn't read bullish); bull only when no bear present.
function netBias(patterns: CandlePattern[]): -1 | 0 | 1 {
  let bull = false;
  let bear = false;
  for (const p of patterns) {
    if (p.bias > 0) bull = true;
    else if (p.bias < 0) bear = true;
  }
  if (bear) return -1;
  if (bull) return 1;
  return 0;
}

// Pattern dots: bull below (green), bear above (red), neutral above (gray).
function patternsToMarkers(patternBars: PatternBar[]): SeriesMarker<Time>[] {
  const out: SeriesMarker<Time>[] = [];
  for (const pb of patternBars) {
    if (!pb.patterns || !pb.patterns.length) continue;
    const b = netBias(pb.patterns);
    out.push({
      time: pb.ts as UTCTimestamp,
      position: b > 0 ? "belowBar" : "aboveBar",
      shape: "circle",
      color: b > 0 ? PATTERN_BULL : b < 0 ? PATTERN_BEAR : PATTERN_NEUTRAL,
    });
  }
  return out;
}

const asLine = (d: LinePoint[]): LineData<Time>[] =>
  d.map((p) =>
    p.color
      ? { time: p.time as UTCTimestamp, value: p.value, color: p.color }
      : { time: p.time as UTCTimestamp, value: p.value },
  );

const asHist = (d: HistPoint[]): HistogramData<Time>[] =>
  d.map((p) => ({ time: p.time as UTCTimestamp, value: p.value, color: p.color }));

export default function CandleChart({
  bars,
  tf,
  height = 420,
  overlays,
  indicators = [],
  patternBars,
  onCandleClick,
  trendlines,
  showTrend = false,
}: {
  bars: Bar[];
  tf: Tf;
  height?: number;
  // Score/regime/breakout markers (Stage 7).
  overlays?: ChartOverlayMarker[];
  // Enabled indicator ids (ordered) — only these are computed & drawn.
  indicators?: IndicatorId[];
  // Per-bar detected patterns → dot markers + click lookup.
  patternBars?: PatternBar[];
  onCandleClick?: (ts: number, patterns: CandlePattern[]) => void;
  // Daemon trendlines (2-point support/resistance lines).
  trendlines?: Trendline[];
  showTrend?: boolean;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const markersRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);
  const fitKeyRef = useRef<string>("");

  // Indicator layer bookkeeping (rebuilt each effect run).
  const indSeriesRef = useRef<ISeriesApi<SeriesType>[]>([]);
  const candleLevelsRef = useRef<IPriceLine[]>([]);
  // Per-pane "type" labels (RSI, MACD, …) so an enabled indicator is never an
  // anonymous line/pane — detached and rebuilt alongside the series.
  const indLabelsRef = useRef<ITextWatermarkPluginApi<Time>[]>([]);
  // Trendline series (rebuilt on trend changes).
  const trendSeriesRef = useRef<ISeriesApi<"Line">[]>([]);

  // Click plumbing — refs so the once-subscribed handler always sees the latest.
  const onClickRef = useRef(onCandleClick);
  const patternIndexRef = useRef<Map<number, CandlePattern[]>>(new Map());
  const barTimesRef = useRef<number[]>([]);

  // Sorted, timestamp-deduped bars — lightweight-charts requires strict order,
  // and every indicator reads the same cleaned series.
  const cleanBars = useMemo(() => {
    const seen = new Map<number, Bar>();
    for (const b of bars) if (isFinite(b.ts)) seen.set(b.ts, b);
    return [...seen.values()].sort((a, b) => a.ts - b.ts);
  }, [bars]);

  // Create the chart once (candles + markers plugin + click subscription).
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const chart = createChart(el, {
      width: el.clientWidth,
      height,
      layout: {
        background: { color: "transparent" },
        textColor: "#94a3b8", // --dim
        fontFamily: "var(--font-mono), ui-monospace, SFMono-Regular, Menlo, monospace",
        fontSize: 12,
        attributionLogo: false,
      },
      grid: {
        vertLines: { color: GRID },
        horzLines: { color: GRID },
      },
      rightPriceScale: { borderColor: GRID },
      timeScale: { borderColor: GRID, secondsVisible: false },
      crosshair: {
        vertLine: { color: "#7a8595", labelBackgroundColor: "#151d2c" },
        horzLine: { color: "#7a8595", labelBackgroundColor: "#151d2c" },
      },
    });

    const candles = chart.addSeries(CandlestickSeries, {
      upColor: UP,
      downColor: DOWN,
      borderVisible: false,
      wickUpColor: UP,
      wickDownColor: DOWN,
    });

    const markers = createSeriesMarkers(candles, []);

    // Resolve a click to the nearest bar's patterns and hand it up.
    const onClick = (param: { time?: Time; point?: unknown }) => {
      const cb = onClickRef.current;
      if (!cb) return;
      const times = barTimesRef.current;
      if (!times.length) return;
      const t = typeof param.time === "number" ? param.time : NaN;
      let best = times[0];
      let bestD = Infinity;
      if (Number.isFinite(t)) {
        for (const bt of times) {
          const d = Math.abs(bt - t);
          if (d < bestD) {
            bestD = d;
            best = bt;
          }
        }
      } else {
        return;
      }
      cb(best, patternIndexRef.current.get(best) ?? []);
    };
    chart.subscribeClick(onClick);

    chartRef.current = chart;
    candleRef.current = candles;
    markersRef.current = markers;
    fitKeyRef.current = "";
    indSeriesRef.current = [];
    candleLevelsRef.current = [];
    trendSeriesRef.current = [];

    const ro = new ResizeObserver(() => {
      const w = el.clientWidth;
      if (w > 0) chart.applyOptions({ width: w });
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.unsubscribeClick(onClick);
      chart.remove();
      chartRef.current = null;
      candleRef.current = null;
      markersRef.current = null;
      indSeriesRef.current = [];
      candleLevelsRef.current = [];
      trendSeriesRef.current = [];
    };
  }, [height]);

  // Keep the click handler ref current every render (cheap, no deps).
  useEffect(() => {
    onClickRef.current = onCandleClick;
  });

  // Push candle data; refit only on timeframe switch, not the 60s refresh.
  useEffect(() => {
    const chart = chartRef.current;
    const candles = candleRef.current;
    if (!chart || !candles) return;

    chart.timeScale().applyOptions({ timeVisible: tf !== "1d" });
    candles.setData(
      cleanBars.map((b) => ({
        time: b.ts as UTCTimestamp,
        open: b.o,
        high: b.h,
        low: b.l,
        close: b.c,
      })),
    );
    barTimesRef.current = cleanBars.map((b) => b.ts);

    if (fitKeyRef.current !== tf && cleanBars.length > 0) {
      fitKeyRef.current = tf;
      chart.timeScale().fitContent();
    }
  }, [cleanBars, tf]);

  // Indicators: rebuild every enabled indicator in one synchronous tick.
  useEffect(() => {
    const chart = chartRef.current;
    const candle = candleRef.current;
    if (!chart || !candle) return;

    // Teardown the previous build.
    for (const l of candleLevelsRef.current) {
      try {
        candle.removePriceLine(l);
      } catch {
        /* series may already be gone */
      }
    }
    candleLevelsRef.current = [];
    for (const s of indSeriesRef.current) {
      try {
        chart.removeSeries(s);
      } catch {
        /* already removed */
      }
    }
    indSeriesRef.current = [];
    // Detach the previous pane labels before panes are torn down.
    for (const wm of indLabelsRef.current) {
      try {
        wm.detach();
      } catch {
        /* already detached */
      }
    }
    indLabelsRef.current = [];

    // Collapse every pane except the price pane (index 0).
    const existing = chart.panes();
    for (let i = existing.length - 1; i >= 1; i--) {
      try {
        chart.removePane(i);
      } catch {
        /* pane already gone */
      }
    }

    if (!cleanBars.length || !indicators.length) return;

    // A left-anchored "NAME · params" label for a pane, colored to the
    // indicator, so the chart always names the type of indicator it's showing.
    const labelPane = (paneIndex: number, lines: { text: string; color: string }[]) => {
      const panes = chart.panes();
      if (paneIndex >= panes.length || !lines.length) return;
      const wm = createTextWatermark(panes[paneIndex], {
        horzAlign: "left",
        vertAlign: "top",
        lines: lines.map((l) => ({ text: l.text, color: l.color, fontSize: 11, fontStyle: "bold" })),
      });
      indLabelsRef.current.push(wm);
    };
    // Overlay indicators share the price pane, so their labels stack there.
    const overlayLabels: { text: string; color: string }[] = [];

    const addPlot = (plot: PlotSpec, paneIndex: number): ISeriesApi<SeriesType> => {
      const lastValueVisible = paneIndex !== 0;
      if (plot.kind === "histogram") {
        const s = chart.addSeries(
          HistogramSeries,
          {
            color: plot.color,
            priceLineVisible: false,
            lastValueVisible,
            ...(plot.id === "vol" ? { priceFormat: { type: "volume" as const } } : {}),
          },
          paneIndex,
        );
        s.setData(asHist(plot.data as HistPoint[]));
        indSeriesRef.current.push(s);
        return s;
      }
      const common = {
        color: plot.color,
        lineWidth: plot.lineWidth ?? 2,
        lineStyle: dashToStyle(plot.style),
        priceLineVisible: false,
        lastValueVisible,
        crosshairMarkerVisible: paneIndex !== 0,
      };
      const opts =
        plot.kind === "dots"
          ? { ...common, lineVisible: false, pointMarkersVisible: true, pointMarkersRadius: 2 }
          : common;
      const s = chart.addSeries(LineSeries, opts, paneIndex);
      s.setData(asLine(plot.data as LinePoint[]));
      indSeriesRef.current.push(s);
      return s;
    };

    for (const id of indicators) {
      const r = computeIndicator(id, cleanBars, tf);
      if (!r) continue;
      const meta = indicatorMeta(id);
      const labelText = `${meta.name} · ${meta.params}`;
      const labelColor = r.plots[0]?.color ?? "#94a3b8";
      if (r.pane === "own") {
        const paneIndex = chart.panes().length; // next free index → new pane
        let host: ISeriesApi<SeriesType> | null = null;
        for (const plot of r.plots) {
          const s = addPlot(plot, paneIndex);
          if (!host) host = s;
        }
        if (host) {
          for (const lv of r.levels) {
            host.createPriceLine({
              price: lv.price,
              color: lv.color,
              lineStyle: dashToStyle(lv.style),
              lineWidth: 1,
              axisLabelVisible: true,
              title: lv.title ?? "",
            });
          }
        }
        // Name the oscillator pane immediately (its index is stable now).
        labelPane(paneIndex, [{ text: labelText, color: labelColor }]);
      } else {
        for (const plot of r.plots) addPlot(plot, 0);
        for (const lv of r.levels) {
          const pl = candle.createPriceLine({
            price: lv.price,
            color: lv.color,
            lineStyle: dashToStyle(lv.style),
            lineWidth: 1,
            axisLabelVisible: true,
            title: lv.title ?? "",
          });
          candleLevelsRef.current.push(pl);
        }
        overlayLabels.push({ text: labelText, color: labelColor });
      }
    }

    // Stack every overlay indicator's label in the top-left of the price pane.
    labelPane(0, overlayLabels);

    // Give the price pane the lion's share of the height.
    const panes = chart.panes();
    if (panes.length > 1) {
      panes[0].setStretchFactor(3);
      for (let i = 1; i < panes.length; i++) panes[i].setStretchFactor(1);
    }
  }, [cleanBars, tf, indicators]);

  // Markers: merge score/regime/breakout overlays with pattern dots (one plugin
  // per series, so both sets go through a single sorted setMarkers call).
  useEffect(() => {
    const plugin = markersRef.current;
    if (!plugin) return;
    // Index patterns by ts for the click lookup.
    const idx = new Map<number, CandlePattern[]>();
    for (const pb of patternBars ?? []) {
      if (pb.patterns && pb.patterns.length) idx.set(pb.ts, pb.patterns);
    }
    patternIndexRef.current = idx;

    const merged: SeriesMarker<Time>[] = [
      ...(overlays && overlays.length ? toSeriesMarkers(overlays) : []),
      ...(patternBars && patternBars.length ? patternsToMarkers(patternBars) : []),
    ].sort((a, b) => (a.time as number) - (b.time as number));
    plugin.setMarkers(merged);
  }, [overlays, patternBars]);

  // Trendlines: one 2-point line series each (support green / resistance red,
  // dashed). Rebuilt whenever the trend payload or toggle changes.
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    for (const s of trendSeriesRef.current) {
      try {
        chart.removeSeries(s);
      } catch {
        /* already removed */
      }
    }
    trendSeriesRef.current = [];
    if (!showTrend || !trendlines || !trendlines.length) return;

    for (const t of trendlines) {
      const a = Math.min(t.fromTs, t.toTs);
      const b = Math.max(t.fromTs, t.toTs);
      if (a === b) continue;
      const from = t.fromTs <= t.toTs ? t : { ...t, fromTs: t.toTs, fromPrice: t.toPrice, toTs: t.fromTs, toPrice: t.fromPrice };
      const s = chart.addSeries(LineSeries, {
        color: t.kind === "support" ? TREND_SUPPORT : TREND_RESISTANCE,
        lineWidth: 2,
        lineStyle: LineStyle.Dashed,
        priceLineVisible: false,
        lastValueVisible: false,
        crosshairMarkerVisible: false,
      });
      s.setData([
        { time: from.fromTs as UTCTimestamp, value: from.fromPrice },
        { time: from.toTs as UTCTimestamp, value: from.toPrice },
      ]);
      trendSeriesRef.current.push(s);
    }
  }, [trendlines, showTrend]);

  return (
    <div
      ref={containerRef}
      role="img"
      aria-label={`candlestick chart, ${tf} timeframe, ${bars.length} bars`}
      style={{ height }}
    />
  );
}
