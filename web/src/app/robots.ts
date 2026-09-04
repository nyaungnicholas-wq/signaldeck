import type { MetadataRoute } from "next";
import { siteUrl } from "@/lib/site";

/**
 * Allow the public pages, disallow everything else.
 *
 * The deny rule is deliberately a blanket `/` with explicit allows on top,
 * rather than a list of private prefixes. A denylist here has the same defect
 * it has in the daemon's auth: a route added later is crawlable by forgetting.
 * This is not a security control -- the daemon's allowlist is -- but a crawler
 * indexing a login-walled research surface produces search results that 401
 * for everyone who clicks them.
 */
export default function robots(): MetadataRoute.Robots {
  return {
    rules: {
      userAgent: "*",
      allow: ["/", "/accuracy", "/proof", "/glossary", "/volatility"],
      disallow: ["/dashboard", "/lab/", "/intel/", "/market/", "/watchlist", "/hud", "/s/", "/api/", "/login"],
    },
    sitemap: `${siteUrl()}/sitemap.xml`,
  };
}
