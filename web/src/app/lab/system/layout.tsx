// LAB → SYSTEM inner hub — Quality · Agents · AI merged into one "System"
// section (user decision). Old routes /quality /agents /ai redirect here via
// next.config.ts. Stage 5: ALL (/lab/system) mounts all three surfaces on one
// page.
//
// This layout no longer renders its own tab strip: SYSTEM is one of the four
// LAB sections, so its surfaces are listed in app/lab/sections.ts and drawn by
// LabTabs like every other section's. Keeping a private copy here would put
// two strips on the page and give /lab/live (a SYSTEM surface that lives
// outside /lab/system/) nowhere to appear.

import type { Metadata } from "next";

// Bare page name — the root layout's title template appends "- SignalDeck".
export const metadata: Metadata = {
  title: "System",
  description:
    "One look at the machine — data quality, the background worker fleet, and the AI surface, with the same honesty gates as the focused tabs.",
};

export default function SystemLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
