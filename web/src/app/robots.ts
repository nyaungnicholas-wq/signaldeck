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
      // Blanket deny with explicit allows on top - what the comment above has
      // always specified, and what the code did NOT do. It shipped a list of
      // private prefixes instead, so /advanced, /welcome and /signals/report/*
      // were crawlable by forgetting: the exact defect this comment warns
      // about. Allow is matched most-specifically-first by every major crawler,
      // so the public pages stay indexable and everything else is denied by
      // default, including a route added next month. The homepage allow is "/$"
      // (end-of-URL anchor), NOT "/": a bare "/" Allow is EXACTLY as specific as
      // the "/" Disallow, and crawlers break specificity ties toward the LEAST
      // restrictive rule - which would have re-opened the entire site.
      allow: ["/$", "/accuracy", "/proof", "/glossary", "/volatility"],
      disallow: ["/"],
    },
    sitemap: `${siteUrl()}/sitemap.xml`,
  };
}
