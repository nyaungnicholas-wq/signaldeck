// Server layout for /lab/insights — route metadata only (the page itself is
// a client component and cannot export metadata). Carried over from
// /signals/insights, whose layout stopped applying once the 2026-07-18 hub
// merge relocated the page by re-export.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Insights",
  description:
    "AI-written briefings generated only from stored data, each with an evidence expander showing the exact numbers behind the sentence — measured tendencies, never forecasts.",
};

export default function InsightsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
