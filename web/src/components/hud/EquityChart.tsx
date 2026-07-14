"use client";

import type { HudHistory } from "./types";
import { pnlColor } from "./util";

const W = 600;
const H = 200;
const PX = 10;
const PY = 14;

function finite(v: number | null | undefined): v is number {
  return typeof v === "number" && isFinite(v);
}

function lastFinite(series: (number | null | undefined)[]): number | null {
  for (let i = series.length - 1; i >= 0; i--) {
    const v = series[i];
    if (finite(v)) return v;
  }
  return null;
}

/** EQUITY vs SPY — two-series normalized (100 = period start) SVG line chart. */
export default function EquityChart({ history }: { history?: HudHistory | null }) {
  const dates = history?.dates ?? [];
  const acct = history?.account ?? [];
  const spy = history?.spy ?? [];
  const n = Math.max(dates.length, acct.length, spy.length);
  const all = [...acct, ...spy].filter(finite);
  const empty = n < 2 || acct.filter(finite).length < 2 || all.length < 2;

  let body: React.ReactNode;
  if (empty) {
    body = (
      <div className="p-4 text-[0.78rem]" style={{ color: "var(--faint)" }}>
        no equity history yet — this fills in after the first synced trading days
      </div>
    );
  } else {
    const min = Math.min(...all);
    const max = Math.max(...all);
    const span = max - min || 1;
    const x = (i: number) => PX + (i / (n - 1)) * (W - 2 * PX);
    const y = (v: number) => H - PY - ((v - min) / span) * (H - 2 * PY);
    const segs = (series: (number | null | undefined)[]): string[] => {
      const out: string[] = [];
      let cur: string[] = [];
      for (let i = 0; i < n; i++) {
        const v = series[i];
        if (finite(v)) {
          cur.push(`${x(i).toFixed(1)},${y(v).toFixed(1)}`);
        } else {
          if (cur.length > 1) out.push(cur.join(" "));
          cur = [];
        }
      }
      if (cur.length > 1) out.push(cur.join(" "));
      return out;
    };
    const acctSegs = segs(acct);
    const spySegs = segs(spy);
    const lastA = lastFinite(acct);
    const lastS = lastFinite(spy);
    const edge = lastA != null && lastS != null ? lastA - lastS : null;

    body = (
      <div className="p-4">
        <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[0.78rem]" style={{ color: "var(--dim)" }}>
          <span className="flex items-center gap-1.5">
            <span aria-hidden="true" className="inline-block h-[2px] w-4" style={{ background: "var(--accent)" }} />
            PUSH-20{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              {lastA != null ? lastA.toFixed(1) : "—"}
            </span>
          </span>
          <span className="flex items-center gap-1.5">
            <span aria-hidden="true" className="inline-block h-[2px] w-4" style={{ background: "var(--dim)" }} />
            SPY{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              {lastS != null ? lastS.toFixed(1) : "—"}
            </span>
          </span>
          {edge != null && (
            <span className="tnum" style={{ color: pnlColor(edge) }}>
              {edge >= 0 ? "+" : "−"}
              {Math.abs(edge).toFixed(1)} pts vs SPY
            </span>
          )}
        </div>
        <svg
          viewBox={`0 0 ${W} ${H}`}
          className="w-full"
          role="img"
          aria-label="Account equity versus SPY over one month, both normalized to 100 at the period start"
        >
          {min <= 100 && max >= 100 && (
            <>
              <line
                x1={PX}
                x2={W - PX}
                y1={y(100)}
                y2={y(100)}
                stroke="var(--border)"
                strokeDasharray="4 4"
              />
              <text x={W - PX} y={y(100) - 4} textAnchor="end" fontSize="12" fill="var(--faint)">
                100
              </text>
            </>
          )}
          <text x={PX} y={y(max) + 10} fontSize="12" fill="var(--faint)" className="tnum">
            {max.toFixed(1)}
          </text>
          <text x={PX} y={y(min) - 4} fontSize="12" fill="var(--faint)" className="tnum">
            {min.toFixed(1)}
          </text>
          {spySegs.map((pts, i) => (
            <polyline key={`s${i}`} points={pts} fill="none" stroke="var(--dim)" strokeWidth="1.5" />
          ))}
          {acctSegs.map((pts, i) => (
            <polyline key={`a${i}`} points={pts} fill="none" stroke="var(--accent)" strokeWidth="2" />
          ))}
        </svg>
        <div className="mt-1 flex justify-between text-[0.75rem] tnum" style={{ color: "var(--faint)" }}>
          <span>{dates[0] ?? ""}</span>
          <span>{dates[dates.length - 1] ?? ""}</span>
        </div>
      </div>
    );
  }

  return (
    <section className="panel h-full">
      <div className="panel-h">
        EQUITY VS SPY
        <span style={{ color: "var(--faint)" }}>· 1M · normalized to 100</span>
      </div>
      {body}
    </section>
  );
}
