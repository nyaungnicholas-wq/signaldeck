"use client";

// Candlestick + volume chart (lightweight-charts v5). Dark-terminal theme,
// green/red bid-ask semantics. Owns chart lifecycle: create on mount,
// resize via ResizeObserver, dispose on unmount.

import { useEffect, useRef } from "react";
import {
  createChart,
  createSeriesMarkers,
  CandlestickSeries,
  HistogramSeries,
  type IChartApi,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from "lightweight-charts";
import type { Bar, ChartOverlayMarker } from "@/lib/api";

const UP = "#34D399";
const DOWN = "#F87171";
const GRID = "#1E2633";

// Overlay marker palette (kept in the existing dark aesthetic).
const OVERLAY_BULL = "#34D399"; // bullish score / uptrend / up-breakout
const OVERLAY_BEAR = "#F87171"; // bearish score / downtrend / down-breakout
const OVERLAY_NEUTRAL = "#8B98A9"; // directionless (range/squeeze/vol-spike)

export type Tf = "1m" | "1h" | "1d";

// toSeriesMarkers converts the daemon's overlay markers into lightweight-charts
// series markers. Score extremes are arrows below/above the bar; regime changes
// are circles; breakouts are squares. Direction (up) picks green/red; neutral
// events (directionless breakouts, range/squeeze regimes) render gray above bar.
function toSeriesMarkers(overlays: ChartOverlayMarker[]): SeriesMarker<Time>[] {
  const out = overlays.map((o): SeriesMarker<Time> => {
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
    // breakout
    return {
      time: o.ts as UTCTimestamp,
      position: "belowBar",
      shape: "square",
      color: o.up ? OVERLAY_BULL : OVERLAY_NEUTRAL,
      text: o.label,
    };
  });
  // lightweight-charts requires markers sorted ascending by time.
  return out.sort((a, b) => (a.time as number) - (b.time as number));
}

export default function CandleChart({
  bars,
  tf,
  height = 420,
  overlays,
}: {
  bars: Bar[];
  tf: Tf;
  height?: number;
  // Stage 7: optional score/regime/breakout markers to overlay on the chart.
  overlays?: ChartOverlayMarker[];
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volumeRef = useRef<ISeriesApi<"Histogram"> | null>(null);
  const markersRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);
  const fitKeyRef = useRef<string>("");

  // Create the chart once.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const chart = createChart(el, {
      width: el.clientWidth,
      height,
      layout: {
        background: { color: "transparent" },
        textColor: "#8B98A9",
        fontFamily:
          "var(--font-mono), ui-monospace, SFMono-Regular, Menlo, monospace",
        fontSize: 11,
        attributionLogo: false,
      },
      grid: {
        vertLines: { color: GRID },
        horzLines: { color: GRID },
      },
      rightPriceScale: { borderColor: GRID },
      timeScale: { borderColor: GRID, secondsVisible: false },
      crosshair: {
        vertLine: { color: "#5B6675", labelBackgroundColor: "#1E2633" },
        horzLine: { color: "#5B6675", labelBackgroundColor: "#1E2633" },
      },
    });

    const candles = chart.addSeries(CandlestickSeries, {
      upColor: UP,
      downColor: DOWN,
      borderVisible: false,
      wickUpColor: UP,
      wickDownColor: DOWN,
    });

    // Volume histogram on its own overlay scale, squeezed to the bottom.
    const volume = chart.addSeries(HistogramSeries, {
      priceScaleId: "",
      priceFormat: { type: "volume" },
      lastValueVisible: false,
      priceLineVisible: false,
    });
    volume.priceScale().applyOptions({
      scaleMargins: { top: 0.82, bottom: 0 },
    });

    // Stage 7: markers plugin bound to the candle series (score/regime/breakout).
    const markers = createSeriesMarkers(candles, []);

    chartRef.current = chart;
    candleRef.current = candles;
    volumeRef.current = volume;
    markersRef.current = markers;
    fitKeyRef.current = "";

    const ro = new ResizeObserver(() => {
      const w = el.clientWidth;
      if (w > 0) chart.applyOptions({ width: w });
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.remove();
      chartRef.current = null;
      candleRef.current = null;
      volumeRef.current = null;
      markersRef.current = null;
    };
  }, [height]);

  // Push data whenever the bars change; refit only when the series changes
  // (timeframe switch), not on the 60s background refresh.
  useEffect(() => {
    const chart = chartRef.current;
    const candles = candleRef.current;
    const volume = volumeRef.current;
    if (!chart || !candles || !volume) return;

    chart.timeScale().applyOptions({ timeVisible: tf !== "1d" });

    // Sort ascending + dedupe timestamps (setData requires strictly ordered).
    const seen = new Map<number, Bar>();
    for (const b of bars) if (isFinite(b.ts)) seen.set(b.ts, b);
    const clean = [...seen.values()].sort((a, b) => a.ts - b.ts);

    candles.setData(
      clean.map((b) => ({
        time: b.ts as UTCTimestamp,
        open: b.o,
        high: b.h,
        low: b.l,
        close: b.c,
      }))
    );
    volume.setData(
      clean.map((b) => ({
        time: b.ts as UTCTimestamp,
        value: b.v,
        color: b.c >= b.o ? "rgba(52,211,153,0.35)" : "rgba(248,113,113,0.35)",
      }))
    );

    if (fitKeyRef.current !== tf && clean.length > 0) {
      fitKeyRef.current = tf;
      chart.timeScale().fitContent();
    }
  }, [bars, tf]);

  // Stage 7: push overlay markers whenever they change. lightweight-charts snaps
  // each marker to the nearest bar, so score/regime/breakout events land on the
  // right candle. Empty/undefined overlays clear the markers.
  useEffect(() => {
    const plugin = markersRef.current;
    if (!plugin) return;
    plugin.setMarkers(overlays && overlays.length ? toSeriesMarkers(overlays) : []);
  }, [overlays]);

  return (
    <div
      ref={containerRef}
      role="img"
      aria-label={`candlestick chart, ${tf} timeframe, ${bars.length} bars`}
      style={{ height }}
    />
  );
}
