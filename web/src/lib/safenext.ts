// safeNext returns the path to resume after sign-in, or null. Only a
// same-origin path is accepted: "/x" yes; "//evil.example", "/\evil",
// "https://…", "javascript:…" and anything with a control character no.
// AuthGate puts the page a signed-out visitor asked for in ?next=, so a
// deep link (/watchlist, /s/stocks/AAPL) survives the trip through /login
// instead of always landing on /today (2026-10-05 audit).
export function safeNext(next: string | null | undefined): string | null {
  if (!next || next.length > 512) return null;
  if (!next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return null;
  if (/[\u0000-\u001f\u007f]/.test(next)) return null;
  // Resolve it the way a browser would: dot segments and backslashes can turn
  // "/..//evil" into a path that starts "//", which a later
  // location.replace() reads as another host (2026-10-05 review). Judge the
  // RESOLVED path and return that, never the raw input.
  const base = "https://same-origin.invalid";
  let u: URL;
  try {
    u = new URL(next, base);
  } catch {
    return null;
  }
  if (u.origin !== base) return null;
  const path = u.pathname + u.search + u.hash;
  if (path.startsWith("//") || path.startsWith("/\\")) return null;
  if (u.pathname === "/login" || u.pathname.startsWith("/login/")) return null;
  return path;
}
