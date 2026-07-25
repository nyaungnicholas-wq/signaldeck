// Server layout for /market/overview — route metadata only (the page itself
// is a client component and cannot export metadata). Carried over from
// /markets/screener, whose layout stopped applying once the 2026-07-18 hub
// merge relocated the page by re-export.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Overview",
  description:
    "Ranks every tracked symbol by pressure score with filters, verdict cards, and a heatmap — stored daily data on worker cadence, not live quotes.",
};

export default function OverviewLayout({ children }: { children: React.ReactNode }) {
  return children;
}
