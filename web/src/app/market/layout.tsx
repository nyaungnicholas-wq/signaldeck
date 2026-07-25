// MARKET hub — the 2026-07-18 MARKETS+SIGNALS merge (user decision: one hub,
// each surface exactly once). Market-wide data AND the per-symbol signal
// surfaces live together; research-flavored tabs (forecasts, confluence,
// insights, debate, memory, graph) moved to LAB. Old /markets/* and
// /signals/* tab URLs 307-redirect here via next.config.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

export const metadata: Metadata = {
  title: "Market",
  description:
    "The whole market in one hub — screener, validated regime signals, predictions, trends and patterns, macro state, and activity.",
};

const TABS = [
  { href: "/market/overview", label: "OVERVIEW" },
  { href: "/market/signals", label: "SIGNALS" },
  { href: "/market/regimes", label: "REGIMES" },
  { href: "/market/trends", label: "TRENDS" },
  { href: "/market/macro", label: "MACRO" },
  { href: "/market/activity", label: "ACTIVITY" },
  { href: "/market/unusual", label: "UNUSUAL" },
];

export default function MarketLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Market sections" tabs={TABS} />
      {children}
    </>
  );
}
