// Server layout for /markets/regimes — route metadata only (the page itself
// is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Regimes",
  description:
    "Names the mode each market is in right now — uptrend, downtrend, range, or squeeze — with transitions, breakouts, and relative-strength rankings.",
};

export default function RegimesLayout({ children }: { children: React.ReactNode }) {
  return children;
}
