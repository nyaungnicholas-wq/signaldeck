import type { Metadata } from "next";

export const metadata: Metadata = { title: "Sign up — SignalDeck", robots: { index: true } };

export default function Layout({ children }: { children: React.ReactNode }) {
  return children;
}
