// Shapes and pure helpers for the visitor Forecasts page (/forecasts), which
// reads the daemon's allowlisted /api/public/forecasts.

export type PublicForecastRow = {
  symbol: string;
  kind: string;
  horizonDays: number;
  regime: string;
  conviction: number;
  ts: number;
};

export type PublicForecasts = {
  source: string;
  rows: PublicForecastRow[];
  note?: string;
  citation?: string;
  coverage?: string;
  computedAt?: string;
};

// Column order and plain-English headings. A kind the daemon sends that is not
// listed here is still shown, after these, under its own name.
export const KIND_LABEL: Record<string, string> = {
  trend21: "Trend, next month",
  trend63: "Trend, next quarter",
  vol21: "Volatility, next month",
  vol63: "Volatility, next quarter",
  liquidity21: "Trading activity, next month",
};

export type ForecastCell = { regime: string; conviction: number };
export type ForecastLine = { symbol: string; cells: Record<string, ForecastCell> };

/** One line per symbol, one cell per kind, plus the kinds present in order. */
export function pivotForecasts(rows: PublicForecastRow[]): { kinds: string[]; lines: ForecastLine[] } {
  const bySymbol = new Map<string, ForecastLine>();
  const seen = new Set<string>();
  for (const r of rows) {
    if (!r || !r.symbol || !r.kind) continue;
    seen.add(r.kind);
    let line = bySymbol.get(r.symbol);
    if (!line) {
      line = { symbol: r.symbol, cells: {} };
      bySymbol.set(r.symbol, line);
    }
    line.cells[r.kind] = { regime: r.regime, conviction: r.conviction };
  }
  const known = Object.keys(KIND_LABEL).filter((k) => seen.has(k));
  const extra = [...seen].filter((k) => !(k in KIND_LABEL)).sort();
  const lines = [...bySymbol.values()].sort((a, b) => a.symbol.localeCompare(b.symbol));
  return { kinds: [...known, ...extra], lines };
}

/** Conviction is a 0..1 distance from the threshold. Shown as a whole percent;
 *  a missing or non-finite value shows nothing rather than 0%. */
export function strengthText(conviction: number | null | undefined): string {
  if (conviction == null || !Number.isFinite(conviction)) return "";
  return `${Math.round(Math.min(1, Math.max(0, conviction)) * 100)}%`;
}
