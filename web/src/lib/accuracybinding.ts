// Bind the accuracy page's two reads to ONE graded artifact.
//
// THE DEFECT (audit R01, 2026-09-20). web/src/app/accuracy/page.tsx asks the
// daemon whether publication is approved, and then — separately, later, from
// disk — reads the registry it renders figures from. Nothing tied the two
// together. Between those reads the grader can replace
// data/accuracy_registry.json, and the page would then pair an approval issued
// for the OLD grade with numbers from the NEW one. Worse, `pubFor(label)`
// returns undefined for a registry row the API never approved, and the row
// renderer fell back to the registry file's own verdict string — so an
// unapproved row rendered its metrics anyway, with the grader's sentence as the
// headline. That is the exact shape the page exists to prevent: the daemon is
// supposed to be the single verdict path.
//
// The fix is a key, not a lock. Every grade stamps graded_at and grader_sha256
// into the registry, and /api/accuracy echoes both from the file it approved.
// If the file on disk does not carry the same pair, the two reads saw different
// artifacts and the page must publish nothing.
//
// Pure functions, no I/O, so the page's behaviour is testable without a daemon.

export type ApprovalIdentity = {
  gradedAt?: string;
  graderSha256?: string;
};

export type RegistryIdentity = {
  graded_at?: string;
  grader_sha256?: string;
};

/**
 * Why these two reads must not be rendered together, or null when they agree.
 *
 * A missing identity on EITHER side is a mismatch, not a pass. An artifact that
 * cannot say which grade produced it cannot be shown to be the approved one,
 * and "unknown" resolving to "publish" is how the original defect would have
 * survived the fix.
 */
export function bindingMismatch(
  reg: RegistryIdentity | null | undefined,
  pub: ApprovalIdentity | null | undefined,
): string | null {
  if (!reg) return "the registry file could not be read, so there is nothing to match against the daemon's approval";
  if (!pub) return "the daemon returned no publication identity to match the registry against";

  const rAt = (reg.graded_at ?? "").trim();
  const pAt = (pub.gradedAt ?? "").trim();
  if (!rAt || !pAt) {
    return "the grade could not be identified on both sides (registry graded_at " +
      `${rAt ? "present" : "missing"}, daemon graded_at ${pAt ? "present" : "missing"}), ` +
      "so the approved grade and the rendered one cannot be shown to be the same";
  }
  if (rAt !== pAt) {
    return `the registry on disk is from a different grade than the one the daemon approved ` +
      `(file ${rAt}, approved ${pAt}) — it was replaced between the two reads`;
  }

  // grader_sha256 is the stronger half: two grades can share a timestamp string
  // far more easily than a hash. It is only compared when the daemon supplies
  // it, because the API omits it on paths that publish no rows.
  const rSha = (reg.grader_sha256 ?? "").trim();
  const pSha = (pub.graderSha256 ?? "").trim();
  if (pSha && rSha !== pSha) {
    return `the registry on disk was written by a different grader than the one the daemon ` +
      `approved (file ${rSha || "none"}, approved ${pSha})`;
  }
  return null;
}

/**
 * Registry row labels with no approved counterpart.
 *
 * The daemon splits "predictor (horizon, variant)" into three fields; this
 * rebuilds the label so a registry row can find its verdict. Anything left over
 * is a row the publication path never judged, and it must not render its
 * numbers.
 */
export function labelOfPublished(x: { predictor: string; horizon?: string; variant?: string }): string {
  return x.predictor + (x.horizon ? ` (${x.horizon}${x.variant ? `, ${x.variant}` : ""})` : "");
}

export function unapprovedLabels(
  registryRows: ReadonlyArray<{ predictor: string }>,
  publishedRows: ReadonlyArray<{ predictor: string; horizon?: string; variant?: string }>,
): string[] {
  const approved = new Set(publishedRows.map(labelOfPublished));
  const out: string[] = [];
  for (const r of registryRows) {
    if (!approved.has(r.predictor) && !out.includes(r.predictor)) out.push(r.predictor);
  }
  return out;
}
