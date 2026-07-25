// SIGNALS namespace — after the 2026-07-18 hub merge only the signal detail
// REPORT pages still live here (/signals/report/[market]/[symbol]); every
// former tab 307-redirects to its new MARKET or LAB home (next.config).
// No hub tabs: the report page carries its own navigation.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Signal report",
};

export default function SignalsLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
