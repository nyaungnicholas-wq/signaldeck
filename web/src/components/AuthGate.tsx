"use client";

import { usePathname, useRouter } from "next/navigation";
import { useEffect } from "react";
import { API_BASE } from "@/lib/api";

/**
 * Lightweight session check: asks the daemon who we are and redirects to
 * /login on 401. Renders nothing — pages are untouched; per-request 401s on
 * protected endpoints are still surfaced by their own error states.
 */
export default function AuthGate() {
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (pathname === "/login") return;
    let cancelled = false;
    fetch(`${API_BASE}/api/auth/me`, {
      cache: "no-store",
      credentials: "include",
      headers: { "X-Signaldeck": "1" },
    })
      .then((res) => {
        if (!cancelled && res.status === 401) router.replace("/login");
      })
      .catch(() => {
        /* daemon unreachable — let the page's own error state handle it */
      });
    return () => {
      cancelled = true;
    };
  }, [pathname, router]);

  return null;
}
