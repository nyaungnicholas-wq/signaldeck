"use client";

// TVRatingChip — TradingView's OWN technical-analysis rating for the selected
// symbol, shown as EXTERNAL CONTEXT beside the SignalScore hero. Honesty
// framing is non-negotiable: this is a delayed, descriptive rating from
// TradingView's public scanner — not our model, never advice — and the API's
// note string renders verbatim in the tooltip line.

import { useEffect, useState } from "react";
import { tvRating, type TVRating, type Market } from "@/lib/api";

const LABEL_COLOR: Record<string, string> = {
  "Strong Buy": "var(--bid)",
  Buy: "var(--bid)",
  Neutral: "var(--dim)",
  Sell: "var(--ask)",
  "Strong Sell": "var(--ask)",
};

export default function TVRatingChip({
  symbol,
  market,
}: {
  symbol: string;
  market: Market;
}) {
  // State keyed by market:symbol so a symbol switch never flashes a stale
  // rating (and no synchronous setState inside the effect body).
  const [slot, setSlot] = useState<{ key: string; data: TVRating; ageMin: number | null } | null>(null);
  const key = `${market}:${symbol}`;

  useEffect(() => {
    let dead = false;
    tvRating(symbol, market)
      .then((r) => {
        if (!dead)
          setSlot({
            key: `${market}:${symbol}`,
            data: r,
            ageMin: r.ts ? Math.max(0, Math.round((Date.now() / 1000 - r.ts) / 60)) : null,
          });
      })
      .catch(() => {
        /* external context is best-effort — absent beats wrong */
      });
    return () => {
      dead = true;
    };
  }, [symbol, market]);

  const data = slot?.key === key ? slot.data : null;
  if (!data?.available || !data.label) return null;
  const age = slot?.key === key ? slot.ageMin : null; // computed at fetch time (render stays pure)
  return (
    <span
      className="chip tnum"
      title={data.note}
      style={{ borderStyle: "dashed" }}
    >
      <span style={{ color: "var(--dim)" }}>TradingView says</span>{" "}
      <span style={{ color: LABEL_COLOR[data.label] ?? "var(--text)" }}>
        {data.label}
      </span>{" "}
      <span style={{ color: "var(--dim)" }}>
        ({(data.recoAll ?? 0).toFixed(2)}
        {age !== null ? ` · ${age}m ago` : ""} · external, delayed — not our model)
      </span>
    </span>
  );
}
