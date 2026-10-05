// Shape of the trader-hud /api/summary payload (stock-trader dashboard/server.py),
// passed through the SignalDeck daemon VERBATIM. Everything is optional —
// the sync may be stale, partial, or the Alpaca link may be down — so every
// consumer must render defensively.

export interface HudAccount {
  equity?: number;
  day_pnl?: number;
  day_pnl_pct?: number; // fraction (0.01 = +1%)
  since_start?: number; // fraction vs the $100k paper start
  cash?: number;
  buying_power?: number;
}

export interface HudPosition {
  symbol?: string;
  qty?: number;
  avg?: number; // avg entry price
  price?: number; // current price
  value?: number; // market value
  upl?: number; // unrealized P&L ($)
  upl_pct?: number; // fraction
}

export interface HudTrade {
  filled_at?: string; // "YYYY-MM-DD HH:MM"
  symbol?: string;
  side?: string; // "buy" | "sell"
  qty?: string | number; // Alpaca returns strings
  price?: string | number;
}

export interface HudHistory {
  dates?: string[];
  account?: (number | null)[]; // normalized to 100 at first point
  spy?: (number | null)[]; // normalized, may contain nulls (holiday gaps)
  equity_raw?: number[];
}

export interface HudStrategy {
  name?: string;
  cagr?: number; // fraction (0.207)
  mdd?: number; // fraction (-0.304)
  monthly_win?: number; // fraction (0.62)
  deployed?: string;
  config?: Record<string, unknown>;
  flags?: Record<string, string>;
  last_rebalance?: string | null;
}

export interface HudSlippage {
  n?: number;
  avg_bps?: number | null;
  worst_bps?: number | null;
  assumed_bps?: number;
}

export interface HudCheckup {
  file?: string;
  text?: string;
  alerts?: string[];
}

export interface HudMacro {
  score?: number;
  gate?: string; // "OPEN" | "FROZEN"
  top_risk?: string;
  sources?: string[];
}

export interface HudSummary {
  asof?: string;
  connected?: boolean;
  account?: HudAccount;
  positions?: HudPosition[];
  trades?: HudTrade[];
  history?: HudHistory | null;
  strategy?: HudStrategy;
  slippage?: HudSlippage;
  checkup?: HudCheckup | null;
  macro?: HudMacro | null;
}
