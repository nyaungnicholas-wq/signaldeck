// Server layout for /lab/system/ai — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "AI Agents",
  description:
    "Status and surfaces for the built-in read-only AI agents — grounded in stored data, with a hard daily spend cap.",
};

export default function AILayout({ children }: { children: React.ReactNode }) {
  return children;
}
