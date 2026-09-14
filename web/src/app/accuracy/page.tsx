// PUBLIC ACCURACY REGISTRY (/accuracy) — the registry's verdicts, verbatim, on
// a page a visitor can reach without an account. The design rule is the same
// one tools/accuracy_registry.py enforces in prose: FAILED is the primary
// visual state, not a footnote. A model the live record contradicts renders
// first, largest, and in red; PENDING backtest claims render as exactly that.
//
// This is a server component that reads data/accuracy_registry.json straight
// from disk (the ops/accuracy-registry.sh LaunchAgent regenerates it daily),
// so the page can never disagree with the file the daily grade wrote — there
// is no second copy of the verdict logic here to drift.

import fs from "node:fs/promises";
import path from "node:path";
import { cookies } from "next/headers";
import Link from "next/link";
import {
  AccuracyStatusBanner,
  type AccuracyStatus,
} from "@/components/accuracy/AccuracyStatusBanner";
import RefusalNotice from "@/components/RefusalNotice";

export const dynamic = "force-dynamic";

// Bare page name; the root layout appends the site suffix. Without it this page
// inherited the layout default and titled its tab "Dashboard".
export const metadata = { title: "Accuracy registry" };

type RegistryRow = {
  predictor: string;
  family: string;
  band: string;
  claimed: number | null;
  live_n: number;
  live_acc: number | null;
  ci: [number, number] | null;
  ci_method?: string;
  distinct_days?: number | null;
  effective_n?: number | null;
  null_hindsight: number | null;
  null_prequential: number | null;
  null_acc: number | null;
  skill: number | null;
  // OPTIONAL on purpose. The grader DROPS this field entirely for rows whose
  // writing binary cannot be resolved to a commit — "the verdict field is
  // dropped, not downgraded, an absent verdict cannot be quoted as one". Typing
  // it as a plain string made every consumer here assume it was present, and
  // the page died in Array.sort with "Cannot read properties of undefined
  // (reading 'startsWith')", taking the whole honesty surface down.
  verdict?: string | null;
  note?: string;
  // Merged by tools/selection_honesty.py after the grader runs: whether the
  // grader's own verdict is RESOLVED by the row's published intervals.
  honesty?: { resolvability?: { supported?: boolean | null; reason?: string | null } | null } | null;
};

// The row's verdict, or an explicit withheld marker when the grader dropped it.
// Never defaults to a real verdict value: an absent verdict must read as absent.
function verdictOf(r: { verdict?: string | null }): string {
  return r.verdict || "WITHHELD (provenance unresolvable)";
}

type CalibrationBin = {
  p_lo: number;
  p_hi: number;
  mean_predicted: number | null;
  realized_up_freq: number | null;
  n: number;
  distinct_days?: number;
};

type Calibration = {
  method: string;
  conviction_threshold: number;
  horizons: Record<string, CalibrationBin[]>;
};

type Registry = {
  generated: string;
  min_independent_n: number;
  survivorship_epoch: string;
  null_policy: string;
  calibration?: Calibration | null;
  rows: RegistryRow[];
};

// SUPERSEDED-SNAPSHOT — the flagship's PRE-EPOCH full-record grade. It is a
// permanent, dated fact, deliberately kept because the post-epoch registry rows
// restart the count and without this block the page would quietly forget the one
// verdict a visitor most needs to see. It is NOT the current record: the live
// rows further down are, and they come from the registry. Do not refresh these
// figures to match a later grade — that would erase the record being disclosed.
const FLAGSHIP_RETIREMENT = {
  date: "2026-07-24",
  rows: [
    { name: "directional-ensemble (1d)", acc: "48.1%", baseline: "54.6%", n: "13,058", skill: "−6.5pp" },
    { name: "directional-ensemble (1w)", acc: "46.2%", baseline: "54.4%", n: "9,164", skill: "−8.2pp" },
    { name: "directional-ensemble (1d, high conviction)", acc: "48.6%", baseline: "56.2%", n: "8,272", skill: "−7.6pp" },
  ],
};

