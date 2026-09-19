"use client";

// THE REGISTRATION THE OTHER PAGES POINT AT.
//
// /volatility told readers "You can read that registration on the receipts
// page", /accuracy printed a bare `proofs/P2_LIVE_RECORD_RECONCILIATION.md`
// path, and /proof rendered no registration at all. GET /api/prereg has carried
// all 116 records the whole time — kind, the frozen spec text, its digest, the
// chain links, and the daemon's own plain-language explanation — and no page in
// the app had ever called it. A link that promises evidence and lands on a page
// without it is worse than no link: a judge who follows it concludes the
// evidence does not exist.
//
// This renders it. No numbers, no verdicts, no scores: what was claimed, when,
// under which digest, and whether it was frozen before anything it predicts
// could be graded. That last flag is the one that matters and it comes from the
// daemon, not from prose here.

import { useEffect, useState } from "react";
import { prereg, ApiError, type PreregResponse, type PreregRecord } from "@/lib/api";
import Skeleton from "@/components/Skeleton";

function when(r: PreregRecord): string {
  if (r.registeredOn) return r.registeredOn;
  if (r.ts) return new Date(r.ts * 1000).toISOString().slice(0, 10);
  return "—";
}

function label(key: string): string {
  // "horizonDays" -> "horizon days". The keys are whatever the record froze, so
  // they are formatted, never translated through a list this file would have to
  // keep in step with six payload shapes.
  return key.replace(/([a-z0-9])([A-Z])/g, "$1 $2").toLowerCase();
}

// Flatten the spec payload into rows for display. The keys are read off the
// payload rather than assumed because each registered kind froze a different
// shape, and a field the page drops is a field the reader was told to check
// and cannot be omitted without losing evidence.
function specFields(spec: unknown): Array<{ key: string; label: string; value: string }> {
  if (spec == null) return [];
  if (typeof spec === "string") return [{ key: "spec", label: "", value: spec }];
  if (typeof spec === "number" || typeof spec === "boolean") return [{ key: "spec", label: "", value: String(spec) }];
  if (Array.isArray(spec)) return [{ key: "spec", label: "", value: JSON.stringify(spec) }];
  const entries = Object.entries(spec as Record<string, unknown>);
  const result: Array<{ key: string; label: string; value: string }> = [];
  for (const [key, value] of entries) {
    if (value == null || value === "" || (Array.isArray(value) && value.length === 0)) continue;
    let v: string;
    if (typeof value === "string") v = value;
    else if (typeof value === "number" || typeof value === "boolean") v = String(value);
    else v = JSON.stringify(value);
    if (v.length > 160) v = v.slice(0, 160) + "\u2026";
    result.push({ key, label: label(key), value: v });
  }
  return result;
}

