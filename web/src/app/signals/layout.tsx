// SIGNALS hub — sub-tabs: Predictions · Forecasts · Insights · Alerts ·
// Unusual (Stage 2 nav consolidation). Old routes /predict /forecast
// /insights /alerts redirect here via next.config.ts; /signals/unusual is
// new (fleet-wide UnusualActivityPanel, previously home-page only).

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

export const metadata: Metadata = {
  title: "Signals — SignalDeck",
};

const TABS = [
  { href: "/signals/predictions", label: "PREDICTIONS" },
  { href: "/signals/forecasts", label: "FORECASTS" },
  { href: "/signals/insights", label: "INSIGHTS" },
  { href: "/signals/alerts", label: "ALERTS" },
  { href: "/signals/unusual", label: "UNUSUAL" },
];

export default function SignalsLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Signals sections" tabs={TABS} />
      {children}
    </>
  );
}
