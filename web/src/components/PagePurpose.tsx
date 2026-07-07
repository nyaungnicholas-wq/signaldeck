"use client";

// STAGE 3 — <PagePurpose>: the "what does this page answer?" banner, one per
// page, directly under the page title. Guided-story rule: a user should never
// wonder what a page is FOR. Plain English, honest not salesy — purpose lines
// state their caveats (legal lags, simulations, gates) when the page's data
// carries them, because hiding a caveat in plain language is still a lie.
//
// Collapsible and remembered: hiding a page's banner stores its id in
// localStorage (sd-purpose-hidden, a JSON string array). Hydration-safe the
// same way as the SIMPLE/PRO toggle: SSR + first paint render expanded (the
// default), then localStorage takes over. Shown in BOTH view modes — knowing
// what a page is for is orientation, not jargon.

import { useEffect, useState } from "react";

const LS_KEY = "sd-purpose-hidden";

function readHidden(): string[] {
  if (typeof window === "undefined") return [];
  try {
    const v: unknown = JSON.parse(localStorage.getItem(LS_KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function writeHidden(ids: string[]) {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(ids));
  } catch {
    /* storage blocked — the toggle still works for this render */
  }
}

export default function PagePurpose({ id, text }: { id: string; text: string }) {
  const [hidden, setHidden] = useState(false);
  useEffect(() => {
    setHidden(readHidden().includes(id));
  }, [id]);

  const toggle = () => {
    const next = !hidden;
    setHidden(next);
    const ids = readHidden().filter((x) => x !== id);
    if (next) ids.push(id);
    writeHidden(ids);
  };

  if (hidden) {
    return (
      <button
        type="button"
        onClick={toggle}
        aria-expanded={false}
        className="min-h-[32px] cursor-pointer self-start text-left text-[0.68rem] tracking-wider transition-colors duration-150 hover:text-[var(--accent)]"
        style={{ color: "var(--faint)" }}
      >
        ? what is this page for
      </button>
    );
  }

  return (
    <div
      role="note"
      aria-label="what this page answers"
      className="flex items-baseline gap-2 rounded border px-3 py-1.5 text-[0.78rem] leading-relaxed"
      style={{ borderColor: "var(--border)", background: "var(--panel2)", color: "var(--dim)" }}
    >
      <span aria-hidden="true" className="shrink-0 font-bold" style={{ color: "var(--accent)" }}>
        ?
      </span>
      <span className="min-w-0">{text}</span>
      <button
        type="button"
        onClick={toggle}
        aria-label="hide this explanation (remembered on this device)"
        className="ml-auto shrink-0 cursor-pointer px-1 text-[0.8rem] transition-colors duration-150 hover:text-[var(--text)]"
        style={{ color: "var(--faint)" }}
      >
        ×
      </button>
    </div>
  );
}
