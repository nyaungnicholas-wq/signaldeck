"use client";

// The complete frozen record, for a reader who is checking rather than skimming.
//
// The receipts table cuts every spec field at 160 characters and shows 12 of a
// 64-character digest (audit F03). That is fine for scanning and useless for
// verifying: the longest field on the live chain is 1,037 characters, so the
// multiplicity rule a reader is told to check was 85% invisible. The only
// expand control on the page adds RECORDS, not field text.
//
// So each row carries this. It is a native <details>: focusable by Tab, toggled
// by Enter or Space, and its expanded state is announced — no JavaScript, no
// focus trap, no ARIA to get wrong. Nothing here is shortened and nothing is
// dropped, including fields frozen as null or empty, because "this was left
// unspecified" is evidence too.

import { specFields } from "@/lib/specfields";
import type { PreregRecord } from "@/lib/api";

function stringifyRecord(r: PreregRecord): string {
  try {
    return JSON.stringify(r, null, 2);
  } catch {
    return "this record could not be serialised for display; read it from GET /api/prereg";
  }
}

export default function FullRecord({ record }: { record: PreregRecord }) {
  const fields = specFields(record.spec, "full");
  const digest = record.specHash ?? "";
  return (
    <details className="mt-1 max-w-[70ch] text-[0.72rem]">
      <summary
        className="cursor-pointer"
        style={{ color: "var(--accent)" }}
        aria-label={`Full frozen record for claim ${record.seq}`}
      >
        Full record
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        {fields.length > 0 ? (
          <dl className="m-0 flex flex-col gap-1 leading-relaxed">
            {fields.map((f) => (
              <div key={f.key}>
                {f.label ? (
                  <dt className="text-[0.7rem]" style={{ color: "var(--faint)" }}>
                    {f.label}
                  </dt>
                ) : null}
                {/* break-words + whitespace-pre-wrap: a 1,037-character rule
                    has to wrap inside the panel rather than widen the table
                    and push the page sideways on a phone. */}
                <dd
                  className="m-0 whitespace-pre-wrap break-words"
                  style={{ color: f.empty ? "var(--faint)" : "var(--dim)" }}
                >
                  {f.empty ? `${f.full} (frozen empty)` : f.full}
                </dd>
              </div>
            ))}
          </dl>
        ) : (
          <p className="m-0" style={{ color: "var(--faint)" }}>
            This record froze no spec fields.
          </p>
        )}

        <div className="mono break-all" style={{ color: "var(--faint)" }}>
          digest {digest || "— (none on this record)"}
        </div>

        {/* The raw payload, so a reader can recompute the digest over exactly
            the bytes the chain holds rather than over this page's rendering. */}
        <details>
          <summary className="cursor-pointer" style={{ color: "var(--faint)" }}>
            Raw JSON
          </summary>
          <pre
            className="mono mt-1 max-h-[24rem] overflow-auto whitespace-pre-wrap break-words text-[0.68rem] leading-relaxed"
            style={{ color: "var(--dim)" }}
          >
            {stringifyRecord(record)}
          </pre>
          <p className="m-0 mt-1" style={{ color: "var(--faint)" }}>
            The whole chain is served as JSON at <span className="mono">/api/prereg</span>.
          </p>
        </details>
      </div>
    </details>
  );
}
