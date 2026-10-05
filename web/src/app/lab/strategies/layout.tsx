// Server layout for /lab/strategies — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Strategy Lab",
  description:
    "Eight classic published strategies backtested nightly on our own bars with costs — fleet medians per strategy and honestly-gated per-symbol results.",
};

export default function StrategiesLayout({ children }: { children: React.ReactNode }) {
  return children;
}
