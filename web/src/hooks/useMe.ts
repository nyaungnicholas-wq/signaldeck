"use client";

import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api, isAuthError, type Me } from "@/lib/api";

export type MeState = {
  /** undefined = not known yet; null = signed out. */
  me: Me | null | undefined;
  /** True once the first check settled either way, so a daemon outage cannot
   *  hold a page in "unknown" forever. */
  known: boolean;
};

/** Who is signed in, re-checked on every navigation (login and logout change
 *  it). api.get dedupes and briefly caches, so several callers cost one fetch. */
export function useMe(): MeState {
  const pathname = usePathname();
  const [state, setState] = useState<MeState>({ me: undefined, known: false });
  useEffect(() => {
    let alive = true;
    api.me().then(
      (m) => {
        if (alive) setState({ me: m, known: true });
      },
      (e: unknown) => {
        // Only a 401 means signed out; any other failure keeps the last answer.
        if (alive) setState((s) => ({ me: isAuthError(e) ? null : s.me, known: true }));
      },
    );
    return () => {
      alive = false;
    };
  }, [pathname]);
  return state;
}

/** True only on the daemon's say-so (Me.member), never inferred from isAdmin. */
export function useIsMember(): { member: boolean; known: boolean } {
  const { me, known } = useMe();
  return { member: me?.member === true, known };
}
