"use client";

// HELP PANEL — the permanent way back in.
//
// SignalDeck has 55 pages and its onboarding used to be a one-shot modal: once
// dismissed, there was no route back to it short of guessing a URL. This panel
// is opened by the header "? Help" chip (which just dispatches HELP_EVENT) and
// is always available, so every explanatory surface — the tour, the watchlist
// setup, the glossary, the advanced door — is two clicks from anywhere.
//
// It also owns the goal switcher, so "what SignalDeck shows you" is changeable
// without hunting for a settings page (there isn't one, deliberately).

import React, { useEffect, useId, useRef, useState } from "react";
import Link from "next/link";
import { GOALS, applyGoal, useGoal } from "@/lib/goal";

/** Window event that opens the panel. Dispatched by the header "? Help" chip. */
export const HELP_EVENT = "sd-help";
/** Dispatched when the user asks to replay the first-run tour from here. */
export const TOUR_OPEN_EVENT = "sd-tour-open";

const ROW =
  "flex w-full cursor-pointer flex-col justify-center gap-0.5 p-3 text-left transition-colors duration-150 hover:bg-[rgba(255,255,255,0.05)]";

function RowText({ title, sub }: { title: string; sub: string }) {
  return (
    <>
      <span className="text-sm font-medium" style={{ color: "var(--text)" }}>
        {title}
      </span>
      <span className="text-[0.75rem] leading-snug" style={{ color: "var(--dim)" }}>
        {sub}
      </span>
    </>
  );
}

export default function HelpPanel(): React.ReactElement | null {
  const [open, setOpen] = useState(false);
  const headingId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const goal = useGoal();

  // The element focused before we opened, so Escape/close puts the caret back
  // where the user left it instead of dumping them at the top of the document.
  const previousFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const onOpen = () => setOpen(true);
    window.addEventListener(HELP_EVENT, onOpen);
    return () => window.removeEventListener(HELP_EVENT, onOpen);
  }, []);

  // Escape + outside-click dismissal, mounted only while open.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onPointerDown = (e: PointerEvent) => {
      if (e.target instanceof Node && !cardRef.current?.contains(e.target)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open]);

  useEffect(() => {
    if (open) {
      if (document.activeElement instanceof HTMLElement) previousFocus.current = document.activeElement;
      closeRef.current?.focus();
      return;
    }
    previousFocus.current?.focus();
    previousFocus.current = null;
  }, [open]);

  if (!open) return null;

  const close = () => setOpen(false);
  // useGoal() returns the stored KEY; resolve it to the definition for the blurb.
  const current = GOALS.find((g) => g.key === goal) ?? GOALS[0];

  return (
    <div
      className="fixed inset-0 z-[70] flex items-end justify-center p-4 sm:items-center"
      style={{ background: "rgba(3,6,12,0.55)", backdropFilter: "blur(3px)" }}
    >
      <div
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingId}
        className="pop-in max-h-[90vh] w-full max-w-lg overflow-y-auto p-4"
        style={{
          background: "rgba(10,16,26,0.94)",
          backdropFilter: "blur(24px) saturate(150%)",
          WebkitBackdropFilter: "blur(24px) saturate(150%)",
          border: "1px solid rgba(255,255,255,0.12)",
          borderRadius: "1rem",
          boxShadow: "var(--shadow-2)",
        }}
      >
        <div className="mb-3 flex items-center justify-between">
          <h2 id={headingId} className="text-base font-semibold" style={{ color: "var(--text)" }}>
            Need a hand?
          </h2>
          <button
            ref={closeRef}
            type="button"
            aria-label="close help"
            onClick={close}
            className="flex min-h-[40px] min-w-[40px] cursor-pointer items-center justify-center rounded-lg text-[var(--dim)] transition-colors duration-150 hover:text-[var(--text)]"
          >
            <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
              <path
                d="M15 5L5 15M5 5L15 15"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>

        <div
          className="mb-4 flex flex-col divide-y overflow-hidden rounded-xl border"
          style={{ borderColor: "var(--border)" }}
        >
          <button
            type="button"
            className={ROW}
            onClick={() => {
              close();
              window.dispatchEvent(new Event(TOUR_OPEN_EVENT));
            }}
          >
            <RowText title="Take the 60-second tour" sub="A quick walk through what SignalDeck does." />
          </button>
          <Link href="/welcome" onClick={close} className={ROW}>
            <RowText title="Set up my watchlist" sub="Pick the companies you want us to track." />
          </Link>
          <Link href="/glossary" onClick={close} className={ROW}>
            <RowText title="Glossary" sub="Every term on the site, in plain English." />
          </Link>
          <Link href="/advanced" onClick={close} className={ROW}>
            <RowText title="Advanced tools" sub="Research, backtests, and the experimental model lab." />
          </Link>
          <Link href="/health" onClick={close} className={ROW}>
            <RowText
              title="Is this actually easy to use?"
              sub="We grade ourselves on that too, and show the score."
            />
          </Link>
        </div>

        <div className="mb-4">
          <h3 className="mb-2 text-[0.75rem] font-medium" style={{ color: "var(--dim)" }}>
            Keyboard shortcuts
          </h3>
          <div className="flex flex-col gap-2">
            {[
              { k: "⌘K / Ctrl K", v: "Search pages and companies" },
              { k: "Esc", v: "Close anything that is open" },
            ].map((s) => (
              <div key={s.k} className="flex items-center justify-between gap-3">
                <span className="chip mono text-[0.75rem]" style={{ color: "var(--text)" }}>
                  {s.k}
                </span>
                <span className="text-[0.85rem]" style={{ color: "var(--dim)" }}>
                  {s.v}
                </span>
              </div>
            ))}
          </div>
        </div>

        <div className="border-t pt-3" style={{ borderColor: "var(--border)" }}>
          <p className="mb-2 text-[0.75rem] font-medium" style={{ color: "var(--dim)" }}>
            What SignalDeck shows you
          </p>
          <div className="mb-2 flex flex-wrap gap-2">
            {GOALS.map((g) => {
              // Pressed reflects an ACTUAL stored choice, not the fallback —
              // claiming a selection the user never made is a small lie.
              const active = goal === g.key;
              return (
                <button
                  key={g.key}
                  type="button"
                  aria-pressed={active}
                  onClick={() => applyGoal(g.key)}
                  className="chip flex min-h-[40px] cursor-pointer items-center px-3 transition-colors duration-150"
                  style={
                    active
                      ? { color: "var(--accent)", borderColor: "var(--accent)" }
                      : { color: "var(--text)" }
                  }
                >
                  {g.title}
                </button>
              );
            })}
          </div>
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {current?.blurb}
          </p>
        </div>
      </div>
    </div>
  );
}
