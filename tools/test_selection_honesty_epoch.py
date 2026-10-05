"""calls_up is measured on the graded window, and its epoch matches the grader's."""
import os
import re
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import selection_honesty  # noqa: E402


class CallsUpEpoch(unittest.TestCase):
    def test_epoch_matches_the_pinned_grader(self):
        src = open(os.path.join(os.path.dirname(__file__), "accuracy_registry.py"), encoding="utf-8").read()
        m = re.search(r"GRADING_EPOCH = dt\.date\((\d+), (\d+), (\d+)\)", src)
        self.assertIsNotNone(m, "accuracy_registry.GRADING_EPOCH not found")
        import datetime as dt
        want = int(dt.datetime(*map(int, m.groups()), tzinfo=dt.timezone.utc).timestamp())
        self.assertEqual(selection_honesty.GRADING_EPOCH_TS, want)

    def test_pre_epoch_rows_do_not_count(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        try:
            con = sqlite3.connect(path)
            con.execute("CREATE TABLE prediction_outcomes (symbol_id INT, horizon TEXT, ts INT, prob REAL, up INT)")
            e = selection_honesty.GRADING_EPOCH_TS
            rows = [(i, "1d", e - 86400 * 3, 0.9, 1) for i in range(9)]  # pre-epoch: all UP calls
            rows += [(i, "1d", e + 86400, 0.1, 0) for i in range(3)]      # graded: all DOWN calls
            con.executemany("INSERT INTO prediction_outcomes VALUES (?,?,?,?,?)", rows)
            con.commit()
            con.close()
            self.assertEqual(selection_honesty.calls_up_by_horizon(path), {"1d": 0.0})
        finally:
            os.remove(path)


if __name__ == "__main__":
    unittest.main()
