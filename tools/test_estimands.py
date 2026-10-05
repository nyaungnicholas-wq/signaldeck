"""Every published metric must state what it estimates, on whom, against what.

The failures this pins are not hypothetical. All three happened in this repository:

  * a STRICT-universe label graded against a full-panel base rate, which put +5.78pp of
    pure universe premium into the "skill" column before the model did anything;
  * a baseline estimated from the same outcomes it is compared against and then treated
    as a known constant, with no interval;
  * a paired comparison scored as if the two sides were independent samples.

A definition living only in prose drifts from the code that computes it. This makes the
definition an object the code must satisfy.
"""
import unittest

import estimands as E

REQUIRED = ["estimand", "universe", "horizon", "timestamp", "outcome",
            "eligible_rows", "dedup_key", "baseline", "baseline_source",
            "baseline_uncertainty", "paired"]

EXPECTED = {
    "unconditional_direction_accuracy",
    "conditional_accuracy",
    "ranking_skill",
    "calibration",
    "cross_sectional_relative_return_skill",
    "portfolio_performance",
}


class TestEstimands(unittest.TestCase):
    def test_all_six_are_defined(self):
        self.assertEqual(set(E.ESTIMANDS), EXPECTED)

    def test_every_definition_is_complete(self):
        for name, d in E.ESTIMANDS.items():
            missing = [k for k in REQUIRED if k not in d]
            self.assertEqual(missing, [], f"{name} is missing {missing}")
            for k in REQUIRED:
                if k == "paired":
                    self.assertIsInstance(d[k], bool, f"{name}.paired must be bool")
                else:
                    self.assertTrue(str(d[k]).strip(), f"{name}.{k} is empty")

    def test_estimand_says_something_the_key_does_not(self):
        """"estimand": "<the key>" is not a definition, it is an echo.

        A first draft passed every shape check while every estimand field simply
        repeated its own dict key. Non-empty is not the same as informative.
        """
        for name, d in E.ESTIMANDS.items():
            text = str(d["estimand"]).strip()
            self.assertNotEqual(text.lower(), name.lower(),
                                f"{name}.estimand merely repeats the key")
            self.assertGreaterEqual(
                len(text.split()), 6,
                f"{name}.estimand is not a definition, it is a label: {text!r}")

    def test_timestamp_names_a_CLOCK_FIELD_not_a_date(self):
        """This field answers "which clock decides membership", not "when".

        A first draft put the literal string "2024-06-01" on all six -- a date that
        appears nowhere in this system, invented to fill the slot. A fabricated value
        is worse than an empty one: an empty field is visibly missing.
        """
        import re
        for name, d in E.ESTIMANDS.items():
            ts = str(d["timestamp"])
            self.assertIsNone(
                re.search(r"\d{4}-\d{2}-\d{2}", ts),
                f"{name}.timestamp is a date, not a clock field: {ts!r}")
            self.assertTrue(
                any(w in ts.lower() for w in
                    ("ts", "close", "settle", "bar", "decision", "timestamp")),
                f"{name}.timestamp must name the field that decides membership: {ts!r}")

    def test_horizon_is_a_real_horizon_of_this_system(self):
        """Live metrics run at 1d/1w; research metrics at 21/42 trading days."""
        allowed = {"1d", "1w", "21", "42", "21d", "42d"}
        for name, d in E.ESTIMANDS.items():
            tokens = {t.strip().lower() for t in
                      str(d["horizon"]).replace("/", ",").replace(" and ", ",").split(",")}
            self.assertTrue(
                tokens & allowed,
                f"{name}.horizon names no horizon this system runs: {d['horizon']!r}")

    def test_direction_accuracy_baseline_is_the_majority_not_a_median_rate(self):
        """The two baselines are different quantities that sit at similar numbers.

        Direction accuracy is scored against the PREQUENTIAL MAJORITY (~0.56 live).
        Beating the cross-sectional median is a different event whose STRICT-universe
        rate is 0.558. A draft put "55.8%" on direction accuracy -- numerically close,
        conceptually a different estimand, and exactly the conflation this audit exists
        to catch.
        """
        for name in ("unconditional_direction_accuracy", "conditional_accuracy"):
            d = E.ESTIMANDS[name]
            blob = (str(d["baseline"]) + " " + str(d["baseline_source"])).lower()
            self.assertIn("major", blob,
                          f"{name} must be scored against the prevailing MAJORITY class, "
                          f"not a median-beating rate: {d['baseline']!r}")
            self.assertNotIn("median", blob,
                             f"{name} baseline conflates majority with median-beating")
            # The source string can say "majority" while the VALUE is lifted from the
            # other estimand. 0.558 / 0.565 are the STRICT universe's median-beating
            # rates, measured in research/dirfix/check_universe_null.py. They are not
            # the prequential-majority accuracy and must never appear here.
            for wrong in ("55.8", "0.558", "56.5", "0.565"):
                self.assertNotIn(
                    wrong, str(d["baseline"]),
                    f"{name}.baseline is {d['baseline']!r} -- that is the STRICT "
                    "median-beating rate, a DIFFERENT estimand that merely sits at a "
                    "similar number. Use the prequential-majority accuracy.")

    def test_universe_is_a_population_not_a_deployment(self):
        """"LIVE" is where a metric runs, not who it is measured over."""
        for name, d in E.ESTIMANDS.items():
            uni = str(d["universe"]).lower()
            self.assertTrue(
                any(w in uni for w in ("strict", "panel", "liquid", "tradable",
                                       "universe", "symbol", "book")),
                f"{name}.universe does not name a population: {d['universe']!r}")

    def test_no_metric_on_a_filtered_universe_claims_a_one_half_baseline(self):
        """The exact defect: 0.5 is only honest on the panel the median was taken over.

        A STRICT/liquid universe beats the full-panel median 55.8% of the time, so a
        0.5 baseline books that premium as skill.
        """
        for name, d in E.ESTIMANDS.items():
            uni = str(d["universe"]).lower()
            base = str(d["baseline"]).strip()
            if "strict" in uni or "liquid" in uni or "tradable" in uni:
                self.assertNotIn(base, {"0.5", "0.50", "50%"},
                                 f"{name} grades a filtered universe against {base}")

    def test_every_baseline_names_where_it_comes_from(self):
        """A number with no stated source cannot be audited, only believed."""
        for name, d in E.ESTIMANDS.items():
            src = str(d["baseline_source"]).lower()
            self.assertTrue(
                any(w in src for w in ("prequential", "training", "expanding",
                                       "preregistered", "external", "universe",
                                       "walk-forward", "out-of-sample")),
                f"{name}.baseline_source does not say how the baseline was obtained: "
                f"{d['baseline_source']!r}")

    def test_no_baseline_is_treated_as_known_without_uncertainty(self):
        for name, d in E.ESTIMANDS.items():
            unc = str(d["baseline_uncertainty"]).strip().lower()
            self.assertNotIn(unc, {"none", "n/a", "", "assumed known", "fixed"},
                             f"{name} treats its baseline as known exactly")

    def test_metrics_sharing_rows_with_their_baseline_are_marked_paired(self):
        """If model and baseline are scored on the same rows, the comparison is paired
        and the variance of the DIFFERENCE is what matters -- not two marginal CIs."""
        for name in ("unconditional_direction_accuracy", "conditional_accuracy"):
            self.assertTrue(E.ESTIMANDS[name]["paired"],
                            f"{name} scores model and baseline on identical rows")

    def test_dedup_key_always_collapses_within_day(self):
        """Many forecasts for one symbol on one day are one observation, not many."""
        for name, d in E.ESTIMANDS.items():
            key = str(d["dedup_key"]).lower()
            self.assertIn("day", key, f"{name}.dedup_key must collapse per day: {key!r}")

    def test_lookup_helper_is_strict(self):
        self.assertEqual(E.get("ranking_skill")["horizon"],
                         E.ESTIMANDS["ranking_skill"]["horizon"])
        with self.assertRaises(KeyError):
            E.get("no_such_metric")


if __name__ == "__main__":
    unittest.main()
