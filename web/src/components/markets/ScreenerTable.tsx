"use client";

// SCREENER table view, extracted verbatim from the page split. The column
// [title] attributes remain as supplements; the load-bearing explanations
// (verdict calibration caveat, score/rank/regime meanings) are surfaced
// accessibly in ScreenerResults via a visible caption + HelpTip, so nothing
// essential is hover-only.

import Link from "next/link";
import { ago, fmtPct, fmtPrice, fmtScore, scoreColor } from "@/lib/format";
import VerdictCard from "@/components/VerdictCard";
import Sparkline from "@/components/viz/Sparkline";
import { regimeColor } from "@/components/regime/regime";
import { COLUMNS, type Derived, type SortDir, type SortKey } from "@/components/markets/screenerModel";

export default function ScreenerTable({
  filtered,
  sortKey,
  sortDir,
  onSort,
  rankingFailed = false,
  regimesFailed = false,
}: {
  filtered: Derived[];
  sortKey: SortKey;
  sortDir: SortDir;
  onSort: (key: SortKey, numericDefaultDesc: boolean) => void;
  /** The ranking fetch failed, so a blank rank means UNKNOWN, not excluded. */
  rankingFailed?: boolean;
  regimesFailed?: boolean;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[0.75rem]">
        <thead>
          <tr style={{ borderBottom: "1px solid var(--border)" }}>
            {COLUMNS.map((c) => (
              <th
                key={c.key}
                scope="col"
                aria-sort={
                  c.sortable === false
                    ? undefined
                    : sortKey === c.key
                      ? sortDir === "asc"
                        ? "ascending"
                        : "descending"
                      : "none"
                }
                className={`px-3 py-2 text-[0.75rem] font-medium tracking-wide ${
                  c.numeric ? "text-right" : "text-left"
                }`}
                style={{ color: "var(--faint)" }}
              >
                {c.sortable === false ? (
                  <span title={c.title} className="tracking-wide">
                    {c.label}
                  </span>
                ) : (
                  <button
                    type="button"
                    title={c.title}
                    onClick={() => onSort(c.key, c.numeric)}
                    className={`cursor-pointer tracking-wide transition-colors duration-150 hover:text-[var(--dim)] ${
                      c.numeric ? "text-right" : "text-left"
                    }`}
                    style={{ color: sortKey === c.key ? "var(--accent)" : undefined }}
                  >
                    {c.label}
                    {sortKey === c.key ? (sortDir === "asc" ? " ▲" : " ▼") : ""}
                  </button>
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="tnum">
          {filtered.map((d) => {
            const r = d.row;
            return (
              <tr
                key={`${r.market}:${r.symbol}`}
                className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                style={{ borderBottom: "1px solid var(--border)" }}
              >
                <td className="px-3 py-2">
                  <Link
                    href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
                    className="cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                  >
                    {r.symbol}
                  </Link>
                </td>
                <td className="px-3 py-2" style={{ color: "var(--dim)" }}>
                  {r.market}
                </td>
                {/* Stage 5: 30d trend from the row's own stored closes */}
                <td className="px-3 py-1">
                  <Sparkline closes={r.spark} width={96} height={26} area={false} />
                </td>
                <td className="px-3 py-2 text-right">{fmtPrice(r.lastClose)}</td>
                <td
                  className="px-3 py-2 text-right"
                  style={{ color: scoreColor(r.dayChangePct) }}
                >
                  {fmtPct(r.dayChangePct)}
                </td>
                <td className="px-3 py-2 text-right">
                  {d.score === null ? (
                    <span style={{ color: "var(--faint)" }}>—</span>
                  ) : (
                    <span style={{ color: scoreColor(d.score) }}>{fmtScore(d.score)}</span>
                  )}
                </td>
                {/* Stage 2: calibrated 1d verdict card — real prob or
                    the honest NO READ YET, tier badge always visible. */}
                <td className="px-3 py-2">
                  <VerdictCard
                    size="sm"
                    symbol={r.symbol}
                    market={r.market}
                    calProb={r.calProb1d ?? null}
                    nUsed={r.nUsed1d ?? 0}
                    tier={r.tier1d ?? ""}
                    tierProgress={{ nSamples: r.nSamples1d ?? 0, threshold: r.tierThreshold ?? 0 }}
                  />
                </td>
                {/* Stage 5: relative-strength rank (1 = strongest) */}
                <td className="px-3 py-2 text-right">
                  {d.rank === null ? (
                    // "not in the latest ranking pass" is a claim that the pass
                    // RAN and excluded this symbol. When the ranking fetch
                    // failed we know nothing of the sort, and every row would
                    // have carried that assertion.
                    <span
                      style={{ color: "var(--faint)" }}
                      title={
                        rankingFailed
                          ? "ranking unavailable — this request failed, so the rank is UNKNOWN, not absent"
                          : "not in the latest ranking pass"
                      }
                    >
                      —
                    </span>
                  ) : (
                    <span style={{ color: d.rank <= 10 ? "var(--accent)" : "var(--dim)" }}>
                      #{d.rank}
                    </span>
                  )}
                </td>
                {/* Stage 5: current regime chip (descriptive, no lookahead) */}
                <td className="px-3 py-2">
                  {d.regime === null ? (
                    // Same distinction the RANK cell above draws: "not
                    // classified yet" says the classifier ran and has nothing
                    // for this symbol. A failed /api/regime tells us nothing
                    // of the kind, and every row would have carried the claim.
                    <span
                      style={{ color: "var(--faint)" }}
                      title={
                        regimesFailed
                          ? "regime unavailable — this request failed, so the regime is UNKNOWN, not unclassified"
                          : "not classified yet — the regime worker fills this in from stored bars"
                      }
                    >
                      —
                    </span>
                  ) : (
                    <span
                      className="chip px-2 py-[1px] text-[0.75rem] tracking-wider"
                      title={`${d.regime.label} — ${d.regime.note || "described from stored bars, no lookahead"}`}
                      style={{ color: regimeColor(d.regime.label), borderColor: regimeColor(d.regime.label) }}
                    >
                      {d.regime.label}
                    </span>
                  )}
                </td>
                <td className="max-w-72 px-3 py-2">
                  {d.driver ? (
                    <span className="flex items-baseline gap-1.5">
                      <span style={{ color: "var(--text)" }}>{d.driver.name}</span>
                      <span
                        className="truncate text-[0.75rem]"
                        style={{ color: "var(--faint)" }}
                        title={d.driver.note}
                      >
                        {d.driver.note}
                      </span>
                    </span>
                  ) : (
                    <span style={{ color: "var(--faint)" }}>—</span>
                  )}
                </td>
                <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                  {ago(r.latestBarTs)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
