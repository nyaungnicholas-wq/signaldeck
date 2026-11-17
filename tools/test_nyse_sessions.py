import datetime as dt
import os
import sys
import unittest
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from accuracy_registry import nyse_full_holidays, is_nyse_session, nyse_sessions_after


def n(y, m, d):
    return (dt.date(y, m, d) - dt.date(1970, 1, 1)).days


class TestNyseSessions(unittest.TestCase):
    def test_2026_holidays(self):
        expected = {
            dt.date(2026, 1, 1),
            dt.date(2026, 1, 19),
            dt.date(2026, 2, 16),
            dt.date(2026, 4, 3),
            dt.date(2026, 5, 25),
            dt.date(2026, 6, 19),
            dt.date(2026, 7, 3),
            dt.date(2026, 9, 7),
            dt.date(2026, 11, 26),
            dt.date(2026, 12, 25),
        }
        self.assertEqual(set(nyse_full_holidays(2026)), expected)

    def test_2027_holidays(self):
        holidays = set(nyse_full_holidays(2027))
        self.assertIn(dt.date(2027, 6, 18), holidays)   # Juneteenth observed Friday
        self.assertIn(dt.date(2027, 7, 5), holidays)    # July 4 observed Monday
        self.assertIn(dt.date(2027, 12, 24), holidays)  # Christmas observed Friday
        self.assertIn(dt.date(2027, 3, 26), holidays)   # Good Friday
        self.assertIn(dt.date(2027, 1, 1), holidays)    # Jan 1 Friday is holiday

    def test_2022_jan1_saturday(self):
        self.assertNotIn(dt.date(2021, 12, 31), nyse_full_holidays(2021))
        self.assertNotIn(dt.date(2022, 1, 1), nyse_full_holidays(2022))

    def test_is_nyse_session(self):
        self.assertFalse(is_nyse_session(dt.date(2026, 11, 21)))  # Saturday
        self.assertFalse(is_nyse_session(dt.date(2026, 11, 26)))  # Thanksgiving
        self.assertTrue(is_nyse_session(dt.date(2026, 11, 27)))   # Friday

    def test_nyse_sessions_after_basic(self):
        self.assertEqual(nyse_sessions_after(n(2026, 11, 20), n(2026, 11, 27)), 4)
        self.assertEqual(nyse_sessions_after(n(2026, 11, 27), n(2026, 11, 20)), 0)

    def test_nyse_sessions_after_21_sessions(self):
        self.assertEqual(nyse_sessions_after(n(2026, 11, 23), n(2026, 12, 23)), 21)
        self.assertEqual(nyse_sessions_after(n(2026, 11, 23), n(2026, 12, 22)), 20)


class TestSessionBlocks(unittest.TestCase):
    """§16: blocks anchored at/after the cut-over close after 21 SESSIONS."""

    def test_post_cutover_block_spans_21_sessions(self):
        from accuracy_registry import horizon_blocks
        a, d = n(2026, 11, 23), n(2026, 12, 15)  # 22 calendar days, 15 sessions
        self.assertEqual(len(horizon_blocks([(a, 10, 6), (d, 10, 4)], 21, True)), 1)
        e = n(2026, 12, 23)  # the 21st session after the anchor
        self.assertEqual(len(horizon_blocks([(a, 10, 6), (e, 10, 4)], 21, True)), 2)
        # Directional and crypto callers keep calendar spans (sessions=False).
        self.assertEqual(len(horizon_blocks([(a, 10, 6), (d, 10, 4)], 21)), 2)
        week = [(a + k, 1, 1) for k in range(7)]
        self.assertEqual(len(horizon_blocks(week, 1)), 7)  # horizon 1 is identity

    def test_pre_cutover_block_keeps_calendar_rule(self):
        from accuracy_registry import horizon_blocks
        a, d = n(2026, 11, 2), n(2026, 11, 23)  # 21 calendar days
        self.assertEqual(len(horizon_blocks([(a, 10, 6), (d, 10, 4)], 21, True)), 2)


class TestNullSupersededRows(unittest.TestCase):
    """§16: from the cut-over a superseded row is out of the null tally too."""

    def test_superseded_row_after_cutover_does_not_move_the_null(self):
        from accuracy_registry import grade_structural, SESSION_BLOCKS_EPOCH_TS
        from test_accuracy_registry import TestNaivePersistenceNull as T
        con = T._db()
        T._seed(con, days=12)
        con.execute("ALTER TABLE regime_outcomes ADD COLUMN superseded_by INTEGER")
        before = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]["null_acc"]
        ts = SESSION_BLOCKS_EPOCH_TS + 86400
        con.execute("INSERT INTO regime_outcomes VALUES (999, 'trend21', 21, ?, ?, ?, 1, 0.82, 'up', 'down', 1)",
                    (ts // 86400, ts, ts + 86400))
        after = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]["null_acc"]
        self.assertEqual(before, after)
        # A superseded row BEFORE the cut-over keeps the old rule: it still counts.
        ts0 = SESSION_BLOCKS_EPOCH_TS - 20 * 86400  # inside the grading window, before the cut-over
        con.execute("INSERT INTO regime_outcomes VALUES (998, 'trend21', 21, ?, ?, ?, 1, 0.82, 'up', 'down', 1)",
                    (ts0 // 86400, ts0, ts0 + 86400))
        legacy = [r for r in grade_structural(con) if r["predictor"] == "trend21"][0]["null_acc"]
        self.assertNotEqual(after, legacy)


if __name__ == "__main__":
    unittest.main()