// "Ask the data" (daemon plan step 10): the shapes of GET/POST /api/ask and the
// helpers the page renders with. The daemon checks every citation; this file
// only turns them into chips.

export const ASK_FOOTER =
  "Answers come only from SignalDeck's own records and cite every row used. Not financial advice.";

export interface AskParam {
  name: string;
  description: string;
  kind: "symbol" | "enum" | "int" | "slug";
  enum?: string[];
  min?: number;
  max?: number;
  required?: boolean;
  default?: unknown;
}

export interface AskStatus {
  available: boolean;
  reason?: string; // why not, when available is false
  dailyLimit?: number;
  maxQuestion?: number;
  catalog?: { name: string; description: string; params: AskParam[] }[];
}

export interface AskCitation {
  id: string; // "q1:r3"
  query: string;
  row: Record<string, unknown>;
}

export interface AskAnswer {
  answer: string;
  citations: AskCitation[];
  // truncated: more rows matched than the query's cap; the answer saw only the first ones.
  queries: { name: string; params: Record<string, unknown>; truncated?: boolean }[];
  model: string;
  tookMs: number;
  fallback: boolean; // no cited answer: citations holds every row returned
}

export type AnswerPart = { text: string } | { ids: string[] };

// A bracketed group of ids, or a bare id (the daemon accepts both).
const CITE = /\[(\s*q\d+:r\d+(?:\s*[,;]\s*q\d+:r\d+)*\s*)\]|\b(q\d+:r\d+)\b/g;

/** Splits an answer into text and citation groups: "a [q1:r1, q1:r2]." gives
 *  [{text:"a "},{ids:["q1:r1","q1:r2"]},{text:"."}]. A bare id is a group of
 *  one; an id repeated within a group appears once. */
export function splitCitations(answer: string): AnswerPart[] {
  const out: AnswerPart[] = [];
  let last = 0;
  for (const m of answer.matchAll(CITE)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ text: answer.slice(last, at) });
    const ids = m[1] !== undefined ? m[1].split(/[,;]/).map((s) => s.trim()) : [m[2]];
    out.push({ ids: [...new Set(ids)] });
    last = at + m[0].length;
  }
  if (last < answer.length) out.push({ text: answer.slice(last) });
  return out;
}

/** A cell for display: unix-second timestamps (*_ts) as UTC dates, null as a dash. */
export function formatCell(key: string, v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "number" && key.endsWith("_ts") && v > 1e9) {
    return new Date(v * 1000).toISOString().slice(0, 16).replace("T", " ") + " UTC";
  }
  return String(v);
}
