// Shared helpers for the dashboard's home/* components — split out of the
// old ~1,190-line src/app/page.tsx (pure refactor; behavior identical).

import type { DashFeedItem, Market } from "@/lib/api";

export type FeedKind = DashFeedItem["kind"];
export type KindFilter = "all" | FeedKind;

export const KIND_FILTERS: { k: KindFilter; label: string }[] = [
  { k: "all", label: "all" },
  { k: "news", label: "news" },
  { k: "filing", label: "filings" },
  { k: "anomaly", label: "anomalies" },
  { k: "briefing", label: "briefings" },
];

/** Today's date key in America/New_York (the daily-briefing worker's clock). */
export function nyToday(): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(new Date());
}

/** NY-day key for a unix ts — pins the briefing card only for TODAY's brief. */
export function nyDayOf(ts: number): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(
    new Date(ts * 1000),
  );
}

export function changeColor(v: number): string {
  if (!Number.isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

export function sentimentColor(s?: string): string {
  if (s === "bullish") return "var(--bid)";
  if (s === "bearish") return "var(--ask)";
  return "var(--faint)";
}

export function feedSymbolMarket(it: DashFeedItem): Market {
  if (it.market === "crypto" || it.market === "stocks") return it.market;
  return it.symbol?.includes("/") ? "crypto" : "stocks";
}
