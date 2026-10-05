// LAB section map — the single source of truth for how the lab's 24 surfaces
// are grouped.
//
// WHY: LAB had 24 sibling tabs, seven visible and seventeen folded into a
// "MORE" menu. That is not navigation, it is a list — nobody scans seventeen
// specialist names to find the one they want, and the fold order silently
// decided which tools existed. The surfaces were never flat in meaning: they
// answer four different questions, and the nav now says so.
//
//   RESEARCH   — what might be true? (idea generation, exploration, analogs)
//   VALIDATION — is it actually true? (backtests, the honesty record)
//   PORTFOLIO  — what do I do about it? (sizing, risk, simulated books)
//   SYSTEM     — is the machine healthy? (fleet, data quality, AI surface)
//
// URLs are UNCHANGED and stay flat (/lab/pairs, not /lab/portfolio/pairs).
// Grouping is a nav concern, and re-homing 24 routes would break every
// bookmark, alert href and doc link to buy a URL that merely repeats what the
// tab strip already shows. Membership is data here, not routing.

import type { HubTab } from "@/components/HubTabs";

export interface LabSection {
  /** Stable key (used for the aria-label of the section's own strip). */
  key: string;
  /** Top-strip label. */
  label: string;
  /** Where the section lands when its top-level tab is clicked. */
  href: string;
  /** The section's surfaces, in the order they should read. */
  tabs: HubTab[];
}

export const LAB_SECTIONS: LabSection[] = [
  {
    key: "research",
    label: "RESEARCH",
    href: "/lab/research",
    tabs: [
      { href: "/lab/research", label: "RESEARCH" },
      { href: "/lab/desk", label: "DESK" },
      { href: "/lab/insights", label: "INSIGHTS" },
      { href: "/lab/debate", label: "DEBATE" },
      { href: "/lab/evolution", label: "EVOLUTION" },
      { href: "/lab/memory", label: "MEMORY" },
      { href: "/lab/graph", label: "GRAPH" },
      { href: "/lab/sentiment", label: "SENTIMENT" },
    ],
  },
  {
    key: "validation",
    label: "VALIDATION",
    href: "/lab/backtest",
    tabs: [
      { href: "/lab/backtest", label: "BACKTEST" },
      { href: "/lab/signal-backtest", label: "SIGNAL BT" },
      { href: "/lab/track-record", label: "TRACK RECORD" },
      { href: "/lab/honesty", label: "HONESTY" },
      { href: "/lab/forecasts", label: "MODELS" },
      { href: "/lab/confluence", label: "CONFLUENCE" },
    ],
  },
  {
    key: "portfolio",
    label: "PORTFOLIO",
    href: "/lab/portfolio",
    tabs: [
      { href: "/lab/portfolio", label: "PORTFOLIO" },
      { href: "/lab/paper", label: "PAPER" },
      { href: "/lab/risk", label: "RISK" },
      { href: "/lab/optimizer", label: "OPTIMIZER" },
      { href: "/lab/scenario", label: "SCENARIO" },
      { href: "/lab/strategies", label: "STRATEGIES" },
      { href: "/lab/pairs", label: "PAIRS" },
      { href: "/lab/options", label: "OPTIONS" },
    ],
  },
  {
    key: "system",
    label: "SYSTEM",
    href: "/lab/system",
    tabs: [
      // ALL is exact: /lab/system is the parent of its own sub-tabs, so prefix
      // matching would keep it lit on every focused tab.
      { href: "/lab/system", label: "ALL", exact: true },
      { href: "/lab/system/quality", label: "QUALITY" },
      { href: "/lab/system/agents", label: "AGENTS" },
      { href: "/lab/system/ai", label: "AI" },
      { href: "/lab/live", label: "LIVE" },
    ],
  },
];

/** The section a lab pathname belongs to, or null for a lab route that no
 *  section claims (a new page whose author has not placed it yet — the strip
 *  degrades to sections-only rather than guessing a home). */
export function sectionFor(pathname: string): LabSection | null {
  let best: LabSection | null = null;
  let bestLen = -1;
  for (const s of LAB_SECTIONS) {
    for (const t of s.tabs) {
      // Longest matching href wins, so /lab/system/quality picks SYSTEM's
      // sub-tab rather than any shorter prefix that also matches.
      if ((pathname === t.href || pathname.startsWith(t.href + "/")) && t.href.length > bestLen) {
        best = s;
        bestLen = t.href.length;
      }
    }
  }
  return best;
}
