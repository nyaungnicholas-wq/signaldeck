"use client";

// FIRST-LOGIN TOUR — a 4-step dismissible glass overlay shown once per
// browser (localStorage `sd-tour-done`). Finish or Skip both set the flag so
// it never reappears. No animation beyond the shared .pop-in ease, which the
// global prefers-reduced-motion kill already disables.

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";

const DONE_KEY = "sd-tour-done";

const STEPS: { title: string; body: string; href?: string; linkLabel?: string }[] = [
  {
    title: "Three validated signals",
    body: "Trend, liquidity, and volatility regimes — with MEASURED walk-forward accuracy tiers, served next to every call.",
    href: "/market/regimes",
    linkLabel: "see the regimes →",
  },
  {
    title: "Everything else is experimental — and labeled",
    body: "The honesty pages grade every score against what actually happened, live. Weak numbers say weak numbers.",
    href: "/lab/track-record",
    linkLabel: "see the track record →",
  },
  {
    title: "Instant company peek",
    body: "Click any ticker anywhere — heatmap tiles, tables, search results — for a slide-over company snapshot without leaving the page.",
  },
  {
    title: "Press ⌘K to jump anywhere",
    body: "⌘K (Ctrl+K on Windows/Linux) opens the command palette: fuzzy-jump to any page or look up any SEC-listed company.",
  },
];

export default function FirstRunTour() {
  const pathname = usePathname();
  const [show, setShow] = useState(false);
  const [step, setStep] = useState(0);

  // Decide after mount (localStorage is client-only); never on the login page.
  useEffect(() => {
    try {
      if (!localStorage.getItem(DONE_KEY)) setShow(true);
    } catch {
      // storage blocked → show once this session, can't persist either way
      setShow(true);
    }
  }, []);

  if (!show || pathname === "/login") return null;

  const finish = () => {
    try {
      localStorage.setItem(DONE_KEY, "1");
    } catch {
      // ignore — worst case the tour shows again next visit
    }
    setShow(false);
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
        <div className="mt-4 flex items-center justify-between">
          <button
            type="button"
            onClick={finish}
            className="cursor-pointer text-[0.8rem] hover:text-[var(--text)]"
            style={{ color: "var(--faint)" }}
          >
            Skip tour
          </button>
          <button
            type="button"
            onClick={() => (last ? finish() : setStep((x) => x + 1))}
            autoFocus
            className="chip cursor-pointer px-4 py-1.5 text-[0.8rem] font-semibold"
            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
          >
            {last ? "Done" : "Next →"}
          </button>
        </div>
      </div>
    </div>
  );
}
