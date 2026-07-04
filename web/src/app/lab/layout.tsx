// LAB hub — research + self-measurement surfaces: Backtest · Signal BT ·
// Risk · Portfolio · Paper · Track record · Honesty · System (Stage 2 nav
// consolidation). System is itself a small hub (Quality / Agents / AI) under
// /lab/system — its SYSTEM tab stays lit for any /lab/system/* path.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

export const metadata: Metadata = {
  title: "Lab — SignalDeck",
};

const TABS = [
  { href: "/lab/backtest", label: "BACKTEST" },
  { href: "/lab/signal-backtest", label: "SIGNAL BT" },
  { href: "/lab/risk", label: "RISK" },
  { href: "/lab/portfolio", label: "PORTFOLIO" },
  { href: "/lab/paper", label: "PAPER" },
  { href: "/lab/track-record", label: "TRACK RECORD" },
  { href: "/lab/honesty", label: "HONESTY" },
  // Stage 5: SYSTEM lands on the combined quality+agents+AI page.
  { href: "/lab/system", label: "SYSTEM" },
];

export default function LabLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Lab sections" tabs={TABS} />
      {children}
    </>
  );
}
