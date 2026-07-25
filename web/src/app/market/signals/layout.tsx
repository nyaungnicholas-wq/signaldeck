// Server layout for /market/signals — route metadata only (the page itself
// is a client component and cannot export metadata). Carried over from
// /signals/predictions, whose layout stopped applying once the 2026-07-18 hub
// merge relocated the page by re-export.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Signals",
  description:
    "One calibrated probability and a 1–10 forced-curve SignalScore per symbol, with the 11-factor evidence, the additive edge ledger, and every gate visible on the card.",
};

export default function SignalsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
