// Server layout for /markets/trends — route metadata only (the page itself
// is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Trends",
  description:
    "Descriptive 1d breadth, top and bottom movers by pressure score, and the latest market brief for the tracked universe.",
};

export default function TrendsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
