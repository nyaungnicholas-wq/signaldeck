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
  if (next === "/login" || next.startsWith("/login?") || next.startsWith("/login/")) return null;
  return next;
}
