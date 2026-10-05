// Server layout for /lab/paper — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Paper Trading",
  description:
    "A costed simulation that trades the platform's own calibrated predictions — not live money, not advice.",
};

export default function PaperLayout({ children }: { children: React.ReactNode }) {
  return children;
}
