"use client";

// HubTabs — reusable URL-driven sub-tab strip for the 6-hub consolidation
// (Stage 2). Each tab is a real route (<Link>), so sub-tab state is the URL
// itself: shareable, bookmarkable, back/forward-friendly. The strip lives in
// the hub's layout.tsx, so it persists (no remount) across tab switches.
//
// A tab is active when the pathname equals its href or sits below it
// (e.g. /lab/system/agents keeps the SYSTEM tab lit). `match` widens the
// active test for tabs whose href differs from the segment they own.
// 40px touch targets; colors ride the same CSS variables as the header nav,
// so reading mode (html[data-reading-mode]) restyles it for free.
//
// Stage 6 progressive disclosure: hubs with more than MAX_VISIBLE tabs keep
// the primary tabs visible and fold the specialist rest into an accessible
// "More research" menu (real routes, presentation only). The active tab is
// always visible — if it lives in the folded set it swaps into the last
// visible slot and the displaced tab moves into the menu.

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useId, useRef, useState } from "react";

export interface HubTab {
  href: string;
  label: string;
  /** Optional extra pathname prefix that also counts as active. */
  match?: string;
  /** Stage 5: active on the EXACT pathname only — for an "ALL" tab whose href
   *  is the parent of its sibling tabs (prefix matching would keep it lit). */
  exact?: boolean;
}

/** Hubs with more tabs than this fold the rest into the "More research" menu.
 *  Folding is currently DISABLED (Infinity): every tab stays directly visible
 *  and the strip scrolls horizontally instead of hiding routes in a menu. */
const MAX_VISIBLE = Infinity;

export default function HubTabs({
  tabs,
  ariaLabel,
  compact = false,
}: {
  tabs: HubTab[];
  ariaLabel: string;
  /** Second-level strips (e.g. LAB → SYSTEM) render slightly quieter. */
  compact?: boolean;
}) {
  const pathname = usePathname();
  const isActive = (t: HubTab) => {
    if (t.exact) return pathname === t.href;
    const under = (base: string) => pathname === base || pathname.startsWith(base + "/");
    return under(t.href) || (t.match !== undefined && under(t.match));
  };

  // Split into the visible row and the folded "More research" set. URLs and
  // tab order are unchanged — this is presentation only.
  let visible = tabs;
  let folded: HubTab[] = [];
  if (tabs.length > MAX_VISIBLE) {
    visible = tabs.slice(0, MAX_VISIBLE);
    folded = tabs.slice(MAX_VISIBLE);
    const activeIdx = folded.findIndex(isActive);
    if (activeIdx !== -1) {
      // The active tab must stay visible: swap it into the last visible slot.
      const displaced = visible[MAX_VISIBLE - 1];
      visible = [...visible.slice(0, MAX_VISIBLE - 1), folded[activeIdx]];
      folded = [displaced, ...folded.filter((_, i) => i !== activeIdx)];
    }
  }

  return (
    <nav
      aria-label={ariaLabel}
      className={`panel relative flex items-center gap-1 px-2 ${
        compact ? "py-1" : "py-1.5"
      } text-[0.75rem] tracking-[0.1em]`}
    >
      <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
        {visible.map((t) => {
          const active = isActive(t);
          return (
            <Link
              key={t.href}
              href={t.href}
              aria-current={active ? "page" : undefined}
              className={`nav-link flex min-h-[40px] shrink-0 cursor-pointer items-center px-3 py-2 font-medium whitespace-nowrap ${
                active ? "nav-link-active" : ""
              }`}
            >
              {t.label}
            </Link>
          );
        })}
      </div>
      {folded.length > 0 && <MoreMenu tabs={folded} pathname={pathname} />}
    </nav>
  );
}

/** The folded specialist tabs behind an accessible menu button. Menu pattern:
 *  click/Enter/Space toggles, arrows move between items, Escape closes and
 *  returns focus to the button, outside click/tap closes. Every item stays a
 *  real <Link>, so middle-click / long-press behave like the visible tabs. */
function MoreMenu({ tabs, pathname }: { tabs: HubTab[]; pathname: string }) {
  const [open, setOpen] = useState(false);
  const menuId = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const itemRefs = useRef<(HTMLAnchorElement | null)[]>([]);
  // Which item receives focus when the menu opens (last, for ArrowUp).
  const openFocusRef = useRef<"first" | "last">("first");

  // Close whenever navigation happens (guarded adjustment during render —
  // no effect, so the menu never paints open on the new page).
  const [prevPathname, setPrevPathname] = useState(pathname);
  if (prevPathname !== pathname) {
    setPrevPathname(pathname);
    if (open) setOpen(false);
  }

  // Outside click / tap closes (no focus steal — just dismiss).
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const items = itemRefs.current;
    (openFocusRef.current === "last" ? items[tabs.length - 1] : items[0])?.focus();
    openFocusRef.current = "first";
  }, [open, tabs.length]);

  const focusItem = (i: number) => {
    const n = tabs.length;
    itemRefs.current[((i % n) + n) % n]?.focus();
  };

  const onMenuKeyDown = (e: React.KeyboardEvent) => {
    const idx = itemRefs.current.findIndex((el) => el === document.activeElement);
    if (e.key === "ArrowDown") {
      e.preventDefault();
      focusItem(idx + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      focusItem(idx - 1);
    } else if (e.key === "Home") {
      e.preventDefault();
      focusItem(0);
    } else if (e.key === "End") {
      e.preventDefault();
      focusItem(tabs.length - 1);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
      buttonRef.current?.focus();
    } else if (e.key === "Tab") {
      setOpen(false); // let focus move on naturally
    }
  };

  const onButtonKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      openFocusRef.current = "first";
      setOpen(true);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      openFocusRef.current = "last";
      setOpen(true);
    } else if (e.key === "Escape" && open) {
      e.preventDefault();
      setOpen(false);
    }
  };

  return (
    <div ref={rootRef} className="relative shrink-0">
      <button
        ref={buttonRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={menuId}
        aria-label="More research"
        onClick={() => {
          // Closing by click can unmount a menu item that holds focus (arrow
          // keys move focus into the menu) — return focus to the button, like
          // the Escape handlers do, so it never falls back to <body>.
          if (open) buttonRef.current?.focus();
          setOpen((o) => !o);
        }}
        onKeyDown={onButtonKeyDown}
        className={`nav-link flex min-h-[40px] cursor-pointer items-center gap-1.5 px-3 py-2 font-medium whitespace-nowrap ${
          open ? "nav-link-active" : ""
        }`}
      >
        MORE
        <svg
          aria-hidden="true"
          width="10"
          height="10"
          viewBox="0 0 10 10"
          fill="none"
          style={{ transform: open ? "rotate(180deg)" : undefined }}
        >
          <path d="M1.5 3.5 L5 7 L8.5 3.5" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </button>
      {open && (
        <div
          id={menuId}
          role="menu"
          aria-label="More research"
          onKeyDown={onMenuKeyDown}
          className="pop-in absolute top-full right-0 z-20 mt-1 flex min-w-[11rem] flex-col rounded-xl border p-1"
          style={{
            background: "var(--panel3)",
            borderColor: "var(--border-strong)",
            boxShadow: "var(--shadow-2)",
          }}
        >
          {tabs.map((t, i) => (
            <Link
              key={t.href}
              href={t.href}
              role="menuitem"
              tabIndex={-1}
              ref={(el) => {
                itemRefs.current[i] = el;
              }}
              onClick={() => setOpen(false)}
              className="nav-link flex min-h-[44px] cursor-pointer items-center px-3 py-2 whitespace-nowrap"
            >
              {t.label}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
