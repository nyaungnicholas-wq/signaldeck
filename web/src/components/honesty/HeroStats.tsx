"use client";

import type { Honesty, Horizon } from "@/lib/api";
import { fmtPct } from "@/lib/format";

/** Plain-English read of the information coefficient. */
function icLine(ic: number, n: number): string {
  if (!n || !Number.isFinite(ic)) return "no resolved outcomes yet";
  const a = Math.abs(ic);
  let s: string;
  if (a < 0.03) s = "no measurable edge yet";
  else if (a < 0.1) s = "weak signal";
  else s = "meaningful signal";
  if (ic < 0 && a >= 0.03) s += " — inverted";
  if (n < 100) s += " (sample still small — keep collecting)";
  return s;
}

function icColor(ic: number, n: number): string {
  if (!n || !Number.isFinite(ic) || Math.abs(ic) < 0.03) return "var(--dim)";
  return ic > 0 ? "var(--bid)" : "var(--ask)";
}

function Stat({
  label,
  value,
  valueColor,
  sub,
}: {
  label: string;
  value: string;
  valueColor?: string;
  sub: string;
}) {
  return (
    <section className="panel">
      <div className="panel-h">{label}</div>
      <div className="px-4 py-3">
        <div className="tnum text-2xl font-bold" style={{ color: valueColor ?? "var(--text)" }}>
          {value}
        </div>
        <div className="mt-1 text-[0.7rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {sub}
        </div>
      </div>
    </section>
  );
}

/** Hero row: resolved sample size, IC with plain-English verdict, bucket spread. */
export default function HeroStats({ data, horizon }: { data: Honesty; horizon: Horizon }) {
  const n = data.n ?? 0;
  const ic = data.ic;
  const buckets = data.buckets ?? [];
  const first = buckets[0];
  const last = buckets[buckets.length - 1];
  const spreadOk =
    buckets.length >= 2 &&
    !!first &&
    !!last &&
    (first.n ?? 0) > 0 &&
    (last.n ?? 0) > 0 &&
    Number.isFinite(first.meanFwd) &&
    Number.isFinite(last.meanFwd);
  const spread = spreadOk ? last.meanFwd - first.meanFwd : NaN;

  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <Stat
        label="RESOLVED OUTCOMES"
        value={n.toLocaleString("en-US")}
        sub={`score → outcome pairs resolved at the ${horizon} horizon`}
      />
      <Stat
        label="IC · SCORE ↔ FWD RETURN"
        value={n && Number.isFinite(ic) ? ic.toFixed(3) : "—"}
        valueColor={icColor(ic, n)}
        sub={icLine(ic, n)}
      />
      <Stat
        label="TOP − BOTTOM BUCKET"
        value={spreadOk ? fmtPct(spread * 100) : "—"}
        valueColor={
          spreadOk ? (spread > 0 ? "var(--bid)" : spread < 0 ? "var(--ask)" : "var(--dim)") : "var(--dim)"
        }
        sub={
          spreadOk
            ? `${last?.label ?? "strong buy"} minus ${first?.label ?? "strong sell"} mean forward return`
            : "needs outcomes in both extreme buckets"
        }
      />
    </div>
  );
}
