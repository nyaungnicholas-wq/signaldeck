// LAB → SYSTEM inner hub — Quality · Agents · AI merged into one "System"
// sub-tab (user decision). Old routes /quality /agents /ai redirect here via
// next.config.ts. Second HubTabs strip (compact) under the LAB strip.
// Stage 5: ALL (/lab/system, exact-match so it doesn't stay lit on the
// focused tabs) mounts all three surfaces together on one page.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

// Bare page name — the root layout's title template appends "- SignalDeck".
export const metadata: Metadata = {
  title: "System",
  description:
    "One look at the machine — data quality, the background worker fleet, and the AI surface, with the same honesty gates as the focused tabs.",
};

const TABS = [
  { href: "/lab/system", label: "ALL", exact: true },
  { href: "/lab/system/quality", label: "QUALITY" },
  { href: "/lab/system/agents", label: "AGENTS" },
  { href: "/lab/system/ai", label: "AI" },
];

export default function SystemLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="System sections" tabs={TABS} compact />
      {children}
    </>
  );
}
