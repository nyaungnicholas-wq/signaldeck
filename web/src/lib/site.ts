/**
 * The site's own absolute URL, for robots.txt, the sitemap and Open Graph.
 *
 * NEXT_PUBLIC_SITE_URL is inlined at BUILD time, not read at runtime. Setting
 * it in the container's environment does nothing; it has to be present when
 * `npm run build` runs, which for the image means a build arg. Verified the
 * hard way: passing it to `next start` produced a sitemap full of
 * localhost URLs that looked entirely correct.
 *
 * The fallback is localhost rather than a guessed production hostname,
 * because a sitemap advertising a domain this build is not served from is
 * worse than one advertising an obviously-local address: the first looks
 * right and would be indexed.
 */
export function siteUrl(): string {
  const raw = process.env.NEXT_PUBLIC_SITE_URL?.trim();
  if (!raw) return "http://localhost:8323";
  return raw.replace(/\/+$/, "");
}
