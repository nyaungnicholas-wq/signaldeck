import type { Metadata } from "next";

export const metadata: Metadata = { title: "New password — SignalDeck", robots: { index: false } };

export default function Layout({ children }: { children: React.ReactNode }) {
  return children;
}
