"use client";

// VOL REGIME LEAD — the platform's best-evidenced claim, given its own place.
//
// Everything else on this page is either descriptive (what moved) or an
// unvalidated read. The volatility regime is the one forecast that has been
// re-tested, independently re-implemented, and survived: it is the claim the
// rest of the product is built on top of, and until now it sat as one tile
// among many, indistinguishable from signals with far weaker support.
//
// What this card must NOT become is a buy button. A regime hit rate is not a
// return — the 2026-07-24 re-validation found the most ACCURATE trend band
// carries a NEGATIVE mean forward return — so the card leads with the measured
// accuracy of the specific conviction band each call falls in, and carries the
// tradeability caveat inline rather than behind a tooltip.

import { useEffect, useState } from "react";
import Link from "next/link";
import { volRegime, pollMs, POLL_SLOW, type VolRegime } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import HelpTip from "@/components/HelpTip";

const pct = (v: number) => `${(v * 100).toFixed(1)}%`;

export default function VolRegimeLead() {
  const [data, setData] = useState<VolRegime | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    const load = () =>
      volRegime()
        .then((d) => {
          if (!alive) return;
          setData(d);
          setFailed(false);
        })
        .catch(() => {
          if (alive) setFailed(true);
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, []);

  // A failed fetch renders nothing rather than an error block: this is one card
  // on a dashboard, and the rest of the page is still worth reading.
  if (failed) return null;
  if (!data) return <Skeleton lines={4} label="loading volatility regime" />;

  // Lead with the highest-conviction calls, because those are the ones whose
  // band accuracy is actually high — showing a low-conviction call first would
  // attach the headline number to the weakest evidence.
  const top = [...data.forecasts]
    .sort((a, b) => b.conviction - a.conviction)
    .slice(0, 6);

  return (
    <section className="panel" aria-label="volatility regime — the validated forecast">
      <div className="panel-h flex-wrap gap-2">
        <span>VOLATILITY REGIME</span>
        <span className="chip px-2 py-[1px] text-[0.75rem]">the validated forecast</span>
        <HelpTip label="Why this one leads">
          {data.whyHonest || data.what}
        </HelpTip>
        <span className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal">
          <Link href="/market/breadth" style={{ color: "var(--faint)" }}>
            market-wide →
          </Link>
        </span>
      </div>

      <div className="p-3">
        <p className="mb-3 text-[0.8rem] leading-relaxed">
          {data.what || "Will realized volatility over the coming window be elevated or calm?"}{" "}
          <span style={{ color: "var(--faint)" }}>
            This is the most re-tested claim on the platform — measured walk-forward, re-verified by
            a second implementation, and the only forecast the options surface is allowed to build
            on.
          </span>
        </p>

        {top.length === 0 ? (
          <p className="text-[0.8rem]" style={{ color: "var(--faint)" }}>
            No qualified volatility call right now. Thin history yields no forecast rather than a
            guess.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[26rem] text-[0.78rem]">
              <thead>
                <tr style={{ color: "var(--faint)" }}>
                  <th className="text-left font-normal">symbol</th>
                  <th className="text-left font-normal">call</th>
                  <th className="text-right font-normal">conviction</th>
                  <th className="text-right font-normal">
                    <span className="inline-flex items-center gap-1">
                      band accuracy
                      <HelpTip label="band accuracy">
                        The measured walk-forward hit rate of the conviction band THIS call falls
                        in — not the average over all calls.
                      </HelpTip>
                    </span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {top.map((f) => (
                  <tr key={f.symbol} style={{ borderTop: "1px solid var(--line)" }}>
                    <td className="py-1">
                      <Link href={`/s/${f.market}/${f.symbol}`} className="font-bold">
                        {f.symbol}
                      </Link>
                    </td>
                    <td className="py-1">{f.regime}</td>
                    <td className="py-1 text-right tnum">{f.conviction.toFixed(2)}</td>
                    <td className="py-1 text-right tnum">
                      {f.historicalAccuracy > 0 ? pct(f.historicalAccuracy) : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <p className="mt-3 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {data.tradeability ||
            data.caveat ||
            "A regime call is situational awareness with a measured hit rate, not a trade."}{" "}
          <Link href="/lab/options" style={{ textDecoration: "underline" }}>
            Turn it into a vol-edge assessment →
          </Link>
        </p>
      </div>
    </section>
  );
}
