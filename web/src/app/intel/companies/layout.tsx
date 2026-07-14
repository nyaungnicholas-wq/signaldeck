import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Companies",
  description:
    "Directory of every SEC-registered company joined to SignalDeck's tracked market data — untracked rows show honest dashes.",
};

export default function CompaniesLayout({ children }: { children: React.ReactNode }) {
  return children;
}
