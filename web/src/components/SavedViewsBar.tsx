"use client";

// <SavedViewsBar> — a compact chip row that remembers a page's filter/view
// state on this device (localStorage via lib/savedViews, key sd-views:<page>).
// Presets are built-in starting points (not deletable); saved views are the
// user's own, each with an explicit labelled × button. Naming happens in an
// inline form — no browser prompt() — and everything is keyboard operable:
// Enter saves, Escape cancels. Hydration-safe: the server snapshot renders no
// saved views, then the client useSyncExternalStore read takes over.

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  deleteView,
  getServerViewsSnapshot,
  getViewsSnapshot,
  saveView,
  subscribeViews,
} from "@/lib/savedViews";

export default function SavedViewsBar({
  pageKey,
  currentState,
  onApply,
  presets = [],
}: {
  pageKey: string;
  currentState: Record<string, unknown>;
  onApply: (state: Record<string, unknown>) => void;
  presets?: { name: string; state: Record<string, unknown> }[];
}) {
  const views = useSyncExternalStore(
    subscribeViews,
    () => getViewsSnapshot(pageKey),
    getServerViewsSnapshot,
  );
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (naming) inputRef.current?.focus();
  }, [naming]);

  const closeForm = () => {
    setNaming(false);
    setName("");
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n) return;
    saveView(pageKey, n, currentState);
    closeForm();
  };

  const remove = (n: string) => {
    deleteView(pageKey, n);
  };

  return (
    <div role="group" aria-label="saved views" className="flex flex-wrap items-center gap-2">
      {presets.map((p) => (
        <button
          key={`preset:${p.name}`}
          type="button"
          onClick={() => onApply(p.state)}
          className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
          style={{ padding: "5px 12px" }}
        >
          {p.name}
        </button>
      ))}
      {views.map((v) => (
        <span
          key={v.name}
          className="chip inline-flex min-h-[40px] items-stretch"
          style={{ padding: 0 }}
        >
          <button
            type="button"
            onClick={() => onApply(v.state)}
            className="min-h-[40px] cursor-pointer pl-3 pr-1.5 transition-colors duration-150 hover:text-[var(--text)]"
            style={{ color: "var(--accent)" }}
          >
            {v.name}
          </button>
          <button
            type="button"
            onClick={() => remove(v.name)}
            aria-label={`delete saved view "${v.name}"`}
            className="inline-flex min-h-[40px] min-w-[28px] cursor-pointer items-center justify-center pl-1 pr-2 transition-colors duration-150 hover:text-[var(--ask)]"
            style={{ color: "var(--dim)" }}
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
            >
              <path d="M2.5 2.5l7 7M9.5 2.5l-7 7" />
            </svg>
          </button>
        </span>
      ))}
      {naming ? (
        <form onSubmit={submit} className="flex flex-wrap items-center gap-2">
          <input
            ref={inputRef}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") closeForm();
            }}
            aria-label="name for this view"
            placeholder="name this view"
            className="min-h-[40px] rounded-lg border px-3 text-[0.75rem]"
            style={{
              background: "var(--panel2)",
              borderColor: "var(--border)",
              color: "var(--text)",
            }}
          />
          <button
            type="submit"
            disabled={!name.trim()}
            className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-50"
            style={{ padding: "5px 12px", color: "var(--accent)" }}
          >
            save
          </button>
          <button
            type="button"
            onClick={closeForm}
            className="chip min-h-[40px] cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
            style={{ padding: "5px 12px" }}
          >
            cancel
          </button>
        </form>
      ) : (
        <button
          type="button"
          onClick={() => setNaming(true)}
          className="chip inline-flex min-h-[40px] cursor-pointer items-center gap-1.5 transition-colors duration-150 hover:text-[var(--text)]"
          style={{ padding: "5px 12px" }}
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
          >
            <path d="M6 2.5v7M2.5 6h7" />
          </svg>
          save current view
        </button>
      )}
    </div>
  );
}
