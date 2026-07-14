// Server layout for /lab/portfolio — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Portfolio",
  description:
    "Log discretionary trades and get graded against what actually happened, plus a correlation view of what really diversifies.",
};

export default function PortfolioLayout({ children }: { children: React.ReactNode }) {
  return children;
}
