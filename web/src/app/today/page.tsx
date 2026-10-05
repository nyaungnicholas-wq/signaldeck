"use client";

// TODAY: the MEMBER home. Built only on routes the daemon's member tier serves:
// the validated regime and volatility reads (each with the accuracy it has
// measured), the live track record that grades them, and the member's own
// watchlist. No price, change or volume appears here; those are licensed vendor
// data SignalDeck does not redistribute. The operator's home stays /dashboard.

import Link from "next/link";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import TodaysRead from "@/components/home/TodaysRead";
import VolRegimeLead from "@/components/home/VolRegimeLead";
import RiskFirst from "@/components/home/RiskFirst";
import ProofStrip from "@/components/home/ProofStrip";
import { WHAT_SIGNALDECK_IS } from "@/lib/hypothetical";

const MORE = [
  { href: "/market/regimes", label: "All regime reads" },
  { href: "/market/breadth", label: "Market breadth" },
  { href: "/watchlist", label: "Your watchlist" },
  { href: "/accuracy", label: "The graded record" },
  { href: "/proof", label: "Check the receipts yourself" },
];

export default function TodayPage() {
  // undefined until the watchlist answers, so TodaysRead ranks the member's
  // own symbols first from its very first render.
  const [watch, setWatch] = useState<string[] | undefined>(undefined);
  useEffect(() => {
    let alive = true;
    api.memberWatchlist().then(
      (rows) => {
        if (alive) setWatch(rows.map((r) => r.symbol));
      },
      () => {
        if (alive) setWatch([]);
      },
    );
    return () => {
      alive = false;
    };
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <header className="panel px-4 py-3">
        <h1 className="m-0 text-lg font-bold">Today</h1>
        <p className="m-0 mt-1 max-w-[75ch] text-[0.85rem]" style={{ color: "var(--dim)" }}>
          SignalDeck&rsquo;s regime reads, each with the accuracy it measured in backtests, and the
          live record that grades every call. What it cannot predict, it says so.
        </p>
        <p className="m-0 mt-1 max-w-[75ch] text-[0.8rem]" style={{ color: "var(--faint)" }}>
          {WHAT_SIGNALDECK_IS}
        </p>
      </header>
      {/* Risk first (plan step 9): leads only when the live volatility verdict passes. */}
      {watch !== undefined && <RiskFirst watchSymbols={watch} />}
      {watch !== undefined && <TodaysRead watchSymbols={watch} />}
      <VolRegimeLead memberView />
      <ProofStrip />
      <nav aria-label="More from SignalDeck" className="panel flex flex-wrap gap-2 px-4 py-3 text-[0.85rem]">
        {MORE.map((m) => (
          <Link key={m.href} href={m.href} className="chip flex min-h-[40px] items-center px-3">
            {m.label}
          </Link>
        ))}
      </nav>
    </div>
  );
}
