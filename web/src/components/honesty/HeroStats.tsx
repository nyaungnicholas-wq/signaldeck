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
  title,
}: {
  label: string;
  value: string;
  valueColor?: string;
  sub: string;
  title?: string;
}) {
  return (
    <section className="panel">
      <div className="panel-h" title={title}>{label}</div>
      <div className="px-4 py-3">
        <div className="tnum text-2xl font-bold" style={{ color: valueColor ?? "var(--text)" }}>
          {value}
        </div>
        <div className="mt-1 text-[0.78rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {sub}
        </div>
      </div>
    </section>
  );
}

/** Hero row: resolved sample size, IC with plain-English verdict, bucket spread. */
export default function HeroStats({ data, horizon }: { data: Honesty; horizon: Horizon }) {
  // Independent (symbol, UTC-day) count is the REAL sample size; raw minute
  // rows over-count. Fall back to n for older payloads.
  const n = data.independentN ?? data.n ?? 0;
  const rawN = data.rawN ?? n;
  const minN = data.minIndependentN ?? 30;
  // IC is withheld (null) below the independence gate — don't invent a number.
  const gated = data.icGated === true || data.ic === null || data.ic === undefined;
  const ic = typeof data.ic === "number" ? data.ic : NaN;
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
        label="INDEPENDENT RESOLUTIONS"
        title="One observation per (symbol, UTC-day). Minute-cadence rows that all resolve against the same daily move are collapsed, so this is the effective independent sample."
        value={n.toLocaleString("en-US")}
        sub={
          rawN > n
            ? `${rawN.toLocaleString("en-US")} raw rows → ${n.toLocaleString("en-US")} independent (symbol, UTC-day) at ${horizon}`
            : `independent score → outcome pairs at the ${horizon} horizon`
        }
      />
      <Stat
        label="IC · SCORE ↔ FWD RETURN"
        title="Information coefficient — correlation between score and forward return over the INDEPENDENT set (+1 perfect, 0 no information). Withheld below the minimum independent sample."
        value={gated ? "—" : n && Number.isFinite(ic) ? ic.toFixed(3) : "—"}
        valueColor={gated ? "var(--dim)" : icColor(ic, n)}
        sub={
          gated
            ? `insufficient independent resolutions (${n}/${minN}) — no IC until the sample is large enough`
            : icLine(ic, n)
        }
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
