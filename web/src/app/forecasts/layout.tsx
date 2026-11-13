// The page itself is a client component, so its title lives here.
export const metadata = {
  title: "Forecasts",
  description:
    "Rule-based readings of trend, volatility and trading activity for each covered stock. Descriptive, not advice.",
};

export default function ForecastsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
