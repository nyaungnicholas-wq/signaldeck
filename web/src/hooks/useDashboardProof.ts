"use client";

// Track-record fetch for the dashboard's PROOF IT WORKS strip — extracted
// from the old page.tsx ProofStrip (pure refactor). The record moves on
// daily cadence, so it runs on the POLL_SLOW tier via the managed pollMs()
// loop (hidden-tab pause, failure backoff, freshness-retry re-fire).

import { useEffect, useState } from "react";
import { pollMs, POLL_SLOW, trackRecordWithGate, untilWarm, type TrackRecordWithGate } from "@/lib/api";

export default function useDashboardProof(): {
  tr: TrackRecordWithGate | null;
  err: string | null;
} {
  const [tr, setTr] = useState<TrackRecordWithGate | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    // A daemon that just restarted answers 503 "warming" until the record is
    // built: keep the loading state (never an error) and ask again after its
    // Retry-After, for up to 3 minutes.
    const load = () =>
      untilWarm(() => trackRecordWithGate("1d"), undefined, () => alive)
        .then((d) => {
          if (!alive) return;
          setTr(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, []);

  return { tr, err };
}
