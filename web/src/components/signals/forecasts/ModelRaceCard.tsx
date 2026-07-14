"use client";

// One lane of the MODEL RACE on the forecasts page: a single model's latest
// P(up) rendered RIGHT NEXT to its out-of-sample report card. The honesty rule
// is inherited from the old single-model page: a probability whose walk-forward
// lift is <= 0 is grayed out and labeled noise — visibly, as a chip, never a
// tooltip. A model row the trainer hasn't produced yet renders an honest empty
// lane, not a fabricated number.

import Plain, { useViewMode } from "@/components/Plain";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";
import GradeMeter from "@/components/viz/GradeMeter";
import { metricLabel, readMetric } from "@/lib/plain";
import { ago } from "@/lib/format";
import type { Horizon } from "@/lib/api";

/** The grade fields every lane shares — both `Forecast` (logistic) and
 *  `ModelForecast` (gbm/meanrev) rows satisfy this shape structurally. */
export interface RaceStats {
  ts: number;
  prob: number;
  accuracy: number;
  brier: number;
  auc: number;
  baseRate: number;
  lift: number;
  nTrain: number;
  nEval: number;
}

function pctText(v: number, digits = 1): string {
  if (!isFinite(v)) return "—";
  return `${(v * 100).toFixed(digits)}%`;
}

function liftText(lift: number): string {
  if (!isFinite(lift)) return "—";
  const pp = lift * 100;
  return `${pp >= 0 ? "+" : ""}${pp.toFixed(1)}pp`;
}

/** AUC has no plain.ts entry — same honest-buckets treatment, locally. */
function aucWord(auc: number): string {
  if (!isFinite(auc)) return "no read";
  if (auc >= 0.55) return "ranks up-bars above down-bars better than chance";
  if (auc > 0.505) return "barely better than chance at ranking";
  if (auc >= 0.495) return "no ranking skill — 0.50 is a coin flip";
  return "ranks WORSE than chance — inverted";
}

