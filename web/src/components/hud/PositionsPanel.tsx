"use client";

import { fmtPct, fmtPrice } from "@/lib/format";
import type { HudPosition } from "./types";
import { fmtQty, pnlColor, usd } from "./util";

const TH = "px-3 py-2 text-right font-medium";

/** POSITIONS — open holdings with unrealized P&L, colored bid/ask. */
export default function PositionsPanel({ positions }: { positions?: HudPosition[] }) {
  const list = Array.isArray(positions) ? positions : [];
  return (
    <section className="panel h-full">
      <div className="panel-h">
        POSITIONS
        {list.length > 0 && (
          <span className="tnum" style={{ color: "var(--faint)" }}>
            · {list.length}
          </span>
        )}
      </div>
      {list.length === 0 ? (
        <div className="p-4 text-[0.72rem]" style={{ color: "var(--faint)" }}>
          no open positions in the last sync — flat (cash / defensive)
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-[0.8rem] tnum">
            <thead>
              <tr className="text-[0.64rem] tracking-wide" style={{ color: "var(--faint)" }}>
                <th className="px-4 py-2 text-left font-medium">SYMBOL</th>
                <th className={TH}>QTY</th>
                <th className={TH}>AVG ENTRY</th>
                <th className={TH}>LAST</th>
                <th className={TH}>VALUE</th>
                <th className={TH}>UNRL P&L</th>
                <th className={`${TH} pr-4`}>UNRL %</th>
              </tr>
            </thead>
            <tbody>
              {list.map((p, i) => (
                <tr
                  key={p.symbol ?? i}
                  className="border-t transition-colors duration-150 hover:bg-[var(--panel2)]"
                  style={{ borderColor: "var(--border)" }}
                >
                  <td className="px-4 py-2 font-bold" style={{ color: "var(--text)" }}>
                    {p.symbol ?? "—"}
                  </td>
                  <td className="px-3 py-2 text-right">{fmtQty(p.qty)}</td>
                  <td className="px-3 py-2 text-right">
                    {p.avg != null && isFinite(p.avg) ? fmtPrice(p.avg) : "—"}
                  </td>
                  <td className="px-3 py-2 text-right">
                    {p.price != null && isFinite(p.price) ? fmtPrice(p.price) : "—"}
                  </td>
                  <td className="px-3 py-2 text-right">{usd(p.value)}</td>
                  <td className="px-3 py-2 text-right" style={{ color: pnlColor(p.upl) }}>
                    {usd(p.upl, true)}
                  </td>
                  <td className="px-3 py-2 pr-4 text-right" style={{ color: pnlColor(p.upl_pct) }}>
                    {p.upl_pct != null && isFinite(p.upl_pct) ? fmtPct(p.upl_pct * 100) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
