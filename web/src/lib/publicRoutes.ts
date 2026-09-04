/**
 * The routes an anonymous visitor may see.
 *
 * ONE list. It used to be spelled twice -- once in AuthGate (which decides
 * whether to gate) and once in Shell (which decides whether to draw the app
 * chrome) -- and two copies of a security-adjacent list is how they drift.
 *
 * This is the BROWSER's half of the decision and it is not the authority. The
 * daemon's api.publicRoutes allowlist is, and it answers independently on
 * every request. Two gates that must both be wrong before anything leaks is
 * the arrangement worth having: a routing mistake here cannot expose data the
 * daemon still refuses to serve.
 *
 * Keep this in step with daemon/internal/api/security.go's publicRoutes when
 * adding a public page.
 */
export const PUBLIC_ROUTES = [
  "/", // the landing page — the front door
  "/accuracy", // the registry verdicts. A FAILED grade behind a login is a FAILED grade hidden.
  "/proof", // the shareable hash-chained receipts
  // NOT "/risk": next.config.ts already redirects that to /lab/risk for old
  // bookmarks, so a page there would be unreachable. Same trap as /deck.
  "/volatility", // the volatility estimates and their live record
  "/glossary", // plain-English terms; static, no daemon call
  "/login",
] as const;

export function isPublicRoute(pathname: string): boolean {
  return (PUBLIC_ROUTES as readonly string[]).includes(pathname);
}
