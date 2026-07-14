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

import { useSyncExternalStore } from "react";

const LS_KEY = "sd-purpose-hidden";
const EVT = "sd-purpose";

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
  window.dispatchEvent(new Event(EVT));
}

// Read straight from localStorage via useSyncExternalStore (same hydration-safe
// idiom as the header toggles): SSR + first paint render the default (expanded),
// then the store snapshot reflects the persisted choice — no setState-in-effect.
function subscribe(cb: () => void): () => void {
  window.addEventListener(EVT, cb);
  window.addEventListener("storage", cb); // cross-tab sync
  return () => {
    window.removeEventListener(EVT, cb);
    window.removeEventListener("storage", cb);
  };
}

export default function PagePurpose({ id, text }: { id: string; text: string }) {
  const hidden = useSyncExternalStore(
    subscribe,
    () => readHidden().includes(id),
    () => false,
  );

  const toggle = () => {
    const ids = readHidden().filter((x) => x !== id);
    if (!hidden) ids.push(id);
    writeHidden(ids);
  };

  if (hidden) {
    return (
      <button
        type="button"
        onClick={toggle}
        aria-expanded={false}
        className="min-h-[32px] cursor-pointer self-start text-left text-[0.75rem] tracking-wider text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
      >
        ? what is this page for
      </button>
    );
  }

  return (
    <div
      role="note"
      aria-label="what this page answers"
      className="flex items-baseline gap-2 rounded-lg border px-3 py-1.5 text-[0.75rem] leading-relaxed"
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
        className="ml-auto inline-flex min-h-[24px] min-w-[24px] shrink-0 cursor-pointer items-center justify-center self-center px-1 text-[0.75rem] text-[var(--faint)] transition-colors duration-150 hover:text-[var(--text)]"
      >
        ×
      </button>
    </div>
  );
}
