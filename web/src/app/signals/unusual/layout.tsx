// Server layout for /signals/unusual — route metadata only (the page
// itself is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Unusual activity",
  description:
    "A descriptive anomaly tape — volume, volatility, and imbalance flags versus each symbol's own baseline, plus compound signals for symbols flagging in multiple ways within 24h.",
};

export default function UnusualLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
