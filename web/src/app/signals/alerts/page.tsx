"use client";

// ALERTS — the session user's actionable event feed (alerts wave).
// Breakouts, regime changes, and calibrated predictions crossing thresholds
// for symbols on YOUR watchlist. Polls /api/alerts; "mark all read" clears
// the header bell.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, notifyStatus, type AlertRow, type NotifyStatusResponse } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

const KIND_META: Record<string, { label: string; color: string }> = {
  breakout: { label: "BREAKOUT", color: "var(--accent)" },
  regime_change: { label: "REGIME", color: "var(--dim)" },
  prediction_high: { label: "P(UP) HIGH", color: "var(--bid)" },
  prediction_low: { label: "P(UP) LOW", color: "var(--ask)" },
  // Signal8 wave Stage 3: anomaly kinds — DESCRIPTIVE z-scores vs the
  // symbol's own baseline (the detail carries window/baseline/proxy labels).
  anomaly_imbalance: { label: "IMBALANCE", color: "var(--warn)" },
  anomaly_vol: { label: "VOLATILITY", color: "var(--warn)" },
  anomaly_volume: { label: "VOLUME", color: "var(--warn)" },
};

function kindMeta(kind: string) {
  return KIND_META[kind] ?? { label: kind.toUpperCase(), color: "var(--dim)" };
}

export default function AlertsPage() {
  const [rows, setRows] = useState<AlertRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [marking, setMarking] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // Stage 3: delivery-transport status (macOS + Discord/Telegram/webhook).
  // Best-effort settings note — a fetch failure just hides the line.
  const [deliveries, setDeliveries] = useState<NotifyStatusResponse | null>(null);

  useEffect(() => {
    let alive = true;
    notifyStatus()
      .then((d) => {
        if (alive) setDeliveries(d);
      })
      .catch(() => {
        /* note is optional — never an error state */
      });
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .alerts(false, 100)
        .then((data) => {
          if (!alive) return;
          setRows(data);
          setError(null);
        })
        .catch((e) => {
          if (!alive) return;
          setError(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, 15000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [tick]);

  const unread = useMemo(() => (rows ?? []).filter((r) => !r.seen).length, [rows]);

  async function onMarkAll() {
    if (marking) return;
    setMarking(true);
    setActionError(null);
    try {
      await api.markAlertsSeen();
      // Nudge the header bell to refresh right away.
      window.dispatchEvent(new Event("sd-alerts-seen"));
      setTick((t) => t + 1);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setMarking(false);
    }
  }

  const signedOut = error !== null && /401/.test(error);

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">ALERTS</h1>
        <span className="chip tnum">{rows === null ? "…" : rows.length} shown</span>
        <span className="chip tnum">
          <span style={{ color: unread > 0 ? "var(--accent)" : "var(--dim)" }}>{unread}</span>{" "}
          unread
        </span>
        {error && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            refresh failed — retrying
          </span>
        )}
        <button
          type="button"
          onClick={onMarkAll}
          disabled={marking || unread === 0}
          className="chip ml-auto min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:text-[var(--text)] disabled:cursor-default"
          style={
            unread > 0
              ? { color: "var(--accent)", borderColor: "var(--accent)" }
              : { color: "var(--faint)" }
          }
        >
          {marking ? "marking…" : "mark all read"}
        </button>
      </div>

      <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Breakouts, regime changes, and calibrated predictions crossing conviction thresholds —
        for symbols on your watchlist only. Alerts are measurements of stored events, not advice.
      </p>

      {deliveries && (
        <div
          className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.72rem]"
          style={{ color: "var(--faint)" }}
        >
          <span>deliveries:</span>
          {deliveries.transports.map((t) => (
            <span
              key={t.name}
              className="chip px-2 py-[2px] text-[0.7rem]"
              style={
                t.configured
                  ? { color: "var(--accent)", borderColor: "var(--accent)" }
                  : undefined
              }
              title={
                t.configured
                  ? (t.note ?? (t.lastError ? `last error (redacted): ${t.lastError}` : "configured"))
                  : `off — set ${t.env} in daemon/.env to enable`
              }
            >
              {t.name} {t.configured ? "✓" : "—"}
            </span>
          ))}
          {deliveries.transports.some((t) => !t.configured) && (
            <span>
              — off transports: set the env shown on hover in daemon/.env (see .env.example).
              Email: {deliveries.email}.
            </span>
          )}
        </div>
      )}

      {actionError && (
        <div role="alert" className="text-[0.78rem]" style={{ color: "var(--bad)" }}>
          {actionError}
        </div>
      )}

      {rows === null && !error && <Skeleton lines={5} label="loading alerts" />}

      {rows === null && error && (
        <ErrorState
          message={signedOut ? "Sign in to see your alerts" : error}
          hint={
            signedOut
              ? "Alerts are scoped to your account and watchlist — log in and this page will load."
              : undefined
          }
          retry={() => {
            setError(null);
            setTick((t) => t + 1);
          }}
        />
      )}

      {rows !== null && rows.length === 0 && (
        <EmptyState
          message="No alerts yet"
          detail="The alert-runner sweeps every 5 minutes: new breakouts, regime changes, and high-conviction calibrated predictions on your watchlist will appear here."
        />
      )}

      {rows !== null && rows.length > 0 && (
        <div className="flex flex-col gap-2">
          {rows.map((a) => {
            const meta = kindMeta(a.kind);
            return (
              <div
                key={a.id}
                className="panel flex min-h-[48px] flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-[0.8rem]"
                style={a.seen ? undefined : { borderColor: meta.color }}
              >
                {!a.seen && (
                  <span
                    aria-label="unread"
                    className="inline-block h-2 w-2 shrink-0 rounded-full"
                    style={{ background: meta.color }}
                  />
                )}
                <span
                  className="chip shrink-0 px-2 py-[2px] text-[0.72rem] tracking-wider"
                  style={{ color: meta.color, borderColor: meta.color }}
                >
                  {meta.label}
                </span>
                {a.symbol && a.market ? (
                  <Link
                    href={`/s/${a.market}/${encodeURIComponent(a.symbol)}`}
                    className="flex min-h-[40px] cursor-pointer items-center font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
                  >
                    {a.symbol}
                  </Link>
                ) : null}
                {a.horizon && <span className="chip px-2 py-[2px] text-[0.72rem]">{a.horizon}</span>}
                <span className="min-w-0 flex-1" style={{ color: "var(--dim)" }}>
                  {a.detail}
                </span>
                <span className="tnum shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  {ago(a.ts)}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
