import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Shorts",
  description:
    "FINRA Reg SHO daily short sale volume ratios for tracked stocks (retired 22 Oct 2026 (licence review); stored history only) — NOT short interest, and not directional.",
};

export default function ShortsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
