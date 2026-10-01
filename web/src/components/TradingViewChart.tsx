"use client";
// The widget is TradingView's free embed with its branding kept.
// The page's Content-Security-Policy must allow script https://s3.tradingview.com
// and frames from https://www.tradingview-widget.com and https://s.tradingview.com.
import { useEffect, useRef } from "react";

export default function TradingViewChart({ symbol, market, height = 420 }: { symbol: string; market: "crypto" | "stocks"; height?: number }) {
  const tvSymbol = market === "stocks" ? symbol.toUpperCase() : `COINBASE:${symbol.replace(/\//g, "").toUpperCase()}`;
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = ref.current;
    if (!container) return;
    container.replaceChildren();

    const widgetDiv = document.createElement('div');
    widgetDiv.className = "tradingview-widget-container__widget";
    widgetDiv.style.height = "100%";
    widgetDiv.style.width = "100%";
    container.appendChild(widgetDiv);

    const script = document.createElement('script');
    script.src = "https://s3.tradingview.com/external-embedding/embed-widget-advanced-chart.js";
    script.type = "text/javascript";
    script.async = true;
    script.textContent = JSON.stringify({
      autosize: true,
      symbol: tvSymbol,
      interval: "D",
      timezone: "Etc/UTC",
      theme: "dark",
      style: "1",
      locale: "en",
      allow_symbol_change: false,
      hide_side_toolbar: true,
      save_image: false,
      calendar: false,
      support_host: "https://www.tradingview.com"
    });
    container.appendChild(script);

    return () => {
      if (container) container.replaceChildren();
    };
  }, [tvSymbol]);

  return (
    <figure className="panel overflow-hidden" style={{ margin: 0 }}>
      <div className="tradingview-widget-container" ref={ref} style={{ height, width: "100%" }} />
      <figcaption className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-[0.72rem]" style={{ color: "var(--faint)" }}>
        <span>Price chart by TradingView. SignalDeck redistributes no price data.</span>
        <a href="https://www.tradingview.com/" rel="noopener nofollow" target="_blank" style={{ color: "var(--dim)" }}>TradingView</a>
      </figcaption>
    </figure>
  );
}