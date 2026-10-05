import type { Metadata } from "next";

// The PAGE is admin-only — the daemon answers /api/hud with 401 anonymously and
// 403 to a non-admin session. Its METADATA was not: Next serves <head> to anyone
// who requests the URL, so an anonymous visitor was told there is a live view of
// a named strategy's brokerage account, and exactly which fields it carries.
//
// No values ever leaked and the account is a PAPER one, so this is the shape of
// the disclosure rather than its severity. But an admin page has no reason to
// describe its contents to someone who cannot open it, and this description was
// written as though the audience were a reader of the page rather than a
// stranger probing the URL.
//
// robots noindex/nofollow for the same reason: nothing behind an admin gate
// should be inviting a crawler. The public surface is the seven routes in
// lib/publicRoutes, and this is not one of them.
export const metadata: Metadata = {
  title: "HUD",
  description: "Operator view. Requires an administrator session.",
  robots: { index: false, follow: false },
};

export default function HudLayout({ children }: { children: React.ReactNode }) {
  return children;
}