export default function Registrations() {
  const [data, setData] = useState<PreregResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    let alive = true;
    prereg()
      .then((d) => alive && setData(d))
      .catch((e) =>
        alive &&
        setErr(
          e instanceof ApiError && e.status === 401
            ? "The registration chain is not readable without a session on this deployment."
            : e instanceof Error
              ? e.message
              : "could not load the registration chain",
        ),
      );
    return () => {
      alive = false;
    };
  }, []);

  if (err) {
    // An unavailable chain says so. It must never render as "nothing was
    // registered", which is the same sentence a reader would write down as a
    // finding about the project.
    return (
      <section className="panel" aria-label="pre-registration chain">
        <div className="panel-h">
          <span>PRE-REGISTRATION CHAIN</span>
        </div>
        <p className="m-0 px-5 py-4 text-[0.8rem]" style={{ color: "var(--dim)" }}>
          {err} This is an availability problem with the chain, not evidence that
          nothing was registered.
        </p>
      </section>
    );
  }

  if (!data) {
    return (
      <section className="panel" aria-label="pre-registration chain">
        <div className="panel-h">
          <span>PRE-REGISTRATION CHAIN</span>
        </div>
        <div className="px-5 py-4">
          <Skeleton />
        </div>
      </section>
    );
  }

  const records = data.records ?? [];
  const shown = open ? records : records.slice(0, 8);
  // Only the records the date actually describes belong in this ratio. Counting
  // the ones that report null as failures made the chain look late when nothing
  // had been compared.
  const timed = records.filter((r) => r.beforeFirstGradable != null);
  const frozenEarly = timed.filter((r) => r.beforeFirstGradable).length;

  return (
    <section className="panel" aria-label="pre-registration chain">
      <div className="panel-h">
        <span>PRE-REGISTRATION CHAIN</span>
        <span className="chip px-2 py-[1px] text-[0.7rem]">
          {records.length} registered claim{records.length === 1 ? "" : "s"}
        </span>
      </div>

      <div className="flex flex-col gap-3 px-5 py-4">
        {data.whatThisIs ? (
          <p className="m-0 max-w-[80ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {data.whatThisIs}
          </p>
        ) : null}

        <div className="flex flex-wrap gap-x-6 gap-y-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          <span>
            chain{" "}
            <strong style={{ color: data.chainVerified ? "var(--ok)" : "var(--bad)" }}>
              {data.chainVerified ? "verified" : `BROKEN at #${data.brokenAtSeq ?? "?"}`}
            </strong>
          </span>
          <span>
            frozen before anything could be graded:{" "}
            <strong style={{ color: "var(--fg)" }}>
              {frozenEarly} of {timed.length}
            </strong>
          </span>
          {data.firstGradableOn ? <span>first gradable {data.firstGradableOn}</span> : null}
        </div>

        {/* The claims themselves. Scrolls in its own box so a long chain cannot
            make the page scroll sideways on a phone. */}
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[0.75rem]">
            <thead>
              <tr style={{ color: "var(--faint)" }}>
                <th className="px-2 py-1 text-left font-normal">#</th>
                <th className="px-2 py-1 text-left font-normal">Claim</th>
                <th className="px-2 py-1 text-left font-normal">Registered</th>
                <th className="px-2 py-1 text-left font-normal">Frozen before gradable</th>
                <th className="px-2 py-1 text-left font-normal">Digest</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r: PreregRecord) => {
                const fields = specFields(r.spec);
                return (
                  <tr key={r.seq} style={{ borderTop: "1px solid var(--border)" }}>
                    <td className="tnum px-2 py-1" style={{ color: "var(--faint)" }}>
                      {r.seq}
                    </td>
                    <td className="px-2 py-1">
                      <span style={{ color: "var(--fg)" }}>{r.kind}</span>
                      {fields.length === 0 ? null : (
                        <dl className="mt-[2px] flex max-w-[60ch] flex-col gap-[2px] leading-snug">
                          {fields.map((field) => (
                            // One div per pair, which the HTML spec allows inside
                            // a dl. It is not decoration: this dl is a flex
                            // column, a flex container blockifies its children,
                            // and bare dt/dd would have ignored `inline` and put
                            // every label on a line of its own.
                            <div key={field.key}>
                              {field.label ? (
                                <>
                                  <dt className="inline text-[0.7rem]" style={{ color: "var(--faint)" }}>{field.label}</dt>
                                  <dd className="m-0 inline" style={{ color: "var(--dim)" }}>{" "}{field.value}</dd>
                                </>
                              ) : (
                                <dd className="m-0" style={{ color: "var(--dim)" }}>{field.value}</dd>
                              )}
                            </div>
                          ))}
                        </dl>
                      )}
                    </td>
                    <td className="px-2 py-1 whitespace-nowrap" style={{ color: "var(--dim)" }}>
                      {when(r)}
                    </td>
                    <td className="px-2 py-1">
                      {r.beforeFirstGradable == null ? (
                        <span title="This date describes the structural predictors only, so this record was not compared against it." style={{ color: "var(--faint)" }}>
                          not evaluated
                        </span>
                      ) : (
                        <span style={{ color: r.beforeFirstGradable ? "var(--ok)" : "var(--warn)" }}>
                          {r.beforeFirstGradable ? "yes" : "no"}
                        </span>
                      )}
                    </td>
                    <td className="mono px-2 py-1" style={{ color: "var(--faint)" }}>
                      {(r.specHash ?? "").slice(0, 12) || "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        {records.length > 8 ? (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="self-start text-[0.75rem] underline"
            style={{ color: "var(--accent)", background: "none", border: 0, padding: 0, cursor: "pointer" }}
          >
            {open ? "Show fewer" : `Show all ${records.length}`}
          </button>
        ) : null}

        {data.appendOnly ? (
          <p className="m-0 max-w-[80ch] text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {data.appendOnly}
          </p>
        ) : null}
        {data.howToUseIt ? (
          <p className="m-0 max-w-[80ch] text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {data.howToUseIt}
          </p>
        ) : null}
      </div>
    </section>
  );
}
