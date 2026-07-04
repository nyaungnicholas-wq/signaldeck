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

import Link from "next/link";
import { usePathname } from "next/navigation";

export interface HubTab {
  href: string;
  label: string;
  /** Optional extra pathname prefix that also counts as active. */
  match?: string;
  /** Stage 5: active on the EXACT pathname only — for an "ALL" tab whose href
   *  is the parent of its sibling tabs (prefix matching would keep it lit). */
  exact?: boolean;
}

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

  return (
    <nav
      aria-label={ariaLabel}
      className={`panel flex items-center gap-1 overflow-x-auto px-2 ${
        compact ? "py-1" : "py-1.5"
      } text-[0.75rem] tracking-[0.1em]`}
    >
      {tabs.map((t) => {
        const active = isActive(t);
        return (
          <Link
            key={t.href}
            href={t.href}
            aria-current={active ? "page" : undefined}
            className="flex min-h-[40px] shrink-0 cursor-pointer items-center rounded px-3 py-2 whitespace-nowrap transition-colors duration-200"
            style={{
              color: active ? "var(--text)" : "var(--dim)",
              background: active ? "var(--panel2)" : "transparent",
              border: `1px solid ${active ? "var(--border)" : "transparent"}`,
            }}
          >
            {t.label}
          </Link>
        );
      })}
    </nav>
  );
}
