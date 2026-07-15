// DESK hub — the AI RESEARCH DESK: an always-on research surface pairing a live
// world model with explainable, auditable recommendations. The hub layout owns
// the sub-tab strip (HubTabs), so tab state is the URL and the strip persists
// without remounting across sub-tab navigation — the same pattern as the INTEL
// and LAB hubs.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

export const metadata: Metadata = {
  title: "Research Desk",
  description:
    "An always-on AI research desk — a live world model, explainable recommendations, a multi-agent panel, and a reproducible audit trail. Measured, not advice.",
};

const TABS = [{ href: "/desk/overview", label: "OVERVIEW" }];

export default function DeskLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Research desk sections" tabs={TABS} />
      {children}
    </>
  );
}
