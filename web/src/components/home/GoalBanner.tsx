"use client";

/**
 * GOAL BANNER — the dashboard's "this page is arranged for you" strip.
 *
 * Two jobs. It tells you the dashboard is currently arranged around an answer
 * you gave, and it lets you change that answer in one click, in place, without
 * hunting for a settings page (there deliberately isn't one).
 *
 * That second job is the reason this is a banner and not a line of text: the
 * dashboard had almost nothing on it that changed what you were looking at.
 * A control that visibly rearranges the page in front of you is the cheapest
 * honest interactivity on the whole surface.
 */

import type { ReactElement } from "react";
import { GOALS, applyGoal, layoutFor, useGoal, type Goal } from "@/lib/goal";
import { bump } from "@/lib/ux";

export default function GoalBanner({ note }: { note?: string } = {}): ReactElement {
  const goal = useGoal();
  // Each page describes what its own arrangement does. Without an override
  // this is the dashboard's note, which is where the banner started.
  const line = note ?? layoutFor(goal).note;
  const current = GOALS.find((g) => g.key === goal) ?? null;

  const choose = (g: Goal) => {
    applyGoal(g);
    // A goal switch is a real interaction with a real consequence — count it
    // so the Personalization signal reflects people using it, not just the
    // control existing.
    bump("interactions");
  };

  return (
    <section
      // data-goal marks this page as genuinely goal-dependent for the UX audit.
      data-goal={goal ?? "none"}
      aria-label="who this dashboard is arranged for"
      className="panel"
    >
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-4 py-2 text-[0.8rem] sm:px-5">
        {current ? (
          <>
            <span style={{ color: "var(--faint)" }}>Arranged for:</span>
            <strong style={{ color: "var(--text)" }}>{current.title}</strong>
          </>
        ) : (
          <strong style={{ color: "var(--text)" }}>
            Not sure where to start? Tell us what you&rsquo;re here for.
          </strong>
        )}
        <span className="basis-full" style={{ color: "var(--dim)" }}>
          {line}
        </span>
      </div>

      <div
        className="flex flex-wrap gap-2 border-t px-4 py-2 sm:px-5"
        style={{ borderColor: "var(--border)" }}
      >
        {GOALS.map((g) => {
          const active = g.key === goal;
          return (
            <button
              key={g.key}
              type="button"
              onClick={() => choose(g.key)}
              aria-pressed={active}
              title={g.blurb}
              className="chip inline-flex min-h-[40px] cursor-pointer items-center px-3 transition-colors duration-150"
              style={
                active
                  ? { color: "var(--bg)", background: "var(--accent)", borderColor: "var(--accent)" }
                  : { color: "var(--dim)" }
              }
            >
              {g.title}
            </button>
          );
        })}
      </div>
    </section>
  );
}
