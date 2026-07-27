"use client";

import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { API_BASE } from "@/lib/api";

/**
 * Session gate for the private workspace. Every route except /login requires a
 * signed-in session: we ask the daemon who we are and, on 401, redirect to
 * /login.
 *
 * Crucially this GATES rendering — the app chrome + page do not paint until the
 * session is confirmed, so an anonymous visitor never sees a flash of the
 * dashboard (or a nav full of links that immediately bounce) before the
 * redirect. A daemon *outage* is deliberately NOT treated as logged-out: we let
 * the app render and each panel's own error state explains the outage, because
 * trapping the whole app behind a spinner when the daemon is merely down would
 * be worse than showing it with empty panels.
 */
export default function AuthGate({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  // `ready` tracks whether a real session was confirmed this mount. /login is
  // the only public route and is rendered regardless of `ready` (see below), so
  // the effect never needs to setState synchronously for it. Once established,
  // `ready` stays true, so navigating between protected pages never re-flashes.
  const [ready, setReady] = useState(false);
  // Public routes render without a session: /login (the sign-in), /proof
  // (the shareable public track-record + ledger page) and /accuracy (the
  // registry verdicts — a FAILED grade gated behind a login is a FAILED grade
  // hidden). Everything else gates.
  const isPublic = pathname === "/login" || pathname === "/proof" || pathname === "/accuracy";
  const onLogin = isPublic;

  useEffect(() => {
    if (onLogin || ready) return;
    let cancelled = false;
    fetch(`${API_BASE}/api/auth/me`, {
      cache: "no-store",
      credentials: "include",
      headers: { "X-Signaldeck": "1" },
    })
      .then((res) => {
        if (cancelled) return;
        if (res.status === 401)
          router.replace("/login"); // stay gated until /login paints — no flash
        else setReady(true); // signed in, or daemon error (panels surface it)
      })
      .catch(() => {
        if (!cancelled) setReady(true); // daemon unreachable — don't trap the app
      });
    return () => {
      cancelled = true;
    };
  }, [onLogin, ready, router]);

  if (!ready && !onLogin) {
    return (
      <div
        role="status"
        aria-live="polite"
        className="flex min-h-screen items-center justify-center text-[0.75rem] tracking-widest"
        style={{ color: "var(--faint)" }}
      >
        checking session…
      </div>
    );
  }
  return <>{children}</>;
}
