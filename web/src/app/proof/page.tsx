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
import { trackRecord, ledgerVerify, type TrackRecord, type LedgerVerifyResponse } from "@/lib/api";
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
  const [tr, setTr] = useState<TrackRecord | null>(null);
  const [lv, setLv] = useState<LedgerVerifyResponse | null>(null);
  const [trErr, setTrErr] = useState<string | null>(null);
  const [lvErr, setLvErr] = useState<string | null>(null);

  // Fetch INDEPENDENTLY — the ledger recompute is fast, the track record can be
  // slow under load; the fast one must not wait on the slow one.
  useEffect(() => {
    let alive = true;
    const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
    ledgerVerify().then((l) => alive && setLv(l)).catch((e) => alive && setLvErr(msg(e)));
    trackRecord("1d").then((t) => alive && setTr(t)).catch((e) => alive && setTrErr(msg(e)));
    return () => {
      alive = false;
    };
  }, []);

  return (
    <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.4rem] font-extrabold tracking-tight">The receipts</h1>
        <p className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
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
          <div className="panel-h">
            <span style={{ color: "var(--accent)" }}>LIVE TRACK RECORD · 1d</span>
            <span className="chip px-2 py-[1px] text-[0.7rem]">{tr.trackLabel || "live — prob frozen at prediction time"}</span>
          </div>
          {tr.gated ? (
            <div className="flex flex-col gap-2 px-5 py-5">
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
                sub={tr.icCI ? `CI ${tr.icCI[0].toFixed(2)}–${tr.icCI[1].toFixed(2)}` : undefined}
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

      <Link
        href="/"
        className="w-fit text-[0.78rem] font-semibold tracking-wide transition-colors duration-150"
        style={{ color: "var(--accent)" }}
      >
        ← open the full workspace
      </Link>
    </div>
  );
}
