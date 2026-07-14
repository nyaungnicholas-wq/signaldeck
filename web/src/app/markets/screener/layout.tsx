// Server layout for /markets/screener — route metadata only (the page itself
// is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Screener",
  description:
    "Ranks every tracked symbol by pressure score with filters, verdict cards, and a heatmap — stored daily data on worker cadence, not live quotes.",
};

export default function ScreenerLayout({ children }: { children: React.ReactNode }) {
  return children;
}
