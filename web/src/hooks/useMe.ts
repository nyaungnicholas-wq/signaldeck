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
    // api.get has no timeout, and a hung proxy (dead daemon) never answers.
    // After 8s, settle as "unknown": the Shell then shows the operator chrome,
    // as it did before this hook existed, instead of a blank page.
    const giveUp = setTimeout(() => {
      if (alive) setState((s) => (s.known ? s : { ...s, known: true }));
    }, 8000);
    api.me().then(
      (m) => {
        clearTimeout(giveUp);
        if (alive) setState({ me: m, known: true });
      },
      (e: unknown) => {
        clearTimeout(giveUp);
        // Only a 401 means signed out; any other failure keeps the last answer.
        if (alive) setState((s) => ({ me: isAuthError(e) ? null : s.me, known: true }));
      },
    );
    return () => {
      alive = false;
      clearTimeout(giveUp);
    };
  }, [pathname]);
  return state;
}

/** True only on the daemon's say-so (Me.member), never inferred from isAdmin. */
export function useIsMember(): { member: boolean; known: boolean } {
  const { me, known } = useMe();
  return { member: me?.member === true, known };
}
