// Server layout for /market/activity — route metadata only (the page itself
// is a client component and cannot export metadata). Carried over from
// /signals/alerts, whose layout stopped applying once the 2026-07-18 hub
// merge relocated the page by re-export.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Activity",
  description:
    "Your watchlist's event feed — every alert cites its published rule, and the outcomes table shows measured forward returns after past alerts, gated below minimum sample size.",
};

export default function ActivityLayout({ children }: { children: React.ReactNode }) {
  return children;
}
