"use client";

// PUBLIC PROOF PAGE (/proof) — the one shareable URL that is SignalDeck's real
// differentiation: not a marketing claim of accuracy, but the verifiable record
// itself. Two things no competitor can retrofit:
//   1. the hash-chained prediction ledger — every prediction committed and
//      tamper-evident; we recompute the chain live and show INTACT / BROKEN.
//   2. the honest live out-of-sample track record — win rate / Brier skill / IC
//      over INDEPENDENT resolutions, or an explicit "still accruing, no skill
//      claimed" when the sample is too thin. It withholds rather than inflates.
//
// Renders for anonymous visitors (AuthGate + Shell allow /proof) and reads only
// the already-public GET endpoints (/api/track-record, /api/ledger/verify). On
// a remote deployment these are gated by SIGNALDECK_PUBLIC_READS / the API
// token exactly like every other read.

import Link from "next/link";
import { useEffect, useState } from "react";
import {
  trackRecord,
  ledgerVerify,
  HORIZONS,
  type Horizon,
  type TrackRecord,
  type LedgerVerifyResponse,
} from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

function pct(x: number | null | undefined, dec = 1): string {
  return x == null ? "—" : `${(x * 100).toFixed(dec)}%`;
}

function Stat({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: "ok" | "muted" }) {
  return (
    <div className="panel flex flex-col gap-1 px-4 py-4">
      <span className="text-[0.7rem] tracking-[0.12em]" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      <span
        className="tnum text-[1.5rem] font-extrabold"
        style={{ color: tone === "ok" ? "var(--ok)" : tone === "muted" ? "var(--dim)" : "var(--text)" }}
      >
        {value}
      </span>
      {sub ? (
        <span className="text-[0.72rem] leading-snug" style={{ color: "var(--dim)" }}>
          {sub}
        </span>
      ) : null}
    </div>
  );
}

