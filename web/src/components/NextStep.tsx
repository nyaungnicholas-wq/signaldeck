"use client";

/**
 * NEXT STEP DOCK — the answer to "what do I do now?", on every page.
 *
 * One small dock, bottom-right, that always shows exactly one suggestion and
 * one escape hatch ("I'm stuck"). It never shows a menu: the whole point of
 * naming a next step is that the person does not have to choose.
 *
 * What it suggests, in order of precedence:
 *   1. No goal picked yet          → "Start here" (the front door)
 *   2. Setup steps outstanding     → the first unfinished step
 *   3. Everything set up           → a suggestion tied to the current page
 *
 * It also feeds the self-grading system: every suggestion rendered counts as a
 * prompt shown, every click as a prompt taken. `promptsTaken / promptsShown` is
 * how the Guidance category grades itself — a suggestion nobody takes is not
 * guidance, it is decoration.
 */

import type { ReactElement } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { STEPS, useGoal, useSteps, useOnboarded } from "@/lib/goal";
import { HELP_EVENT } from "@/components/HelpPanel";
import { bump } from "@/lib/ux";

interface Suggestion {
  /** Stable id — used to avoid double-counting one impression across renders. */
  id: string;
  /** The nudge, in the second person. Short enough to read without stopping. */
  headline: string;
  /** Why it is worth doing. One line, plain English, no jargon. */
  detail: string;
  cta: string;
  href: string;
}

// Page-specific suggestions. Longest prefix wins, so "/lab/backtest" beats
// "/lab". Anything not listed falls through to DEFAULT_SUGGESTION.
const BY_ROUTE: ReadonlyArray<readonly [prefix: string, suggestion: Omit<Suggestion, "id">]> = [
  [
    "/market/overview",
    {
      headline: "Found something interesting?",
      detail: "Add it to your watchlist and we'll tell you when it changes.",
      cta: "Open my watchlist",
      href: "/watchlist",
    },
  ],
  [
    "/market",
    {
      headline: "See how today compares",
      detail: "The same numbers, plotted against the last few months.",
      cta: "Show me the trend",
      href: "/market/trends",
    },
  ],
  [
    "/watchlist",
    {
      headline: "Check one of your symbols",
      detail: "Open a full report to see what's driving it right now.",
      cta: "Read a report",
      href: "/market/overview",
    },
  ],
  [
    "/lab/backtest",
    {
      headline: "Compare it to what actually happened",
      detail: "A backtest looks good until you check it against the real record.",
      cta: "See the track record",
      href: "/lab/track-record",
    },
  ],
  [
    "/lab/track-record",
    {
      headline: "Now try it on your own idea",
      detail: "Run the same test on a strategy you pick.",
      cta: "Run a backtest",
      href: "/lab/backtest",
    },
  ],
  [
    "/glossary",
    {
      headline: "Ready to use one of these?",
      detail: "The dashboard puts these terms next to real numbers.",
      cta: "Back to the dashboard",
      href: "/",
    },
  ],
  [
    "/advanced",
    {
      headline: "Not sure which of these you need?",
      detail: "Start with the track record — it shows what's actually been reliable.",
      cta: "See what works",
      href: "/lab/track-record",
    },
  ],
];

const DEFAULT_SUGGESTION: Omit<Suggestion, "id"> = {
  headline: "Not sure what to look at?",
  detail: "The dashboard leads with the one thing that changed most today.",
  cta: "Take me to the dashboard",
  href: "/",
};

const START_HERE: Omit<Suggestion, "id"> = {
  headline: "New here? Start with one question.",
  detail: "Tell us what you're here for and we'll hide everything else.",
  cta: "Start here",
  href: "/welcome",
};

/** Routes where a dock would be in the way or redundant. */
// /dashboard has its own onboarding surface (the goal banner, then the setup
// checklist); a dock there asked the same question a second time.
const SILENT = ["/welcome", "/login", "/health", "/dashboard"];

