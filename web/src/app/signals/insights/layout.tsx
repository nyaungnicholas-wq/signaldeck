// Server layout for /signals/insights — route metadata only (the page
// itself is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Insights",
  description:
    "AI-written briefings generated only from stored data, each with an evidence expander showing the exact numbers behind the sentence — measured tendencies, never forecasts.",
};

export default function InsightsLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
