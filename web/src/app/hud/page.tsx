"use client";

// PUSH-20 HUD — embeds the live stock-trader strategy (V7 PUSH-20, Alpaca
// paper) as synced from trader-hud (:8787) by the SignalDeck daemon.

import { useEffect, useState } from "react";
import { api, pollMs, type Hud } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import type { HudSummary } from "@/components/hud/types";
import AccountPanel from "@/components/hud/AccountPanel";
import EquityChart from "@/components/hud/EquityChart";
import PositionsPanel from "@/components/hud/PositionsPanel";
import StrategyPanel from "@/components/hud/StrategyPanel";
import TradesPanel from "@/components/hud/TradesPanel";
import PagePurpose from "@/components/PagePurpose";

export default function HudPage() {
  const [hud, setHud] = useState<Hud | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const tick = () =>
      api
        .hud()
        .then((h) => {
          if (!alive) return;
          setHud(h);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    tick();
    const t = setInterval(tick, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  const summary = (hud?.summary ?? undefined) as HudSummary | undefined;
  const macro = summary?.macro;
  const alerts = summary?.checkup?.alerts ?? [];
  const frozen = (macro?.gate ?? "").toUpperCase() === "FROZEN";

  return (
    <div className="flex flex-col gap-4">
      {/* Header row: title + contextual chips */}
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-extrabold tracking-[0.14em]" style={{ color: "var(--text)" }}>
          PUSH-20 HUD
        </h1>
        <span className="chip">live strategy · Alpaca paper</span>
        {hud?.fetchedAt ? (
          <span className="chip tnum">fetched {ago(hud.fetchedAt)} via hud-sync</span>
        ) : null}
        {summary?.asof ? <span className="chip tnum">trader as of {summary.asof}</span> : null}
        {summary && summary.connected === false ? (
          <span className="chip" style={{ color: "var(--warn)" }}>
            alpaca unreachable at last sync
          </span>
        ) : null}
        {macro?.gate ? (
          <span
            className="chip tnum"
            aria-label={`macro regime gate ${macro.gate}`}
            style={{ color: frozen ? "var(--bad)" : "var(--ok)" }}
            title={macro.top_risk ?? undefined}
          >
            macro {macro.gate}
            {macro.score != null && isFinite(macro.score) ? ` · risk ${macro.score.toFixed(2)}` : ""}
            {macro.top_risk ? ` · ${macro.top_risk}` : ""}
          </span>
        ) : null}
        {err && hud ? (
          <span className="chip" style={{ color: "var(--warn)" }}>
            refresh failing — showing last data
          </span>
        ) : null}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="hud"
        text="How is the live PUSH-20 strategy doing on its Alpaca PAPER account? Synced from the trader daemon — simulated money, real discipline."
      />

      {/* Daemon unreachable and nothing to show yet */}
      {err && !hud ? (
        <ErrorState
          message={err}
          hint="Is the SignalDeck daemon running? Start signaldeckd (:8322) — this page recovers on its own once it is up."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      ) : !hud ? (
        <Skeleton lines={4} label="loading trader HUD" />
      ) : !hud.available ? (
        <EmptyState
          message="trader-hud not synced yet"
          detail="Start it (stock-trader dashboard/server.py :8787); SignalDeck shows the last known state once synced."
        />
      ) : !summary ? (
        <EmptyState
          message="synced, but the last trader-hud payload was empty"
          detail="Waiting for the next sync."
        />
      ) : (
        <>
          {alerts.length > 0 && (
            <div className="panel">
              <div className="panel-h" style={{ color: "var(--warn)" }}>
                CHECKUP ALERTS
                {summary.checkup?.file ? (
                  <span className="tnum" style={{ color: "var(--faint)" }}>
                    · {summary.checkup.file}
                  </span>
                ) : null}
              </div>
              <ul className="flex flex-col gap-1 p-4 text-[0.76rem]" style={{ color: "var(--warn)" }}>
                {alerts.map((a, i) => (
                  <li key={i}>{a}</li>
                ))}
              </ul>
            </div>
          )}

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
            <AccountPanel account={summary.account} />
            <div className="lg:col-span-2">
              <EquityChart history={summary.history} />
            </div>
            <div className="lg:col-span-2">
              <PositionsPanel positions={summary.positions} />
            </div>
            <StrategyPanel strategy={summary.strategy} slippage={summary.slippage} />
            <div className="lg:col-span-3">
              <TradesPanel trades={summary.trades} />
            </div>
          </div>
        </>
      )}
    </div>
  );
}
