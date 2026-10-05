// B6 (2026-10-02): a build with no NEXT_PUBLIC_SITE_URL must emit no absolute
// localhost URL (og:image, sitemap, robots), so siteUrl() is null, not a
// loopback fallback, and every consumer handles null. The consumers import
// through the @/ alias, which node --test cannot resolve, so they are checked
// as source; the built output was grepped by hand when this landed.
//   node --test src/lib/site.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { siteUrl } from "./site.ts";

const src = (rel) => readFileSync(new URL(rel, import.meta.url), "utf8");

test("unset or blank site URL is null, never a localhost fallback", () => {
  const was = process.env.NEXT_PUBLIC_SITE_URL;
  try {
    delete process.env.NEXT_PUBLIC_SITE_URL;
    assert.equal(siteUrl(), null);
    process.env.NEXT_PUBLIC_SITE_URL = "   ";
    assert.equal(siteUrl(), null);
    process.env.NEXT_PUBLIC_SITE_URL = "https://x.test//";
    assert.equal(siteUrl(), "https://x.test");
  } finally {
    if (was === undefined) delete process.env.NEXT_PUBLIC_SITE_URL;
    else process.env.NEXT_PUBLIC_SITE_URL = was;
  }
});

test("no consumer can put a loopback URL in og:image, the sitemap or robots.txt", () => {
  // The file convention attaches og:image to every page and Next resolves it
  // against http://localhost:$PORT when metadataBase is null.
  assert.ok(!existsSync(new URL("../app/opengraph-image.tsx", import.meta.url)), "opengraph-image file convention is back");
  const layout = src("../app/layout.tsx");
  assert.ok(layout.includes("metadataBase: site ? new URL(site) : null"));
  assert.ok(layout.includes("...(site ? { openGraph: { images: [OG_IMAGE] } } : {})"));
  assert.ok(src("../app/sitemap.ts").includes("if (!base) return [];"));
  assert.ok(src("../app/robots.ts").includes("...(site ? { sitemap: `${site}/sitemap.xml` } : {})"));
});
