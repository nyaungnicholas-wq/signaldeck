"use client";

// Candlestick + volume chart (lightweight-charts v5). Dark-terminal theme,
// green/red bid-ask semantics. Owns chart lifecycle: create on mount,
// resize via ResizeObserver, dispose on unmount.

import { useEffect, useRef } from "react";
import {
  createChart,
  CandlestickSeries,
  HistogramSeries,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from "lightweight-charts";
import type { Bar } from "@/lib/api";

const UP = "#34D399";
const DOWN = "#F87171";
const GRID = "#1E2633";

export type Tf = "1m" | "1h" | "1d";

export default function CandleChart({
  bars,
  tf,
  height = 420,
}: {
  bars: Bar[];
  tf: Tf;
  height?: number;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volumeRef = useRef<ISeriesApi<"Histogram"> | null>(null);
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

    chartRef.current = chart;
    candleRef.current = candles;
    volumeRef.current = volume;
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

  return (
    <div
      ref={containerRef}
      role="img"
      aria-label={`candlestick chart, ${tf} timeframe, ${bars.length} bars`}
      style={{ height }}
    />
  );
}
