// Server layout for /lab/backtest — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Backtest",
  description:
    "Compare a plain-English trading rule against stored history — next-bar fills, honest costs, no lookahead.",
};

export default function BacktestLayout({ children }: { children: React.ReactNode }) {
  return children;
}
