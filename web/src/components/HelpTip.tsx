"use client";

// <HelpTip> — the accessible replacement for the [title] dotted-underline
// tooltips (see the note in globals.css). A small "?" icon button that
// toggles an inline popover: click/Enter/Space open it (native button
// semantics), Escape and outside-click dismiss it, Escape returns focus to
// the button. Works on touch — nothing here requires hover.
//
// The visible glyph is 24px; the button itself is 44x44 for the touch-target
// floor, with a negative margin so its inline footprint stays glyph-sized.
// The popover is position:fixed and placed from the button's rect (clamped
// to the viewport, flipped above when there's no room below), so it can't be
// clipped by .panel overflow or scrolling table wrappers. No dependencies.

import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { ReactNode } from "react";

function HelpTip({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const wrapRef = useRef<HTMLSpanElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const popId = `helptip-${useId()}`;

  const close = useCallback((refocus: boolean) => {
    setOpen(false);
    setPos(null);
    if (refocus) btnRef.current?.focus();
  }, []);

  const place = useCallback(() => {
    const btn = btnRef.current;
    const pop = popRef.current;
    if (!btn || !pop) return;
    const r = btn.getBoundingClientRect();
    const pw = pop.offsetWidth;
    const ph = pop.offsetHeight;
    const left = Math.min(Math.max(8, r.left + r.width / 2 - pw / 2), window.innerWidth - pw - 8);
    let top = r.bottom + 8;
    if (top + ph > window.innerHeight - 8 && r.top - ph - 8 >= 8) top = r.top - ph - 8;
    setPos({ top, left });
  }, []);

  // Position before paint on open, then track scroll (capture phase catches
  // scrolling containers like .table-wrap) and resize while open.
  useLayoutEffect(() => {
    if (open) place();
  }, [open, place]);
  useEffect(() => {
    if (!open) return;
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open, place]);

  // Escape (from the button OR inside the popover) and outside-click dismiss.
  // Escape restores focus to the button; an outside click keeps focus where
  // the user just put it.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close(true);
    };
    const onPointerDown = (e: PointerEvent) => {
      if (e.target instanceof Node && !wrapRef.current?.contains(e.target)) close(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open, close]);

  return (
    <span ref={wrapRef} className="relative inline-flex align-middle">
      <button
        ref={btnRef}
        type="button"
        aria-label={label}
        aria-expanded={open}
        aria-controls={popId}
        onClick={() => (open ? close(true) : setOpen(true))}
        className={`-m-2.5 inline-flex h-11 w-11 shrink-0 cursor-pointer items-center justify-center rounded-full transition-colors duration-150 ${
          open ? "text-[var(--accent)]" : "text-[var(--dim)] hover:text-[var(--text)]"
        }`}
      >
        <svg
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="9" />
          <path d="M9.3 9.2a2.8 2.8 0 0 1 5.44.93c0 1.87-2.8 2.8-2.8 2.8" />
          <line x1="11.94" y1="16.6" x2="11.95" y2="16.6" strokeWidth="2.4" />
        </svg>
      </button>
      {open && (
        <div
          id={popId}
          ref={popRef}
          className="pop-in fixed z-[1000] max-w-[min(320px,calc(100vw-16px))] rounded-lg border p-3 text-left text-[0.75rem] font-normal normal-case leading-relaxed tracking-normal"
          style={{
            top: pos?.top ?? 0,
            left: pos?.left ?? 0,
            visibility: pos ? "visible" : "hidden",
            background: "var(--panel3)",
            borderColor: "var(--border-strong)",
            color: "var(--text)",
            boxShadow: "var(--shadow-2)",
          }}
        >
          {children}
        </div>
      )}
    </span>
  );
}

export default HelpTip;
export { HelpTip };
