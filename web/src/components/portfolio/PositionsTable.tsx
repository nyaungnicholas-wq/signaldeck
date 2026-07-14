"use client";

// Positions table — extracted from the old monolithic /lab/portfolio page
// (pure refactor; behavior identical). The score-at-entry explanation moved
// from a hover-only header title to a click/keyboard HelpTip.

import Link from "next/link";
import { useState } from "react";
import { api, type PositionRow } from "@/lib/api";
import { ago, fmtPct, fmtPrice, fmtScore, fmtTs, scoreColor } from "@/lib/format";
import HelpTip from "@/components/HelpTip";

export default function PositionsTable({
  positions,
  onClosed,
}: {
  positions: PositionRow[];
  onClosed: () => void;
}) {
  const [closingId, setClosingId] = useState<number | null>(null);
  const [rowErr, setRowErr] = useState<{ id: number; msg: string } | null>(null);

  const close = async (p: PositionRow) => {
    if (closingId !== null) return;
    setClosingId(p.id);
    setRowErr(null);
    try {
      await api.portfolioClose(p.id, p.symbol, p.market);
      onClosed();
    } catch (x) {
      setRowErr({ id: p.id, msg: x instanceof Error ? x.message : String(x) });
    } finally {
      setClosingId(null);
    }
  };

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[0.75rem]">
        <thead>
          <tr
            className="text-left text-[0.75rem] tracking-wide"
            style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}
          >
            <th className="px-3 py-2 font-medium">SYMBOL</th>
            <th className="px-2 py-2 text-right font-medium" title="quantity">QTY</th>
            <th className="px-2 py-2 text-right font-medium" title="entry price">ENTRY</th>
            <th className="px-2 py-2 text-right font-medium" title="latest price (or exit price if closed)">LAST</th>
            <th className="px-2 py-2 text-right font-medium" title="profit and loss, percent">P&L %</th>
            <th className="px-2 py-2 text-right font-medium">
              <span className="inline-flex items-center gap-1">
                SCORE@ENTRY
                <HelpTip label="What is score at entry?">
                  The pressure score captured server-side the moment you logged the
                  position — so you can grade later whether the read was right.
                </HelpTip>
              </span>
            </th>
            <th className="px-3 py-2 font-medium">NOTE</th>
            <th className="px-2 py-2 text-right font-medium">LOGGED</th>
            <th className="px-2 py-2 font-medium">STATUS</th>
            <th className="px-2 py-2 font-medium" aria-label="actions" />
          </tr>
        </thead>
        <tbody className="tnum">
          {positions.map((p) => {
            const closing = closingId === p.id;
            const hasScore = isFinite(p.scoreAtEntry);
            return (
              <tr
                key={p.id}
                className="align-top transition-colors duration-150 hover:bg-[var(--panel2)]"
                style={{ borderBottom: "1px solid var(--border)" }}
              >
                <td className="px-3 py-2">
                  <Link
                    href={`/s/${p.market}/${encodeURIComponent(p.symbol)}`}
                    className="cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                  >
                    {p.symbol}
                  </Link>
                  <span className="ml-1.5 text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
                    {p.market}
                  </span>
                </td>
                <td className="px-2 py-2 text-right">{p.qty}</td>
                <td className="px-2 py-2 text-right">{fmtPrice(p.entryPrice)}</td>
                <td className="px-2 py-2 text-right">
                  {p.open ? (
                    fmtPrice(p.lastPrice)
                  ) : (
                    <span title="exit price" style={{ color: "var(--dim)" }}>
                      {fmtPrice(p.exitPrice ?? p.lastPrice)}
                    </span>
                  )}
                </td>
                <td className="px-2 py-2 text-right" style={{ color: scoreColor(p.pnlPct) }}>
                  {fmtPct(p.pnlPct)}
                </td>
                <td className="px-2 py-2 text-right">
                  {hasScore ? (
                    <span style={{ color: scoreColor(p.scoreAtEntry) }}>
                      {fmtScore(p.scoreAtEntry)}
                    </span>
                  ) : (
                    <span style={{ color: "var(--faint)" }}>—</span>
                  )}
                </td>
                <td className="max-w-56 px-3 py-2">
                  {p.note ? (
                    <span className="block truncate" style={{ color: "var(--dim)" }} title={p.note}>
                      {p.note}
                    </span>
                  ) : (
                    <span style={{ color: "var(--faint)" }}>—</span>
                  )}
                </td>
                <td className="px-2 py-2 text-right whitespace-nowrap" style={{ color: "var(--dim)" }} title={fmtTs(p.entryTs)}>
                  {ago(p.entryTs)}
                </td>
                <td className="px-2 py-2">
                  <span
                    className="chip"
                    style={{
                      padding: "1px 8px",
                      color: p.open ? "var(--ok)" : "var(--faint)",
                      borderColor: p.open ? "rgba(52,211,153,.35)" : "var(--border)",
                    }}
                  >
                    {p.open ? "open" : "closed"}
                  </span>
                  {!p.open && p.exitTs ? (
                    <span className="ml-2 text-[0.75rem]" style={{ color: "var(--faint)" }} title={fmtTs(p.exitTs)}>
                      {ago(p.exitTs)}
                    </span>
                  ) : null}
                </td>
                <td className="px-2 py-2">
                  {p.open ? (
                    <button
                      type="button"
                      onClick={() => close(p)}
                      disabled={closing}
                      aria-label={`close ${p.symbol} position`}
                      className="cursor-pointer rounded-lg border px-2.5 py-1 text-[0.75rem] transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                      style={{ borderColor: "var(--border)", color: "var(--dim)", background: "var(--panel2)" }}
                    >
                      {closing ? "closing…" : "close"}
                    </button>
                  ) : null}
                  {rowErr && rowErr.id === p.id && (
                    <div className="mt-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
                      {rowErr.msg}
                    </div>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
