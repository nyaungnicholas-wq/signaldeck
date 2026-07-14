"use client";

// The EVIDENCE expander under each insight (Benzinga-WIIM style receipts):
// the raw data blob the sentence was written from, rendered as labeled
// key-value bullets. Malformed/empty blobs render NOTHING — parseEvidence
// never throws. Labels follow the simple/pro toggle (plain English vs raw
// key names); the numbers themselves are identical in both modes.

import { useViewMode } from "@/components/Plain";
import { evidenceFields, evidenceKind, parseEvidence } from "./evidence";

export default function EvidencePanel({ data }: { data: string }) {
  const mode = useViewMode();
  const ev = parseEvidence(data);
  if (!ev) return null;
  const { fields, omitted } = evidenceFields(ev, mode);
  if (fields.length === 0) return null;
  const kind = evidenceKind(ev);

  return (
    <details className="mt-2">
      <summary
        className="chip inline-flex min-h-[40px] cursor-pointer select-none list-none items-center gap-1.5 transition-colors duration-150 hover:text-[var(--text)] [&::-webkit-details-marker]:hidden"
        title="the stored data blob this insight was generated from"
      >
        {mode === "simple" ? "show the receipts" : "evidence"}{" "}
        <span className="tnum">{fields.length + omitted}</span>
      </summary>
      <div
        className="mt-2 rounded-lg px-3 py-2.5"
        style={{ border: "1px solid var(--border)", background: "var(--panel2)" }}
      >
        <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {mode === "simple"
            ? "the stored numbers this sentence was written from"
            : `raw evidence blob${kind ? ` · kind=${kind}` : ""}`}
        </p>
        <ul className="mt-1.5 flex flex-col gap-1">
          {fields.map((f) => (
            <li
              key={f.key}
              className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-[0.75rem] leading-relaxed"
            >
              <span className="min-w-[9.5rem]" style={{ color: "var(--faint)" }}>
                {f.label}
              </span>
              <span className="tnum" style={{ color: "var(--dim)" }}>
                {f.value}
              </span>
            </li>
          ))}
        </ul>
        {omitted > 0 && (
          <p className="mt-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            +{omitted} more field{omitted === 1 ? "" : "s"} in the raw blob (not shown)
          </p>
        )}
      </div>
    </details>
  );
}