async function loadRegistry(): Promise<Registry | null> {
  // npm run dev / next start run from web/, the LaunchAgent sets the same
  // WorkingDirectory; the repo-root fallback covers ad-hoc invocations.
  for (const p of [
    path.resolve(process.cwd(), "..", "data", "accuracy_registry.json"),
    path.resolve(process.cwd(), "data", "accuracy_registry.json"),
  ]) {
    try {
      return JSON.parse(await fs.readFile(p, "utf8")) as Registry;
    } catch {
      /* try the next location */
    }
  }
  return null;
}

function pct(x: number | null | undefined, dec = 1): string {
  return x == null ? "—" : `${(x * 100).toFixed(dec)}%`;
}

// null_policy: hindsight and prequential publish side by side; the stricter
// (higher) drives the verdict.
function drivingBaseline(r: RegistryRow): number | null {
  const vals = [r.null_hindsight, r.null_prequential].filter((v): v is number => v != null);
  if (vals.length) return Math.max(...vals);
  return r.null_acc;
}

function verdictTone(verdict: string): string {
  if (verdict.startsWith("FAILED") || verdict.startsWith("DECAYED")) return "var(--bad)";
  if (verdict.startsWith("VALIDATED") || verdict.startsWith("HOLDING")) return "var(--ok)";
  if (verdict.startsWith("NO SKILL") || verdict.startsWith("WIDE")) return "var(--warn)";
  return "var(--dim)"; // INSUFFICIENT / PENDING / UNGRADED
}

function Cell({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[0.65rem] tracking-[0.12em]" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      <span className="tnum text-[0.95rem] font-bold">{value}</span>
    </div>
  );
}

function statusTone(s: string): string {
  if (s === "FAILED" || s === "RETIRED") return "var(--bad)";
  if (s === "OK") return "var(--ok)";
  return "var(--warn)";
}

function DirectionalRow({ r, minN, pub }: { r: RegistryRow; minN: number; pub?: PublishedRow }) {
  // The daemon's publication verdict leads when it exists for this row; the
  // grader's own sentence is quoted beneath it, labelled as the grader's. The
  // sentence compares the accuracy interval to the null's POINT estimate
  // (tools/selection_honesty.py re-tests the paired difference and can mark it
  // unresolved), so it must never be the headline on its own.
  const failed = pub
    ? pub.publication_status === "FAILED" || pub.publication_status === "RETIRED"
    : verdictOf(r).startsWith("FAILED");
  const tone = pub ? statusTone(pub.publication_status) : verdictTone(verdictOf(r));
  const unresolved = r.honesty?.resolvability?.supported === false ? r.honesty?.resolvability?.reason : null;
  // Conviction slices below the evidence floor get NO percentage. The early
  // high-conviction record graded WORSE than the base row — anti-calibrated —
  // and a 6-observation "33.3%" reads as a measurement it is not. The floor is
  // the registry's own min_independent_n; the accuracy renders once n clears it.
  const convictionGated = r.band !== "all" && r.live_n < minN;
  return (
    <section
      className="panel"
      style={failed ? { borderColor: "var(--bad)" } : undefined}
      aria-label={`${r.predictor} verdict`}
    >
      <div className="panel-h">
        <span>{r.predictor}</span>
        <span className="chip px-2 py-[1px] text-[0.7rem]">band {r.band}</span>
      </div>
      <div className="flex flex-col gap-3 px-5 py-4">
        {/* publication verdict first (daemon), grader sentence second (labelled) */}
        <span
          className={failed ? "text-[1.35rem] font-extrabold" : "text-[1.05rem] font-bold"}
          style={{ color: tone }}
        >
          {pub
            ? `${pub.publication_status}${pub.retired ? " — retired; retirement does not lapse" : ""}`
            : verdictOf(r)}
        </span>
        {pub && pub.reasons && pub.reasons.length > 0 ? (
          <span className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {pub.reasons.join(" · ")}
          </span>
        ) : null}
        {pub ? (
          <span className="text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            Grader&apos;s sentence: {verdictOf(r)}
          </span>
        ) : null}
        {unresolved ? (
          <span className="text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
            Not resolved by this sample: {unresolved}
          </span>
        ) : null}
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
          <Cell
            label="LIVE ACC"
            value={convictionGated ? `insufficient, n=${r.live_n}/${minN}` : pct(r.live_acc)}
          />
          <Cell
            label="SKILL VS BASELINE"
            value={
              convictionGated || r.skill == null
                ? "—"
                : `${r.skill >= 0 ? "+" : ""}${(r.skill * 100).toFixed(1)}pp`
            }
          />
          <Cell label="BASELINE (STRICTER NULL)" value={pct(drivingBaseline(r))} />
          <Cell
            label="95% CI (DAY-CLUSTERED)"
            value={r.ci ? `${pct(r.ci[0])}–${pct(r.ci[1])}` : r.ci_method === "withheld" ? "withheld" : "—"}
          />
          <Cell
            label="EFFECTIVE N"
            value={r.effective_n != null ? r.effective_n.toLocaleString() : `${r.live_n.toLocaleString()} raw`}
          />
        </div>
        {convictionGated ? (
          <span className="text-[0.72rem] leading-relaxed" style={{ color: "var(--warn)" }}>
            Accuracy withheld: this conviction slice is below the {minN}-observation evidence
            floor, and its early record runs worse than the base row (anti-calibrated). See the
            reliability bins below for where the probabilities are wrong.
          </span>
        ) : null}
        {r.note ? (
          <span className="text-[0.72rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {r.note}
          </span>
        ) : null}
      </div>
    </section>
  );
}

