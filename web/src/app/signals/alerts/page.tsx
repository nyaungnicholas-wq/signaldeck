"use client";

// ALERTS — the session user's actionable event feed, rebuilt as
// "Unusual-Whales-with-receipts":
//   · every kind chip is a NAMED rule (rules.ts) with a collapsible
//     METHODOLOGY panel citing the exact daemon thresholds;
//   · the OUTCOMES panel shows MEASURED 1d/5d forward returns after past
//     alerts by kind — gated cells say "withheld", never a tiny-sample stat;
//   · unread-first toggle uses the API's ?unseen=1 (mark-all-read + the
//     header-bell "sd-alerts-seen" event kept intact);
//   · client-side kind filter chips (unknown kinds render generically);
//   · delivery transport chips now state lastOk ("delivered 12m ago").
// Polls /api/alerts on the POLL_FAST tier (15s). Alerts are measurements of
// stored events, not advice.

import { useEffect, useMemo, useState } from "react";
import { api, notifyStatus, pollMs, POLL_FAST, type AlertRow, type NotifyStatusResponse } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import AlertItem from "@/components/signals/alerts/AlertItem";
import DeliveryStatus from "@/components/signals/alerts/DeliveryStatus";
import MethodologyPanel from "@/components/signals/alerts/MethodologyPanel";
import OutcomesPanel from "@/components/signals/alerts/OutcomesPanel";
import { ruleFor, sortKinds } from "@/components/signals/alerts/rules";

export default function AlertsPage() {
  const [rows, setRows] = useState<AlertRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [marking, setMarking] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // Unread-first: flips the fetch to /api/alerts?unseen=1 (server-side filter,
  // not a client-side hide — what you see is exactly what the API returned).
  const [unseenOnly, setUnseenOnly] = useState(false);
  // Client-side kind filter (null = all kinds).
  const [kindFilter, setKindFilter] = useState<string | null>(null);
  // Delivery-transport status — best-effort; a fetch failure just hides the row.
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
        .alerts(unseenOnly, 100)
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
    // POLL_FAST tier — an actively-watched feed (managed loop: hidden-tab
    // pause, failure backoff).
    const stop = pollMs(load, POLL_FAST);
    return () => {
      alive = false;
      stop();
    };
  }, [tick, unseenOnly]);

  const unread = useMemo(() => (rows ?? []).filter((r) => !r.seen).length, [rows]);

  // Kinds actually present in the feed (published order first, unknown kinds
  // after) — drives both the filter chips and the methodology panel.
  const kinds = useMemo(() => {
    const present = new Set((rows ?? []).map((r) => r.kind));
    if (kindFilter) present.add(kindFilter); // keep the active chip alive across refetches
    return sortKinds([...present]);
  }, [rows, kindFilter]);

  const filtered = useMemo(
    () => (kindFilter ? (rows ?? []).filter((r) => r.kind === kindFilter) : (rows ?? [])),
    [rows, kindFilter],
  );

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
        <h1 className="text-sm font-bold tracking-[0.18em]">ALERTS</h1>
        <span className="chip tnum">
          {rows === null
            ? "…"
            : kindFilter
              ? `${filtered.length}/${rows.length} shown`
              : `${rows.length} shown`}
        </span>
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
          aria-pressed={unseenOnly}
          onClick={() => setUnseenOnly((u) => !u)}
          className="chip ml-auto min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
          style={
            unseenOnly
              ? { color: "var(--accent)", borderColor: "var(--accent)" }
              : { color: "var(--dim)" }
          }
          title="server-side filter — fetches /api/alerts?unseen=1"
        >
          unread only {unseenOnly ? "✓" : ""}
        </button>
        <button
          type="button"
          onClick={onMarkAll}
          disabled={marking || unread === 0}
          className="chip min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:bg-[var(--panel3)] hover:text-[var(--text)] disabled:cursor-default"
          style={
            unread > 0
              ? { color: "var(--accent)", borderColor: "var(--accent)" }
              : { color: "var(--faint)" }
          }
        >
          {marking ? "marking…" : "mark all read"}
        </button>
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="signals-alerts"
        text="What just happened to the symbols you watch — and what actually happened AFTER alerts like these fired before? Every alert cites its published rule; the outcomes table shows measured forward returns, gated below minimum sample size."
      />

      <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Breakouts, regime changes, anomalies, and calibrated predictions crossing conviction
        thresholds — for symbols on your watchlist only. Alerts are measurements of stored
        events written as they were detected, not advice.
      </p>

      {deliveries && <DeliveryStatus status={deliveries} />}

      {actionError && (
        <div role="alert" className="text-[0.75rem]" style={{ color: "var(--bad)" }}>
          {actionError}
        </div>
      )}

      {/* the published rulebook for the kinds actually in this feed */}
      {kinds.length > 0 && <MethodologyPanel kinds={kinds} />}

      {/* the receipts: measured forward returns after past alerts, by kind */}
      <OutcomesPanel days={90} />

      {/* client-side kind filter — unknown kinds get a generic chip, never hidden */}
      {rows !== null && kinds.length > 1 && (
        <div
          className="flex flex-wrap items-center gap-1"
          role="tablist"
          aria-label="alert kind filter"
        >
          <button
            type="button"
            role="tab"
            aria-selected={kindFilter === null}
            onClick={() => setKindFilter(null)}
            className="chip min-h-[36px] cursor-pointer px-3 text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)]"
            style={{
              color: kindFilter === null ? "var(--accent)" : "var(--dim)",
              borderColor: kindFilter === null ? "var(--accent)" : "var(--border)",
            }}
          >
            all
          </button>
          {kinds.map((k) => {
            const r = ruleFor(k);
            const active = kindFilter === k;
            const count = (rows ?? []).filter((a) => a.kind === k).length;
            return (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={active}
                title={r.rule}
                onClick={() => setKindFilter(active ? null : k)}
                className="chip min-h-[36px] cursor-pointer px-3 text-[0.75rem] tracking-wider transition-colors duration-150 hover:bg-[var(--panel3)]"
                style={{
                  color: active ? r.color : "var(--dim)",
                  borderColor: active ? r.color : "var(--border)",
                }}
              >
                {r.label} <span className="tnum">{count}</span>
              </button>
            );
          })}
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
          message={unseenOnly ? "No unread alerts" : "No alerts yet"}
          detail={
            unseenOnly
              ? "You're caught up — switch off “unread only” to see the full feed."
              : "The alert-runner sweeps every 5 minutes: new breakouts, regime changes, anomalies, and high-conviction calibrated predictions on your watchlist will appear here."
          }
        />
      )}

      {rows !== null && rows.length > 0 && filtered.length === 0 && (
        <EmptyState
          message={`No ${ruleFor(kindFilter ?? "").label.toLowerCase()} alerts in this feed`}
          detail="The kind filter is client-side — clear it to see everything the API returned."
        />
      )}

      {filtered.length > 0 && (
        <div className="flex flex-col gap-2">
          {filtered.map((a) => (
            <AlertItem key={a.id} a={a} />
          ))}
        </div>
      )}
    </div>
  );
}
