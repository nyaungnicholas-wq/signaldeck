// user-agent blocking stops crawlers that identify themselves.
// It is not a security control (a scraper can lie about its UA).
// Rate limits and the daemon route allowlist are the controls.

import { NextResponse, type NextRequest } from "next/server";

const AI_BOTS = [
  "gptbot",
  "chatgpt-user",
  "oai-searchbot",
  "claudebot",
  "claude-web",
  "anthropic-ai",
  "claude-searchbot",
  "claude-user",
  "ccbot",
  "google-extended",
  "perplexitybot",
  "perplexity-user",
  "bytespider",
  "amazonbot",
  "applebot-extended",
  "meta-externalagent",
  "meta-externalfetcher",
  "facebookbot",
  "diffbot",
  "cohere-ai",
  "cohere-training-data-crawler",
  "ai2bot",
  "youbot",
  "timpibot",
  "omgili",
  "imagesiftbot",
  "petalbot",
  "scrapy",
  "img2dataset",
  "firecrawl",
  "duckassistbot",
  "mistralai-user",
];

const LOOPBACK = /^(localhost|127\.0\.0\.1|\[::1\])(:\d+)?$/;

export function proxy(req: NextRequest) {
  // The quick tunnel serves http:// as-is (no edge redirect), so a sign-in
  // there set the session cookie without Secure — the daemon keys Secure on
  // X-Forwarded-Proto. Send any plain-http edge visit to https first. Direct
  // loopback use (no proxy, or a local host) is untouched. Host only, never
  // X-Forwarded-Host: the edge passes a client's copy of that through, and the
  // redirect must not point anywhere the client chose. no-store so no cache
  // keeps one visitor's redirect for another.
  const host = req.headers.get("host") ?? "";
  if (req.headers.get("x-forwarded-proto") === "http" && host && !LOOPBACK.test(host)) {
    const res = NextResponse.redirect(`https://${host}${req.nextUrl.pathname}${req.nextUrl.search}`, 308);
    res.headers.set("cache-control", "no-store");
    return res;
  }
  if (req.nextUrl.pathname === "/robots.txt") {
    return NextResponse.next();
  }
  const ua = (req.headers.get("user-agent") ?? "").toLowerCase();
  if (ua && AI_BOTS.some((b) => ua.includes(b))) {
    return new NextResponse("AI crawling is not permitted on this site.\n", {
      status: 403,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "x-robots-tag": "noai, noimageai, noindex",
      },
    });
  }
  const res = NextResponse.next();
  res.headers.set("x-robots-tag", "noai, noimageai");
  return res;
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};