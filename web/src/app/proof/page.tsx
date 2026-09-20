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
  ApiError,
  HORIZONS,
  type Horizon,
  type TrackRecord,
  type LedgerVerifyResponse,
} from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import RefusalNotice from "@/components/RefusalNotice";
import Registrations from "@/components/proof/Registrations";

// Two retries, not more. Each failed attempt costs the daemon's FULL 30s
// deadline before it answers, so three attempts is already ~70s of waiting —
// past that a visitor is better served by the error state, which names what
// happened, than by a spinner that keeps promising.
// Whether this build is serving the public internet. Controls whether
// operator-only remediation copy is shown; see the hint below.
const PUBLIC_MODE = process.env.NEXT_PUBLIC_SIGNALDECK_PUBLIC === "1";

const LEDGER_VERIFY_RETRIES = 2;
// Longer than a page normally waits between retries, on purpose: the cause is
// database contention during the daemon's boot storm, and retrying instantly
// just adds a third competitor to the thing that is already too busy.
const LEDGER_RETRY_DELAY_MS = 5000;

function pct(x: number | null | undefined, dec = 1): string {
  return x == null ? "—" : `${(x * 100).toFixed(dec)}%`;
}

function Stat({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: "ok" | "warn" | "muted" }) {
  return (
    <div className="panel flex flex-col gap-1 px-4 py-4">
      <span className="text-[0.7rem] tracking-[0.12em]" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      <span
        className="tnum text-[1.5rem] font-extrabold"
        style={{ color: tone === "ok" ? "var(--ok)" : tone === "warn" ? "var(--warn)" : tone === "muted" ? "var(--dim)" : "var(--text)" }}
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

/** What the verification actually covered, in the response's own numbers.
 *
 * Three DIFFERENT guarantees get collapsed into "tamper-proof" if you let them,
 * so they are separated here and each is shown with its extent:
 *
 *   edit-detection   any modified, deleted, reordered or inserted row breaks
 *                    the recomputation at that seq. Always on.
 *   anteriority      an Ed25519 anchor signed earlier still reproduces, so the
 *                    history up to THAT seq could not have been rebuilt since.
 *                    It stops at provenAnteriorThroughSeq; everything appended
 *                    after the newest anchor has none.
 *   external proof   the anchor digest published outside this machine. The
 *                    operator holds the signing key, so nothing on this page
 *                    constrains him without it.
 *
 * Numbers come from the response. Nothing here is written as prose that could
 * drift from what the daemon computed.
 */
function LedgerProvenance({ lv }: { lv: LedgerVerifyResponse }) {
  const te = lv.tamperEvidence;
  if (!te) return null;

  const provenSeq = te.provenAnteriorThroughSeq ?? null;
  const beyond = provenSeq === null ? lv.count : Math.max(0, lv.count - provenSeq);
  const failing = te.failingAnchors ?? 0;
  const asOf = te.provenAnteriorAsOf
    ? new Date(te.provenAnteriorAsOf * 1000).toISOString().replace("T", " ").slice(0, 16) + "Z"
    : null;

  const rows: Array<[string, string, string?]> = [
    [
      "Verification mode",
      lv.incremental === false
        ? "full walk from genesis"
        : "incremental — only the rows after the last checkpoint were re-hashed",
      lv.incremental === false ? undefined : "?full=1 forces the complete walk",
    ],
    [
      "Anchor check",
      te.anchorCheckMode === "stored"
        ? `${te.anchorCount ?? 0} anchor(s), compared against stored head hashes`
        : `${te.anchorCount ?? 0} anchor(s), re-derived from payloads`,
      te.anchorCheckMode === "stored" ? "?full=1 re-derives — the auditor's check" : undefined,
    ],
    [
      // NOT "anteriority proven through". The signature is this machine's, over
      // a chain this machine holds, checked by this machine; it constrains
      // anyone WITHOUT the key and nobody who has it (audit F09).
      "Local anchor reproduces through",
      provenSeq === null
        ? "nothing — no anchor currently reproduces"
        : `entry #${provenSeq.toLocaleString()}${asOf ? `, signed ${asOf}` : ""}`,
      provenSeq === null ? undefined : "anteriority against an adversary WITHOUT the signing key",
    ],
    [
      "Carries no anteriority proof",
      `${beyond.toLocaleString()} entr${beyond === 1 ? "y" : "ies"} appended after the newest reproducing anchor`,
    ],
    [
      "Anteriority against the operator",
      te.externalWitness?.verified
        ? "established — an anchor digest was matched against an external receipt"
        : "NOT established — no external receipt is verified here. The operator holds the signing key, so he can re-sign a fabricated chain and every check above passes.",
      "compare a digest from /api/ledger/anchors against the third-party copy yourself",
    ],
    [
      "Anchors that stopped reproducing",
      failing > 0
        ? `${failing} — positive evidence history was rewritten after signing (first at #${te.firstFailingSeq ?? "?"})`
        : "none",
    ],
  ];

  return (
    <div className="border-t px-5 py-4" style={{ borderColor: "var(--border)" }}>
      <div className="mb-2 text-[0.7rem] uppercase tracking-[0.15em]" style={{ color: "var(--faint)" }}>
        What this check covered
      </div>
      <dl className="m-0 grid gap-x-4 gap-y-2 sm:grid-cols-[minmax(0,15rem)_1fr]">
        {rows.map(([k, v, hint]) => (
          <div key={k} className="contents">
            <dt className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              {k}
            </dt>
            <dd className="m-0 text-[0.75rem]" style={{ color: "var(--fg)" }}>
              {v}
              {hint ? (
                <span className="mono ml-2 text-[0.68rem]" style={{ color: "var(--faint)" }}>
                  {hint}
                </span>
              ) : null}
            </dd>
          </div>
        ))}
      </dl>
      {te.claim ? (
        <p
          className="m-0 mt-3 max-w-[80ch] text-[0.72rem] leading-relaxed"
          style={{ color: failing > 0 ? "var(--bad)" : "var(--faint)" }}
        >
          {te.claim}
        </p>
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
  // Which retry we are on, purely so the skeleton can say so. A page that sits
  // on an unchanging "recomputing…" for a minute is indistinguishable from one
  // that has hung, and this read genuinely can take that long after a restart.
  const [lvRetry, setLvRetry] = useState(0);
  // The STATUS, not just the message. ApiError carries it precisely so a caller
  // can tell "refused" from "unreachable" — its own doc cites a 451 rendered as
  // "is the daemon running?" about a daemon that had just answered. This page
  // still made that mistake one code over: a 401 was reported as "the daemon
  // looks offline" while /api/health returned 200. null means no ApiError at
  // all, i.e. the request never got an answer.
  const [lvStatus, setLvStatus] = useState<number | null>(null);

  const settled = trState?.horizon === horizon ? trState : null;
  const tr = settled?.data ?? null;
  const trErr = settled?.err ?? null;

  // The ledger recompute is independent of the horizon, so it is fetched once
  // and never re-fetched when the selector moves.
  //
  // It IS retried, but only on the two statuses that are transient by
  // construction, and only a bounded number of times:
  //
  //   503 — the verify exceeded the daemon's 30s deadline;
  //   429 — ledgerVerifyConcurrency (2) was already saturated.
  //
  // Both cluster in the minutes after a daemon restart, when the whole worker
  // fleet boots at once and contends for the database. That restart happens
  // DAILY at market close, so without this the one page built to be shared
  // served "verification exceeded 30s" to every visitor in that window.
  // Measured across one such restart: 503, 503, 503, then 19.4s, then 3.1s.
  //
  // Deliberately NOT retried on anything else. A 401/403 is a deployment
  // posture and a 500 is a bug; retrying either just spends the visitor's time
  // to show the same message, and the existing hint already explains them.
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
    const attempt = (n: number) => {
      ledgerVerify()
        .then((l) => {
          if (alive) setLv(l);
        })
        .catch((e) => {
          if (!alive) return;
          const status = e instanceof ApiError ? e.status : null;
          if ((status === 503 || status === 429) && n < LEDGER_VERIFY_RETRIES) {
            setLvRetry(n + 1);
            timer = setTimeout(() => attempt(n + 1), LEDGER_RETRY_DELAY_MS);
            return;
          }
          setLvErr(msg(e));
          setLvStatus(status);
        });
    };
    attempt(0);
    return () => {
      alive = false;
      // Without this a pending retry fires after unmount and setState warns —
      // the `alive` flag alone stops the write, not the timer.
      if (timer) clearTimeout(timer);
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
        {/* "commits EVERY prediction" was a guarantee this page cannot make.
            The prediction write is four separate transactions -- UpsertPrediction,
            SeedBenchmarkOutcome, then AppendLedger, each opening its own BeginTx --
            and the ledger append is deliberately best-effort, because a ledger
            failure must not cost a user their forecast. A process killed between
            the first and the third therefore leaves a SERVED prediction with no
            ledger entry.

            Measured 2026-09-13: 30 of 496,987 served predictions since the ledger
            began (0.0060%) have no entry, isolated singletons across five months,
            and it is still accruing. Small, and not nothing -- and "every" is
            exactly the kind of unqualified claim this page exists to argue
            against making.

            So the copy now describes the PROCESS, which is true, and says the gap
            is counted rather than hidden, which is also true: ops/ledger coverage
            reports it on every run (fd440e5). No number is written into the copy
            on purpose -- a hand-typed count here is the defect that put five wrong
            figures on this site already (8f959f7), and this one is still moving. */}
        <p
          data-purpose="proof"
          className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          Most signal products claim a win rate you can&rsquo;t check. SignalDeck commits each
          prediction to a tamper-evident hash chain as it is made, counts the rare write that
          does not land instead of rounding it away, and grades itself against what actually
          happened — withholding any skill claim until the sample is real. This page is that
          record, recomputed live. It is descriptive, not advice.
        </p>
      </header>

      {/* ledger loading / error (independent of the track record) */}
      {!lv && !lvErr && (
        <div className="panel p-4">
          <Skeleton
            lines={3}
            label={
              lvRetry === 0
                ? "recomputing the ledger hash chain"
                : `ledger verification timed out — retrying (${lvRetry}/${LEDGER_VERIFY_RETRIES}); the daemon is busy, which is usual for a few minutes after a restart`
            }
          />
        </div>
      )}
      {lvErr && !lv && (
        <ErrorState
          message={lvErr}
          // /proof is a PUBLIC page: most people who see this error have no
          // account and no server to restart. The operator instructions that
          // used to live here ("start signaldeckd (:8322)", "open public reads
          // with SIGNALDECK_PUBLIC_READS") are useful to exactly one person and
          // read as a leak to everyone else, so they are shown only when this
          // build is NOT running in public mode. The diagnosis itself stays
          // either way -- a visitor is still told whether the service failed to
          // answer or answered and declined.
          hint={
            lvStatus === null
              ? PUBLIC_MODE
                ? "The service did not answer. That is on our side, and this page recovers on its own once it is back."
                : "The daemon did not answer at all — start signaldeckd (:8322) and this page recovers on its own."
              : lvStatus === 401 || lvStatus === 403
                ? PUBLIC_MODE
                  ? `The service answered and declined this read (${lvStatus}). The ledger is not published on this deployment.`
                  : `The daemon ANSWERED and refused this read (${lvStatus}). It is gated on this deployment, so restarting it changes nothing — sign in, or open public reads with SIGNALDECK_PUBLIC_READS.`
                : `The service answered ${lvStatus} for this read, so it is running; the refusal is what needs explaining.`
          }
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
              {/* THE HEADLINE IS NOT THE CLAIM. This used to read "recomputed
                  just now, top to bottom ... A prediction can't be edited or
                  back-dated after the fact" — and the response that produced it
                  says `incremental: true` and, verbatim, that intact means "the
                  stored rows are internally consistent — NOT that they were
                  written when they claim". Two sentences, one page, opposite
                  claims. Both halves were wrong: the fast path re-hashes only
                  the suffix since the last checkpoint, and edit-detection is not
                  anteriority. `intactMeans` is the daemon's own scoping, so it
                  is shown rather than paraphrased. */}
              <span className="text-[0.78rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                {lv.intact
                  ? (lv.intactMeans ??
                     "Every committed prediction links to the previous one by hash, with no break. That establishes the stored rows are internally consistent — not that they were written when they claim.")
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

          {/* WHAT WAS ACTUALLY CHECKED, AND HOW FAR. A reader cannot evaluate
              "intact" without the extent, and every number here comes straight
              out of the response rather than from prose. */}
          <LedgerProvenance lv={lv} />

          {lv.head ? (
            <div className="border-t px-5 py-2 text-[0.7rem]" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
              head <span className="mono">{lv.head.slice(0, 16)}…</span>
            </div>
          ) : null}
        </section>
      )}

      {/* THE REGISTRATION ITSELF, not a promise of one. /volatility sends
          readers here to "read that registration" and until now there was
          nothing on this page to read. */}
      <Registrations />

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
          hint={
            // Only claim the ledger verified if it actually did. Unconditionally
            // telling a visitor "the ledger above still verifies independently"
            // while that panel is ALSO showing an error is the page vouching for
            // evidence it never obtained — on the one page whose whole purpose is
            // that the evidence can be checked.
            lv
              ? "The track record read failed — the ledger above still verifies independently."
              : "The track record read failed AND the ledger above did not verify either, so this page is showing no record at all right now."
          }
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
              <RefusalNotice compact tone="warn" title="Why no skill is claimed yet" testId="proof-withheld" reason={tr.note ?? `Only ${tr.independentN} deduplicated symbol-day resolutions so far (need ${tr.minIndependentN}) over ${tr.distinctDays} distinct days (need ${tr.minDistinctDays}). Win rate, Brier and IC are withheld until the record is statistically real.`} />
              <span className="tnum text-[0.72rem]" style={{ color: "var(--faint)" }}>
                {tr.rawN.toLocaleString()} raw resolutions → {tr.independentN.toLocaleString()} deduplicated symbol-day observations over {tr.distinctDays} distinct trading days (observations on one day share a market move, so they are not independent)
              </span>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3 p-4 sm:grid-cols-4">
              {/* The tone is DERIVED, never hardcoded. This read "tone=ok" with no
                  baseline rendered anywhere on the page, so a directional accuracy of
                  45.6% against a 54.0% always-up baseline — an edge of -8.4pp — was
                  published in success green on the one page meant to be shared. The
                  daemon ships naiveBaseline, edgeVsNaive and an accuracyNote saying in
                  words that a non-positive edge means no skill (trackrecord.go:312-314);
                  this page referenced none of the three. components/home/ProofStrip.tsx
                  already does this correctly — the logic below is its logic. */}
              <Stat
                label="WIN RATE"
                value={pct(tr.winRate)}
                sub={
                  tr.naiveBaseline != null
                    ? `vs ${pct(tr.naiveBaseline)} always-up`
                      + (tr.edgeVsNaive != null
                          ? ` · ${tr.edgeVsNaive >= 0 ? "+" : ""}${(tr.edgeVsNaive * 100).toFixed(1)}pp`
                            + (tr.edgeVsNaive > 0 ? "" : " — does not beat the naive guess")
                          : "")
                    : tr.winRateCI ? `95% CI ${pct(tr.winRateCI[0])}–${pct(tr.winRateCI[1])}` : undefined
                }
                tone={tr.edgeVsNaive != null && tr.edgeVsNaive > 0 ? "ok" : "warn"}
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
                label="SYMBOL-DAY OBS."
                value={tr.independentN.toLocaleString()}
                sub={`${tr.rawN.toLocaleString()} raw → deduplicated · ${tr.distinctDays} days`}
                tone="muted"
              />
            </div>
          )}
          <div className="border-t px-5 py-3 text-[0.72rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
            Graded over deduplicated (symbol, trading day) resolutions, with intervals corrected for same-day dependence by a measured design effect (day is the unit of resampling) — the probability was frozen at
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
        href="/dashboard"
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
