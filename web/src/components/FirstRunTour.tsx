"use client";

// FIRST-RUN TOUR — the single front door.
//
// This used to be one of TWO competing first-run surfaces: this modal (keyed on
// `sd-tour-done`) and a dashboard welcome card (keyed on `sd-onboarded`). They
// did not know about each other, so a new user could dismiss the tour and still
// be prompted by the card. Now there is one flag — `sd-onboarded`, owned by
// lib/goal.ts — and one prompt.
//
// It is also no longer one-shot: the Help panel dispatches TOUR_OPEN_EVENT to
// replay it, which is why `forced` exists alongside the persisted flag.
//
// Motion is the shared .pop-in ease only, which the global prefers-reduced-
// motion kill already disables.

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { markOnboarded, useOnboarded } from "@/lib/goal";
import { TOUR_OPEN_EVENT } from "@/components/HelpPanel";

const STEPS: { title: string; body: string; href?: string; linkLabel?: string }[] = [
  {
    title: "What SignalDeck does",
    body: "It watches the market and tells you what changed, in plain English. Everything it shows you is measured — and when there is not enough evidence for a call, it says so instead of guessing.",
  },
  {
    title: "Three signals we can back up",
    body: "Trend, liquidity and volatility, each served next to how accurate it has actually been. Everything else is labelled experimental, on the page itself.",
    href: "/market/regimes",
    linkLabel: "see the market mood →",
  },
  {
    title: "We grade ourselves in public",
    body: "Our scorecard compares every call against what actually happened. The weak numbers stay visible — hiding them would make the good ones meaningless.",
    href: "/lab/track-record",
    linkLabel: "see the scorecard →",
  },
  {
    title: "Two things worth knowing",
    body: "Click any ticker anywhere for a quick company snapshot without leaving the page. Press ⌘K to jump to any page or company — and ? at the top of the screen brings this back any time.",
  },
];

export default function FirstRunTour() {
  const pathname = usePathname();
  const [step, setStep] = useState(0);
  const [forced, setForced] = useState(false);
  const onboarded = useOnboarded();

  // Replay from the Help panel. Always restarts at step 1 — someone asking for
  // the tour again wants the tour, not wherever they abandoned it last time.
  useEffect(() => {
    const onOpen = () => {
      setStep(0);
      setForced(true);
    };
    window.addEventListener(TOUR_OPEN_EVENT, onOpen);
    return () => window.removeEventListener(TOUR_OPEN_EVENT, onOpen);
  }, []);

  const show = forced || !onboarded;

  // /welcome is the hands-on setup flow; overlaying the tour on top of it is
  // the exact double-prompt this component was rewritten to remove.
  if (!show || pathname === "/login" || pathname === "/welcome") return null;

  const finish = () => {
    markOnboarded();
    setForced(false);
  };

  const s = STEPS[step];
  const last = step === STEPS.length - 1;

  return (
    <div
      className="fixed inset-0 z-[65] flex items-end justify-center p-4 sm:items-center"
      role="dialog"
      aria-modal="true"
      aria-label={`welcome tour, step ${step + 1} of ${STEPS.length}`}
      style={{ background: "rgba(3,6,12,0.55)", backdropFilter: "blur(3px)" }}
    >
      <div
        className="pop-in w-full max-w-md rounded-2xl p-5"
        style={{
          background: "rgba(10,16,26,0.9)",
          backdropFilter: "blur(24px) saturate(150%)",
          WebkitBackdropFilter: "blur(24px) saturate(150%)",
          border: "1px solid rgba(255,255,255,0.12)",
          boxShadow: "var(--shadow-2)",
        }}
      >
        <div className="mb-2 flex items-center gap-2">
          {STEPS.map((_, i) => (
            <span
              key={i}
              aria-hidden="true"
              className="h-1.5 w-6 rounded-full"
              style={{ background: i <= step ? "var(--accent)" : "var(--border-strong)" }}
            />
          ))}
          <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
            {step + 1}/{STEPS.length}
          </span>
        </div>
        <h2 className="text-base font-semibold" style={{ color: "var(--text)" }}>
          {s.title}
        </h2>
        <p className="mt-1 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {s.body}
        </p>
        {s.href ? (
          <Link
            href={s.href}
            onClick={finish}
            className="mt-2 inline-block text-[0.8rem] font-medium hover:underline"
            style={{ color: "var(--accent)" }}
          >
            {s.linkLabel}
          </Link>
        ) : null}
        <div className="mt-4 flex items-center justify-between gap-3">
          <button
            type="button"
            onClick={finish}
            className="min-h-[40px] cursor-pointer text-[0.8rem] hover:text-[var(--text)]"
            style={{ color: "var(--faint)" }}
          >
            {last ? "Not now" : "Skip tour"}
          </button>
          {last ? (
            <Link
              href="/welcome"
              onClick={finish}
              autoFocus
              className="chip flex min-h-[40px] cursor-pointer items-center px-4 text-[0.8rem] font-semibold"
              style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
            >
              Set up my watchlist →
            </Link>
          ) : (
            <button
              type="button"
              onClick={() => setStep((x) => x + 1)}
              autoFocus
              className="chip min-h-[40px] cursor-pointer px-4 text-[0.8rem] font-semibold"
              style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
            >
              Next →
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
