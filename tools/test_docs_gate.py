#!/usr/bin/env python3
"""Contract for tools/docs_gate.py — the docs publication gate.

WHY THIS EXISTS. On 2026-08-04 a P0 truth-stop froze ten strategy documents
because four of them published mutually inconsistent live-accuracy figures, three
asserted survivorship control that `ALPHA_WORKFLOW.md` §B2 measured open, and one
cited a kill switch living in a *different repository*. Every one of those was a
number or a status a human typed into markdown by hand, with nothing in the
repository able to contradict it.

The gate exists so that class of defect fails a build instead of reaching a
reader. Six checks, each named by the phase that unblocks it when it fires:

  C1 no-hardcoded-live-accuracy  a live-accuracy % typed into a doc
  C2 grader-status               the grader is refusing to publish
  C3 docs-index                  a doc without owner/version/data-as-of/status
  C4 forbidden-claims            "guaranteed"/"zero risk"/"surefire", or an
                                 unproven "verified" in a risk/control table
  C5 single-source-of-truth      a generated partial missing or stale
  C6 data-integrity              empty PIT universe, implausible delisting rate,
                                 or a pre-survivorship-fix backtest with no label

THE CI SPLIT, and why it is not a hole. `data/` is gitignored and the database is
4 GB, so a runner cannot ask it anything. This mirrors ops/ledger-provenance.sh
exactly: the question the database can answer is answered on the machine that has
it (`write-integrity` → `ops/data-integrity.json`, committed), and `check` — the
mode CI runs — reads only committed files. A missing or unparseable snapshot is a
VIOLATION, never a skip, so the split cannot degrade into silence.

Every test below builds its own repo fixture in a temp dir. None reads the real
repository, so this file states the contract rather than describing today's tree.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GATE = os.path.join(HERE, "docs_gate.py")

# A snapshot with every value in its passing state. Tests mutate one key at a
# time, so a failure names exactly one cause.
CLEAN_INTEGRITY = {
    "generated": "2026-08-04T17:00:00Z",
    "source_db": "data/signaldeck.db",
    "grader": {
        "status": "OK",
        "graded_at": "2026-08-04T06:06:30Z",
        "refused_since": None,
        "refusal_reason": None,
        "rows": 13,
    },
    "universe_membership_rows": 1_900_000,
    "delisting": {
        "stocks_total": 1773,
        "stocks_delisted": 716,
        "span_years": 7.5,
        "rate_per_year": 0.0539,
    },
    "survivorship_fix_date": "2026-08-04",
}

CLEAN_REGISTRY = {
    "thresholds": {"min_delist_rate_per_year": 0.02},
    "strategy_docs": {
        "GOOD_DOC.md": {
            "owner": "Nicholas Nyaung",
            "version": "1.0.0",
            "data_as_of": "2026-08-04",
            "status": "ACTIVE",
            "backtest_data": "none",
        }
    },
    "exempt": ["NOTES.md"],
    "partials": ["live_record.md"],
}

GOOD_DOC = """# A document that passes every check

Prose with no live-accuracy figure and no forbidden claim.

