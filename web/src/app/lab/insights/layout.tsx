// Server layout for /lab/insights — route metadata only (the page itself is
// a client component and cannot export metadata). Carried over from
// /signals/insights, which the 2026-08-02 merge folded away (the
// page component now lives here; the old URL 307s in).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Insights",
  description:
    "AI-written briefings generated only from stored data, each with an evidence expander showing the exact numbers behind the sentence — measured tendencies, never forecasts.",
};

export default function InsightsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
