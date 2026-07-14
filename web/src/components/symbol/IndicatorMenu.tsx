"use client";

// [Indicators ▾] — the toggle menu for the client-side chart indicators.
//
// Controlled: the parent owns the enabled-id list and its localStorage
// persistence; this component only renders the grouped checkboxes and emits
// changes. Grouped by the four categories; SIMPLE view-mode shows plain names,
// PRO appends each indicator's parameters. Popover positioning / dismissal
// mirrors HelpTip (fixed placement from the button rect, Escape + outside-click
// close), so it can't be clipped by the chart panel's overflow.

import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useViewMode } from "@/components/Plain";
import {
  GROUP_LABEL,
  INDICATOR_META,
  type IndicatorGroup,
  type IndicatorId,
} from "@/components/symbol/indicators";

const GROUP_ORDER: IndicatorGroup[] = ["essential", "momentum", "price", "advanced"];

export default function IndicatorMenu({
  value,
  onChange,
}: {
  value: IndicatorId[];
  onChange: (ids: IndicatorId[]) => void;
}) {
  const mode = useViewMode();
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const wrapRef = useRef<HTMLSpanElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const popId = `indmenu-${useId()}`;

  const enabled = new Set(value);
  const count = value.length;

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
    const left = Math.min(Math.max(8, r.left), window.innerWidth - pw - 8);
    let top = r.bottom + 6;
    if (top + ph > window.innerHeight - 8 && r.top - ph - 6 >= 8) top = r.top - ph - 6;
    setPos({ top, left });
  }, []);

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

  const toggle = (id: IndicatorId) => {
    if (enabled.has(id)) onChange(value.filter((v) => v !== id));
    else onChange([...value, id]);
  };

  return (
    <span ref={wrapRef} className="relative inline-flex align-middle">
      <button
        ref={btnRef}
        type="button"
        aria-haspopup="true"
        aria-expanded={open}
        aria-controls={popId}
        onClick={() => (open ? close(true) : setOpen(true))}
        className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
        style={{
          color: count > 0 || open ? "var(--accent)" : "var(--dim)",
          borderColor: count > 0 || open ? "var(--accent)" : "var(--border)",
        }}
      >
        Indicators
        {count > 0 && <span className="ml-1.5 tnum">· {count}</span>}
        <svg
          aria-hidden="true"
          width="11"
          height="11"
          viewBox="0 0 12 12"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="ml-1 inline-block transition-transform duration-150"
          style={{ transform: open ? "rotate(180deg)" : "none" }}
        >
          <path d="M2.5 4.5L6 8l3.5-3.5" />
        </svg>
      </button>
      {open && (
        <div
          id={popId}
          ref={popRef}
          role="dialog"
          aria-label="chart indicators"
          className="pop-in fixed z-[1000] max-h-[70vh] w-[min(300px,calc(100vw-16px))] overflow-y-auto rounded-lg border p-2 text-left text-[0.8125rem] font-normal normal-case tracking-normal"
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
          <div className="mb-1 flex items-center justify-between px-1">
            <span className="text-[0.6875rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
              {count > 0 ? `${count} on` : "none on"}
            </span>
            {count > 0 && (
              <button
                type="button"
                onClick={() => onChange([])}
                className="cursor-pointer text-[0.75rem] underline transition-colors hover:text-[var(--text)]"
                style={{ color: "var(--dim)" }}
              >
                clear all
              </button>
            )}
          </div>
          {GROUP_ORDER.map((group) => (
            <div key={group} className="mb-1.5">
              <div
                className="px-1 pb-1 pt-1.5 text-[0.6875rem] font-semibold uppercase tracking-wider"
                style={{ color: "var(--faint)" }}
              >
                {GROUP_LABEL[group]}
              </div>
              {INDICATOR_META.filter((m) => m.group === group).map((m) => {
                const on = enabled.has(m.id);
                return (
                  <label
                    key={m.id}
                    className="flex min-h-[34px] cursor-pointer items-center gap-2 rounded-md px-1.5 py-1 transition-colors hover:bg-[var(--panel2)]"
                  >
                    <input
                      type="checkbox"
                      checked={on}
                      onChange={() => toggle(m.id)}
                      className="h-3.5 w-3.5 shrink-0 cursor-pointer accent-[var(--accent)]"
                    />
                    <span style={{ color: on ? "var(--text)" : "var(--dim)" }}>{m.name}</span>
                    {mode === "pro" && (
                      <span className="tnum ml-auto text-[0.6875rem]" style={{ color: "var(--faint)" }}>
                        {m.params}
                      </span>
                    )}
                  </label>
                );
              })}
            </div>
          ))}
        </div>
      )}
    </span>
  );
}
