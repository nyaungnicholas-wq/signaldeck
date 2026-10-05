"use client";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";

export default function HeaderTools({ label, dot, children }: { label: string; dot?: "ok" | "warn" | "bad" | null; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onMouse = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onMouse);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onMouse);
    };
  }, [open]);

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        aria-haspopup="dialog"
        onClick={() => setOpen((o) => !o)}
        title="status and view settings"
        className="chip flex min-h-[40px] cursor-pointer items-center gap-2 px-3 transition-colors duration-150 hover:text-[var(--text)]"
      >
        {dot && (
          <span
            aria-hidden="true"
            className="inline-block h-2 w-2 rounded-full"
            style={{ background: dot === "ok" ? "var(--ok)" : dot === "warn" ? "var(--warn)" : "var(--bad)" }}
          />
        )}
        <span>{label}</span>
        <span aria-hidden="true" className="text-[0.7rem]">{open ? "▴" : "▾"}</span>
      </button>
      {open && (
        <div
          id={panelId}
          role="dialog"
          aria-label="status and view settings"
          className="panel absolute right-0 top-[calc(100%+0.5rem)] z-50 flex min-w-[260px] flex-col gap-2 p-3"
          style={{ boxShadow: "0 12px 40px rgba(0,0,0,0.45)" }}
        >
          {children}
        </div>
      )}
    </div>
  );
}