"use client";
// Demo builds only (NEXT_PUBLIC_DEMO_DATA=1, see MemberSymbolView): the demo's
// tickers are fictional, so TradingView has no chart for them. This draws the
// demo stub's seeded random-walk bars instead, captioned as synthetic.
import { useEffect, useState } from "react";
import { api, type Bar } from "@/lib/api";
import CandleChart from "@/components/symbol/CandleChart";

export default function DemoPriceChart({ symbol, height = 420 }: { symbol: string; height?: number }) {
  const [bars, setBars] = useState<Bar[] | null>(null);
  useEffect(() => {
    let alive = true;
    api.bars(symbol, "stocks", "1d", 250).then(
      (b) => alive && setBars(b),
      () => alive && setBars([]),
    );
    return () => {
      alive = false;
    };
  }, [symbol]);
  return (
    <figure className="panel overflow-hidden" style={{ margin: 0 }}>
      {bars && bars.length > 0 ? <CandleChart bars={bars} tf="1d" height={height} /> : <div style={{ height }} />}
      <figcaption className="px-3 py-2 text-[0.72rem]" style={{ color: "var(--faint)" }}>
        Synthetic demo bars (a seeded random walk for a fictional ticker), not market data.
      </figcaption>
    </figure>
  );
}
