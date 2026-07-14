import type { Metadata } from "next";

// Next 16: route params are a Promise — await them in generateMetadata.
export async function generateMetadata({
  params,
}: {
  params: Promise<{ market: string; symbol: string }>;
}): Promise<Metadata> {
  const p = await params;
  const symbol = decodeURIComponent(p.symbol);
  return {
    title: `${symbol}`,
    description: `Symbol deep-dive for ${symbol}: the honest verdict first, the score components and expectancy behind it, then the raw SEC records.`,
  };
}

export default function SymbolLayout({ children }: { children: React.ReactNode }) {
  return children;
}
