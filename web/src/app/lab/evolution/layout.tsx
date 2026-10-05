// Server layout for /lab/evolution — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Model Evolution",
  description:
    "How the learned model changes over time — measured factor skill, adaptive blend weights, and the daily deterministic self-audit of calibration drift and bias.",
};

export default function EvolutionLayout({ children }: { children: React.ReactNode }) {
  return children;
}
