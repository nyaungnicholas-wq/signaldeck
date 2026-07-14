// Server layout for /signals/alerts — route metadata only (the page
// itself is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Alerts",
  description:
    "Your watchlist's event feed — every alert cites its published rule, and the outcomes table shows measured forward returns after past alerts, gated below minimum sample size.",
};

export default function AlertsLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
