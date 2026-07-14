"use client";

import type { HonestyBucket } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import CellBar from "@/components/viz/CellBar";

function monotoneUp(vals: number[]): boolean {
  if (vals.length < 2) return false;
  for (let i = 1; i < vals.length; i++) {
    if (vals[i] < vals[i - 1] - 1e-12) return false;
  }
  return vals[vals.length - 1] > vals[0];
}

function fwdColor(v: number): string {
  if (!Number.isFinite(v)) return "var(--dim)";
  if (v > 0) return "var(--bid)";
  if (v < 0) return "var(--ask)";
  return "var(--dim)";
}

/** Stage 4 (tables→charts): the quintile BAR CHART — mean forward return per
 *  score bucket as diverging bars from the 0% center, so "does strong-buy
 *  actually beat strong-sell?" reads in one glance before the exact table.
 *  Bars scale to the largest |mean fwd| among FILLED buckets; an empty bucket
 *  renders an honest empty track labeled "no outcomes yet" — never a bar. */
function QuintileBars({ rows }: { rows: HonestyBucket[] }) {
  const filled = rows.filter((b) => (b?.n ?? 0) > 0 && Number.isFinite(b?.meanFwd));
  if (filled.length === 0) return null;
  const maxAbs = Math.max(...filled.map((b) => Math.abs(b.meanFwd)), 1e-9);
  return (
    <div className="flex flex-col gap-1.5 px-4 py-3" style={{ borderBottom: "1px solid var(--border)" }}>
      {rows.map((b, i) => {
        const empty = (b?.n ?? 0) === 0 || !Number.isFinite(b?.meanFwd);
        const frac = empty ? 0 : Math.max(-1, Math.min(1, b.meanFwd / maxAbs));
        const left = frac < 0 ? 50 + frac * 50 : 50;
        const width = Math.abs(frac) * 50;
        return (
          <div key={`${b?.label ?? "bucket"}-${i}`} className="flex items-center gap-2 text-[0.75rem]">
            <span className="w-24 shrink-0 truncate" style={{ color: empty ? "var(--faint)" : "var(--dim)" }}>
              {b?.label || `bucket ${i + 1}`}
            </span>
            <div
              className="relative h-3 flex-1 overflow-hidden rounded-sm"
              style={{ background: "color-mix(in srgb, var(--faint) 12%, transparent)" }}
              role="img"
              aria-label={
                empty
                  ? `${b?.label || `bucket ${i + 1}`}: no resolved outcomes yet`
                  : `${b.label}: mean forward return ${fmtPct(b.meanFwd * 100)} over ${b.n} outcomes`
              }
              title={
                empty
                  ? "no resolved outcomes in this bucket yet — nothing drawn"
                  : `mean fwd ${fmtPct(b.meanFwd * 100)} · n=${b.n} — bar scaled to the largest bucket move`
              }
            >
              {!empty && (
                <div
                  className="absolute top-0 h-full"
                  style={{ left: `${left}%`, width: `${width}%`, background: fwdColor(b.meanFwd) }}
                />
              )}
              {/* the 0% center line */}
              <div aria-hidden="true" className="absolute top-0 h-full" style={{ left: "50%", width: 1, background: "var(--dim)" }} />
            </div>
            <span className="tnum w-16 shrink-0 text-right" style={{ color: empty ? "var(--faint)" : fwdColor(b.meanFwd) }}>
              {empty ? "—" : fmtPct(b.meanFwd * 100)}
            </span>
            <span className="tnum w-12 shrink-0 text-right" style={{ color: "var(--faint)" }}>
              n={b?.n ?? 0}
            </span>
          </div>
        );
      })}
      <p className="m-0 mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        mean forward return per score bucket — bars grow from the 0% line; exact numbers in the table below.
      </p>
    </div>
  );
}

/** Score-quintile outcome table: does strong-buy actually beat strong-sell? */
export default function QuintileTable({ buckets }: { buckets: HonestyBucket[] }) {
  const rows = buckets ?? [];
  const filled = rows.filter((b) => (b?.n ?? 0) > 0 && Number.isFinite(b?.meanFwd));
  const monotone = monotoneUp(filled.map((b) => b.meanFwd));

  let caption: string;
  if (filled.length === 0) {
    caption = "no resolved outcomes yet — buckets fill in as scores resolve.";
  } else if (filled.length < 2) {
    caption = "only one bucket has outcomes so far — no ranking to judge yet.";
  } else if (monotone) {
    caption =
      "mean forward return rises monotonically from strong-sell to strong-buy — the scores rank outcomes at this horizon.";
  } else {
    caption =
      "returns are not monotone across buckets yet — the score ordering has not proven itself at this horizon.";
  }

  return (
    <section className="panel">
      <div className="panel-h">QUINTILES · SCORE BUCKET vs OUTCOME</div>
      {rows.length === 0 ? (
        <div className="px-4 py-6 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          no resolved outcomes yet — scores resolve after their horizon elapses.
        </div>
      ) : (
        <>
        {/* Stage 4: chart first, exact table right below — data never lost. */}
        <QuintileBars rows={rows} />
        <div className="table-wrap">
        <table className="w-full text-[0.75rem]">
          <thead>
            <tr className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
              <th
                className="px-4 py-2 text-left font-medium"
                title="Score quintile (strong sell = lowest scores, strong buy = highest)"
              >
                BUCKET
              </th>
              <th
                className="px-4 py-2 text-right font-medium"
                title="Sample size — resolved outcomes in this bucket"
              >
                N
              </th>
              <th
                className="px-4 py-2 text-right font-medium"
                title="Mean forward return — average of what the market did after these scores"
              >
                MEAN FWD
              </th>
              <th
                className="px-4 py-2 text-right font-medium"
                title="Share of scores that predicted the right direction"
              >
                HIT RATE
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((b, i) => {
              const empty = (b?.n ?? 0) === 0;
              return (
                <tr
                  key={`${b?.label ?? "bucket"}-${i}`}
                  className="border-t"
                  style={{ borderColor: "var(--border)" }}
                >
                  <td className="px-4 py-2" style={{ color: empty ? "var(--faint)" : "var(--text)" }}>
                    {b?.label || `bucket ${i + 1}`}
                  </td>
                  <td className="tnum px-4 py-2 text-right" style={{ color: "var(--dim)" }}>
                    {b?.n ?? 0}
                  </td>
                  <td
                    className="tnum px-4 py-2 text-right"
                    style={{ color: empty ? "var(--faint)" : fwdColor(b.meanFwd) }}
                  >
                    {empty || !Number.isFinite(b?.meanFwd) ? "—" : fmtPct(b.meanFwd * 100)}
                  </td>
                  <td className="px-4 py-2 text-right" style={{ color: "var(--dim)" }}>
                    {/* Stage 4: inline magnitude bar (absolute 0–100% scale) */}
                    <CellBar
                      frac={empty || !Number.isFinite(b?.hitRate) ? null : b.hitRate}
                      label={empty || !Number.isFinite(b?.hitRate) ? "—" : fmtPct(b.hitRate * 100, false)}
                      color="var(--accent)"
                      title="share of scores in this bucket that called the direction right — bar on an absolute 0–100% scale"
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        </div>
        </>
      )}
      <div
        className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: monotone && filled.length >= 2 ? "var(--bid)" : "var(--faint)" }}
      >
        {caption}
      </div>
    </section>
  );
}
