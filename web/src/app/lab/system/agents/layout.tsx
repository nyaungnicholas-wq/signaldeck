// Server layout for /lab/system/agents — metadata only (the page is a
// client component and cannot export it). Bare page name; the root layout's
// title template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Agents",
  description:
    "The daemon's background worker fleet — last run, status, and recent history for every agent.",
};

export default function AgentsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
