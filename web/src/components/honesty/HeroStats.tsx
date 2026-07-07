"use client";

import type { Honesty, Horizon } from "@/lib/api";
import { fmtPct } from "@/lib/format";
// Stage-1 translation layer: IC/spread wording now comes from the central
// plain-English dictionary so every page says it the same way.
import { metricLabel, readMetric } from "@/lib/plain";
import { useViewMode } from "@/components/Plain";
// Stage 4 (tables→charts): the big grade meters render the SAME central-
// dictionary readings as the stat cards — bar and sentence can't disagree.
import GradeMeter from "@/components/viz/GradeMeter";

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

  const mode = useViewMode();
  // Central-dictionary readings — gated/null inputs degrade to "no read yet".
  const gateRead = readMetric("sample_gate", n, { minN });
  const icRead = readMetric("ic", gated ? null : ic, { gated, n });
  const spreadRead = readMetric("quintile_spread", spreadOk ? spread : null, { n });

  return (
    <div className="flex flex-col gap-4">
    <div className="grid gap-4 sm:grid-cols-3">
      <Stat
        label={mode === "simple" ? "EVIDENCE COLLECTED" : "INDEPENDENT RESOLUTIONS"}
        title="One observation per (symbol, UTC-day). Minute-cadence rows that all resolve against the same daily move are collapsed, so this is the effective independent sample."
        value={n.toLocaleString("en-US")}
        sub={
          n < minN
            ? gateRead.plain
            : rawN > n
              ? `${rawN.toLocaleString("en-US")} raw rows → ${n.toLocaleString("en-US")} independent (symbol, UTC-day) at ${horizon}`
              : `independent score → outcome pairs at the ${horizon} horizon`
        }
      />
      <Stat
        label={mode === "simple" ? metricLabel("ic", "simple").toUpperCase() : "IC · SCORE ↔ FWD RETURN"}
        title={icRead.detail}
        value={gated ? "—" : n && Number.isFinite(ic) ? ic.toFixed(3) : "—"}
        valueColor={
          gated || !Number.isFinite(ic) || Math.abs(ic) < 0.02
            ? "var(--dim)"
            : ic > 0
              ? "var(--bid)"
              : "var(--ask)"
        }
        sub={
          gated
            ? `insufficient independent resolutions (${n}/${minN}) — no signal-quality read until the sample is large enough`
            : icRead.plain
        }
      />
      <Stat
        label={mode === "simple" ? metricLabel("quintile_spread", "simple").toUpperCase() : "TOP − BOTTOM BUCKET"}
        title={spreadRead.detail}
        value={spreadOk ? fmtPct(spread * 100) : "—"}
        valueColor={
          spreadOk ? (spread > 0 ? "var(--bid)" : spread < 0 ? "var(--ask)" : "var(--dim)") : "var(--dim)"
        }
        sub={
          spreadOk
            ? mode === "simple"
              ? spreadRead.plain
              : `${last?.label ?? "strong buy"} minus ${first?.label ?? "strong sell"} mean forward return`
            : "needs outcomes in both extreme buckets"
        }
      />
    </div>

    {/* Stage 4 (tables→charts): the REPORT CARD — the same two readings as
        big centered grade meters. Gated/withheld inputs render an EMPTY
        meter with the honest "no read yet" sentence, never a fake fill. */}
    <section className="panel">
      <div className="panel-h">
        REPORT CARD
        <span className="ml-auto text-[0.66rem] font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
          bar and sentence come from the same reading — a withheld number stays an empty bar
        </span>
      </div>
      <div className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-2">
        <GradeMeter label={metricLabel("ic", mode)} reading={icRead} />
        <GradeMeter label={metricLabel("quintile_spread", mode)} reading={spreadRead} />
      </div>
    </section>
    </div>
  );
}
