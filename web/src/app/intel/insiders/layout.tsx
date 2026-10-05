import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Insiders",
  description:
    "Parsed SEC Form 4 insider trades, separating open-market conviction buys and sells from grants and other mechanics.",
};

export default function InsidersLayout({ children }: { children: React.ReactNode }) {
  return children;
}
