"use client";

// LabTabs — the LAB hub's two-level navigation: a section strip (RESEARCH /
// VALIDATION / PORTFOLIO / SYSTEM) over the active section's own surfaces.
//
// It replaces a single 24-tab strip whose last seventeen tabs lived behind a
// "MORE" menu. The pattern is not new here — LAB → SYSTEM has rendered a
// compact second strip since Stage 5; this generalizes it to the whole hub, so
// every lab surface is at most two clicks away and always visible in context
// rather than buried in an overflow list.
//
// Membership lives in app/lab/sections.ts. This component only resolves the
// active section from the URL and renders two strips.

import { usePathname } from "next/navigation";
import HubTabs from "@/components/HubTabs";
import { LAB_SECTIONS, sectionFor } from "@/app/lab/sections";

export default function LabTabs() {
  const pathname = usePathname();
  const active = sectionFor(pathname);

  const sectionTabs = LAB_SECTIONS.map((s) => ({
    href: s.href,
    label: s.label,
    // Lit by MEMBERSHIP, not by href: /lab/pairs must light PORTFOLIO even
    // though PORTFOLIO points at /lab/portfolio. The explicit override keeps
    // sections.ts the only authority on which section owns a path — HubTabs'
    // own prefix test would light both RESEARCH and SYSTEM on /lab/research
    // vs /lab/live and be wrong on most of the hub.
    active: active?.key === s.key,
  }));

  return (
    <>
      <HubTabs ariaLabel="Lab sections" tabs={sectionTabs} />
      {active && (
        <HubTabs
          ariaLabel={`${active.label.toLowerCase()} surfaces`}
          tabs={active.tabs}
          compact
          // Section strips never fold: a section holds at most eight surfaces,
          // and hiding one behind an overflow menu is the exact failure this
          // restructure exists to undo.
          maxVisible={Infinity}
        />
      )}
    </>
  );
}
