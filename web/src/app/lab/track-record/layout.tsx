// Server layout for /lab/track-record — metadata only (the page is a client
// component and cannot export it). Bare page name; the root layout's title
// template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Track Record",
  description:
    "The live out-of-sample scoreboard — frozen predictions graded against realized outcomes, with skill numbers withheld below the significance gate.",
};

export default function TrackRecordLayout({ children }: { children: React.ReactNode }) {
  return children;
}
