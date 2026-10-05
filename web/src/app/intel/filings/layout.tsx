import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "SEC Filings",
  description:
    "Plain-English feed of SEC EDGAR filings across the tracked universe — filings lag by law and process, never real-time.",
};

export default function FilingsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
