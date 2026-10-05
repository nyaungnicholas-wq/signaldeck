// Server layout for /lab/signal-backtest — metadata only (the page is a
// client component and cannot export it). Bare page name; the root layout's
// title template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Signal Backtest",
  description:
    "Grades SignalDeck's own flagship signal out of sample — IC, quintile spread, and a costed equity curve versus SPY buy-and-hold.",
};

export default function SignalBacktestLayout({ children }: { children: React.ReactNode }) {
  return children;
}
