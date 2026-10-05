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
// AI list is advisory.
// src/proxy.ts refuses the same agents with a 403.

export default function robots(): MetadataRoute.Robots {
  const site = siteUrl();
  return {
    rules: [
      {
        userAgent: [
          "GPTBot",
          "ChatGPT-User",
          "OAI-SearchBot",
          "ClaudeBot",
          "Claude-Web",
          "anthropic-ai",
          "Claude-SearchBot",
          "Claude-User",
          "CCBot",
          "Google-Extended",
          "PerplexityBot",
          "Perplexity-User",
          "Bytespider",
          "Amazonbot",
          "Applebot-Extended",
          "meta-externalagent",
          "Meta-ExternalFetcher",
          "FacebookBot",
          "Diffbot",
          "cohere-ai",
          "cohere-training-data-crawler",
          "AI2Bot",
          "YouBot",
          "Timpibot",
          "omgili",
          "ImagesiftBot",
          "PetalBot",
          "img2dataset",
          "DuckAssistBot",
          "MistralAI-User",
        ],
        disallow: ["/"],
      },
      {
        userAgent: "*",
        allow: ["/$", "/accuracy", "/proof", "/glossary", "/volatility", "/signup", "/login"],
        disallow: ["/"],
      },
    ],
    // No site URL, no Sitemap line: it must be absolute (see lib/site.ts).
    ...(site ? { sitemap: `${site}/sitemap.xml` } : {}),
  };
}