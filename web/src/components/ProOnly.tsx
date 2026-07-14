"use client";

// <ProOnly> — gates methodology / raw-numbers content behind the SIMPLE/PRO
// toggle. PRO mode renders children directly; SIMPLE mode folds them behind
// an explicit disclosure button — demoted, never deleted, the same doctrine
// as StorySection. Hydration-safe: SSR + first paint are always SIMPLE
// (collapsed), then the sd-view-mode subscription takes over. The reveal
// animation runs through the Web Animations API and is skipped entirely
// when prefers-reduced-motion is set.

import { useEffect, useId, useRef, useState } from "react";
import { useViewMode } from "@/components/Plain";

export default function ProOnly({
  summary = "Show methodology",
  children,
}: {
  /** Button label while collapsed in simple mode. */
  summary?: string;
  children: React.ReactNode;
}) {
  const mode = useViewMode();
  const panelId = useId();
  const [open, setOpen] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const el = panelRef.current;
    if (!el || typeof el.animate !== "function") return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    el.animate(
      [
        { opacity: 0, transform: "translateY(-4px)" },
        { opacity: 1, transform: "none" },
      ],
      { duration: 160, easing: "ease-out" }
    );
  }, [open]);

  if (mode === "pro") return <>{children}</>;

  return (
    <div className="flex flex-col gap-2">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen(!open)}
        className="chip inline-flex min-h-[40px] cursor-pointer items-center gap-1.5 self-start transition-colors duration-150 hover:text-[var(--text)]"
        style={{ padding: "5px 12px", ...(open ? { color: "var(--text)" } : undefined) }}
      >
        <svg
          aria-hidden="true"
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="shrink-0 transition-transform duration-150"
          style={{ transform: open ? "rotate(180deg)" : "none" }}
        >
          <path d="M2.5 4.5L6 8l3.5-3.5" />
        </svg>
        {summary}
      </button>
      <div id={panelId} ref={panelRef} hidden={!open}>
        {open ? children : null}
      </div>
    </div>
  );
}
