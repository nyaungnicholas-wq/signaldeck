// Server layout for /signals/predictions — route metadata only (the page
// itself is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Predictions",
  description:
    "One calibrated probability and a 1–10 forced-curve SignalScore per symbol, with the 11-factor evidence, the additive edge ledger, and every gate visible on the card.",
};

export default function PredictionsLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
