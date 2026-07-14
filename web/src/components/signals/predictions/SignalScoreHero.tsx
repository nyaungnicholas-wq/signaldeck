"use client";

// SIGNALSCORE HERO — the flagship card. One symbol's 1–10 forced-curve score
// (decile color), the calibrated edge line VERBATIM from the API, and every
// caveat on the card in plain sight: trackLabel chip, curveNote + edgeNote as
// always-visible footnotes (never tooltips). available:false renders the
// API's reason prominently — an honest miss, never a fabricated score.

import Link from "next/link";
import type { Composite } from "@/lib/api";
import { ago } from "@/lib/format";
import { useViewMode } from "@/components/Plain";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import { scoreColor, convictionMeta } from "./compositeUi";

/** Always-visible footnote block: the honesty chips ride the card itself. */
function Footnotes({ notes }: { notes: string[] }) {
  return (
    <div
      className="flex flex-col gap-1 px-4 py-2.5"
      style={{ borderTop: "1px solid var(--border)" }}
    >
      {notes.map((n) => (
        <p key={n} className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {n}
        </p>
      ))}
    </div>
  );
}

export default function SignalScoreHero({
  symbol,
  market,
  data,
  err,
  retry,
}: {
  symbol: string;
  market: string;
  data: Composite | null;
  err: string | null;
  retry: () => void;
}) {
  const mode = useViewMode();

  if (err) {
    const notDeployed = err.includes("404");
    return (
      <ErrorState
        message={notDeployed ? `no composite endpoint / no such symbol — ${err}` : err}
        hint={
          notDeployed
            ? "Either the running daemon predates GET /api/composite (restart it with the current binary) or this symbol is not in the tracked universe. Nothing is faked meanwhile."
            : "Is the daemon running? Start signaldeckd and the SignalScore card will recover."
        }
        retry={retry}
      />
    );
  }

  if (data === null) {
    return <Skeleton lines={4} label={`loading SignalScore for ${symbol} (${market})`} />;
  }

  // ── honest miss: the scorer has no verdict for this symbol (yet) ──
  if (!data.available) {
    return (
      <section className="panel">
        <div className="panel-h flex-wrap gap-2">
          {mode === "simple" ? "SIGNAL SCORE" : "SIGNALSCORE (COMPOSITE)"}
          <span className="chip tnum">
            {data.symbol} <span style={{ color: "var(--faint)" }}>{data.market}</span>
          </span>
        </div>
        <div className="px-4 py-4">
          <p className="text-[0.9rem] font-bold" style={{ color: "var(--warn)" }}>
            no score for {data.symbol} yet
          </p>
          {/* the API's reason, verbatim and prominent — this IS the content */}
          <p className="mt-1.5 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {data.reason}
          </p>
        </div>
        <Footnotes notes={[data.curveNote]} />
      </section>
    );
  }

  const color = scoreColor(data.score);
  const conv = data.conviction;
  const cm = convictionMeta(conv?.band);
  // Top 5% = 10 → the score's rough position in today's cross-section.
  const topPct = Math.max(0, Math.min(100, 100 - data.curvePct));

  // Every honesty note that rides the card. skillNote + riskNote come from the
  // conviction axis — the persistent "this is a rank, not a certainty" caveat.
  const notes = [data.curveNote, data.edgeNote];
  if (conv?.skillNote) notes.push(conv.skillNote);
  if (conv?.riskNote) notes.push(conv.riskNote);

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        {mode === "simple" ? "SIGNAL SCORE" : "SIGNALSCORE (COMPOSITE)"}
        <span className="chip tnum">
          {data.symbol} <span style={{ color: "var(--faint)" }}>{data.market}</span>
        </span>
        <span className="chip tnum" title="the horizon this verdict is scored on">
          {data.horizon}
        </span>
        {/* CONVICTION — the honest headline: how much to trust the rank. */}
        {conv && (
          <span
            className="chip ml-auto font-bold"
            style={{ color: cm.color, borderColor: cm.color }}
            title="conviction is a SEPARATE axis from the rank — a top rank on a weak or unproven edge is low conviction"
          >
            {conv.label}
          </span>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-x-8 gap-y-4 px-4 py-4">
        {/* the 1–10 — a RANK on today's forced curve, never a probability. */}
        <div className="flex flex-col gap-1">
          <span className="text-[0.7rem] font-medium tracking-wide" style={{ color: "var(--faint)" }}>
            RANK · TOP {topPct.toFixed(0)}% TODAY
          </span>
          <div
            className="flex items-baseline gap-1.5"
            role="img"
            aria-label={`composite rank ${data.score} of 10 — top ${topPct.toFixed(0)} percent of today's cross-section, a relative rank and not a probability of profit`}
          >
            <span className="tnum text-[3.2rem] font-extrabold leading-none" style={{ color }}>
              {data.score}
            </span>
            <span className="text-[1rem]" style={{ color: "var(--faint)" }}>
              /10 rank
            </span>
          </div>
        </div>

        <div className="flex min-w-[16rem] flex-1 flex-col gap-2">
          {/* the edge line — VERBATIM from the API: the REAL probability. */}
          <p className="tnum text-[0.95rem] font-bold leading-snug">{data.edgeLine}</p>

          {/* conviction band + its drivers — the antidote to "10 = sure thing". */}
          {conv && (
            <div
              className="flex flex-col gap-1 rounded-md px-2.5 py-2"
              style={{ background: "var(--panel2)", border: `1px solid ${cm.color}` }}
            >
              <span className="text-[0.8rem] font-bold" style={{ color: cm.color }}>
                {conv.label}
              </span>
              {conv.drivers.length > 0 && (
                <ul className="flex flex-col gap-0.5">
                  {conv.drivers.map((dstr) => (
                    <li key={dstr} className="text-[0.75rem] leading-snug" style={{ color: "var(--dim)" }}>
                      · {dstr}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}

          <div className="flex flex-wrap items-center gap-1.5">
            <span
              className="chip tnum"
              title="cross-sectional percentile of the edge in today's pass (100 = best) — see the curve footnote"
            >
              curve {data.curvePct.toFixed(0)}th pctile
            </span>
            <span
              className="chip tnum"
              title="ensemble legs behind the calibrated probability"
            >
              {mode === "simple" ? "signals used" : "blend n"} {data.nUsed}
            </span>
            <span className="chip tnum">scored {ago(data.ts)}</span>
            <span
              className="chip tnum"
              title="the stored ensemble prediction this verdict is built on — never recomputed here"
            >
              prediction {ago(data.predTs)}
            </span>
            <span className="chip tnum" title="calibration track status">
              {data.trackLabel}
            </span>
            {mode === "pro" && (
              <span
                className="chip tnum"
                title="raw ensemble probability → calibrated probability"
              >
                raw {(data.rawProb * 100).toFixed(1)}% → cal {(data.calProb * 100).toFixed(1)}%
              </span>
            )}
          </div>
          {/* next action, in the app's verb language */}
          <Link
            href={`/s/${data.market}/${encodeURIComponent(data.symbol)}`}
            className="cursor-pointer self-start text-[0.75rem] text-[var(--accent)] transition-colors duration-150 hover:text-[var(--text)]"
          >
            Investigate {data.symbol} — charts, filings, and the full evidence →
          </Link>
        </div>
      </div>

      {/* honesty footnotes — visible on the card, never hidden in hover */}
      <Footnotes notes={notes} />
    </section>
  );
}
