"use client";

import { fmtPrice } from "@/lib/format";
import type { HudTrade } from "./types";
import { fmtQty, num } from "./util";

/** RECENT TRADES — last filled orders from the paper account. */
export default function TradesPanel({ trades }: { trades?: HudTrade[] }) {
  const list = Array.isArray(trades) ? trades : [];
  return (
    <section className="panel">
      <div className="panel-h">
        RECENT TRADES
        {list.length > 0 && (
          <span className="tnum" style={{ color: "var(--faint)" }}>
            · last {list.length} fills
          </span>
        )}
      </div>
      {list.length === 0 ? (
        <div className="p-4 text-[0.78rem]" style={{ color: "var(--faint)" }}>
          no recent fills in the last sync
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-[0.8rem] tnum">
            <thead>
              <tr className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                <th className="px-4 py-2 text-left font-medium" title="When the order filled">FILLED</th>
                <th className="px-3 py-2 text-left font-medium" title="Buy or sell">SIDE</th>
                <th className="px-3 py-2 text-left font-medium">SYMBOL</th>
                <th className="px-3 py-2 text-right font-medium" title="Quantity — shares filled">QTY</th>
                <th className="px-3 py-2 pr-4 text-right font-medium" title="Fill price">PRICE</th>
              </tr>
            </thead>
            <tbody>
              {list.map((t, i) => {
                const side = (t.side ?? "").toLowerCase();
                const px = num(t.price);
                return (
                  <tr
                    key={`${t.filled_at ?? ""}-${t.symbol ?? ""}-${i}`}
                    className="border-t transition-colors duration-150 hover:bg-[var(--panel2)]"
                    style={{ borderColor: "var(--border)" }}
                  >
                    <td className="px-4 py-2" style={{ color: "var(--dim)" }}>
                      {t.filled_at ?? "—"}
                    </td>
                    <td
                      className="px-3 py-2 uppercase"
                      style={{
                        color:
                          side === "buy"
                            ? "var(--bid)"
                            : side === "sell"
                              ? "var(--ask)"
                              : "var(--dim)",
                      }}
                    >
                      {t.side ?? "—"}
                    </td>
                    <td className="px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                      {t.symbol ?? "—"}
                    </td>
                    <td className="px-3 py-2 text-right">{fmtQty(t.qty)}</td>
                    <td className="px-3 py-2 pr-4 text-right">
                      {px != null ? fmtPrice(px) : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
