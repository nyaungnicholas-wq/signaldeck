"use client";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";

declare global {
  interface Window {
    google?: { accounts: { id: GsiId } };
  }
}

type GsiId = {
  initialize: (o: {
    client_id: string;
    callback: (r: { credential: string }) => void;
    ux_mode?: "popup";
    auto_select?: boolean;
    cancel_on_tap_outside?: boolean;
  }) => void;
  renderButton: (
    el: HTMLElement,
    o: {
      theme?: "outline" | "filled_blue" | "filled_black";
      size?: "large" | "medium" | "small";
      shape?: "rectangular" | "pill";
      text?: "signin_with" | "signup_with" | "continue_with";
      width?: number;
      logo_alignment?: "left" | "center";
    }
  ) => void;
};

// One script per page; a failed load clears the promise so a remount retries.
let gsiPromise: Promise<void> | null = null;

// Google keeps ONE callback per page, so initialize once per client ID and route
// each token to whichever button is mounted now. None mounted = the token is
// dropped: a popup finished after leaving the page signs nobody in.
let initializedFor: string | null = null;
let onCredential: ((credential: string) => void) | null = null;
function loadGsi(): Promise<void> {
  if (gsiPromise) return gsiPromise;
  gsiPromise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "https://accounts.google.com/gsi/client";
    script.async = true;
    script.onload = () => {
      let attempts = 0;
      const interval = setInterval(() => {
        if (window.google?.accounts?.id) {
          clearInterval(interval);
          resolve();
        } else if (++attempts >= 50) {
          clearInterval(interval);
          gsiPromise = null;
          reject(new Error("Google sign-in did not load"));
        }
      }, 100);
    };
    script.onerror = () => {
      gsiPromise = null;
      reject(new Error("Google sign-in did not load"));
    };
    document.head.appendChild(script);
  });
  return gsiPromise;
}

/**
 * Renders Google's own "Sign in with Google" button (Google Identity Services,
 * popup mode), and nothing at all unless /api/health reports a googleClientId.
 * The ID token Google returns is posted to /api/auth/google, which verifies it
 * and signs the visitor in, creating a member account on first use. Errors go
 * to onError.
 */
export default function GoogleButton({
  text = "continue_with",
  onError,
}: {
  text?: "signin_with" | "signup_with" | "continue_with";
  onError?: (message: string) => void;
}) {
  const router = useRouter();
  const boxRef = useRef<HTMLDivElement>(null);
  // A ref, so a parent passing a new inline onError never re-runs the setup.
  const onErrorRef = useRef(onError);
  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  const [clientId, setClientId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // A second popup while the first token is being exchanged would post twice.
  const busyRef = useRef(false);

  useEffect(() => {
    let live = true;
    fetch("/api/health")
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (live && typeof data?.googleClientId === "string" && data.googleClientId !== "") {
          setClientId(data.googleClientId);
        }
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (!clientId) return;
    let live = true;
    const handle = async (credential: string) => {
      if (busyRef.current) return;
      busyRef.current = true;
      setBusy(true);
      try {
        const me = await api.googleSignIn(credential);
        router.replace((me.member ?? !me.isAdmin) ? "/today" : "/dashboard");
      } catch (err) {
        busyRef.current = false;
        setBusy(false);
        onErrorRef.current?.(
          err instanceof ApiError ? err.message : "Google sign-in failed — try again"
        );
      }
    };
    loadGsi()
      .then(() => {
        if (!live) return;
        const el = boxRef.current;
        const id = window.google?.accounts?.id;
        if (!el || !id) return;
        if (initializedFor !== clientId) {
          id.initialize({
            client_id: clientId,
            ux_mode: "popup",
            auto_select: false,
            cancel_on_tap_outside: true,
            callback: ({ credential }) => onCredential?.(credential),
          });
          initializedFor = clientId;
        }
        onCredential = handle;
        el.innerHTML = "";
        // Google draws a fixed-width button; fit it to the panel (200-400px).
        const width = Math.max(200, Math.min(400, Math.floor(el.getBoundingClientRect().width || 320)));
        id.renderButton(el, {
          theme: "filled_black",
          size: "large",
          shape: "pill",
          text,
          width,
          logo_alignment: "left",
        });
      })
      .catch(() => {
        if (live) {
          onErrorRef.current?.("Google sign-in couldn't load — check your connection, or use the form below");
        }
      });
    return () => {
      live = false;
      if (onCredential === handle) onCredential = null;
    };
  }, [clientId, text, router]);

  if (!clientId) return null;
  return (
    <div className="mb-2">
      <div
        ref={boxRef}
        className={`flex min-h-[44px] w-full justify-center${busy ? " pointer-events-none opacity-50" : ""}`}
        aria-busy={busy}
      />
      {busy && (
        <p role="status" className="mt-2 text-center text-xs text-[var(--dim)]">
          Signing you in…
        </p>
      )}
      <div
        className="my-4 flex items-center gap-3 text-[10px] tracking-[0.2em] text-[var(--faint)]"
        aria-hidden="true"
      >
        <span className="h-px flex-1 bg-[var(--border)]" />
        OR
        <span className="h-px flex-1 bg-[var(--border)]" />
      </div>
    </div>
  );
}
