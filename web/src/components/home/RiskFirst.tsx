"use client";

// RISK FIRST (plan step 9). The member's /today headline is decided by the
// live graded verdict of the HAR volatility forecast and nothing else
// (lib/riskHeadline). PASS: this block leads the page with the graded next-day
// result and today's forecast for the member's watched symbols; only the
// next-day forecast is graded, so the next-week column says it is not. INSUFFICIENT /
// ACCRUING: a compact progress strip. FAIL: an honest strip, no promotion.

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, type VolForecastLatest } from "@/lib/api";
import { riskHeadline, type RiskHeadline } from "@/lib/riskHeadline";
import HypotheticalNote from "@/components/HypotheticalNote";

const LABEL: Record<number, string> = { 1: "next day", 5: "next week" };

export default function RiskFirst({ watchSymbols }: { watchSymbols: string[] }) {
  const [head, setHead] = useState<RiskHeadline | null>(null);
  const [latest, setLatest] = useState<VolForecastLatest | null>(null);

  useEffect(() => {
    let alive = true;
    api.volForecastRecord().then(
      (rec) => {
        if (!alive) return;
        const h = riskHeadline(rec);
        setHead(h);
        // The per-symbol numbers are shown only once the forecast has passed.
        if (h.lead) api.volForecastLatest().then((l) => alive && setLatest(l), () => {});
      },
      () => alive && setHead(riskHeadline(undefined)),
    );
    return () => {
      alive = false;
    };
  }, []);

  if (!head) return null;

  if (!head.lead) {
    return (
      <section
        className="panel px-4 py-2 text-[0.8rem]"
        aria-label="volatility forecast live test"
        data-risk-state={head.state}
        style={{ color: head.state === "fail" ? "var(--warn)" : "var(--dim)" }}
      >
        <p className="m-0 max-w-[90ch]">
          {head.text}{" "}
          {head.state !== "unavailable" && (
            <Link href="/volatility" className="underline">
              The live record
            </Link>
          )}
        </p>
      </section>
    );
  }

  const watched = new Set(watchSymbols);
  const rows = (latest?.forecasts ?? []).filter((f) => watched.has(f.symbol));
  const bySymbol = new Map<string, { asOf: number; vol: Record<number, number> }>();
  for (const f of rows) {
    const e = bySymbol.get(f.symbol) ?? { asOf: f.asOf, vol: {} };
    e.vol[f.horizon] = f.volPct;
    bySymbol.set(f.symbol, e);
  }

  return (
    <section className="panel flex flex-col gap-2 px-4 py-3" aria-label="risk first" data-risk-state="pass">
      <h2 className="m-0 text-base font-bold">Risk first: volatility forecast</h2>
      <p className="m-0 max-w-[80ch] text-[0.85rem]">{head.text}</p>
      {bySymbol.size > 0 ? (
        <table className="text-[0.8rem]">
          <thead>
            <tr style={{ color: "var(--faint)" }}>
              <th className="pr-4 text-left font-normal">Your symbols</th>
              <th className="pr-4 text-right font-normal">Next day</th>
              <th className="pr-4 text-right font-normal">Next week (not graded)</th>
              <th className="text-left font-normal">As of</th>
            </tr>
          </thead>
          <tbody>
            {[...bySymbol].map(([sym, e]) => (
              <tr key={sym}>
                <td className="mono pr-4">{sym}</td>
                {[1, 5].map((h) => (
                  <td key={h} className="tnum pr-4 text-right" aria-label={LABEL[h]}>
                    {e.vol[h] !== undefined ? `${e.vol[h].toFixed(1)}%` : "—"}
                  </td>
                ))}
                <td className="tnum" style={{ color: "var(--faint)" }}>
                  {new Date(e.asOf * 1000).toISOString().slice(0, 10)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="m-0 text-[0.8rem]" style={{ color: "var(--faint)" }}>
          None of your watched symbols has a current volatility forecast.
        </p>
      )}
      <p className="m-0 text-[0.72rem]" style={{ color: "var(--faint)" }}>
        Annualised realized volatility, forecast. A risk number, not a price direction. Only the
        next-day forecast is graded; the next-week figure is shown ungraded.{" "}
        <Link href="/volatility" className="underline">
          The live record
        </Link>
      </p>
      <HypotheticalNote live />
    </section>
  );
}
