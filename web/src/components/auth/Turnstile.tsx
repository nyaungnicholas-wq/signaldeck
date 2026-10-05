"use client";
import { useEffect, useRef } from "react";

declare global {
  interface Window {
    turnstile?: {
      render: (
        container: HTMLElement | Element,
        options: {
          sitekey: string;
          theme?: string;
          callback: (token: string) => void;
          "expired-callback"?: () => void;
          "error-callback"?: () => void;
        }
      ) => string;
      remove: (widgetId: string) => void;
    };
  }
}

let turnstileReadyPromise: Promise<void> | null = null;

function getTurnstileReady(): Promise<void> {
  if (!turnstileReadyPromise) {
    turnstileReadyPromise = (async () => {
      const script = document.createElement("script");
      script.src =
        "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
      script.async = true;
      await new Promise<void>((resolve, reject) => {
        script.onload = () => resolve();
        script.onerror = reject;
        document.head.appendChild(script);
      });
      let attempts = 0;
      const maxAttempts = 100; // 10s / 100ms
      while (!window.turnstile && attempts < maxAttempts) {
        await new Promise((r) => setTimeout(r, 100));
        attempts++;
      }
      if (!window.turnstile) {
        throw new Error("Turnstile not available");
      }
    })();
  }
  return turnstileReadyPromise;
}

export default function Turnstile({
  onToken,
}: {
  onToken: (token: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const widgetIdRef = useRef<string | null>(null);
  const liveRef = useRef<boolean>(false);

  useEffect(() => {
    liveRef.current = true;
    return () => {
      liveRef.current = false;
    };
  }, []);

  useEffect(() => {
    (async () => {
      try {
        const resp = await fetch("/api/health", { credentials: "include" });
        if (!resp.ok) throw new Error("Failed to fetch health");
        const data = await resp.json();
        const siteKey = data.turnstileSiteKey as string | undefined;
        if (!siteKey) {
          // No widget configured: the daemon skips the check when it has no
          // secret, but the forms disable submit on an EMPTY token — so
          // handing back "" made sign-up impossible for every visitor.
          if (liveRef.current) onToken("turnstile-off");
          return;
        }
        await getTurnstileReady();
        if (!liveRef.current) return;
        const container = containerRef.current;
        if (!container) return;
        if (widgetIdRef.current !== null) return;
        const widgetId = window.turnstile!.render(container, {
          sitekey: siteKey,
          theme: "dark",
          callback: (t: string) => {
            if (liveRef.current) onToken(t);
          },
          "expired-callback": () => {
            if (liveRef.current) onToken("");
          },
          "error-callback": () => {
            if (liveRef.current) onToken("");
          },
        });
        widgetIdRef.current = widgetId;
      } catch (err) {
        console.error("Turnstile init error", err);
        if (liveRef.current) onToken("");
      }
    })();
  }, []);

  useEffect(() => {
    return () => {
      liveRef.current = false;
      if (widgetIdRef.current !== null && window.turnstile) {
        try {
          window.turnstile.remove(widgetIdRef.current);
        } catch (e) {
          console.error("Error removing turnstile widget", e);
        }
      }
    };
  }, []);

  return <div ref={containerRef} className="mb-4 min-h-[65px]" />;
}