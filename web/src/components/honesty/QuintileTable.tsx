"use client";

import type { HonestyBucket } from "@/lib/api";
import { fmtPct } from "@/lib/format";

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
        <table className="w-full text-[0.8rem]">
          <thead>
            <tr className="text-[0.64rem] tracking-wide" style={{ color: "var(--faint)" }}>
              <th className="px-4 py-2 text-left font-medium">BUCKET</th>
              <th className="px-4 py-2 text-right font-medium">N</th>
              <th className="px-4 py-2 text-right font-medium">MEAN FWD</th>
              <th className="px-4 py-2 text-right font-medium">HIT RATE</th>
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
                  <td className="tnum px-4 py-2 text-right" style={{ color: "var(--dim)" }}>
                    {empty || !Number.isFinite(b?.hitRate) ? "—" : fmtPct(b.hitRate * 100, false)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <div
        className="border-t px-4 py-2 text-[0.68rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: monotone && filled.length >= 2 ? "var(--bid)" : "var(--faint)" }}
      >
        {caption}
      </div>
    </section>
  );
}
