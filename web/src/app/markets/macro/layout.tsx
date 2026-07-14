// Server layout for /markets/macro — route metadata only (the page itself
// is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Macro",
  description:
    "Big-picture market backdrop from free official sources: VIX and breadth gauges, sector rotation, and FRED-based calendars, each on its own publication lag.",
};

export default function MacroLayout({ children }: { children: React.ReactNode }) {
  return children;
}
