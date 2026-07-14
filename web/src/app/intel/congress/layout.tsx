import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Congress",
  description:
    "STOCK Act trade disclosures by members of Congress — amounts are ranges and disclosures lag 30-45 days by law.",
};

export default function CongressLayout({ children }: { children: React.ReactNode }) {
  return children;
}
