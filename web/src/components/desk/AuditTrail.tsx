// AUDIT TRAIL — the transparency record behind a recommendation, collapsed into
// a details/summary expander so it never crowds the verdict but is always one
// click away. It carries the chain sequence number, the entry hash (the tamper-
// evident link), whether the hash chain still verifies intact, the exact data
// sources and model versions that produced the call, the assumptions it rests
// on, when it was recorded, and whether it can be re-derived. This is what makes
// a call auditable rather than a black box.

import { fmtTs } from "@/lib/format";
import type { RecoAudit } from "@/components/desk/RecommendationCard";

/** Show the hash as head…tail so the mono string stays legible without hiding
 *  that it is a full digest. */
function shortHash(h: string): string {
  if (!h) return "—";
  return h.length > 20 ? `${h.slice(0, 12)}…${h.slice(-6)}` : h;
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[0.75rem] font-medium tracking-wide" style={{ color: "var(--faint)" }}>
        {label}
      </span>
      {children}
    </div>
  );
}

export default function AuditTrail({ audit }: { audit: RecoAudit }) {
  const a = audit;
  const intact = !!a?.intact;
  const sources = Array.isArray(a?.sources) ? a.sources : [];
  const assumptions = Array.isArray(a?.assumptions) ? a.assumptions : [];
  const versions = a?.modelVersions && typeof a.modelVersions === "object" ? a.modelVersions : {};

  return (
    <details className="panel group">
      <summary
        className="panel-h flex cursor-pointer list-none flex-wrap items-center gap-2 select-none"
        style={{ borderBottom: "none" }}
      >
        AUDIT TRAIL
        <span className="normal-case" style={{ color: "var(--faint)", letterSpacing: "normal" }}>
          — reproducible
        </span>
        <span
          className="tnum ml-auto text-[0.75rem] font-bold"
          style={{ color: intact ? "var(--ok)" : "var(--bad)" }}
          title="whether the hash chain still verifies (no silent edits)"
        >
          {intact ? "chain intact ✓" : "chain broken ✗"}
        </span>
        <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
          seq #{Number.isFinite(a?.seq) ? a.seq : "—"}
        </span>
        <span
          aria-hidden="true"
          className="transition-transform duration-150 group-open:rotate-180"
          style={{ color: "var(--faint)" }}
        >
          ⌄
        </span>
      </summary>

      <div
        className="flex flex-col gap-4 px-4 py-4"
        style={{ borderTop: "1px solid var(--border)" }}
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Section label="ENTRY HASH">
            <span className="mono text-[0.75rem]" style={{ color: "var(--text)" }} title={a?.hash || ""}>
              {shortHash(a?.hash)}
            </span>
          </Section>
          <Section label="RECORDED">
            <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
              {a?.timestamp ? fmtTs(a.timestamp) : "—"}
            </span>
          </Section>
        </div>

        <Section label="DATA SOURCES">
          {sources.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {sources.map((s, i) => (
                <span key={`${s}-${i}`} className="chip" style={{ color: "var(--dim)" }}>
                  {s}
                </span>
              ))}
            </div>
          ) : (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              none recorded
            </span>
          )}
        </Section>

        <Section label="MODEL VERSIONS">
          {Object.keys(versions).length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(versions).map(([k, v]) => (
                <span key={k} className="chip tnum" style={{ color: "var(--dim)" }}>
                  {k}: <span style={{ color: "var(--text)" }}>{String(v)}</span>
                </span>
              ))}
            </div>
          ) : (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              none recorded
            </span>
          )}
        </Section>

        <Section label="ASSUMPTIONS">
          {assumptions.length > 0 ? (
            <ul className="flex flex-col gap-1">
              {assumptions.map((x, i) => (
                <li key={i} className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                  · {x}
                </li>
              ))}
            </ul>
          ) : (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              none recorded
            </span>
          )}
        </Section>

        <p className="text-[0.75rem] leading-relaxed" style={{ color: intact ? "var(--dim)" : "var(--bad)" }}>
          {a?.reproducible
            ? "Reproducible — this call can be re-derived from the recorded inputs, sources, and model versions above."
            : "Not marked reproducible — the recorded inputs may not fully re-derive this call."}
        </p>
      </div>
    </details>
  );
}
