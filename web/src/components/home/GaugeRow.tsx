"use client";

// MARKET GAUGES — extracted from the old monolithic page.tsx. SIMPLE mode is
// now genuinely simple: a plain-English one-liner per gauge (each keeping its
// API gate caption VERBATIM — plain language supplements the caveats, never
// replaces them) with the technical dials demoted behind a <ProOnly>
// "Show methodology" disclosure. PRO mode renders the dials directly.

import { fmtPct } from "@/lib/format";
import { confidenceWord, readMetric } from "@/lib/plain";
import Plain, { useViewMode } from "@/components/Plain";
import Gauge from "@/components/viz/Gauge";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";
import { regimesGauge, type DashboardResponse } from "@/lib/api";
import { changeColor } from "@/components/home/helpers";

/** Count tile matching the Gauge footprint — a dial needs a bounded scale and
 *  a 24h anomaly count has none, so the honest shape is a plain number. */
function StatTile({
  value,
  label,
  sub,
  caption,
}: {
  value: string;
  label: string;
  sub: string;
  caption: string;
}) {
  return (
    <figure
      className="m-0 flex flex-col items-center"
      style={{ width: 168 }}
      role="img"
      aria-label={`${label}: ${value} ${sub}. ${caption}`}
    >
      <div className="flex h-[99px] flex-col items-center justify-center gap-0.5">
        <span className="tnum text-[1.7rem] font-bold leading-none">{value}</span>
        <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
          {sub}
        </span>
      </div>
      <figcaption className="flex flex-col items-center gap-0.5 text-center">
        <span className="text-[0.75rem] font-medium tracking-[0.14em]" style={{ color: "var(--dim)" }}>
          {label}
        </span>
        <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
          {caption}
        </span>
      </figcaption>
    </figure>
  );
}

function vixRegimeColor(regime?: string): string {
  if (regime === "calm") return "var(--bid)";
  if (regime === "elevated") return "var(--warn)";
  if (regime === "stressed") return "var(--ask)";
  return "var(--dim)";
}

/** SIMPLE-mode summary: one plain sentence per gauge + its gate caption
 *  verbatim — the honesty caveats stay visible even with the dials folded. */
function SimpleGaugeSummary({ g }: { g: DashboardResponse["gauges"] }) {
  const vixRead = g.vix.hasData ? readMetric("vix", g.vix.level ?? null) : null;
  const rows: { label: string; text: string; caption: string; tip?: React.ReactNode }[] = [
    {
      label: "BREADTH",
      text: g.breadth.hasData
        ? `${g.breadth.pct.toFixed(0)}% of tracked symbols closed higher today (${g.breadth.advancers} up, ${g.breadth.decliners} down).`
        : "no read yet — not enough fresh daily bars.",
      caption: g.breadth.caption,
    },
    {
      label: "VOLATILITY",
      text:
        g.vix.hasData && g.vix.regime
          ? `${vixRead?.plain ?? `VIX at ${g.vix.level?.toFixed(1)}`} (regime: ${g.vix.regime})`
          : "no read yet.",
      caption: g.vix.caption,
    },
    {
      label: "UNUSUAL ACTIVITY",
      text: `${g.anomalies.count} unusual-activity events in the last ${g.anomalies.windowH}h — descriptive, not predictions.`,
      caption: g.anomalies.caption,
    },
    {
      label: "PREDICTION CONFIDENCE",
      text: g.confidence.hasData
        ? `typical prediction: ${confidenceWord(g.confidence.avg)}.`
        : "no read yet.",
      caption: g.confidence.caption,
      tip: (
        <HelpTip label="How prediction confidence is computed">
          The mean of |calibrated P(up) − 50%| × 2 across the latest 1-day predictions — how far
          from a coin flip the model fleet leans on average. It says nothing about direction, and
          it stays gated until enough predictions have resolved.
        </HelpTip>
      ),
    },
  ];
  return (
    <ul className="m-0 flex list-none flex-col gap-2 px-4 py-3">
      {rows.map((r) => (
        <li key={r.label} className="flex flex-col gap-0.5">
          <span className="flex items-center gap-1.5 text-[0.75rem] leading-relaxed">
            <span
              className="shrink-0 text-[0.75rem] font-medium tracking-[0.14em]"
              style={{ color: "var(--dim)" }}
            >
              {r.label}
            </span>
            <span>{r.text}</span>
            {r.tip}
          </span>
          <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
            {r.caption}
          </span>
        </li>
      ))}
    </ul>
  );
}

