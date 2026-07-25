// LAB hub — research + self-measurement surfaces: Backtest · Signal BT ·
// Risk · Portfolio · Paper · Track record · Honesty · System (Stage 2 nav
// consolidation). System is itself a small hub (Quality / Agents / AI) under
// /lab/system — its SYSTEM tab stays lit for any /lab/system/* path.

import type { Metadata } from "next";
import HubTabs from "@/components/HubTabs";

// Bare page name — the root layout's title template appends "- SignalDeck".
export const metadata: Metadata = {
  title: "Lab",
  description:
    "Research and self-measurement tools — backtests, risk, paper trading, and the honesty scoreboard, all graded on stored data.",
};

// Tabs beyond the first seven (HubTabs MAX_VISIBLE) fold into the "MORE"
// menu — order here decides what stays visible. The seven core research +
// self-measurement surfaces stay out front; specialist/experimental tabs fold.
// 2026-07-19 nav consolidation: DESK (former /desk, the AI research desk) and
// LIVE (former /live, pipeline health) folded in here.
const TABS = [
  { href: "/lab/backtest", label: "BACKTEST" },
  { href: "/lab/risk", label: "RISK" },
  { href: "/lab/portfolio", label: "PORTFOLIO" },
  { href: "/lab/paper", label: "PAPER" },
  { href: "/lab/track-record", label: "TRACK RECORD" },
  { href: "/lab/honesty", label: "HONESTY" },
  { href: "/lab/desk", label: "DESK" },
  // ── everything below folds into "MORE" ──
  { href: "/lab/system", label: "SYSTEM" },
  { href: "/lab/forecasts", label: "MODELS" },
  { href: "/lab/confluence", label: "CONFLUENCE" },
  { href: "/lab/insights", label: "INSIGHTS" },
  { href: "/lab/signal-backtest", label: "SIGNAL BT" },
  { href: "/lab/research", label: "RESEARCH" },
  { href: "/lab/sentiment", label: "SENTIMENT" },
  { href: "/lab/evolution", label: "EVOLUTION" },
  { href: "/lab/strategies", label: "STRATEGIES" },
  { href: "/lab/options", label: "OPTIONS" },
  { href: "/lab/pairs", label: "PAIRS" },
  { href: "/lab/scenario", label: "SCENARIO" },
  { href: "/lab/optimizer", label: "OPTIMIZER" },
  { href: "/lab/debate", label: "DEBATE" },
  { href: "/lab/memory", label: "MEMORY" },
  { href: "/lab/graph", label: "GRAPH" },
  { href: "/lab/live", label: "LIVE" },
];

export default function LabLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <HubTabs ariaLabel="Lab sections" tabs={TABS} />
      {children}
    </>
  );
}
