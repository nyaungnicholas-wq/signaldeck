// Server layout for /market/trends — route metadata only (the page itself
// is a client component and cannot export metadata). Carried over from
// /markets/trends, which the 2026-08-02 merge folded away (the
// page component now lives here; the old URL 307s in).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Trends",
  description:
    "Descriptive 1d breadth, top and bottom movers by pressure score, the candlestick patterns firing on recent daily bars, and the latest market brief.",
};

export default function TrendsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
