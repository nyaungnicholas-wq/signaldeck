import type { Metadata, Viewport } from "next";
import { Inter, JetBrains_Mono } from "next/font/google";
import "./globals.css";
import Shell from "@/components/Shell";
import AuthGate from "@/components/AuthGate";
import CompanyPeekProvider from "@/components/CompanyPeek";

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
  // PWA install surface (static manifest + SVG icon in /public). No service
  // worker yet — installability/offline caching is deliberately out of scope.
  manifest: "/manifest.webmanifest",
  icons: {
    icon: "/icon.svg",
    apple: "/icon.svg",
  },
};

// Next 16: themeColor lives on the viewport export, not metadata.
export const viewport: Viewport = {
  themeColor: "#060910",
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
          <CompanyPeekProvider>
            <Shell>{children}</Shell>
          </CompanyPeekProvider>
        </AuthGate>
      </body>
    </html>
  );
}
