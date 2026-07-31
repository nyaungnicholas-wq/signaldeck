"""Regression channel for the pre-cut refusals in tools/make_repro_snapshot.py.

Discovery command (the same one CI runs):
    python3 -m unittest discover -s tools -p 'test_*.py'

Both refusals guard REPRODUCE.md's headline claim -- that one command
regenerates every published number from this repository alone. They can only
ever stop a bundle from being cut; nothing here may change a value inside one.
"""

import hashlib
import os
import subprocess
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import make_repro_snapshot as m  # noqa: E402
import accuracy_registry as reg  # noqa: E402

# The ON-DISK grader hash -- not the committed one.  The pre-cut check folds
# the chain through the same resolver the snapshot grade uses, so it must
# match the running file, not the version at HEAD.
DISK_SHA = hashlib.sha256(
    open(os.path.join(m.REPO, "tools", "accuracy_registry.py"), "rb").read()
).hexdigest()
HEAD = subprocess.run(["git", "rev-parse", "HEAD"], cwd=m.REPO, text=True,
                      capture_output=True, check=True).stdout.strip()


def rec(sha=DISK_SHA, commit=HEAD, min_n="30", min_days="10",
        min_blocks="10", max_alpha="0.05", mult=None, looks="0"):
    return [["11", sha, commit, min_n, min_days, min_blocks, max_alpha,
             mult if mult is not None else reg.MULTIPLICITY_RULE, looks]]


class RefusalTest(unittest.TestCase):
    def test_a_resolvable_pin_with_every_field_frozen_is_allowed(self):
        m.refuse_unverifiable_grading_protocol(rec())

    def test_no_record_still_cuts_an_empty_file(self):
        # The loader already refuses to grade from an empty protocol file, and
        # that path is honest -- the refusal must not turn it into a hard stop.
        m.refuse_unverifiable_grading_protocol([])

    def test_an_unfrozen_threshold_refuses(self):
        """A chain whose folded protocol never froze a required floor refuses.

        The refusal message comes from grader_registration_error (called via
        the fold), which names the field and the expected value.
        """
        for field in ("min_n", "min_days", "min_blocks", "max_alpha", "mult"):
            with self.subTest(field=field):
                with self.assertRaises(m.SnapshotRefused) as cm:
                    m.refuse_unverifiable_grading_protocol(rec(**{field: ""}))
                # The fold sees the missing floor and grader_registration_error
                # refuses with "UNREGISTERED GRADER" naming the field.
                self.assertIn("UNREGISTERED GRADER", str(cm.exception))

    def test_a_pin_naming_code_at_no_commit_refuses(self):
        with self.assertRaises(m.SnapshotRefused) as cm:
            m.refuse_unverifiable_grading_protocol(rec(sha="de" * 32))
        self.assertIn("UNREGISTERED GRADER", str(cm.exception))

    def test_an_unreadable_commit_still_allowed_when_digest_matches(self):
        """A stale commit is not a refusal when the digest matches.

        The fold path checks graderSha256 against the running file first.  If
        the digest matches, the commit is never looked up -- a single-record
        chain has no cross-record borrowing, so a stale commit does not block
        grading.
        """
        # Must not raise -- the fold passes since the digest matches.
        m.refuse_unverifiable_grading_protocol(rec(commit="0" * 40))

    def test_a_missing_digest_refuses(self):
        with self.assertRaises(m.SnapshotRefused):
            m.refuse_unverifiable_grading_protocol(rec(sha=""))

    def test_a_missing_commit_with_matching_digest_allows(self):
        """An empty commit is not a refusal when the digest matches.

        The fold path checks graderSha256 against the running file and
        inspects the folded protocol's fields.  An empty commit in a
        single-record chain does not cause a refusal when the digest
        matches and there is no cross-record borrowing.
        """
        m.refuse_unverifiable_grading_protocol(rec(commit=""))


if __name__ == "__main__":
    unittest.main()
