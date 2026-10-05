// Server layout for /market/macro — route metadata only (the page itself is
// a client component and cannot export metadata). Carried over from
// /markets/macro, whose layout stopped applying once the 2026-07-18 hub merge
// folded macro and regimes into this one page.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Macro",
  description:
    "Big-picture market backdrop from free official sources: breadth and volatility state, the fleet regime map, sector rotation, and FRED-based calendars, each on its own publication lag.",
};

export default function MacroLayout({ children }: { children: React.ReactNode }) {
  return children;
}
