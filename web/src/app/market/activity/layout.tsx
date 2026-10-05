// Server layout for /market/activity — route metadata only (the page itself
// is a client component and cannot export metadata). Carried over from
// /signals/alerts, which the 2026-08-02 merge folded away (the
// page component now lives here; the old URL 307s in).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Activity",
  description:
    "Your watchlist's event feed — every alert cites its published rule, and the outcomes table shows measured forward returns after past alerts, gated below minimum sample size.",
};

export default function ActivityLayout({ children }: { children: React.ReactNode }) {
  return children;
}