export default function ProofPage() {
  // The horizon is the one thing a sceptic actually wants to vary here: a
  // record that only holds at one horizon is not much of a record. The API
  // already took this argument; the page just never let anyone change it.
  const [horizon, setHorizon] = useState<Horizon>("1d");

  // The track-record result is stored WITH the horizon it belongs to, and the
  // displayed value is derived from whether those match. That is what makes a
  // horizon change show the loading state without resetting state inside an
  // effect — and it also means a slow response for an abandoned horizon can
  // never paint under the wrong label.
  const [trState, setTrState] = useState<{
    horizon: Horizon;
    data: TrackRecord | null;
    err: string | null;
  } | null>(null);
  const [lv, setLv] = useState<LedgerVerifyResponse | null>(null);
  const [lvErr, setLvErr] = useState<string | null>(null);

  const settled = trState?.horizon === horizon ? trState : null;
  const tr = settled?.data ?? null;
  const trErr = settled?.err ?? null;

  // The ledger recompute is independent of the horizon, so it is fetched once
  // and never re-fetched when the selector moves.
  useEffect(() => {
    let alive = true;
    const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
    ledgerVerify().then((l) => alive && setLv(l)).catch((e) => alive && setLvErr(msg(e)));
    return () => {
      alive = false;
    };
  }, []);

  // Re-reads on every horizon change. Nothing is reset here — the derived
  // `settled` above already treats a result for a different horizon as
  // "still loading".
  useEffect(() => {
    let alive = true;
    const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
    trackRecord(horizon)
      .then((t) => alive && setTrState({ horizon, data: t, err: null }))
      .catch((e) => alive && setTrState({ horizon, data: null, err: msg(e) }));
    return () => {
      alive = false;
    };
  }, [horizon]);

  return (
    <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.4rem] font-extrabold tracking-tight">The receipts</h1>
        <p
          data-purpose="proof"
          className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          Most signal products claim a win rate you can&rsquo;t check. SignalDeck commits every
          prediction to a tamper-evident hash chain and grades itself against what actually
          happened — and withholds any skill claim until the sample is real. This page is that
          record, recomputed live. It is descriptive, not advice.
        </p>
      </header>

      {/* ledger loading / error (independent of the track record) */}
      {!lv && !lvErr && (
        <div className="panel p-4">
          <Skeleton lines={3} label="recomputing the ledger hash chain" />
        </div>
      )}
      {lvErr && !lv && (
        <ErrorState
          message={lvErr}
          hint="The SignalDeck daemon looks offline or this read is gated on this deployment."
        />
      )}

      {/* ── LEDGER INTEGRITY: the differentiator ── */}
      {lv && (
        <section
          className="panel"
          style={{ borderColor: lv.intact ? "color-mix(in srgb, var(--ok) 45%, var(--border))" : "var(--bad)" }}
          aria-label="ledger integrity"
        >
          <div className="panel-h">
            <span style={{ color: lv.intact ? "var(--ok)" : "var(--bad)" }}>PREDICTION LEDGER</span>
            <span className="chip px-2 py-[1px] text-[0.7rem]">hash-chained · tamper-evident</span>
          </div>
          <div className="flex flex-col gap-3 px-5 py-5 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-col gap-1">
              <span className="text-[1.15rem] font-bold" style={{ color: lv.intact ? "var(--ok)" : "var(--bad)" }}>
                {lv.intact ? "Chain intact" : `BROKEN at #${lv.brokenAtSeq}`}
              </span>
              <span className="text-[0.78rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                {lv.intact
                  ? "Every committed prediction links to the previous one by hash — recomputed just now, top to bottom, with no break. A prediction can't be edited or back-dated after the fact."
                  : "The recomputed chain disagrees with a stored hash — surfaced, never hidden."}
              </span>
            </div>
            <div className="flex shrink-0 gap-3">
              <div className="flex flex-col">
                <span className="tnum text-[1.5rem] font-extrabold">{lv.count.toLocaleString()}</span>
                <span className="text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  entries verified
                </span>
              </div>
            </div>
          </div>
          {lv.head ? (
            <div className="border-t px-5 py-2 text-[0.7rem]" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
              head <span className="mono">{lv.head.slice(0, 16)}…</span>
            </div>
          ) : null}
        </section>
      )}

      {/* The horizon picker lives ABOVE the three track-record states, not
          inside the loaded one — changing horizon clears `tr`, and a control
          that disappears the moment you use it is worse than no control. */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="text-[0.78rem] font-semibold tracking-wide" style={{ color: "var(--dim)" }}>
          Grade the record over
        </span>
        <div role="group" aria-label="Outcome horizon" className="flex items-center gap-1">
          {HORIZONS.map((h) => {
            const active = h === horizon;
            return (
              <button
                key={h}
                type="button"
                onClick={() => setHorizon(h)}
                aria-pressed={active}
                title={`Grade predictions against realized ${h} outcomes`}
                className="chip min-h-[40px] cursor-pointer px-3 text-[0.78rem] transition-colors duration-150 hover:text-[var(--text)]"
                style={active ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
              >
                {h}
              </button>
            );
          })}
        </div>
        <span className="text-[0.72rem]" style={{ color: "var(--faint)" }}>
          A record that only holds at one horizon is not much of a record.
        </span>
      </div>

      {/* track-record loading / error (independent of the ledger) */}
      {!tr && !trErr && (
        <div className="panel p-4">
          <Skeleton lines={3} label="grading the live track record" />
        </div>
      )}
      {trErr && !tr && (
        <ErrorState
          message={trErr}
          hint="The track record read timed out or is gated — the ledger above still verifies independently."
        />
      )}

      {/* ── LIVE TRACK RECORD (gated honest) ── */}
      {tr && (
        <section className="panel" aria-label="live track record">
          <div className="panel-h flex-wrap gap-2">
            <span style={{ color: "var(--accent)" }}>LIVE TRACK RECORD · {horizon}</span>
            <span className="chip px-2 py-[1px] text-[0.7rem]">{tr.trackLabel || "live — prob frozen at prediction time"}</span>
          </div>
          {tr.gated ? (
            // data-empty marks a "there is deliberately nothing here yet"
            // state for the UX audit — a withheld number is an empty state,
            // and this one is the most important on the site.
            <div data-empty="" className="flex flex-col gap-2 px-5 py-5">
              <span className="text-[1.05rem] font-bold" style={{ color: "var(--dim)" }}>
                Still accruing — no skill claimed yet.
              </span>
              <span className="max-w-[64ch] text-[0.78rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                {tr.note ??
                  `Only ${tr.independentN} independent resolutions so far (need ${tr.minIndependentN}). Win rate, Brier and IC are withheld until the record is statistically real — inflating a number off a handful of outcomes is exactly what this page refuses to do.`}
              </span>
              <span className="tnum text-[0.72rem]" style={{ color: "var(--faint)" }}>
                {tr.rawN.toLocaleString()} raw resolutions → {tr.independentN.toLocaleString()} independent (one per symbol-day)
              </span>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3 p-4 sm:grid-cols-4">
              <Stat
                label="WIN RATE"
                value={pct(tr.winRate)}
                sub={tr.winRateCI ? `95% CI ${pct(tr.winRateCI[0])}–${pct(tr.winRateCI[1])}` : undefined}
                tone="ok"
              />
              <Stat
                label="BRIER SKILL"
                value={tr.brierSkill != null ? `${tr.brierSkill >= 0 ? "+" : ""}${(tr.brierSkill * 100).toFixed(0)}%`
                  : "—"}
                sub="vs a base-rate constant"
              />
              <Stat
                label="IC"
                value={tr.ic != null ? tr.ic.toFixed(3) : "—"}
                sub={
                  tr.icCI
                    ? `how well ranking tracked outcome · CI ${tr.icCI[0].toFixed(2)}–${tr.icCI[1].toFixed(2)}`
                    : "how well ranking tracked outcome"
                }
              />
              <Stat
                label="INDEPENDENT N"
                value={tr.independentN.toLocaleString()}
                sub={`${tr.rawN.toLocaleString()} raw → deduped`}
                tone="muted"
              />
            </div>
          )}
          <div className="border-t px-5 py-3 text-[0.72rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
            Graded over INDEPENDENT (symbol, UTC-day) resolutions — the probability was frozen at
            prediction time, the outcome filled in later, no look-ahead. Descriptive, not a
            forecast. Not financial advice.
          </div>
        </section>
      )}

      {/* The registry verdicts — including the retired flagship's FAILED grade —
          must be one click from this headline page, not buried in the repo. */}
      <Link
        href="/accuracy"
        className="w-fit text-[0.78rem] font-semibold tracking-wide transition-colors duration-150"
        style={{ color: "var(--bad)" }}
      >
        accuracy registry — every verdict, failures first (flagship retired 2026-07-24) →
      </Link>

      {/* Verb-first, so it reads as the next thing to do rather than a link
          back. This is the one shareable page: most people arrive here first
          and need somewhere to go. */}
      <Link
        href="/"
        className="w-fit text-[0.78rem] font-semibold tracking-wide transition-colors duration-150"
        style={{ color: "var(--accent)" }}
      >
        Open the full workspace &rarr;
      </Link>
      <p className="m-0 text-[0.72rem]" style={{ color: "var(--faint)" }}>
        Unfamiliar with a term above?{" "}
        <Link href="/glossary" style={{ color: "var(--dim)", textDecoration: "underline" }}>
          Every one of them is in the glossary, in plain English.
        </Link>
      </p>
    </div>
  );
}
