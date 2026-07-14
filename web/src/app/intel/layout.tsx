// INTEL hub — ALL stock intel in one place (user decision): News · Filings ·
// Insiders · Institutions · Congress. Old routes /news /filings /insiders
// /institutions /congress redirect here via next.config.ts. Per-symbol intel
// stays on the symbol pages.
//
// Stage 5: IntelShared wraps the sub-tabs with the merged hub header
// ("stock intelligence — public-domain SEC + news data") and the ONE
// symbol-filter box shared across all five sub-tabs (the context lives in
// this layout, which never remounts across sub-tab navigation).

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";
import IntelShared from "@/components/intel/IntelShared";

export const metadata: Metadata = {
  title: "Intel",
  description:
    "All stock intelligence in one hub — news, SEC filings, insiders, institutions, congressional trades, the company directory and short volume.",
};

// Tabs beyond the first five fold into HubTabs' "More research" menu
// (progressive disclosure) — order here decides what stays visible.
const TABS = [
  { href: "/intel/news", label: "NEWS" },
  { href: "/intel/filings", label: "FILINGS" },
  { href: "/intel/insiders", label: "INSIDERS" },
  { href: "/intel/institutions", label: "INSTITUTIONS" },
  { href: "/intel/congress", label: "CONGRESS" },
  // Signal8 wave Stage 5: the full SEC-registered company directory (free
  // EDGAR map joined to our tracked bars/fundamentals; honest "—" elsewhere).
  { href: "/intel/companies", label: "COMPANIES" },
  // Stage 5 FINRA Reg SHO: daily short sale VOLUME ratio (free FINRA files,
  // universe-scoped). The NOT-short-interest caveat renders verbatim.
  { href: "/intel/shorts", label: "SHORTS" },
];

export default function IntelLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Intel sections" tabs={TABS} />
      <IntelShared>{children}</IntelShared>
    </>
  );
}
