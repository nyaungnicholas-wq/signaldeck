// WATCHLIST hub (2026-07-19 nav consolidation): the old DECK (watchlist as
// cards) + COMPARE (two symbols side by side) merged into one hub. WATCHLIST is
// the default; COMPARE is a sub-tab. Old /deck and /compare URLs 307-redirect
// here via next.config.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

export const metadata: Metadata = {
  title: "Watchlist",
  description:
    "Your symbols in one place — each as a card with its regime stack and latest alert, plus a side-by-side compare view.",
};

const TABS = [
  { href: "/watchlist", label: "WATCHLIST", exact: true },
  { href: "/watchlist/compare", label: "COMPARE" },
];

export default function WatchlistLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Watchlist sections" tabs={TABS} />
      {children}
    </>
  );
}
