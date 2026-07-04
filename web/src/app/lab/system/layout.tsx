// LAB → SYSTEM inner hub — Quality · Agents · AI merged into one "System"
// sub-tab (user decision). Old routes /quality /agents /ai redirect here via
// next.config.ts. Second HubTabs strip (compact) under the LAB strip.
// Stage 5: ALL (/lab/system, exact-match so it doesn't stay lit on the
// focused tabs) mounts all three surfaces together on one page.

import HubTabs from "@/components/HubTabs";

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
