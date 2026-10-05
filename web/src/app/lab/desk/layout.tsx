// Metadata-only leaf layout for the LAB → Research Desk page (2026-07-19 nav
// consolidation; formerly /desk/overview). The page is a client component, so
// its per-route <title>/description live here in a server layout.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Research Desk",
  description:
    "The AI research desk — high-conviction opportunities, an explainable recommendation with its multi-agent panel and reproducible audit trail, and the live world model.",
};

export default function LabDeskLayout({ children }: { children: React.ReactNode }) {
  return children;
}
