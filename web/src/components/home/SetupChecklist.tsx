"use client";

// SETUP CHECKLIST — the "Get set up" card at the top of the dashboard.
//
// Four steps, driven by real state rather than a wizard: "watch" comes from the
// server (does this account actually track a symbol?), the other three are
// recorded by noteVisit() in Shell when the user reaches the page. At 4/4 the
// card returns null forever — no dismiss button, no "well done", no stored
// dismissal flag to go stale.
//
// It waits for onboarding to finish (`useOnboarded`) because a brand-new
// visitor is looking at the tour right now, and stacking a checklist behind a
// modal is the duplicate-prompt problem this whole pass exists to remove.

import type { ReactElement } from "react";
import Link from "next/link";
import { STEPS, useSteps, useOnboarded } from "@/lib/goal";

export default function SetupChecklist({
  watchlistEmpty,
}: {
  watchlistEmpty: boolean;
}): ReactElement | null {
  const steps = useSteps();
  const onboarded = useOnboarded();

  // "watch" is the one step derived from server truth, not from navigation —
  // real symbols on the watchlist beat any locally stored flag.
  const isDone = (id: (typeof STEPS)[number]["id"]) =>
    id === "watch" ? !watchlistEmpty : steps.includes(id);

  const doneCount = STEPS.filter((s) => isDone(s.id)).length;

  if (!onboarded || doneCount === STEPS.length) return null;

  return (
    <section className="panel" aria-label="setup progress">
      <div className="panel-h">
        <span>GET SET UP</span>
        <span className="tnum ml-auto" style={{ color: "var(--dim)" }}>
          {doneCount} of {STEPS.length}
        </span>
      </div>

      {/* Decorative — the "{n} of {m}" readout above already says this to AT. */}
      <div
        aria-hidden="true"
        className="w-full"
        style={{ height: "3px", background: "var(--border)", borderRadius: "999px" }}
      >
        <div
          style={{
            width: `${(doneCount / STEPS.length) * 100}%`,
            height: "100%",
            background: "var(--accent)",
            borderRadius: "999px",
            transition: "width 300ms ease",
          }}
        />
      </div>

      <ul className="flex flex-col">
        {STEPS.map((step) => {
          const done = isDone(step.id);
          return (
            <li
              key={step.id}
              className="flex items-center gap-2 border-t px-4 py-1.5 text-[0.85rem] sm:px-5"
              style={{ borderColor: "var(--border)" }}
            >
              {done ? (
                <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" className="shrink-0">
                  <path
                    d="M3 8l3 3 7-7"
                    fill="none"
                    stroke="var(--ok)"
                    strokeWidth="1.8"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              ) : (
                <span
                  aria-hidden="true"
                  className="shrink-0 border"
                  style={{
                    width: "16px",
                    height: "16px",
                    borderColor: "var(--border-strong)",
                    borderRadius: "999px",
                  }}
                />
              )}
              <span
                style={{
                  color: done ? "var(--faint)" : "var(--text)",
                  textDecoration: done ? "line-through" : "none",
                }}
              >
                {step.label}
              </span>
              {/* The glyph is aria-hidden, so the status rides here instead —
                  aria-label on a plain <li> is not reliably announced. */}
              <span className="sr-only">{done ? "done" : "not done yet"}</span>
              {!done && (
                <Link
                  href={step.href}
                  className="ml-auto inline-flex min-h-[40px] shrink-0 cursor-pointer items-center px-2 hover:underline"
                  style={{ color: "var(--accent)" }}
                >
                  Start →
                </Link>
              )}
            </li>
          );
        })}
      </ul>

      <div className="px-4 py-2 text-[0.75rem] sm:px-5" style={{ color: "var(--faint)" }}>
        This disappears once you are set up.
      </div>
    </section>
  );
}
