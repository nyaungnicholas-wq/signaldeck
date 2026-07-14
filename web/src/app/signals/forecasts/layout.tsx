// Server layout for /signals/forecasts — route metadata only (the page
// itself is a client component and cannot export metadata).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Forecasts",
  description:
    "Three honestly graded models — walk-forward logistic, boosted trees, and costed mean-reversion — race on one symbol, each P(up) shown beside its out-of-sample grade.",
};

export default function ForecastsLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
