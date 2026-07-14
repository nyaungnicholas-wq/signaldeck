import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "PUSH-20 HUD",
  description:
    "Live view of the PUSH-20 strategy's Alpaca paper account — equity, positions and trades synced from the trader daemon.",
};

export default function HudLayout({ children }: { children: React.ReactNode }) {
  return children;
}
