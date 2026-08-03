// LAB hub — research + self-measurement, grouped into four sections
// (RESEARCH · VALIDATION · PORTFOLIO · SYSTEM) by app/lab/sections.ts.
//
// It used to be one strip of 24 sibling tabs: seven visible, seventeen folded
// into a "MORE" menu, so the fold order silently decided which tools a user
// knew existed. The four sections are the four questions the lab actually
// answers, so a surface is found by what you want to know rather than by
// scrolling an overflow list. URLs are unchanged — grouping is navigation, not
// routing.
//
// The strips live here in the layout so they persist across tab switches
// without remounting. LabTabs is a Client Component because the active section
// is resolved from the pathname, and a layout cannot read that itself (Next 16:
// usePathname inside a Client Component).

import type { Metadata } from "next";
import LabTabs from "@/components/LabTabs";

// Bare page name — the root layout's title template appends "- SignalDeck".
export const metadata: Metadata = {
  title: "Lab",
  description:
    "Research and self-measurement tools — backtests, risk, paper trading, and the honesty scoreboard, all graded on stored data.",
};

export default function LabLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <LabTabs />
      {children}
    </>
  );
}
