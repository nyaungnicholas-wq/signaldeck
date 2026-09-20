// Flatten a frozen pre-registration spec into displayable rows.
//
// THE DEFECT (audit F03, 2026-09-20). This lived inside Registrations.tsx and
// sliced every value at 160 characters, unconditionally and with no way to see
// the rest. The only expand control on the page adds RECORDS, not field text.
// Measured on the live /api/prereg: all 116 specs are objects, and the longest
// single field — grading-protocol.multiplicityRule — is 1,037 characters, so
// 877 characters of the multiplicity rule a reader is explicitly told to check
// were unreachable in the UI. Resolution rules, baseline definitions and
// numeric bands lost their endings the same way. It also DROPPED null and empty
// fields entirely, which is a different kind of loss: a field frozen as null is
// evidence about what was not specified.
//
// So there are two views. `summary` keeps the table scannable; `full` is the
// evidence, complete, with nothing dropped and nothing cut.
//
// Every value is stringified here rather than in JSX. Handing React a raw
// object is error #31, which this component has already shipped once.

export const SUMMARY_LIMIT = 160;

export type SpecField = {
  key: string;
  label: string;
  /** Possibly shortened, for the table. */
  value: string;
  /** Never shortened, never omitted. */
  full: string;
  truncated: boolean;
  /** The frozen value was null, "" or [] — kept in the full view, not dropped. */
  empty: boolean;
};

export function humanLabel(key: string): string {
  // "horizonDays" -> "horizon days". The keys are whatever the record froze, so
  // they are formatted, never translated through a list this file would have to
  // keep in step with six payload shapes.
  return key.replace(/([a-z0-9])([A-Z])/g, "$1 $2").toLowerCase();
}

/** Render one frozen value as a string, without ever handing React an object. */
function asText(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (value === null) return "null";
  if (value === undefined) return "undefined";
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    // A cyclic or otherwise unserialisable payload must still render something
    // rather than throwing inside the tree.
    return String(value);
  }
}

function field(key: string, value: unknown, empty: boolean): SpecField {
  const full = asText(value);
  const truncated = full.length > SUMMARY_LIMIT;
  return {
    key,
    label: key === "spec" ? "" : humanLabel(key),
    value: truncated ? full.slice(0, SUMMARY_LIMIT) + "…" : full,
    full,
    truncated,
    empty,
  };
}

/**
 * Flatten `spec` into rows.
 *
 * `mode: "summary"` skips empty fields and shortens long ones — the table.
 * `mode: "full"`    keeps every field at full length — the disclosure.
 *
 * Objects, arrays, strings, numbers, booleans and null are all handled, because
 * each registered kind froze a different shape and a field this function drops
 * is a field the reader was told to check.
 */
export function specFields(spec: unknown, mode: "summary" | "full" = "summary"): SpecField[] {
  if (spec === null) return mode === "full" ? [field("spec", null, true)] : [];
  if (spec === undefined) return [];
  if (typeof spec === "string") {
    return spec === "" && mode === "summary" ? [] : [field("spec", spec, spec === "")];
  }
  if (typeof spec === "number" || typeof spec === "boolean") return [field("spec", spec, false)];
  if (Array.isArray(spec)) {
    return spec.length === 0 && mode === "summary" ? [] : [field("spec", spec, spec.length === 0)];
  }
  if (typeof spec !== "object") return [field("spec", spec, false)];

  const out: SpecField[] = [];
  for (const [key, value] of Object.entries(spec as Record<string, unknown>)) {
    const empty = value == null || value === "" || (Array.isArray(value) && value.length === 0);
    if (empty && mode === "summary") continue;
    out.push(field(key, value, empty));
  }
  return out;
}
