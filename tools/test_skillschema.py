"""The typed result record must carry every field a verdict is read off.

A number without its universe, its baseline, its dependence treatment and its
provenance is not a result -- it is a digit that happens to be true today. This pins
the shape so a consumer can never receive a partial one.
"""
import json
import unittest

import skillschema as S

REGISTRY = {
    "generated": "2026-08-15T17:08:35",
    "graded_at": "2026-08-15T17:08:35",
    "grader_sha256": "cb3c01e91a04b8d85539a58f4efc965a20dcafde7ca57d9fd50c7e4ee5df628c",
    "grading_protocol_seq": 69,
    "min_independent_n": 30,
    "min_distinct_blocks": 10,
    "corrected_alpha": 0.00012406947890818859,
    "ci_z": 3.8379425893891175,
    "null_policy": "prequential-majority only",
    "settlement_quarantine": {"applied": True, "rows_excluded": 16726},
    "thin_day_exclusion": {"applied": True, "min_day_observations": 30},
    "stale_feed_exclusion": {"applied": True, "excluded_rows": 320},
}

ROW_1D = {
    "predictor": "directional-ensemble (1d)", "family": "direction", "band": "all",
    "live_n": 2479, "live_acc": 0.43404598628479224,
    "ci": [0.3345970431881845, 0.539105266114977],
    "ci_method": "day-clustered-wilson", "distinct_days": 16,
    "design_effect": 7.4760654979682135, "effective_n": 331.5915304211451,
    "null_acc": 0.563735377168213,
    "null_ci": [0.3857009819105808, 0.7267293279916269],
    "null_method": "prequential-majority (walk-forward)",
    "skill": -0.12968939088342074,
    "verdict": "FAILED - significantly worse than the naive baseline",
}

REQUIRED = [
    "schema_version", "metric", "estimand", "predictor", "horizon", "band",
    "universe", "evaluation", "counts", "estimate", "baseline", "dependence",
    "population_filters", "status", "reason_code", "reason", "provenance",
]

COMMIT = "af10f43fbc691cd027ef01bc0971d16e0d9dcc86"


class TestSchema(unittest.TestCase):
    def rec(self, row=None):
        return S.build(row or ROW_1D, REGISTRY, code_commit=COMMIT)

    def test_every_required_field_present(self):
        r = self.rec()
        self.assertEqual([k for k in REQUIRED if k not in r], [],
                         "record is missing required fields")

    def test_version_is_pinned_and_declared(self):
        self.assertIsInstance(S.SCHEMA_VERSION, int)
        self.assertEqual(self.rec()["schema_version"], S.SCHEMA_VERSION)

    def test_counts_carry_rows_days_and_effective_n(self):
        c = self.rec()["counts"]
        self.assertEqual(c["rows"], 2479)
        self.assertEqual(c["distinct_days"], 16)
        self.assertAlmostEqual(c["effective_n"], 331.5915304211451, places=6)
        self.assertAlmostEqual(c["design_effect"], 7.4760654979682135, places=6)
        self.assertLess(c["effective_n"], c["rows"],
                        "effective n must not exceed the row count")

    def test_baseline_carries_its_own_uncertainty(self):
        b = self.rec()["baseline"]
        self.assertAlmostEqual(b["value"], 0.563735377168213, places=9)
        self.assertEqual(b["ci"], [0.3857009819105808, 0.7267293279916269])
        self.assertIn("prequential", b["method"].lower())
        self.assertTrue(b["definition"], "the baseline must say what it IS")

    def test_provenance_binds_grader_protocol_and_code(self):
        p = self.rec()["provenance"]
        self.assertEqual(p["grader_sha256"], REGISTRY["grader_sha256"])
        self.assertEqual(p["grading_protocol_seq"], 69)
        self.assertEqual(p["code_commit"], COMMIT)
        self.assertEqual(p["registry_generated"], "2026-08-15T17:08:35")

    def test_overlapping_null_is_withheld_with_a_machine_readable_code(self):
        r = self.rec()
        self.assertEqual(r["status"], "WITHHELD")
        self.assertEqual(r["reason_code"], "NULL_INTERVAL_OVERLAP")
        self.assertIn(r["reason_code"], S.REASON_CODES)
        self.assertTrue(r["reason"])

    def test_separated_null_is_supported(self):
        row = dict(ROW_1D, predictor="directional-ensemble (1w, high conviction)",
                   band="|p-0.5|>=0.15", live_acc=0.3522727272727273,
                   ci=[0.2612, 0.4560], null_acc=0.6292613636363636,
                   null_ci=[0.4850, 0.7540])
        r = S.build(row, REGISTRY, code_commit=COMMIT)
        self.assertEqual(r["status"], "SUPPORTED")
        self.assertEqual(r["reason_code"], "OK")
        self.assertEqual(r["horizon"], "1w")
        self.assertIn("0.15", r["band"])

    def test_missing_interval_is_its_own_state_not_a_refusal(self):
        row = dict(ROW_1D, ci=None, null_ci=None,
                   verdict="INSUFFICIENT DAYS (9/10) - no interval, so no verdict")
        r = S.build(row, REGISTRY, code_commit=COMMIT)
        self.assertEqual(r["status"], "NO_INTERVAL")
        self.assertEqual(r["reason_code"], "NO_PUBLISHED_INTERVAL")

    def test_population_filters_are_recorded_not_assumed(self):
        f = self.rec()["population_filters"]
        self.assertTrue(f["settlement_quarantine"]["applied"])
        self.assertEqual(f["settlement_quarantine"]["rows_excluded"], 16726)
        self.assertTrue(f["thin_day_exclusion"]["applied"])

    def test_record_is_json_serialisable_and_deterministic(self):
        a, b = self.rec(), self.rec()
        self.assertEqual(a, b)
        json.dumps(a)          # must not raise

    def test_horizon_is_derived_for_every_published_predictor_name(self):
        for name, want in (("directional-ensemble (1d)", "1d"),
                           ("directional-ensemble (1w)", "1w"),
                           ("directional-ensemble (1d, high conviction)", "1d"),
                           ("directional-ensemble (1w, high conviction)", "1w")):
            r = S.build(dict(ROW_1D, predictor=name), REGISTRY, code_commit=COMMIT)
            self.assertEqual(r["horizon"], want, name)


if __name__ == "__main__":
    unittest.main()
