// Server layout for /lab/system/quality — metadata only (the page is a
// client component and cannot export it). Bare page name; the root layout's
// title template appends "- SignalDeck".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Data Quality",
  description:
    "Bar coverage, freshness, and the incident log — if it is not measured on this page, it was not measured.",
};

export default function QualityLayout({ children }: { children: React.ReactNode }) {
  return children;
}
