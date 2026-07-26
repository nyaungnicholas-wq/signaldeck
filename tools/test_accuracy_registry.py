#!/usr/bin/env python3
"""Tests for the accuracy registry — the script that decides whether a shipped
claim survives contact with the live record.

It had none until 2026-07-26, which is how it came to publish binomial intervals
over ~1,000 correlated symbols per day for eight months. The tests below pin the
day-resampling discipline so that cannot come back silently.

Run: python3 -m unittest discover -s tools -p 'test_*.py'
"""
import math
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from accuracy_registry import (  # noqa: E402
    MIN_DISTINCT_DAYS,
    clustered_ci,
    design_effect,
    verdict_for,
    wilson,
    wilson_eff,
)


class TestDesignEffect(unittest.TestCase):
    def test_uniform_days_carry_no_penalty(self):
        """Days that all behave identically have no between-day variance."""
        days = [(100, 60)] * 20
        self.assertAlmostEqual(design_effect(days), 1.0, places=6)

    def test_clustered_days_are_penalised(self):
        """Days that swing between mostly-right and mostly-wrong are the live shape."""
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        deff = design_effect(days)
        self.assertGreater(deff, 50, f"deff {deff} does not reflect extreme clustering")

    def test_never_below_one(self):
        """A deff under 1 would make the interval narrower than independence."""
        days = [(100, 50), (100, 50), (100, 50)]
        self.assertGreaterEqual(design_effect(days), 1.0)

    def test_degenerate_proportion_is_worst_case(self):
        """All-right (or all-wrong) is the most perfectly clustered sample there is."""
        days = [(100, 100)] * 20
        self.assertAlmostEqual(design_effect(days), 100.0, places=6)  # n/k

    def test_unmeasurable_below_two_days(self):
        self.assertIsNone(design_effect([(100, 50)]))
        self.assertIsNone(design_effect([]))


class TestClusteredCI(unittest.TestCase):
    def test_withholds_below_the_day_floor(self):
        """Nine days is not enough to estimate between-day variance."""
        g = clustered_ci([(400, 300)] * (MIN_DISTINCT_DAYS - 1))
        self.assertIsNone(g["ci"])
        self.assertEqual(g["ci_method"], "withheld")
        self.assertIn("distinct days", g["ci_reason"])

    def test_publishes_at_and_above_the_floor(self):
        g = clustered_ci([(400, 300)] * MIN_DISTINCT_DAYS)
        self.assertIsNotNone(g["ci"])
        self.assertEqual(g["ci_method"], "day-clustered-wilson")

    def test_interval_is_wider_than_the_row_count_interval(self):
        """The whole point: a clustered sample cannot carry a row-count interval."""
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        g = clustered_ci(days)
        pooled_lo, pooled_hi = wilson(g["hits"], g["n"])
        self.assertGreater(g["ci"][1] - g["ci"][0], (pooled_hi - pooled_lo) * 5)

    def test_effective_n_is_n_over_deff(self):
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        g = clustered_ci(days)
        self.assertAlmostEqual(g["effective_n"], g["n"] / g["design_effect"], places=6)
        self.assertLess(g["effective_n"], g["n"])

    def test_point_estimate_is_untouched(self):
        """Correcting the interval must not move the accuracy itself."""
        days = [(500, 450)] * 11 + [(500, 25)] * 9
        g = clustered_ci(days)
        self.assertAlmostEqual(g["acc"], (11 * 450 + 9 * 25) / 10000, places=9)


class TestVerdict(unittest.TestCase):
    def test_no_interval_means_no_verdict(self):
        """A missing interval must never fall through to a point-estimate verdict."""
        v = verdict_for(0.82, None, None, 408, None, 0.82, distinct_days=1)
        self.assertIn("INSUFFICIENT DAYS", v)
        self.assertNotIn("HOLDING", v)

    def test_one_market_day_cannot_validate_a_claim(self):
        """The 2026-08-07 scenario: ~408 forecasts resolving on a single call day.

        Under the old row-count interval this produced a decisive HOLDING or
        DECAYED verdict on the platform's headline 82% claim. One market day is
        one observation, whatever its symbol count.
        """
        g = clustered_ci([(408, 335)])          # one day, 82.1% correct
        self.assertIsNone(g["ci"])
        v = verdict_for(g["acc"], None, None, g["n"], None, 0.82,
                        distinct_days=g["distinct_days"])
        self.assertIn("INSUFFICIENT DAYS", v)

    def test_interval_straddling_the_null_is_no_skill(self):
        v = verdict_for(0.487, 0.451, 0.524, 7115, 0.523, None, distinct_days=13)
        self.assertTrue(v.startswith("NO SKILL"), v)

    def test_interval_entirely_below_the_null_still_fails(self):
        """Widening intervals must not rescue a genuinely failing predictor."""
        v = verdict_for(0.481, 0.448, 0.514, 13058, 0.546, None, distinct_days=23)
        self.assertTrue(v.startswith("FAILED"), v)

    def test_sample_floor_still_applies(self):
        v = verdict_for(0.9, 0.8, 1.0, 10, 0.5, None, distinct_days=10)
        self.assertTrue(v.startswith("INSUFFICIENT"), v)


class TestWilsonEff(unittest.TestCase):
    def test_matches_plain_wilson_at_deff_one(self):
        lo1, hi1 = wilson(600, 1000)
        lo2, hi2 = wilson_eff(0.6, 1000)
        self.assertAlmostEqual(lo1, lo2, places=6)
        self.assertAlmostEqual(hi1, hi2, places=6)

    def test_widens_as_effective_n_falls(self):
        narrow = wilson_eff(0.6, 1000)
        wide = wilson_eff(0.6, 50)
        self.assertGreater(wide[1] - wide[0], narrow[1] - narrow[0])

    def test_bounded_to_the_unit_interval(self):
        lo, hi = wilson_eff(0.99, 3)
        self.assertGreaterEqual(lo, 0.0)
        self.assertLessEqual(hi, 1.0)
        self.assertFalse(math.isnan(lo) or math.isnan(hi))


if __name__ == "__main__":
    unittest.main()