| Control | Status |
|---|---|
| Kill switch | present |
"""


class GateFixture:
    """A throwaway repository the gate can be pointed at."""

    def __init__(self) -> None:
        self.root = tempfile.mkdtemp(prefix="docsgate-")
        for d in ("ops", "partials", "logs"):
            os.makedirs(os.path.join(self.root, d), exist_ok=True)
        self.write_registry(CLEAN_REGISTRY)
        self.write_integrity(CLEAN_INTEGRITY)
        self.write_doc("GOOD_DOC.md", GOOD_DOC)
        # A real repository has its generated partials committed, so the clean
        # fixture must too — otherwise "clean" and "a partial is missing" would
        # be the same state and no test below could distinguish them.
        built = self.run("build")
        assert built.returncode == 0, f"fixture build failed:\n{built.stdout}\n{built.stderr}"

    # -- fixture mutation -------------------------------------------------
    def write_registry(self, obj) -> None:
        self._json("ops/docs-registry.json", obj)

    def write_integrity(self, obj) -> None:
        self._json("ops/data-integrity.json", obj)

    def write_doc(self, name: str, body: str) -> None:
        self._text(name, body)

    def add_strategy_doc(self, name: str, body: str, **meta) -> None:
        """Register a doc with complete metadata unless a test overrides it."""
        reg = self.read_registry()
        entry = {
            "owner": "Nicholas Nyaung",
            "version": "1.0.0",
            "data_as_of": "2026-08-04",
            "status": "ACTIVE",
            "backtest_data": "none",
        }
        entry.update(meta)
        reg["strategy_docs"][name] = entry
        self.write_registry(reg)
        self.write_doc(name, body)

    def read_registry(self):
        with open(os.path.join(self.root, "ops/docs-registry.json"), encoding="utf-8") as f:
            return json.load(f)

    def path(self, rel: str) -> str:
        return os.path.join(self.root, rel)

    def _json(self, rel: str, obj) -> None:
        self._text(rel, json.dumps(obj, indent=2))

    def _text(self, rel: str, body: str) -> None:
        p = os.path.join(self.root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8", newline="\n") as f:
            f.write(body)

    # -- running the gate -------------------------------------------------
    def run(self, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, GATE, *args, "--repo", self.root],
            capture_output=True, text=True)

    def check(self) -> subprocess.CompletedProcess:
        """`check --json`, the mode CI runs. Reads only committed files."""
        return self.run("check", "--json")

    def violations(self) -> list[dict]:
        r = self.check()
        self.owner.assertIn(r.returncode, (0, 1), f"gate errored: {r.stderr}")
        return json.loads(r.stdout)["violations"]

    def codes(self) -> set[str]:
        return {v["check"] for v in self.violations()}

    def cleanup(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)


class GateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.fx = GateFixture()
        self.fx.owner = self
        self.addCleanup(self.fx.cleanup)

    def assertClean(self) -> None:
        r = self.fx.check()
        self.assertEqual(r.returncode, 0,
                         f"expected a clean gate, got:\n{r.stdout}\n{r.stderr}")

    def assertFires(self, code: str) -> list[dict]:
        r = self.fx.check()
        self.assertEqual(r.returncode, 1, f"expected a violation, got exit 0:\n{r.stdout}")
        hits = [v for v in json.loads(r.stdout)["violations"] if v["check"] == code]
        self.assertTrue(hits, f"{code} did not fire; violations were {self.fx.codes()}")
        return hits

    # ---------------------------------------------------------------- base
    def test_clean_fixture_passes(self):
        """The fixture must start green, or no failure below proves anything."""
        self.assertClean()

    def test_exit_code_1_means_violations_not_crash(self):
        self.fx.add_strategy_doc("BAD.md", "# Bad\n\nThis is guaranteed to work.\n")
        r = self.fx.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("violations", json.loads(r.stdout))

    # ------------------------------------------------ C1 live accuracy
    def test_c1_flags_hardcoded_live_accuracy(self):
        self.fx.add_strategy_doc(
            "REC.md", "# Record\n\nOur live accuracy is 62.5% across all horizons.\n")
        hits = self.assertFires("no-hardcoded-live-accuracy")
        self.assertEqual(hits[0]["file"], "REC.md")

    def test_c1_flags_the_vocabulary_that_actually_bit(self):
        """Each phrasing the frozen documents used to state a live figure."""
        for phrase in ("live record shows 48.1%",
                       "directional accuracy of 46.3%",
                       "hit rate 58%",
                       "win rate of 53.2%",
                       "graded at 52.9% against baseline"):
            with self.subTest(phrase=phrase):
                fx = GateFixture()
                fx.owner = self
                self.addCleanup(fx.cleanup)
                fx.add_strategy_doc("D.md", f"# D\n\n{phrase}\n")
                r = fx.check()
                self.assertEqual(r.returncode, 1, f"{phrase!r} passed the gate")
                self.assertIn("no-hardcoded-live-accuracy",
                              {v["check"] for v in json.loads(r.stdout)["violations"]})

    def test_c1_ignores_percentages_with_no_accuracy_context(self):
        """Thresholds, coverage floors and allocations are not live accuracy."""
        self.fx.add_strategy_doc(
            "CFG.md",
            "# Config\n\nCoverage floor is 70.1%. Position cap 25% of equity.\n"
            "Volatility target 35%.\n")
        self.assertClean()

    def test_c1_ignores_a_confidence_level(self):
        """`95% CI` is the interval's confidence, not an accuracy figure.

        Every one of these fired on the real repository's first gate run. A gate
        that flags the statistics vocabulary of the documents it guards is a gate
        someone deletes by the end of the week.
        """
        for line in ("| Arm | trades | 95% CI (block) | win rate | Sharpe |",
                     "With `[lo, hi]` the day-clustered 95% interval on live accuracy",
                     "the 99% confidence bound on the directional accuracy"):
            with self.subTest(line=line):
                fx = GateFixture()
                fx.owner = self
                self.addCleanup(fx.cleanup)
                fx.add_strategy_doc("D.md", f"# D\n\n{line}\n")
                r = fx.check()
                self.assertEqual(r.returncode, 0, f"{line!r} was wrongly flagged:\n{r.stdout}")

    def test_c1_ignores_the_coin_flip_baseline(self):
        """`never 50%` is the null being argued against, not a claimed result."""
        self.fx.add_strategy_doc(
            "D.md",
            "# D\n\nThe baseline is the prequential majority, never 50%. "
            "This is why a directional accuracy in the high forties can still be skill.\n")
        self.assertClean()

    def test_c1_still_fires_on_a_real_figure_beside_a_confidence_level(self):
        """Sharpening must not become a hole: a real figure on the same line still counts."""
        self.fx.add_strategy_doc(
            "D.md", "# D\n\nOur live accuracy is 62.5% (95% CI [58, 67]).\n")
        self.assertFires("no-hardcoded-live-accuracy")

    def test_c1_allows_a_figure_inside_a_generated_region(self):
        """A generated number is the fix, not the defect — it must not fire."""
        self.fx.add_strategy_doc(
            "GEN.md",
            "# Generated\n\n"
            "<!-- BEGIN GENERATED: live_record.md -->\n"
            "Live accuracy 46.3% (n=2,257).\n"
            "<!-- END GENERATED: live_record.md -->\n")
        self.assertClean()

    def test_c1_allowlist_requires_a_reason(self):
        """An escape hatch with no stated reason is how a gate rots."""
        reg = self.fx.read_registry()
        reg["allow"] = {"no-hardcoded-live-accuracy": [{"file": "REC.md"}]}
        self.fx.write_registry(reg)
        self.fx.add_strategy_doc("REC.md", "# R\n\nlive accuracy 62.5%\n")
        r = self.fx.check()
        self.assertEqual(r.returncode, 1,
                         "a reasonless allowlist entry must not suppress anything")

    def test_c1_allowlist_with_a_reason_suppresses(self):
        reg = self.fx.read_registry()
        reg["allow"] = {"no-hardcoded-live-accuracy": [
            {"file": "REC.md", "reason": "quoting a competitor's published claim"}]}
        self.fx.write_registry(reg)
        self.fx.add_strategy_doc("REC.md", "# R\n\nlive accuracy 62.5%\n")
        self.assertClean()

    # ---------------------------------------------------- C2 grader status
    def test_c2_fires_when_grader_is_refusing(self):
        snap = json.loads(json.dumps(CLEAN_INTEGRITY))
        snap["grader"].update(status="REFUSED", refusal_reason="grader exited 1",
                              refused_since="2026-08-04T14:05:05Z")
        self.fx.write_integrity(snap)
        hits = self.assertFires("grader-status")
        self.assertIn("grader exited 1", hits[0]["message"],
                      "the refusal reason must reach the build log")

    def test_c2_fires_when_the_snapshot_is_missing(self):
        """No snapshot is a violation, never a skip — that is the CI split."""
        os.remove(self.fx.path("ops/data-integrity.json"))
        self.assertFires("integrity-snapshot")

    def test_c2_fires_when_the_snapshot_is_unparseable(self):
        with open(self.fx.path("ops/data-integrity.json"), "w", encoding="utf-8") as f:
            f.write("{ not json")
        self.assertFires("integrity-snapshot")

    # ------------------------------------------------------- C3 docs index
    def test_c3_fires_on_each_missing_required_field(self):
        for field in ("owner", "version", "data_as_of", "status"):
            with self.subTest(field=field):
                fx = GateFixture()
                fx.owner = self
                self.addCleanup(fx.cleanup)
                reg = fx.read_registry()
                del reg["strategy_docs"]["GOOD_DOC.md"][field]
                fx.write_registry(reg)
                r = fx.check()
                self.assertEqual(r.returncode, 1, f"missing {field} passed the gate")
                self.assertIn("docs-index",
                              {v["check"] for v in json.loads(r.stdout)["violations"]})

    def test_c3_fires_on_an_empty_required_field(self):
        reg = self.fx.read_registry()
        reg["strategy_docs"]["GOOD_DOC.md"]["owner"] = "   "
        self.fx.write_registry(reg)
        self.assertFires("docs-index")

    def test_c3_fires_on_an_unparseable_data_as_of(self):
        reg = self.fx.read_registry()
        reg["strategy_docs"]["GOOD_DOC.md"]["data_as_of"] = "last Tuesday"
        self.fx.write_registry(reg)
        self.assertFires("docs-index")

    def test_c3_fires_on_an_unknown_status(self):
        reg = self.fx.read_registry()
        reg["strategy_docs"]["GOOD_DOC.md"]["status"] = "probably fine"
        self.fx.write_registry(reg)
        self.assertFires("docs-index")

    def test_c3_fires_on_a_registered_doc_that_does_not_exist(self):
        os.remove(self.fx.path("GOOD_DOC.md"))
        self.assertFires("docs-index")

    def test_c3_fires_on_a_new_doc_that_is_neither_registered_nor_exempt(self):
        """The recurrence-blocker: a new strategy doc cannot dodge the gate."""
        self.fx.write_doc("NEW_STRATEGY.md", "# New\n\nSome claims.\n")
        hits = self.assertFires("docs-index")
        self.assertTrue(any(h["file"] == "NEW_STRATEGY.md" for h in hits))

    def test_c3_respects_the_exempt_list(self):
        self.fx.write_doc("NOTES.md", "# Notes\n\nScratch.\n")
        self.assertClean()

    # -------------------------------------------------- C4 forbidden claims
    def test_c4_flags_each_forbidden_phrase(self):
        for phrase in ("This is guaranteed.", "There is zero risk here.",
                       "A surefire setup."):
            with self.subTest(phrase=phrase):
                fx = GateFixture()
                fx.owner = self
                self.addCleanup(fx.cleanup)
                fx.add_strategy_doc("D.md", f"# D\n\n{phrase}\n")
                r = fx.check()
                self.assertEqual(r.returncode, 1, f"{phrase!r} passed the gate")
                self.assertIn("forbidden-claims",
                              {v["check"] for v in json.loads(r.stdout)["violations"]})

    def test_c4_is_case_insensitive(self):
        self.fx.add_strategy_doc("D.md", "# D\n\nGUARANTEED returns.\n")
        self.assertFires("forbidden-claims")

    def test_c4_does_not_flag_a_substring_of_another_word(self):
        """`unguaranteed` is a different word; word boundaries must hold."""
        self.fx.add_strategy_doc("D.md", "# D\n\nThe payout is unguaranteed.\n")
        self.assertClean()

    def test_c4_flags_unproven_verified_in_a_control_table(self):
        self.fx.add_strategy_doc(
            "CTRL.md",
            "# Controls\n\n"
            "| Control | Status |\n|---|---|\n"
            "| Kill switch | Verified |\n")
        hits = self.assertFires("forbidden-claims")
        self.assertIn("verified", hits[0]["message"].lower())

    def test_c4_accepts_verified_with_a_proof_link(self):
        self.fx.add_strategy_doc(
            "CTRL.md",
            "# Controls\n\n"
            "| Control | Status |\n|---|---|\n"
            "| Kill switch | Verified — [proof](proofs/P8_DOCS_GATE.md) |\n")
        self.assertClean()

    def test_c4_ignores_verified_in_ordinary_prose(self):
        """The check is scoped to table rows; prose is not an attestation."""
        self.fx.add_strategy_doc(
            "D.md", "# D\n\nWe verified this by hand on 2026-08-04.\n")
        self.assertClean()

    # ------------------------------------------------ C5 single source of truth
    def test_c5_fires_when_a_declared_partial_is_missing(self):
        os.remove(self.fx.path("partials/live_record.md"))
        hits = self.assertFires("single-source-of-truth")
        self.assertTrue(any("live_record.md" in h["message"] for h in hits))

    def test_build_then_check_is_green(self):
        """`build` must produce exactly what `check` demands."""
        shutil.rmtree(self.fx.path("partials"))
        b = self.fx.run("build")
        self.assertEqual(b.returncode, 0, f"build failed:\n{b.stdout}\n{b.stderr}")
        self.assertTrue(os.path.exists(self.fx.path("partials/live_record.md")))
        self.assertClean()

    def test_c5_fires_when_a_partial_is_hand_edited(self):
        with open(self.fx.path("partials/live_record.md"), "a", encoding="utf-8") as f:
            f.write("\nHand-typed live accuracy: 71%.\n")
        hits = self.assertFires("single-source-of-truth")
        self.assertTrue(any("stale" in h["message"].lower() for h in hits))

    def test_c5_fires_when_the_source_moved_under_a_built_partial(self):
        """The staleness case that matters: the snapshot changed, docs did not."""
        self.assertClean()
        snap = json.loads(json.dumps(CLEAN_INTEGRITY))
        snap["grader"]["rows"] = 99
        snap["generated"] = "2026-08-05T09:00:00Z"
        self.fx.write_integrity(snap)
        self.assertFires("single-source-of-truth")

    def test_build_is_deterministic(self):
        """Two builds of one input must be byte-identical, or `check` is a coin flip."""
        with open(self.fx.path("partials/live_record.md"), "rb") as f:
            first = f.read()
        self.assertEqual(self.fx.run("build").returncode, 0)
        with open(self.fx.path("partials/live_record.md"), "rb") as f:
            self.assertEqual(first, f.read())

    def test_build_writes_a_log(self):
        """A proof artifact P8 names explicitly."""
        self.assertEqual(self.fx.run("build").returncode, 0)
        self.assertTrue(os.path.exists(self.fx.path("logs/docs_build.log")))

    def test_build_refuses_without_a_snapshot(self):
        """Generating a live-record partial from nothing would invent a number."""
        os.remove(self.fx.path("ops/data-integrity.json"))
        self.assertNotEqual(self.fx.run("build").returncode, 0)

    def test_a_refusing_grader_generates_a_refusal_not_a_number(self):
        """The whole point: when the grader refuses, no figure is publishable."""
        snap = json.loads(json.dumps(CLEAN_INTEGRITY))
        snap["grader"].update(status="REFUSED", refusal_reason="grader exited 1",
                              refused_since="2026-08-04T14:05:05Z")
        self.fx.write_integrity(snap)
        self.assertEqual(self.fx.run("build").returncode, 0)
        with open(self.fx.path("partials/live_record.md"), encoding="utf-8") as f:
            body = f.read()
        self.assertIn("REFUSED", body)
        self.assertNotIn("%", body,
                         "a refusing grader must not yield a percentage anywhere")

    # ------------------------------------------------------ C6 data integrity
    def test_c6_fires_on_an_empty_universe_membership(self):
        snap = json.loads(json.dumps(CLEAN_INTEGRITY))
        snap["universe_membership_rows"] = 0
        self.fx.write_integrity(snap)
        hits = self.assertFires("data-integrity")
        self.assertIn("universe_membership", hits[0]["message"])

    def test_c6_fires_on_an_implausibly_low_delisting_rate(self):
        """The measured B2 defect: 21 delistings / 1,077 names / 7.5 years."""
        snap = json.loads(json.dumps(CLEAN_INTEGRITY))
        snap["delisting"] = {"stocks_total": 1077, "stocks_delisted": 21,
                             "span_years": 7.5, "rate_per_year": 0.0026}
        self.fx.write_integrity(snap)
        hits = self.assertFires("data-integrity")
        self.assertIn("delist", hits[0]["message"].lower())

    def test_c6_accepts_a_plausible_delisting_rate(self):
        self.assertClean()

    def test_c6_threshold_comes_from_the_registry(self):
        reg = self.fx.read_registry()
        reg["thresholds"]["min_delist_rate_per_year"] = 0.10
        self.fx.write_registry(reg)
        self.assertFires("data-integrity")

    def test_c6_fires_on_an_unlabelled_pre_survivorship_fix_backtest(self):
        self.fx.add_strategy_doc(
            "BT.md", "# Backtest\n\nBand table computed 2026-07-27.\n",
            backtest_data="pre-survivorship-fix")
        hits = self.assertFires("data-integrity")
        self.assertTrue(any("BT.md" == h["file"] for h in hits))

    def test_c6_accepts_a_labelled_pre_survivorship_fix_backtest(self):
        self.fx.add_strategy_doc(
            "BT.md",
            "# Backtest\n\n> FROZEN — computed on pre-survivorship-fix data.\n\n"
            "Band table computed 2026-07-27.\n",
            backtest_data="pre-survivorship-fix")
        self.assertClean()

    def test_c6_requires_backtest_data_to_be_declared(self):
        reg = self.fx.read_registry()
        del reg["strategy_docs"]["GOOD_DOC.md"]["backtest_data"]
        self.fx.write_registry(reg)
        self.assertFires("docs-index")

    def test_c6_rejects_an_unknown_backtest_data_value(self):
        reg = self.fx.read_registry()
        reg["strategy_docs"]["GOOD_DOC.md"]["backtest_data"] = "probably fine"
        self.fx.write_registry(reg)
        self.assertFires("docs-index")

    # ------------------------------------------- hardening (review findings)
    def test_unterminated_generated_region_does_not_exempt_the_rest(self):
        """The bypass: one BEGIN with no END would exempt every line after it."""
        self.fx.add_strategy_doc(
            "GEN.md",
            "# Generated\n\n"
            "<!-- BEGIN GENERATED: live_record.md -->\n"
            "Live accuracy 46.3%.\n"
            "\n"
            "Our live accuracy is really 99.9% though.\n")
        r = self.fx.check()
        self.assertEqual(r.returncode, 1, "an unterminated region exempted the document")
        codes = {v["check"] for v in json.loads(r.stdout)["violations"]}
        self.assertIn("single-source-of-truth", codes,
                      "an unterminated generated marker must itself be a violation")

    def test_partial_comparison_is_newline_insensitive(self):
        """Committed CRLF on Windows vs generated LF must not read as stale."""
        p = self.fx.path("partials/live_record.md")
        with open(p, "rb") as f:
            body = f.read()
        with open(p, "wb") as f:
            f.write(body.replace(b"\n", b"\r\n"))
        self.assertClean()

    def test_c4_does_not_flag_a_negated_verified(self):
        """`Not verified` / `unverified` are honest disclaimers, not attestations."""
        for cell in ("Not verified", "not yet verified", "unverified", "never verified"):
            with self.subTest(cell=cell):
                fx = GateFixture()
                fx.owner = self
                self.addCleanup(fx.cleanup)
                fx.add_strategy_doc(
                    "CTRL.md",
                    "# Controls\n\n| Control | Status |\n|---|---|\n"
                    f"| Kill switch | {cell} |\n")
                r = fx.check()
                self.assertEqual(r.returncode, 0,
                                 f"{cell!r} was wrongly flagged:\n{r.stdout}")

    def test_allowlist_line_contains_narrows_the_suppression(self):
        """Without this wired, the only escape hatch is whole-file — too blunt."""
        reg = self.fx.read_registry()
        reg["allow"] = {"no-hardcoded-live-accuracy": [
            {"file": "REC.md", "reason": "quoting a competitor",
             "line_contains": "competitor"}]}
        self.fx.write_registry(reg)
        self.fx.add_strategy_doc(
            "REC.md",
            "# R\n\n"
            "A competitor advertises live accuracy 62.5%.\n"
            "Our own live accuracy is 71.0%.\n")
        hits = self.assertFires("no-hardcoded-live-accuracy")
        self.assertEqual(len(hits), 1,
                         "line_contains must suppress only the matching line")
        self.assertIn("71.0%", str(hits[0]))

    def test_an_empty_registry_is_a_tool_error_not_a_pass(self):
        """A gate that passes because it was handed nothing is worse than none."""
        self.fx.write_registry({})
        self.assertEqual(self.fx.check().returncode, 2)

    def test_a_missing_registry_is_a_tool_error(self):
        os.remove(self.fx.path("ops/docs-registry.json"))
        self.assertEqual(self.fx.check().returncode, 2)

    # NOTE ON SCOPE. Embedding the live-accuracy partial into documents, and
    # detecting a document whose embedded copy has drifted from it, is NOT
    # checked here. `tools/live_accuracy.py --check --inject` owns that contract
    # and CI runs it. Implementing it a second time would give the repository
    # two definitions of "drifted" that can disagree — the single-source-of-truth
    # defect this gate exists to prevent, reproduced inside the gate itself.
    # C5 below covers only partials this tool itself declares and generates.

    # ------------------------------------------------------------ reporting
    def test_every_violation_names_file_line_and_remedy(self):
        """A gate nobody can act on gets disabled. Each row must be actionable."""
        self.fx.add_strategy_doc("D.md", "# D\n\nOur live accuracy is 62.5%.\n")
        for v in self.fx.violations():
            self.assertTrue(v.get("check"), v)
            self.assertIn("file", v)
            self.assertTrue(str(v.get("message", "")).strip(), v)
        hit = [v for v in self.fx.violations()
               if v["check"] == "no-hardcoded-live-accuracy"][0]
        self.assertIsInstance(hit.get("line"), int)
        self.assertGreater(hit["line"], 0)

    def test_human_output_lists_every_violation(self):
        self.fx.add_strategy_doc("D.md", "# D\n\nThis is guaranteed.\n")
        r = self.fx.run("check")
        self.assertEqual(r.returncode, 1)
        self.assertIn("forbidden-claims", r.stdout + r.stderr)

    def test_unknown_mode_is_a_tool_error_not_a_violation(self):
        """Exit 2 keeps 'the gate broke' distinguishable from 'the docs are wrong'."""
        r = self.fx.run("frobnicate")
        self.assertEqual(r.returncode, 2)


if __name__ == "__main__":
    unittest.main()
