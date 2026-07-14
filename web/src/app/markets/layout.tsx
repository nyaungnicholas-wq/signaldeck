// MARKETS hub — sub-tabs: Screener · Trends · Regimes · Macro (Stage 2 nav
// consolidation). Old routes /screener /trends /regime /macro redirect here
// via next.config.ts. The tab strip lives in this layout so it persists
// across sub-tab navigation.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

export const metadata: Metadata = {
  title: "Markets",
};

const TABS = [
  { href: "/markets/screener", label: "SCREENER" },
  { href: "/markets/trends", label: "TRENDS" },
  { href: "/markets/regimes", label: "REGIMES" },
  { href: "/markets/macro", label: "MACRO" },
  { href: "/markets/memory", label: "MEMORY" },
  { href: "/markets/graph", label: "GRAPH" },
];

export default function MarketsLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Markets sections" tabs={TABS} />
      {children}
    </>
  );
}
