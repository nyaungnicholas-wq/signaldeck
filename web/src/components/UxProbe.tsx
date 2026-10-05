"use client";

/**
 * UX PROBE — the sensor half of the self-grading system.
 *
 * Mounted once in Shell. Watches for the handful of behaviours that reliably
 * mean "this person is confused" and turns them into counters that
 * `src/lib/rubric.ts` grades. Nothing here leaves the browser: the counters
 * live in localStorage and are only ever read by /health.
 *
 * What it can see, and what each one means:
 *   rage click  — 3+ clicks in one spot inside 700ms: "this isn't responding"
 *   dead click  — a click that hit nothing clickable: "I thought that was a button"
 *   back thrash — A → B → A inside 20s: "that wasn't it either"
 *   stall       — 90s on one page with no interaction at all: "now what?"
 *   bounce      — a whole session with zero interactions
 *
 * Deliberately NOT tracked: symbols viewed, search terms, timings finer than a
 * second, anything that could identify a session across tabs. The grade does
 * not need them, so collecting them would just be a liability.
 */

import { useEffect, useRef } from "react";
import { usePathname } from "next/navigation";
import { STEPS, readSteps, readGoal, readOnboarded, STEPS_EVENT } from "@/lib/goal";
import { HELP_EVENT } from "@/components/HelpPanel";
import { bump, noteBounce, noteInteraction, setGoalSet, startSession } from "@/lib/ux";

const RAGE_WINDOW_MS = 700;
const RAGE_RADIUS_PX = 30;
const RAGE_THRESHOLD = 3;
const THRASH_WINDOW_MS = 20_000;
const STALL_MS = 90_000;

/** Anything a person could reasonably expect to respond to a click. */
const INTERACTIVE = 'a,button,input,select,textarea,label,summary,[role="button"],[role="link"],[role="tab"],[role="menuitem"],[tabindex]:not([tabindex="-1"])';

export default function UxProbe(): null {
  const pathname = usePathname();

  // Refs throughout — none of this may cause a render.
  const clicks = useRef<{ t: number; x: number; y: number }[]>([]);
  const ragedAt = useRef(0);
  const history = useRef<{ path: string; t: number }[]>([]);
  const interactedHere = useRef(false);
  const stallTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const activeMinute = useRef(-1);
  const knownSteps = useRef<number | null>(null);
  const sessionInteractions = useRef(0);
  const bounced = useRef(false);

  // ─── Session bootstrap ───────────────────────────────────────────────────
  useEffect(() => {
    const isNewSession = startSession();
    setGoalSet(readGoal() !== null);
    knownSteps.current = readSteps().length;

    // The setup checklist is offered to everyone past onboarding, so that is
    // when the four steps count as "started" — but only once per session, or
    // a reload would add four more and push the completion rate below what
    // the person actually experienced. Completion is counted below, on the
    // steps event, so it is never double-credited either.
    if (isNewSession && readOnboarded()) {
      bump("tasksStarted", STEPS.length);
    }
  }, []);

  // ─── Interaction + rage + dead clicks ────────────────────────────────────
  useEffect(() => {
    const markActive = (viaKeyboard: boolean) => {
      interactedHere.current = true;
      sessionInteractions.current += 1;
      noteInteraction(viaKeyboard);

      // One credit per wall-clock minute that contained any interaction.
      const minute = Math.floor(Date.now() / 60_000);
      if (minute !== activeMinute.current) {
        activeMinute.current = minute;
        bump("activeMinutes");
      }
    };

    const onClick = (e: MouseEvent) => {
      const target = e.target instanceof Element ? e.target : null;
      const hit = target?.closest(INTERACTIVE) ?? null;

      if (hit) {
        markActive(e.detail === 0); // detail 0 = keyboard-activated click
      } else {
        // A click on nothing is only interesting inside content, not on the
        // page chrome or on a text selection the person made deliberately.
        const selecting = (window.getSelection()?.toString().length ?? 0) > 0;
        if (!selecting && target?.closest("main")) bump("deadClicks");
      }

      const now = Date.now();
      clicks.current = [
        ...clicks.current.filter((c) => now - c.t < RAGE_WINDOW_MS),
        { t: now, x: e.clientX, y: e.clientY },
      ];
      const cluster = clicks.current.filter(
        (c) => Math.hypot(c.x - e.clientX, c.y - e.clientY) < RAGE_RADIUS_PX,
      );
      // One rage event per outburst, not one per extra click.
      if (cluster.length >= RAGE_THRESHOLD && now - ragedAt.current > RAGE_WINDOW_MS) {
        ragedAt.current = now;
        bump("rageClicks");
      }
    };

    const onKeyDown = (e: KeyboardEvent) => {
      // Only commands count as interaction — plain typing in a field is already
      // covered by the click that focused it, and modifier keys alone are noise.
      if (e.key === "Enter" || e.key === " " || e.key === "Escape" || e.metaKey || e.ctrlKey) {
        markActive(true);
      }
    };

    const onHelp = () => bump("helpOpens");

    document.addEventListener("click", onClick, true);
    document.addEventListener("keydown", onKeyDown, true);
    window.addEventListener(HELP_EVENT, onHelp);
    return () => {
      document.removeEventListener("click", onClick, true);
      document.removeEventListener("keydown", onKeyDown, true);
      window.removeEventListener(HELP_EVENT, onHelp);
    };
  }, []);

  // ─── Per-route: back-thrash and stalls ───────────────────────────────────
  useEffect(() => {
    const now = Date.now();
    const recent = history.current.filter((h) => now - h.t < THRASH_WINDOW_MS);

    // A → B → A: the current path already appears two entries back.
    if (recent.length >= 2 && recent[recent.length - 2].path === pathname) {
      bump("backThrash");
    }
    history.current = [...recent, { path: pathname, t: now }].slice(-6);

    interactedHere.current = false;
    if (stallTimer.current) clearTimeout(stallTimer.current);
    stallTimer.current = setTimeout(() => {
      if (!interactedHere.current && document.visibilityState === "visible") {
        bump("stalls");
      }
    }, STALL_MS);

    return () => {
      if (stallTimer.current) clearTimeout(stallTimer.current);
    };
  }, [pathname]);

  // ─── Task completion ─────────────────────────────────────────────────────
  useEffect(() => {
    const sync = () => {
      const n = readSteps().length;
      if (knownSteps.current === null) {
        knownSteps.current = n;
        return;
      }
      if (n > knownSteps.current) {
        bump("tasksCompleted", n - knownSteps.current);
        knownSteps.current = n;
      }
    };
    window.addEventListener(STEPS_EVENT, sync);
    return () => window.removeEventListener(STEPS_EVENT, sync);
  }, []);

  // ─── Bounce ──────────────────────────────────────────────────────────────
  useEffect(() => {
    // pagehide, not beforeunload: it is the only one that fires reliably on
    // mobile Safari, and localStorage writes are synchronous so it is enough.
    const onLeave = () => {
      if (bounced.current) return;
      if (document.visibilityState === "visible") return;
      bounced.current = true;
      // noteBounce is session-guarded, so a reload or a second tab-hide in the
      // same visit cannot log a second bounce against one session.
      if (sessionInteractions.current === 0) noteBounce();
    };
    window.addEventListener("pagehide", onLeave);
    document.addEventListener("visibilitychange", onLeave);
    return () => {
      window.removeEventListener("pagehide", onLeave);
      document.removeEventListener("visibilitychange", onLeave);
    };
  }, []);

  return null;
}
