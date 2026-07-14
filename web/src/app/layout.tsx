import type { Metadata } from "next";
import { Inter, JetBrains_Mono } from "next/font/google";
import "./globals.css";
import Shell from "@/components/Shell";
import AuthGate from "@/components/AuthGate";

// UI voice = Inter; data voice = JetBrains Mono (.tnum/.mono/charts keep the
// --font-mono variable name, so CandleChart's axis font needs no change).
const sans = Inter({
  variable: "--font-inter",
  subsets: ["latin"],
});
const mono = JetBrains_Mono({
  variable: "--font-mono",
  subsets: ["latin"],
  weight: ["400", "500", "700", "800"],
});

export const metadata: Metadata = {
  // Child route segments set a bare page name and inherit the suffix; the
  // dashboard (this segment) keeps the full default title.
  title: {
    template: "%s — SignalDeck",
    default: "Dashboard — SignalDeck",
  },
  description:
    "Continuously records crypto + stock market data, computes decomposable pressure scores, and grades itself against realized returns.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={`${sans.variable} ${mono.variable} h-full antialiased`}>
      <body className="flex min-h-full flex-col">
        <AuthGate>
          <Shell>{children}</Shell>
        </AuthGate>
      </body>
    </html>
  );
}
