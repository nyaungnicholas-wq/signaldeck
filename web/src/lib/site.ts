/**
 * The site's own absolute URL, for robots.txt, the sitemap and Open Graph.
 *
 * NEXT_PUBLIC_SITE_URL is inlined at BUILD time, not read at runtime. Setting
 * it in the container's environment does nothing; it has to be present when
 * `npm run build` runs, which for the image means a build arg. Verified the
 * hard way: passing it to `next start` produced a sitemap full of
 * localhost URLs that looked entirely correct.
 *
 * Unset, this is null and the build emits NO absolute URL: no metadataBase,
 * no og:image, an empty sitemap and no Sitemap line in robots.txt. It used to
 * fall back to http://localhost:8323, and the live build (variable unset)
 * shipped localhost in og:image, twitter:image and every sitemap <loc> (B6,
 * 2026-10-02): nothing a crawler or a link preview can use. A guessed
 * production hostname would be worse still, because it looks right and would
 * be indexed.
 */
export function siteUrl(): string | null {
  const raw = process.env.NEXT_PUBLIC_SITE_URL?.trim();
  if (!raw) return null;
  return raw.replace(/\/+$/, "");
}

/** The social card (app/og-image/route.tsx), attached by layout.tsx only when siteUrl() is set. */
export const OG_IMAGE = {
  url: "/og-image",
  width: 1200,
  height: 630,
  alt: "SignalDeck - a market instrument that grades itself in public",
  type: "image/png",
};
