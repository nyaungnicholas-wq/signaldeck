"use client";

// Congressional-activity chip (Signal8 wave, Stage 2): shown on a stock's
// symbol page ONLY when members of Congress disclosed trades in this ticker
// within the last 90 days. Renders nothing otherwise — absence of the chip is
// honest absence of activity (or of data; the /congress page carries the full
// mirror-health story). The legal 30-45d disclosure lag is stated on the chip.

import { useEffect, useState } from "react";
import Link from "next/link";
import { congress } from "@/lib/api";

export default function CongressChip({ symbol }: { symbol: string }) {
  const [recent, setRecent] = useState(0);

  useEffect(() => {
    let alive = true;
    // limit=1: the chip only needs the recent90d aggregate the API computes.
    congress(symbol, undefined, undefined, 1)
      .then((r) => {
        if (!alive) return;
        setRecent(r.recent90d ?? 0);
      })
      .catch(() => {
        if (!alive) return;
        setRecent(0); // no chip on error — never fabricate activity
      });
    return () => {
      alive = false;
    };
  }, [symbol]);

  if (recent <= 0) return null;

  return (
    <Link
      href="/congress"
      className="chip inline-flex min-h-[36px] items-center gap-2 px-3 hover:underline"
      style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
      title="Congressional stock disclosures lag 30-45 days by law — never real-time."
    >
      <span className="font-bold tracking-[0.08em]">CONGRESS</span>
      <span className="tnum">
        {recent} trade{recent === 1 ? "" : "s"} disclosed · last 90d
      </span>
      <span style={{ color: "var(--faint)" }}>(lags 30–45d by law)</span>
    </Link>
  );
}
