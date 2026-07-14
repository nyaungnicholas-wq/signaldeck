"use client";

// METHODOLOGY — collapsible published rulebook for the anomaly detectors.
// Every window/threshold below is quoted from the daemon's constants
// (internal/anomaly/worker.go "Scan windows" block + anomaly.go doc comments)
// so the UI can never describe a different scanner than the one running.
// Starts collapsed (reference material) — folded, never hidden.

import { useState } from "react";
import { SEVERITY_COLOR } from "./measure";

interface MethodEntry {
  label: string;
  color: string;
  scope: string;
  lines: string[];
}

// Mirrors daemon constants verbatim: imbRecentSec=300 imbBaseSec=3600
// minuteWindow=30 minuteBaseWins=8 volumeMinDays=5 dailyVolWindow=5
// dailyVolBase=10 dailyVolumeN=60 dailyVolumeMin=20; DefaultZ=2.5;
// trSpikeRatio=3.0 atrLookback=14 (anomaly.go).
const ENTRIES: MethodEntry[] = [
  {
    label: "IMBALANCE",
    color: "var(--bid)",
    scope: "crypto — real order-book data",
    lines: [
      "mean signed order-book imbalance over the last 5m of 1Hz snapshots, z-scored against the means of the 12 trailing 5m windows across the preceding 60m baseline",
      "a window needs ≥30 snapshots for its mean to count; ≥6 usable trailing window means required — feed gaps yield NO signal",
    ],
  },
  {
    label: "IMBALANCE",
    color: "var(--bid)",
    scope: "stocks — volume-side PROXY",
    lines: [
      "up-vs-down volume share over the last 30×1m bars, z-scored against ≥8 trailing 30-bar windows of the same symbol",
      "volume-side proxy (no order-book on free stock data) — every stock row carries this label verbatim; it is never presented as book imbalance",
    ],
  },
  {
    label: "VOLATILITY",
    color: "var(--warn)",
    scope: "1m bars (+ 1d daily sweep)",
    lines: [
      "realized (log-return) volatility of the last 30×1m bars, z-scored against ≥8 trailing 30-bar windows; the daily sweep runs the same test on the last 5 daily bars vs ≥10 trailing 5-bar windows",
      "fallback: a single-bar true-range spike at ≥3.0× the trailing ATR(14) — that row's number is the TR/ATR RATIO, not a z-score, and is always labeled as such",
      "fires on the HIGH side only — unusually low vol is a squeeze, which the breakout alerts already cover",
    ],
  },
  {
    label: "VOLUME",
    color: "var(--warn)",
    scope: "1m bars (+ 1d daily sweep)",
    lines: [
      "summed volume of the last 30 minutes vs the SAME clock minutes across ≥5 prior days (days missing half the window's minutes are skipped)",
      "daily sweep: the last daily bar's volume vs up to 60 trailing daily volumes (≥20 required). High side only",
    ],
  },
];

export default function MethodologyPanel() {
  const [open, setOpen] = useState(false);

  return (
    <section className="panel">
      <div className="panel-h">
        METHODOLOGY — WINDOWS &amp; THRESHOLDS
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="chip ml-auto min-h-[36px] cursor-pointer px-3 text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)] hover:text-[var(--text)]"
          style={open ? undefined : { color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          {open ? "hide the windows" : "show the exact windows ↓"}
        </button>
      </div>

      {open ? (
        <div className="flex flex-col gap-3 px-4 py-3">
          <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            Every detection is a z-score of recent activity vs the SAME symbol&rsquo;s own trailing
            baseline — like is always compared with like (window means vs window means, never a
            window mean vs raw ticks). The |z| threshold defaults to 2.5 (SIGNALDECK_ANOM_Z).
            Insufficient or zero-variance baselines yield NO signal — never a fabricated z. The
            windows below are the daemon&rsquo;s running constants, quoted verbatim.
          </p>

          {ENTRIES.map((e) => (
            <div key={`${e.label}:${e.scope}`} className="flex flex-col gap-1">
              <div className="flex flex-wrap items-center gap-2">
                <span
                  className="chip shrink-0 px-2 py-[2px] text-[0.75rem] tracking-wider"
                  style={{ color: e.color, borderColor: e.color }}
                >
                  {e.label}
                </span>
                <span className="text-[0.75rem]" style={{ color: "var(--text)" }}>
                  {e.scope}
                </span>
              </div>
              <ul className="m-0 flex list-none flex-col gap-0.5 pl-3">
                {e.lines.map((t, i) => (
                  <li key={i} className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                    · {t}
                  </li>
                ))}
              </ul>
            </div>
          ))}

          <div className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="chip shrink-0 px-2 py-[2px] text-[0.75rem] tracking-wider">
                SEVERITY LANES
              </span>
              <span className="text-[0.75rem]" style={{ color: "var(--text)" }}>
                presentation buckets on the same statistic — not new information
              </span>
            </div>
            <ul className="m-0 flex list-none flex-col gap-0.5 pl-3">
              <li className="text-[0.75rem] leading-relaxed" style={{ color: SEVERITY_COLOR.notable }}>
                · NOTABLE — |z| 2–3 (in practice ≥2.5, the default fire threshold)
              </li>
              <li className="text-[0.75rem] leading-relaxed" style={{ color: SEVERITY_COLOR.elevated }}>
                · ELEVATED — |z| 3–4
              </li>
              <li className="text-[0.75rem] leading-relaxed" style={{ color: SEVERITY_COLOR.extreme }}>
                · EXTREME — |z| ≥ 4
              </li>
              <li className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                · TR/ATR-ratio rows band by the ratio value on the same cuts and are always labeled
                as a ratio, never as a z-score
              </li>
            </ul>
          </div>
        </div>
      ) : (
        <p className="m-0 px-4 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          every detector runs fixed published windows vs each symbol&rsquo;s own baseline (|z| ≥
          2.5 default) — folded, never hidden.
        </p>
      )}
    </section>
  );
}
