// LAB section map — pure-logic checks (no browser: these tests take no `page`
// fixture, so playwright runs them in Node and they pass without a browser
// download).
//
// The load-bearing one is COVERAGE: it reads src/app/lab off disk and fails if
// a route exists that no section claims. Without it, adding a lab page and
// forgetting sections.ts produces a page that is reachable only by typing its
// URL — invisible in the nav, which is exactly the failure the 24-tab "MORE"
// menu used to cause and this restructure exists to end.

import { test, expect } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { LAB_SECTIONS, sectionFor } from "../src/app/lab/sections";

const LAB_DIR = path.resolve(__dirname, "..", "src", "app", "lab");

/** Every route under /lab that has a page.tsx, as a URL path. */
function labRoutes(dir = LAB_DIR, prefix = "/lab"): string[] {
  const out: string[] = [];
  if (fs.existsSync(path.join(dir, "page.tsx"))) out.push(prefix);
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.isDirectory()) out.push(...labRoutes(path.join(dir, e.name), `${prefix}/${e.name}`));
  }
  return out;
}

test("every lab route is claimed by exactly one section", () => {
  const unclaimed = labRoutes().filter((r) => sectionFor(r) === null);
  expect(unclaimed, `lab routes missing from sections.ts: ${unclaimed.join(", ")}`).toEqual([]);
});

test("no route is listed in two sections", () => {
  const seen = new Map<string, string>();
  for (const s of LAB_SECTIONS) {
    for (const t of s.tabs) {
      expect(seen.has(t.href), `${t.href} in both ${seen.get(t.href)} and ${s.key}`).toBe(false);
      seen.set(t.href, s.key);
    }
  }
});

test("sectionFor resolves the cases prefix matching gets wrong", () => {
  // A surface whose href does not start with its section's href.
  expect(sectionFor("/lab/pairs")?.key).toBe("portfolio");
  // A SYSTEM surface that lives OUTSIDE /lab/system/.
  expect(sectionFor("/lab/live")?.key).toBe("system");
  // Longest match wins: /lab/system is also a tab, but the nested one owns it.
  expect(sectionFor("/lab/system/quality")?.key).toBe("system");
  // RESEARCH's section href and one of its tabs are the same route.
  expect(sectionFor("/lab/research")?.key).toBe("research");
  // A route no section claims resolves to null rather than a guess.
  expect(sectionFor("/lab/not-a-real-page")).toBeNull();
});

test("each section's landing href is one of its own tabs", () => {
  for (const s of LAB_SECTIONS) {
    expect(
      s.tabs.some((t) => t.href === s.href),
      `${s.key} lands on ${s.href}, which is not one of its tabs`,
    ).toBe(true);
  }
});
