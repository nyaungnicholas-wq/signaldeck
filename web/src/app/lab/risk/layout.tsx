// Server layout for /lab/risk — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Risk",
  description:
    "Price 1-day Value-at-Risk, risk drivers, and stress scenarios for any mix of holdings — computed from stored return history.",
};

export default function RiskLayout({ children }: { children: React.ReactNode }) {
  return children;
}