/** Reliability diagram as a table: per bin of predicted P(up), what actually
 * happened. This is the surface where an anti-calibrated conviction tier is
 * visible per-bin — and correctable (threshold, isotonic recalibration) —
 * rather than buried inside a band average. */
function CalibrationPanel({ cal }: { cal: Calibration }) {
  const horizons = Object.entries(cal.horizons).filter(([, bins]) => bins.length > 0);
  if (!horizons.length) return null;
  const thr = cal.conviction_threshold;
  const isConviction = (b: CalibrationBin) => b.p_hi <= 0.5 - thr + 1e-9 || b.p_lo >= 0.5 + thr - 1e-9;
  return (
    <section className="panel" aria-label="calibration reliability bins">
      <div className="panel-h">
        <span style={{ color: "var(--dim)" }}>CALIBRATION — PREDICTED VS REALIZED</span>
        <span className="chip px-2 py-[1px] text-[0.7rem]">
          conviction gate |p−0.5| ≥ {thr}
        </span>
      </div>
      <div className="overflow-x-auto px-5 py-3">
        <table className="w-full text-left text-[0.78rem]">
          <thead>
            <tr style={{ color: "var(--faint)" }}>
              <th className="pr-4 font-medium">horizon</th>
              <th className="pr-4 font-medium">predicted P(up)</th>
              <th className="pr-4 font-medium">mean predicted</th>
              <th className="pr-4 font-medium">realized up-freq</th>
              <th className="pr-4 font-medium">n</th>
              <th className="font-medium">tier</th>
            </tr>
          </thead>
          <tbody className="tnum">
            {horizons.flatMap(([horizon, bins]) =>
              bins.map((b) => {
                const conv = isConviction(b);
                // Miscalibration flag: realized frequency on the wrong side of
                // 0.5 relative to the bin's predicted probability.
                const inverted =
                  b.mean_predicted != null &&
                  b.realized_up_freq != null &&
                  (b.mean_predicted - 0.5) * (b.realized_up_freq - 0.5) < 0;
                return (
                  <tr key={`${horizon}|${b.p_lo}`}>
                    <td className="pr-4 py-1">{horizon}</td>
                    <td className="pr-4">
                      {b.p_lo.toFixed(1)}–{b.p_hi.toFixed(1)}
                    </td>
                    <td className="pr-4">{pct(b.mean_predicted)}</td>
                    <td className="pr-4" style={inverted ? { color: "var(--bad)", fontWeight: 700 } : undefined}>
                      {pct(b.realized_up_freq)}
                      {inverted ? " (inverted)" : ""}
                    </td>
                    <td className="pr-4">{b.n.toLocaleString()}</td>
                    <td style={conv ? { color: "var(--warn)" } : { color: "var(--faint)" }}>
                      {conv ? "conviction" : "—"}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
      <div className="border-t px-5 py-2 text-[0.72rem]" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
        {cal.method}. A conviction-tier bin whose realized frequency sits on the wrong side of
        its predicted probability is the miscalibration to fix — raise the conviction threshold
        or recalibrate — before the auto-retire gate fires on the graded slice.
      </div>
    </section>
  );
}

// PUBLICATION GATE (audit F-1, 2026-08-03). Before this, the page rendered a
// REFUSED registry as a successful one: blank fields, no refusal message, and a
// visitor with no way to tell a grading outage from a quiet week. The daemon's
// /api/accuracy is the single authority on whether these numbers may be shown —
// it applies publication.BuildVerdict, reconciles the registry against
// evidence_claims and the retirement history, and refuses when the grader is
// stale. If it refuses, this page shows the refusal AND NOTHING ELSE.
//
// This runs server-side deliberately. A client-side gate would paint the stale
// table first and the refusal a moment later, which is the defect with extra
// steps.
async function loadPublicationStatus(): Promise<{
  status: AccuracyStatus;
  reason?: string;
  gradedAt?: string;
  refusedSince?: string;
  rows: PublishedRow[];
} | null> {
  const daemon = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";
  try {
    // FORWARD THE VIEWER'S SESSION COOKIE. This fetch is server-side, so it
    // carries no browser credential of its own — which was invisible while
    // SIGNALDECK_PUBLIC_READS defaulted open, and broke this page outright when
    // it closed: the daemon answered 401, body.status was undefined, and the
    // page rendered a permanent REFUSED for every visitor including a signed-in
    // one. A grading outage and a working grader became indistinguishable,
    // which is the exact F-1 defect this page exists to prevent.
    //
    // The viewer's own cookie is used rather than SIGNALDECK_API_TOKEN on
    // purpose. That token maps to the ADMIN user, so attaching it here would
    // serve the accuracy record to anonymous visitors and quietly defeat
    // PublicReads=false — the same inversion the /api/[...path] proxy documents
    // and deliberately avoids. Not signed in => 401 => the refusal path, which
    // is the honest answer.
    const cookie = (await cookies()).toString();
    const res = await fetch(`${daemon}/api/accuracy`, {
      cache: "no-store",
      headers: cookie ? { cookie } : undefined,
    });
    const body = await res.json();
    if (!res.ok || body?.status !== "OK") {
      // A 401 carries no `reason` field, so it used to fall through to the
      // "the grading daemon is unreachable" default below — naming the wrong
      // cause for a daemon that answered instantly. Observed live 2026-08-09:
      // anonymous visitor, daemon healthy on :8322, page blamed an outage.
      // "Not authorised" and "cannot be reached" are opposite problems and send
      // a reader to opposite places; conflating them is the same defect this
      // function's own comment describes, one status code over.
      if (res.status === 401 || res.status === 403) {
        // NOT a refusal. The grader may be perfectly healthy; this deployment
        // keeps the record behind a session. Rendering that as REFUSED told a
        // visitor the grader had withheld the figures — the opposite claim.
        return {
          status: "PRIVATE",
          reason: "the accuracy record is private on this deployment — sign in to read it",
          rows: [],
        };
      }
      return { status: (body?.status ?? "REFUSED") as AccuracyStatus,
               reason: body?.reason, gradedAt: body?.graded_at, refusedSince: body?.refused_since, rows: [] };
    }
    return { status: "OK", gradedAt: body.graded_at, rows: (body.rows ?? []) as PublishedRow[] };
  } catch {
    // Unreachable daemon is not "no news". It is an unknown, and an unknown
    // about whether these numbers are current resolves to not publishing them.
    return null;
  }
}

type PublishedRow = {
  predictor: string;
  horizon: string;
  variant: string;
  publication_status: AccuracyStatus;
  retired: boolean;
  retirement_sticky: boolean;
  reasons?: string[];
  evidence_refs?: string[];
};

export default async function AccuracyPage() {
  const pub = await loadPublicationStatus();

  // Fail closed. A refusal, or a daemon that cannot be reached, ends the page.
  if (!pub || pub.status !== "OK") {
    return (
      <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5">
        <header className="flex flex-col gap-2">
          <h1 className="text-[1.4rem] font-extrabold tracking-tight">Accuracy registry</h1>
        </header>
        <RefusalNotice
          status={pub?.status ?? "REFUSED_STALE"}
          title={pub?.status === "PRIVATE" ? "Sign-in required" : "Publication refused"}
          tone={pub?.status === "PRIVATE" ? "warn" : "bad"}
          reason={
            pub?.reason ??
            "the grading daemon is unreachable, so it cannot be confirmed that these numbers are current"
          }
          gradedAt={pub?.gradedAt}
          refusedSince={pub?.refusedSince}
          testId="accuracy-status-banner"
        >
          {pub?.status === "PRIVATE" ? (
            <Link href="/login" className="chip w-fit">
              Sign in
            </Link>
          ) : null}
        </RefusalNotice>
        <p className="m-0 max-w-[68ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {pub?.status === "PRIVATE"
            ? "Nothing statistical is being withheld: once signed in, the same daemon verdict renders here."
            : "No accuracy figures are shown while publication is refused. This is deliberate: a grading outage must be impossible to mistake for a quiet week. When the refusal names collapsed cross-sections, those are historical days inside a window anchored to the survivorship epoch — the window does not roll forward, so they cannot age out and further grading alone will not clear them."}
        </p>
        {pub?.status !== "PRIVATE" ? (
          <section className="panel px-5 py-4" aria-label="historical record">
            <div className="mono text-[0.7rem] uppercase tracking-[0.15em]" style={{ color: "var(--dim)" }}>
              Historical record — unaffected by today&apos;s refusal
            </div>
            <p className="m-0 mt-2 max-w-[68ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
              The flagship directional model was retired on {FLAGSHIP_RETIREMENT.date} by a
              pre-registered rule, and retirement does not lapse.
            </p>
            {/* A BARE REPO PATH IS NOT A RECEIPT. This used to print
                `proofs/P2_LIVE_RECORD_RECONCILIATION.md` in a <code> tag, which
                is only actionable for someone standing in the source tree. An
                anonymous judge follows it, finds nothing, and reasonably
                concludes the evidence does not exist.
                The document is NOT served here, and that is deliberate rather
                than an oversight: it contains the dated pre-epoch grade, and
                publishing it would reprint exactly the figures this page is
                currently withholding. So the link goes to what IS inspectable
                without the repository — the rule that forced the retirement,
                frozen on the chain before the outcome existed. */}
            <p className="m-0 mt-2 max-w-[68ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
              The rule that forced it was registered in advance and is readable on the{" "}
              <Link href="/proof" style={{ color: "var(--accent)" }}>
                receipts page
              </Link>
              , with its date and digest. The dated grade the rule was applied to is held as a
              repository document and is <strong>not published here</strong>: reprinting it would
              republish the same figures the current window is refusing. Withholding it is the
              same decision applied consistently, not a missing page.
            </p>
          </section>
        ) : null}
      </div>
    );
  }

  // Rows the daemon will not publish as healthy. Surfaced ABOVE the tables,
  // because the whole failure this fixes was a condemned model reading as
  // merely absent further down the page.
  const flagged = pub.rows.filter((r) => r.publication_status !== "OK");
  // The registry labels rows "predictor (horizon, variant)"; the daemon splits
  // them. Rebuild the label so each registry row finds its publication verdict.
  const labelOf = (x: PublishedRow) =>
    x.predictor + (x.horizon ? ` (${x.horizon}${x.variant ? `, ${x.variant}` : ""})` : "");
  const pubFor = (label: string) => pub.rows.find((x) => labelOf(x) === label);

  const reg = await loadRegistry();
  const rows = reg?.rows ?? [];
  const directional = rows
    .filter((r) => r.family === "direction")
    .sort((a, b) => Number(verdictOf(b).startsWith("FAILED")) - Number(verdictOf(a).startsWith("FAILED")));
  const structural = rows.filter((r) => r.family === "structure");
  const pendingCount = structural.filter((r) => verdictOf(r).startsWith("PENDING")).length;

  return (
    <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.4rem] font-extrabold tracking-tight">Accuracy registry</h1>
        <p className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          Every predictor, its claim, and what the live record actually supports — regraded daily
          against the naive baseline on independent (symbol, horizon, trading day) observations. The
          failures lead. Descriptive, not advice.
        </p>
      </header>

      {/* Publication status per row, from the daemon's single verdict path.
          A RETIRED row appears here even when its current window is too thin to
          publish an interval — retirement does not lapse when evidence thins. */}
      {flagged.length > 0 ? (
        <section className="flex flex-col gap-2" aria-label="publication status">
          {flagged.map((r) => (
            <AccuracyStatusBanner
              key={`${r.predictor}-${r.horizon}-${r.variant}`}
              status={r.publication_status}
              reasons={[
                `${r.predictor}${r.horizon ? ` (${r.horizon}${r.variant ? `, ${r.variant}` : ""})` : ""}`,
                ...(r.reasons ?? []),
              ]}
              evidenceRefs={r.evidence_refs}
            />
          ))}
        </section>
      ) : null}

      {/* ── FLAGSHIP RETIREMENT: the disclosure that must not be buried ── */}
      <section className="panel" style={{ borderColor: "var(--bad)" }} aria-label="flagship retirement">
        <div className="panel-h">
          <span style={{ color: "var(--bad)" }}>HISTORICAL RECORD · FLAGSHIP RETIRED {FLAGSHIP_RETIREMENT.date}</span>
          <span className="chip px-2 py-[1px] text-[0.7rem]">dated pre-epoch grade, not the current window</span>
        </div>
        <div className="flex flex-col gap-3 px-5 py-4">
          <span className="text-[1.35rem] font-extrabold" style={{ color: "var(--bad)" }}>
            FAILED — significantly worse than the naive baseline
          </span>
          <p className="m-0 max-w-[68ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {/* SUPERSEDED-SNAPSHOT: prose describing the dated pre-epoch grade above. */}
            On its full live record every directional row graded FAILED — the entire day-clustered
            confidence interval below the majority-class baseline — so the model was automatically
            retired and stopped emitting. Inverting or relabeling it is not a rescue: the competing
            model is the constant majority guess, whose rate is above 50%, so flipping the sign
            relabels the call without creating an edge. The directional rows below are its
            post-retirement shadow record, restarted at the survivorship epoch.
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[0.78rem]">
              <thead>
                <tr style={{ color: "var(--faint)" }}>
                  <th className="pr-4 font-medium">predictor</th>
                  <th className="pr-4 font-medium">live acc</th>
                  <th className="pr-4 font-medium">baseline</th>
                  <th className="pr-4 font-medium">independent n</th>
                  <th className="font-medium">skill</th>
                </tr>
              </thead>
              <tbody className="tnum">
                {FLAGSHIP_RETIREMENT.rows.map((f) => (
                  <tr key={f.name}>
                    <td className="pr-4 py-1">{f.name}</td>
                    <td className="pr-4">{f.acc}</td>
                    <td className="pr-4">{f.baseline}</td>
                    <td className="pr-4">{f.n}</td>
                    <td style={{ color: "var(--bad)" }} className="font-bold">
                      {f.skill}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </section>

      {/* data-empty: nothing is here, and it is ours to fix, not the reader's
          — so this states the cause and offers no button that cannot help. */}
      {!reg && (
        <section data-empty="" className="panel px-5 py-4" aria-label="registry unavailable">
          <span className="text-[0.85rem]" style={{ color: "var(--warn)" }}>
            No verdicts to show right now &mdash; the registry file is not readable on this
            deployment, so the live rows cannot render. Nothing has been hidden: the retirement
            disclosure above still stands, and the rows return as soon as the daily grade writes
            the file again.
          </span>
        </section>
      )}

      {/* ── LIVE DIRECTIONAL ROWS, FAILED FIRST ── */}
      {directional.map((r) => (
        <DirectionalRow key={`${r.predictor}|${r.band}`} r={r} minN={reg?.min_independent_n ?? 30} pub={pubFor(r.predictor)} />
      ))}

      {/* ── RELIABILITY BINS: where the probabilities are actually wrong ── */}
      {reg?.calibration ? <CalibrationPanel cal={reg.calibration} /> : null}

      {/* ── STRUCTURAL CLAIMS: PENDING means backtest, not evidence ── */}
      {structural.length > 0 && (
        <section className="panel" aria-label="structural claims">
          <div className="panel-h">
            <span style={{ color: "var(--dim)" }}>STRUCTURAL CLAIMS</span>
            <span className="chip px-2 py-[1px] text-[0.7rem]">
              {pendingCount}/{structural.length} pending — backtested, not live
            </span>
          </div>
          <div className="overflow-x-auto px-5 py-3">
            <table className="w-full text-left text-[0.78rem]">
              <thead>
                <tr style={{ color: "var(--faint)" }}>
                  <th className="pr-4 font-medium">predictor</th>
                  <th className="pr-4 font-medium">backtest claim</th>
                  <th className="font-medium">status</th>
                </tr>
              </thead>
              <tbody>
                {structural.map((r) => (
                  <tr key={r.predictor}>
                    <td className="tnum pr-4 py-1">{r.predictor}</td>
                    <td className="tnum pr-4">{pct(r.claimed)}</td>
                    <td style={{ color: verdictTone(verdictOf(r)) }}>{verdictOf(r)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="border-t px-5 py-2 text-[0.72rem]" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
            A PENDING claim is a backtested number, not a live record — it becomes evidence on the
            first-grade date in its status, never before.
          </div>
        </section>
      )}

      {reg && (
        <p className="m-0 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Regenerated {reg.generated} · minimum {reg.min_independent_n} independent observations
          for any verdict · survivorship epoch {reg.survivorship_epoch} (earlier rows were graded
          against a survivor-seeded universe and are excluded) · intervals resample days, not rows.
        </p>
      )}

      {/* Verb-first so it reads as the next move, not a way back. This page is
          a list of verdicts; the useful thing to do after reading it is to go
          look at the record those verdicts were graded against. */}
      <Link
        href="/proof"
        className="w-fit text-[0.78rem] font-semibold tracking-wide transition-colors duration-150"
        style={{ color: "var(--accent)" }}
      >
        See the receipts &mdash; the ledger and the live track record &rarr;
      </Link>

      <p className="m-0 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        <strong style={{ color: "var(--dim)" }}>In plain English:</strong> a claim here is only
        called good once enough real outcomes have been graded. Backtest numbers are marked
        PENDING until then, and anything the live record contradicts is shown first, in red.{" "}
        <Link href="/glossary" style={{ color: "var(--dim)", textDecoration: "underline" }}>
          Every term on this page is in the glossary.
        </Link>
      </p>
    </div>
  );
}
