// Server layout for /live — metadata only (the page is a client component and
// cannot export it). Bare page name; the root layout's title template appends
// "— SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Live",
  description:
    "Is the pipeline live? TradingView webhook feed + daemon, worker and tunnel health at a glance.",
};

export default function LiveLayout({ children }: { children: React.ReactNode }) {
  return children;
}
