import type { Metadata } from "next";
import { JetBrains_Mono } from "next/font/google";
import "./globals.css";
import Shell from "@/components/Shell";
import AuthGate from "@/components/AuthGate";

const mono = JetBrains_Mono({
  variable: "--font-mono",
  subsets: ["latin"],
  weight: ["400", "500", "700", "800"],
});

export const metadata: Metadata = {
  title: "SignalDeck — data-first market intelligence",
  description:
    "Continuously records crypto + stock market data, computes decomposable pressure scores, and grades itself against realized returns.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={`${mono.variable} h-full antialiased`}>
      <body className="flex min-h-full flex-col">
        <AuthGate />
        <Shell>{children}</Shell>
      </body>
    </html>
  );
}
