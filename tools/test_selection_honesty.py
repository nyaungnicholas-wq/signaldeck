"""Tests for the one-sided-book guard. Both polarities of the defect are pinned
here, because the second one LOOKS like a win and is the easier to ship."""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from selection_honesty import verdict, ONE_SIDED_AGREEMENT, SELECTION_TOL  # noqa: E402


def test_refuses_the_short_everything_book():
    """Live 2026-08-15 shape: acc 43.3% against a 56.5% null == 1 - null."""
    v = verdict(0.4333, 0.5652, 0.95, 0.05)
    assert v["publishable"] is False
    assert v["one_sided"] is True
    assert "DOWN" in v["reason"] and "complement" in v["reason"]


def test_refuses_the_inverted_long_everything_book():
    """The same defect wearing a win: acc 61.4% against a 60.1% null.

    A book calling UP on everything in a rising market. Measured on the
    2025-2026 holdout at 1d. Accuracy alone reads as success.
    """
    v = verdict(0.6137, 0.6010, 0.96, 0.92)
    assert v["publishable"] is False
    assert "UP" in v["reason"] and "the null itself" in v["reason"]


def test_publishes_a_genuine_cross_section():
    v = verdict(0.60, 0.50, 0.62, 0.48)
    assert v["publishable"] is True
    assert v["one_sided"] is False


def test_publishes_a_one_sided_book_that_genuinely_beats_its_null():
    """One-sidedness alone is not the defect -- being PINNED to the null is.

    Refusing on agreement alone would be red-by-construction, and a guard that
    always fires is one everybody learns to ignore.
    """
    v = verdict(0.75, 0.55, 0.97, 0.98)
    assert v["publishable"] is True
    assert v["one_sided"] is True


def test_boundary_is_the_measured_separation():
    # healthy days measured 0.75-0.86, broken 0.95-1.00
    assert 0.86 < ONE_SIDED_AGREEMENT < 0.95
    # just inside the tolerance refuses, just outside publishes
    assert verdict(0.5652 - SELECTION_TOL / 2, 0.5652, 0.99, 0.9)["publishable"] is False
    assert verdict(0.5652 + 0.10, 0.5652, 0.99, 0.9)["publishable"] is True


def test_missing_inputs_are_not_judged():
    assert verdict(None, 0.5, 0.95, 0.5) is None
    assert verdict(0.5, None, 0.95, 0.5) is None
    assert verdict(0.5, 0.5, None, 0.5) is None
