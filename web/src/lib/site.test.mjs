// B6 (2026-10-02): a build with no NEXT_PUBLIC_SITE_URL must emit no absolute
// localhost URL (og:image, sitemap, robots), so siteUrl() is null, not a
// loopback fallback. The built artifacts are grepped in the release check.
//   node --test src/lib/site.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { siteUrl } from "./site.ts";

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
