import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "News",
  description:
    "Headlines for tracked stocks with model-rated sentiment tags — a tag, not a recommendation.",
};

export default function NewsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