export default function GaugeRow({ dash }: { dash: DashboardResponse }) {
  const g = dash.gauges;
  // The API's honesty gate captions stay verbatim in BOTH modes — in the
  // simple summary above AND on every dial below.
  const mode = useViewMode();
  return (
    <section className="panel" aria-label="market gauges">
      <div className="panel-h">
        <span>MARKET GAUGES</span>
        <span
          className="text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          stored data on worker cadence — each dial keeps its gate caption
        </span>
      </div>

      {mode === "simple" && <SimpleGaugeSummary g={g} />}

      <div className={mode === "simple" ? "px-4 pb-3" : undefined}>
        <ProOnly summary="Show methodology">
          <div className="grid grid-cols-2 justify-items-center gap-x-2 gap-y-5 px-3 py-4 lg:grid-cols-5">
            <div className="flex flex-col items-center gap-1">
              <Gauge
                value={g.breadth.pct}
                min={0}
                max={100}
                label="BREADTH"
                caption={g.breadth.caption}
                hasData={g.breadth.hasData}
                format={(v) => `${v.toFixed(0)}%`}
                zones={[
                  { from: 0, to: 45, color: "var(--ask)" },
                  { from: 55, to: 100, color: "var(--bid)" },
                ]}
              />
              {g.breadth.hasData && (
                <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
                  <span style={{ color: "var(--bid)" }}>{g.breadth.advancers} adv</span>
                  {" · "}
                  <span style={{ color: "var(--ask)" }}>{g.breadth.decliners} dec</span>
                </span>
              )}
            </div>

            <div className="flex flex-col items-center gap-1">
              <Gauge
                value={g.vix.level ?? 0}
                min={10}
                max={40}
                label="VIX REGIME"
                caption={g.vix.caption}
                hasData={g.vix.hasData}
                format={(v) => v.toFixed(1)}
                zones={[
                  { from: 10, to: 15, color: "var(--bid)" },
                  { from: 20, to: 30, color: "var(--warn)" },
                  { from: 30, to: 40, color: "var(--ask)" },
                ]}
              />
              {g.vix.hasData && g.vix.regime && (
                <span
                  className="text-[0.75rem] font-bold tracking-wider"
                  style={{ color: vixRegimeColor(g.vix.regime) }}
                >
                  {g.vix.regime.toUpperCase()}
                  {typeof g.vix.dayChangePct === "number" && (
                    <span
                      className="tnum ml-1 font-normal"
                      style={{ color: changeColor(g.vix.dayChangePct) }}
                    >
                      {fmtPct(g.vix.dayChangePct)}
                    </span>
                  )}
                </span>
              )}
              {/* plain-English read of the VIX level (SIMPLE mode only; no data →
                  the dial's own gate caption already says why, so add nothing) */}
              {g.vix.hasData && (
                <Plain
                  metric="vix"
                  value={g.vix.level ?? null}
                  compact
                  className="max-w-[170px] justify-center text-center"
                />
              )}
            </div>

            <StatTile
              value={String(g.anomalies.count)}
              label="UNUSUAL ACTIVITY"
              sub={`events, last ${g.anomalies.windowH}h`}
              caption={g.anomalies.caption}
            />

            <div className="flex flex-col items-center gap-1">
              <Gauge
                value={g.confidence.avg}
                min={0}
                max={1}
                label="PREDICTION CONFIDENCE"
                caption={g.confidence.caption}
                hasData={g.confidence.hasData}
                format={(v) => `${(v * 100).toFixed(0)}%`}
              />
            </div>

            {(() => {
              const rg = regimesGauge(dash);
              if (!rg) return null;
              return (
                <div className="flex flex-col items-center gap-1">
                  <Gauge
                    value={rg.uptrendPct}
                    min={0}
                    max={100}
                    label="TREND BREADTH"
                    caption={rg.caption}
                    hasData={rg.hasData}
                    format={(v) => `${v.toFixed(0)}%`}
                    zones={[
                      { from: 0, to: 45, color: "var(--ask)" },
                      { from: 55, to: 100, color: "var(--bid)" },
                    ]}
                  />
                  {rg.hasData && (
                    <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
                      {rg.uptrendPct.toFixed(0)}% uptrend · {rg.elevatedPct.toFixed(0)}% elev vol
                    </span>
                  )}
                </div>
              );
            })()}
          </div>
        </ProOnly>
      </div>
    </section>
  );
}