function suggestFor(pathname: string): Omit<Suggestion, "id"> {
  const match = BY_ROUTE.filter(([prefix]) => pathname.startsWith(prefix)).sort(
    (a, b) => b[0].length - a[0].length,
  )[0];
  return match ? match[1] : DEFAULT_SUGGESTION;
}

export default function NextStep(): ReactElement | null {
  const pathname = usePathname();
  const goal = useGoal();
  const steps = useSteps();
  const onboarded = useOnboarded();
  // Which route the dock was dismissed on, rather than a boolean reset by an
  // effect. Navigating anywhere else makes `dismissed` false during render —
  // a dock that stays shut forever stops being help.
  const [dismissedOn, setDismissedOn] = useState<string | null>(null);
  const dismissed = dismissedOn === pathname;

  const suggestion: Suggestion = (() => {
    if (goal === null) return { ...START_HERE, id: "start-here" };
    const pending = STEPS.find((s) => !steps.includes(s.id));
    if (pending) {
      return {
        id: `step:${pending.id}`,
        headline: "You're partway set up.",
        detail: `${STEPS.length - STEPS.filter((s) => steps.includes(s.id)).length} step${
          STEPS.length - STEPS.filter((s) => steps.includes(s.id)).length === 1 ? "" : "s"
        } left. Next: ${pending.label.toLowerCase()}.`,
        cta: "Do this next",
        href: pending.href,
      };
    }
    return { ...suggestFor(pathname), id: `route:${pathname}` };
  })();

  // Count the impression once per distinct suggestion. A ref rather than state:
  // this must not cause a render, and StrictMode double-invokes effects.
  const counted = useRef<string | null>(null);
  const visible = onboarded && !dismissed && !SILENT.includes(pathname);

  useEffect(() => {
    if (!visible) return;
    if (counted.current === suggestion.id) return;
    counted.current = suggestion.id;
    bump("promptsShown");
  }, [visible, suggestion.id]);

  const take = useCallback(() => {
    bump("promptsTaken");
  }, []);

  const askForHelp = useCallback(() => {
    window.dispatchEvent(new Event(HELP_EVENT));
  }, []);

  if (!visible) return null;

  return (
    <aside
      aria-label="what to do next"
      data-nextstep=""
      className="fixed bottom-3 right-3 z-40 max-w-[min(22rem,calc(100vw-1.5rem))]"
    >
      <div
        className="flex flex-col gap-2 border p-3 text-[0.82rem] shadow-lg"
        style={{
          borderColor: "var(--border-strong, var(--border))",
          background: "var(--panel, var(--bg))",
          borderRadius: "10px",
        }}
      >
        <div className="flex items-start gap-2">
          <span aria-hidden="true" style={{ color: "var(--accent)" }}>
            →
          </span>
          <div className="flex flex-col gap-1">
            <strong style={{ color: "var(--text)", fontWeight: 600 }}>{suggestion.headline}</strong>
            <span style={{ color: "var(--dim)" }}>{suggestion.detail}</span>
          </div>
          <button
            type="button"
            onClick={() => setDismissedOn(pathname)}
            aria-label="hide this suggestion"
            className="ml-auto inline-flex h-10 w-10 shrink-0 cursor-pointer items-center justify-center"
            style={{ color: "var(--faint)", marginTop: "-0.5rem", marginRight: "-0.5rem" }}
          >
            ×
          </button>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Link
            href={suggestion.href}
            onClick={take}
            className="inline-flex min-h-[40px] items-center px-3"
            style={{
              background: "var(--accent)",
              color: "var(--bg, #000)",
              borderRadius: "6px",
              fontWeight: 600,
            }}
          >
            {suggestion.cta}
          </Link>
          <button
            type="button"
            onClick={askForHelp}
            className="inline-flex min-h-[40px] cursor-pointer items-center px-3 hover:underline"
            style={{ color: "var(--dim)" }}
          >
            I&rsquo;m stuck
          </button>
        </div>
      </div>
    </aside>
  );
}
