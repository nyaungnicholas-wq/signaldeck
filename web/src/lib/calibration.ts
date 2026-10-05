// The Brier-skill stat's sub-line on the calibration panel.
//
// A null skill is withheld for a reason the daemon states in brierNote: too
// little history, a zero-variance base rate, a gated record, or SD-30 (the
// directional label is mostly realised at issue). The panel used to show that
// note only when gated, so an SD-30 withholding read "not gradable yet" —
// a wait that will never end. Any stated reason now wins over the generic one.
export function brierSkillSub(
  d: { brierSkill?: number | null; brierNote?: string; baseRate?: number | null } | null | undefined,
): string {
  if (d?.brierSkill == null) {
    return d?.brierNote || "not gradable yet — needs resolved outcomes on both sides";
  }
  return (
    `vs always forecasting the ${((d.baseRate ?? 0) * 100).toFixed(1)}% base rate; ` +
    (d.brierSkill > 0 ? "positive = real probabilistic skill" : "negative = worse than the constant")
  );
}
