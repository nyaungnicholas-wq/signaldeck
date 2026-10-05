"use client";

// useSnapStream — subscribe to the daemon's live microstructure push.
//
// Opens an EventSource to /api/stream/snaps and returns the newest snapshot,
// updated the instant the server pushes one (1 Hz, faster if the tick is
// lowered). This is what lets a view "keep up" with the live feed without
// hammering REST on a timer.
//
// Graceful fallback: if the stream endpoint is unavailable (e.g. an older
// daemon that predates it), the browser's auto-reconnect is capped after a few
// failures and the hook goes dormant — callers keep their existing poll as the
// safety net, so nothing regresses.

import { useEffect, useState } from "react";
import { snapStreamUrl, type Market, type Snap } from "@/lib/api";

const MAX_RECONNECTS = 4;

export function useSnapStream(
  symbol: string,
  market: Market,
  ms = 1000,
): { snap: Snap | null; connected: boolean } {
  const [snap, setSnap] = useState<Snap | null>(null);
  const [connected, setConnected] = useState(false);

  // Reset when the subscription target changes so a stale symbol's last
  // snapshot never leaks into a different symbol's view (guarded adjustment
  // during render — the effect below then reconnects with clean state).
  const target = `${market}:${symbol}:${ms}`;
  const [prevTarget, setPrevTarget] = useState(target);
  if (prevTarget !== target) {
    setPrevTarget(target);
    setSnap(null);
    setConnected(false);
  }

  useEffect(() => {
    if (typeof window === "undefined" || typeof EventSource === "undefined") return;

    let es: EventSource | null = null;
    let fails = 0;

    const open = () => {
      es = new EventSource(snapStreamUrl(symbol, market, ms));

      es.addEventListener("snap", (e) => {
        fails = 0;
        setConnected(true);
        try {
          setSnap(JSON.parse((e as MessageEvent).data) as Snap);
        } catch {
          // Ignore a malformed frame; the next tick supersedes it.
        }
      });

      es.onopen = () => {
        fails = 0;
        setConnected(true);
      };

      es.onerror = () => {
        setConnected(false);
        // EventSource reconnects on its own; cap it so an unavailable endpoint
        // does not become a reconnect storm — let the caller's poll take over.
        fails += 1;
        if (fails >= MAX_RECONNECTS && es) {
          es.close();
          es = null;
        }
      };
    };

    open();
    return () => {
      es?.close();
      es = null;
    };
  }, [symbol, market, ms]);

  return { snap, connected };
}
