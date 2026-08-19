"""One typed, versioned record per graded claim.

A number without its universe, its baseline, its dependence treatment and its
provenance is not a result -- it is a digit that happens to be true today. This
repository has already been bitten twice by exactly that: a partial sum published as
"Total Value", and a day-blocked statistic computed on 28 days and printed beside a
verdict graded on 16. Both were individually correct and jointly misleading.

So every consumer gets the whole record or nothing. Nothing here is computed: the
registry already carries the interval method, the design effect, the effective n, the
null policy, the population filters and the grader hash -- they are merely scattered
across two levels. This assembles them, adds the code commit and a machine-readable
reason code, and stamps a schema version so a consumer can tell what shape it holds.

Pure and deterministic: no I/O, no clock, no randomness, no globals.
"""
from skillpower import verdict_supported_by_intervals

SCHEMA_VERSION = 1
REASON_CODES = frozenset({"OK", "NULL_INTERVAL_OVERLAP", "NO_PUBLISHED_INTERVAL"})


def build(row, registry, *, code_commit):
    live_acc = row.get("live_acc")
    ci = row.get("ci")
    null_acc = row.get("null_acc")
    null_ci = row.get("null_ci")
    r = verdict_supported_by_intervals(live_acc, ci, null_acc, null_ci)
    if r["supported"] is None:
        status = "NO_INTERVAL"
        reason_code = "NO_PUBLISHED_INTERVAL"
        reason = r["reason"]
    elif r["supported"] is False:
        status = "WITHHELD"
        reason_code = "NULL_INTERVAL_OVERLAP"
        reason = r["reason"]
    else:
        status = "SUPPORTED"
        reason_code = "OK"
        reason = ""
    predictor = row.get("predictor")
    # Name the baseline, not just the word "baseline": the whole point of the record
    # is that a reader never has to go looking for what a number was measured against.
    estimand = (
        f"accuracy of {predictor} on its graded cross-section, against the "
        f"{row.get('null_method') or 'stated'} baseline"
        if predictor is not None else "accuracy against the stated baseline")
    horizon = "1w" if "1w" in (predictor or "") else "1d"
    band = row.get("band") or "all"
    universe = str(row.get("family")) + " / " + band
    evaluation = {"graded_at": registry.get("graded_at"), "generated": registry.get("generated")}
    counts = {
        "rows": row.get("live_n"),
        "distinct_days": row.get("distinct_days"),
        "effective_n": row.get("effective_n"),
        "design_effect": row.get("design_effect"),
    }
    estimate = {
        "point": live_acc,
        "ci": ci,
        "ci_method": row.get("ci_method"),
        "ci_z": registry.get("ci_z"),
        "alpha": registry.get("corrected_alpha"),
    }
    baseline = {
        "value": null_acc,
        "ci": null_ci,
        "method": row.get("null_method") or "",
        "policy": registry.get("null_policy"),
        "definition": "prevailing majority class computed only from days STRICTLY BEFORE each graded day",
    }
    dependence = {
        "method": row.get("ci_method"),
        "design_effect": row.get("design_effect"),
        "min_independent_n": registry.get("min_independent_n"),
        "min_distinct_blocks": registry.get("min_distinct_blocks"),
    }
    population_filters = {
        "settlement_quarantine": registry.get("settlement_quarantine"),
        "thin_day_exclusion": registry.get("thin_day_exclusion"),
        "stale_feed_exclusion": registry.get("stale_feed_exclusion"),
    }
    provenance = {
        "grader_sha256": registry.get("grader_sha256"),
        "grading_protocol_seq": registry.get("grading_protocol_seq"),
        "code_commit": code_commit,
        "registry_generated": registry.get("generated"),
    }
    return {
        "schema_version": SCHEMA_VERSION,
        "metric": "directional_accuracy",
        "estimand": estimand,
        "predictor": predictor,
        "horizon": horizon,
        "band": band,
        "universe": universe,
        "evaluation": evaluation,
        "counts": counts,
        "estimate": estimate,
        "baseline": baseline,
        "dependence": dependence,
        "population_filters": population_filters,
        "status": status,
        "reason_code": reason_code,
        "reason": reason,
        "provenance": provenance,
    }