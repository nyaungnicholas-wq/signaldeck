// Server layout for /lab/honesty — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Honesty",
  description:
    "Grades persisted pressure scores against the returns that actually followed — the page that argues against the product when the data says so.",
};

export default function HonestyLayout({ children }: { children: React.ReactNode }) {
  return children;
}
