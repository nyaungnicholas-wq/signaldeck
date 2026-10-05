// Metadata-only leaf layout for the LAB → Live page (2026-07-19 nav
// consolidation; formerly /live). The page is a client component, so its
// per-route <title>/description live here in a server layout.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Live",
  description:
    "Is the pipeline live? TradingView webhook feed + daemon, worker and tunnel health at a glance.",
};

export default function LabLiveLayout({ children }: { children: React.ReactNode }) {
  return children;
}
