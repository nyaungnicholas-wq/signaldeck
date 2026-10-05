import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Today",
  description:
    "SignalDeck's validated regime and volatility reads, each with the accuracy it has measured, and the live record that grades them.",
};

export default function TodayLayout({ children }: { children: React.ReactNode }) {
  return children;
}
