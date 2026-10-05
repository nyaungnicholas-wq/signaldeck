import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Institutions",
  description:
    "Quarterly 13F-HR holdings of curated notable managers — snapshots filed up to 45 days after quarter end.",
};

export default function InstitutionsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
