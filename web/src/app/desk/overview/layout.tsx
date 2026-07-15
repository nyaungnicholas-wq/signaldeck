// Metadata-only leaf layout for the desk OVERVIEW sub-tab. The page itself is a
// client component (it fetches + polls), so its per-route <title>/description
// live here in a server layout, the same split the other hub leaves use.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Overview",
  description:
    "The AI research desk overview — high-conviction opportunities, an explainable recommendation with its multi-agent panel and reproducible audit trail, and the live world model.",
};

export default function DeskOverviewLayout({ children }: { children: React.ReactNode }) {
  return children;
}