export default function ModelRaceCard({
  name,
  plainName,
  tagline,
  horizon,
  symbol,
  stats,
  gradeNote,
}: {
  /** Technical model name (PRO mode). */
  name: string;
  /** Plain-English model name (SIMPLE mode). */
  plainName: string;
  /** One-line description of what the model looks at. */
  tagline: string;
  horizon: Horizon;
  symbol: string;
  /** null = the trainer hasn't produced this model row yet (honest empty lane). */
  stats: RaceStats | null;
  /** Extra grading caveat shown as a chip (e.g. meanrev's net-of-cost grade). */
  gradeNote?: string;
}) {
  const mode = useViewMode();
  const label = mode === "simple" ? plainName : name;

  if (stats === null) {
    return (
      <section className="panel flex flex-col">
        <div className="panel-h">
          <span style={{ color: "var(--text)" }}>{label.toUpperCase()}</span>
          <span className="chip" style={{ color: "var(--faint)" }}>
            not trained yet
          </span>
        </div>
        <div className="flex flex-1 flex-col justify-center px-4 py-6 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          No {label} model for {symbol} on {horizon} yet.
          <div className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            The trainer needs 60 resolved outcomes on this symbol + horizon before this
            model earns a grade — an honest blank, not a made-up number.
          </div>
        </div>
      </section>
    );
  }

  // Honesty gate (unchanged from the old page): if the model can't beat the
  // base rate out-of-sample, the probability is grayed and labeled noise.
  const beatsBaseRate = isFinite(stats.lift) && stats.lift > 0;
  const smallSample = isFinite(stats.nEval) && stats.nEval < 100;

  const probClamped = Math.max(0, Math.min(1, isFinite(stats.prob) ? stats.prob : 0.5));
  const probColor = beatsBaseRate
    ? stats.prob >= 0.5
      ? "var(--bid)"
      : "var(--ask)"
    : "var(--faint)";

  return (
    <section className="panel flex flex-col">
      <div className="panel-h">
        <span style={{ color: "var(--text)" }}>{label.toUpperCase()}</span>
        <span className="chip tnum" style={{ color: "var(--faint)" }}>
          trained {ago(stats.ts)}
        </span>
      </div>

      <div className="flex flex-1 flex-col gap-3 px-4 py-4">
        <p className="m-0 text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
          {tagline}
        </p>

        {/* ── the probability, prominent, with its gate riding as a chip ── */}
        <div className="flex flex-col gap-2">
          <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
            {mode === "simple" ? `chance ${symbol} rises over ${horizon}` : `P(up over ${horizon})`}
          </span>
          <div className="flex flex-wrap items-baseline gap-2">
            <span
              className="tnum text-3xl font-bold leading-none"
              style={{ color: beatsBaseRate ? "var(--text)" : "var(--faint)" }}
            >
              {pctText(stats.prob, 1)}
            </span>
            {beatsBaseRate ? (
              <span
                className="chip"
                style={{ color: "var(--ok)", borderColor: "var(--ok)" }}
              >
                beats base rate {liftText(stats.lift)} OOS
              </span>
            ) : (
              <span
                className="chip"
                style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              >
                labeled noise — no OOS edge
              </span>
            )}
            {smallSample && (
              <span className="chip" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>
                small sample (n={stats.nEval})
              </span>
            )}
            {gradeNote && (
              <span className="chip" style={{ color: "var(--dim)" }}>
                {gradeNote}
              </span>
            )}
          </div>

          <div
            role="meter"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Number((probClamped * 100).toFixed(1))}
            aria-label={`${label} probability up over ${horizon} for ${symbol}`}
            className="mt-1 flex h-2.5 overflow-hidden rounded-full border"
            style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
          >
            <div
              className="transition-[width] duration-300"
              style={{
                width: `${probClamped * 100}%`,
                background: probColor,
                opacity: beatsBaseRate ? 1 : 0.5,
              }}
            />
          </div>
          <div className="tnum flex justify-between text-[0.75rem]" style={{ color: "var(--faint)" }}>
            <span>0%</span>
            <span>base rate {pctText(stats.baseRate, 0)}</span>
            <span>100%</span>
          </div>
        </div>

        {/* ── the out-of-sample report card, right under the number.
            Technical metrics fold in SIMPLE mode; the noise / beats-base-rate
            gate chip above stays visible in both. ── */}
        <ProOnly summary="Show the out-of-sample report card">
          <div className="flex flex-col gap-3">
        <span
          className="flex items-center gap-1.5 text-[0.75rem] tracking-wide"
          style={{ color: "var(--faint)" }}
        >
          OUT-OF-SAMPLE GRADE
          <HelpTip label="How this grade is measured">
            Every metric below is graded walk-forward with no lookahead: predictions are scored
            only against bars that came strictly after the training window. AUC is the chance a
            random up bar scored above a random down bar (0.50 = coin flip). n train is the bars
            the model was fit on; n test is the out-of-sample predictions graded.
          </HelpTip>
        </span>

        {/* lift is the gate metric — it gets the headline meter */}
        <GradeMeter
          label={metricLabel("lift", mode)}
          reading={readMetric("lift", stats.lift, { n: stats.nEval })}
        />

        <div className="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-2">
          <div className="flex flex-col gap-0.5">
            <span className="text-[0.75rem] uppercase tracking-[0.14em]" style={{ color: "var(--faint)" }}>
              {metricLabel("win_rate", mode)}
            </span>
            <Plain
              metric="win_rate"
              value={stats.accuracy}
              ctx={{ baseRate: stats.baseRate, n: stats.nEval }}
              className="text-[0.75rem]"
            />
          </div>
          <div className="flex flex-col gap-0.5">
            <span className="text-[0.75rem] uppercase tracking-[0.14em]" style={{ color: "var(--faint)" }}>
              {metricLabel("base_rate", mode)}
            </span>
            <Plain metric="base_rate" value={stats.baseRate} className="text-[0.75rem]" />
          </div>
          <div className="flex flex-col gap-0.5">
            <span className="text-[0.75rem] uppercase tracking-[0.14em]" style={{ color: "var(--faint)" }}>
              {metricLabel("brier", mode)}
            </span>
            <Plain metric="brier" value={stats.brier} ctx={{ n: stats.nEval }} className="text-[0.75rem]" />
          </div>
          <div
            className="flex flex-col gap-0.5"
            title="AUC — probability a random up bar scored above a random down bar. 0.50 = no skill."
          >
            <span className="text-[0.75rem] uppercase tracking-[0.14em]" style={{ color: "var(--faint)" }}>
              {mode === "simple" ? "ranking skill" : "AUC"}
            </span>
            <span
              className="tnum text-[0.75rem]"
              style={{
                color: isFinite(stats.auc)
                  ? stats.auc > 0.5
                    ? "var(--ok)"
                    : "var(--bad)"
                  : "var(--faint)",
              }}
            >
              {isFinite(stats.auc) ? stats.auc.toFixed(3) : "—"}
            </span>
            <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
              {aucWord(stats.auc)}
            </span>
          </div>
        </div>
          </div>
        </ProOnly>

        {/* sample sizes — visible chips, not fine print */}
        <div className="mt-auto flex flex-wrap items-center gap-2 pt-1">
          <span className="chip tnum" title="Bars the model was fit on.">
            n train {isFinite(stats.nTrain) ? stats.nTrain.toLocaleString("en-US") : "—"}
          </span>
          <span
            className="chip tnum"
            style={smallSample ? { color: "var(--warn)", borderColor: "var(--warn)" } : undefined}
            title="Out-of-sample predictions graded via walk-forward."
          >
            n test {isFinite(stats.nEval) ? stats.nEval.toLocaleString("en-US") : "—"}
          </span>
          <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
            walk-forward, no lookahead
          </span>
        </div>
      </div>
    </section>
  );
}
