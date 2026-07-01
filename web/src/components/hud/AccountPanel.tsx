"use client";

import { fmtPct } from "@/lib/format";
import type { HudAccount } from "./types";
import { pnlColor, usd } from "./util";

function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div>
      <div className="text-[0.64rem] tracking-wide" style={{ color: "var(--faint)" }}>
        {label}
      </div>
      <div className="tnum text-[0.9rem]" style={{ color: color ?? "var(--text)" }}>
        {value}
      </div>
    </div>
  );
}

/** ACCOUNT — equity headline, day P&L, cash / buying power / since-start. */
export default function AccountPanel({ account }: { account?: HudAccount }) {
  return (
    <section className="panel">
      <div className="panel-h">ACCOUNT</div>
      <div className="p-4">
        {account ? (
          <>
            <div className="text-[0.64rem] tracking-wide" style={{ color: "var(--faint)" }}>
              EQUITY
            </div>
            <div className="tnum text-2xl font-bold" style={{ color: "var(--text)" }}>
              {usd(account.equity)}
            </div>
            <div
              className="tnum mt-1 text-[0.8rem]"
              style={{ color: pnlColor(account.day_pnl) }}
              aria-label="day profit and loss"
            >
              {usd(account.day_pnl, true)}
              {account.day_pnl_pct != null && isFinite(account.day_pnl_pct)
                ? ` (${fmtPct(account.day_pnl_pct * 100)})`
                : ""}{" "}
              today
            </div>
            <div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-[0.8rem] sm:grid-cols-3 lg:grid-cols-2">
              <Stat label="CASH" value={usd(account.cash)} />
              <Stat label="BUYING POWER" value={usd(account.buying_power)} />
              <Stat
                label="SINCE START"
                value={
                  account.since_start != null && isFinite(account.since_start)
                    ? fmtPct(account.since_start * 100)
                    : "—"
                }
                color={pnlColor(account.since_start)}
              />
            </div>
          </>
        ) : (
          <div className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
            no account data in the last sync
          </div>
        )}
      </div>
    </section>
  );
}
