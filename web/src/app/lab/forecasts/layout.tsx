// Server layout for /lab/forecasts — route metadata only (the page itself is
// a client component and cannot export metadata). Carried over from
// /signals/forecasts, which the 2026-08-02 merge folded away (the
// page component now lives here; the old URL 307s in).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Models",
  description:
    "Three honestly graded models — walk-forward logistic, boosted trees, and costed mean-reversion — race on one symbol, each P(up) shown beside its out-of-sample grade.",
};

export default function ForecastsLayout({ children }: { children: React.ReactNode }) {
  return children;
}
