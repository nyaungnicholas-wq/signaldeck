"use client";

// PLAIN-ENGLISH NAV LABELS — the SIMPLE-mode half of the translation layer.
// lib/plain.ts translates metric VALUES; this translates the words you click.
//
// Nav labels are the one piece of jargon a tooltip cannot rescue: nobody hovers
// CONFLUENCE to find out what CONFLUENCE means, they just never click it. In
// SIMPLE view every hub, section and tab renders the sentence-case name below;
// PRO keeps the terminal labels exactly as they were.
//
// Keys are the PRO label verbatim. A label with no entry here renders
// unchanged, so adding a surface can never break the nav — worst case it keeps
// its terminal name until someone writes a plain one.
//
// HONESTY: these rename, they never soften. HONESTY -> "How wrong were we"
// keeps the page's entire point; it does not become "Performance".

import { useViewMode } from "@/components/Plain";

export const PLAIN_LABEL: Readonly<Record<string, string>> = {
  // ── hubs (Shell) ────────────────────────────────────────────────────────
  HOME: "Home",
  MARKET: "What's moving",
  WATCHLIST: "My watchlist",
  INTEL: "Company news",
  LAB: "Research & proof",
  ADVANCED: "Advanced",

  // ── MARKET tabs ─────────────────────────────────────────────────────────
  OVERVIEW: "Overview",
  SIGNALS: "Signals",
  REGIMES: "Market mood",
  BREADTH: "How broad the move is",
  TRENDS: "Trends",
  MACRO: "Big picture",
  ACTIVITY: "Alerts",
  UNUSUAL: "Odd activity",

  // ── WATCHLIST tabs ──────────────────────────────────────────────────────
  COMPARE: "Compare two",

  // ── INTEL tabs ──────────────────────────────────────────────────────────
  NEWS: "News",
  FILINGS: "SEC filings",
  INSIDERS: "Insider buying",
  INSTITUTIONS: "Big funds",
  COMPANIES: "Companies",
  SHORTS: "Short interest",
  "SMART MONEY": "Smart money",
  COMPANY: "One company",
  CONGRESS: "Congress trades",

  // ── LAB sections ────────────────────────────────────────────────────────
  RESEARCH: "Research",
  VALIDATION: "Proof",
  PORTFOLIO: "Portfolio",
  SYSTEM: "System",

  // ── LAB surfaces ────────────────────────────────────────────────────────
  DESK: "AI desk",
  INSIGHTS: "Insights",
  DEBATE: "Debate",
  EVOLUTION: "How the model changed",
  MEMORY: "Memory",
  GRAPH: "Connections",
  SENTIMENT: "Mood from the news",
  BACKTEST: "Backtest",
  "SIGNAL BT": "Signal backtest",
  "TRACK RECORD": "Our scorecard",
  HONESTY: "How wrong were we",
  MODELS: "Models",
  CONFLUENCE: "Signals that agree",
  PAPER: "Paper trading",
  RISK: "Risk",
  OPTIMIZER: "Suggested weights",
  SCENARIO: "What-ifs",
  STRATEGIES: "Strategies",
  PAIRS: "Paired trades",
  OPTIONS: "Options",
  ALL: "All",
  QUALITY: "Data quality",
  AGENTS: "Workers",
  AI: "AI",
  LIVE: "Pipeline health",
};

/** Translate one PRO label; unknown labels pass through untouched. */
export function plainLabel(label: string): string {
  return PLAIN_LABEL[label] ?? label;
}

/** The label translator for the current view mode. SIMPLE (the default, and
 *  what SSR renders) speaks English; PRO keeps the terminal labels. */
export function useLabel(): (label: string) => string {
  const mode = useViewMode();
  return mode === "pro" ? (l: string) => l : plainLabel;
}
