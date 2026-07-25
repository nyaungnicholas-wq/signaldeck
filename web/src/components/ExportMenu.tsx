"use client";

// EXPORT UNIFICATION (#23) — one small "↓ export" chip that opens a glass
// menu of export actions. Items are either real download links (href — only
// endpoints that actually exist in the daemon get linked) or client-side
// actions (onClick, e.g. copy-as-markdown) that flash a done label after
// running. Closes on outside click and Escape.

import { useEffect, useRef, useState } from "react";

export interface ExportItem {
  label: string;
  /** Download link — rendered as a plain <a>. */
  href?: string;
  /** Client-side action (e.g. clipboard copy). */
  onClick?: () => void | Promise<void>;
  /** Flash text shown after onClick resolves (default "done"). */
  doneLabel?: string;
}

export default function ExportMenu({ items }: { items: ExportItem[] }) {
  const [open, setOpen] = useState(false);
  const [flashIdx, setFlashIdx] = useState<number | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (items.length === 0) return null;

  return (
    <div ref={wrapRef} className="relative inline-block">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="chip cursor-pointer hover:border-[var(--border-strong)] hover:text-[var(--text)]"
        title="export this surface's data"
      >
        ↓ export
      </button>
      {open ? (
        <div
          role="menu"
          className="panel absolute right-0 z-30 mt-1 flex min-w-[240px] flex-col p-1"
          style={{
            background: "rgba(10,16,26,0.92)",
            backdropFilter: "blur(18px) saturate(150%)",
            WebkitBackdropFilter: "blur(18px) saturate(150%)",
          }}
        >
          {items.map((it, i) =>
            it.href ? (
              <a
                key={it.label}
                role="menuitem"
                href={it.href}
                onClick={() => setOpen(false)}
                className="cursor-pointer rounded px-3 py-2 text-[0.75rem] hover:bg-[rgba(255,255,255,0.07)]"
              >
                {it.label}
              </a>
            ) : (
              <button
                key={it.label}
                type="button"
                role="menuitem"
                onClick={() => {
                  Promise.resolve(it.onClick?.()).then(() => {
                    setFlashIdx(i);
                    setTimeout(() => {
                      setFlashIdx((cur) => (cur === i ? null : cur));
                      setOpen(false);
                    }, 900);
                  });
                }}
                className="cursor-pointer rounded px-3 py-2 text-left text-[0.75rem] hover:bg-[rgba(255,255,255,0.07)]"
              >
                {flashIdx === i ? (
                  <span style={{ color: "var(--ok)" }}>✓ {it.doneLabel ?? "done"}</span>
                ) : (
                  it.label
                )}
              </button>
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}
