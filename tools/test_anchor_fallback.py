"""H-6: the SQLite fallback in ops/anchor-publish.sh publishes an anchor only when the daemon's own checks pass."""
import os, sys, tempfile, unittest, hashlib, shutil, contextlib, io, sqlite3
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import anchor_fallback as af

PUB = "ea4a6c63e29c520abef5507b132ec5f9954776aebebe7b92421eea691446d22c"
HEAD = "d817ee70a2663231731f15af5567bd5439d5555c2037905ce1e3f877635d46c0"
SIG = "f95f610e1a0a4ee76f891dcf4a8b9605de72b27fcd69e647b8cc31a89096eab346fdc907bb62075f5566575636f37c56a51f9c6686baabb514cc5c40de9ac905"
OPENSSL = shutil.which(os.environ.get("SD_OPENSSL", "openssl"))

class TestAnchorFallback(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.tmp.name, "test.db")
        self.pinned_path = os.path.join(self.tmp.name, "pinned.txt")
        self.conn = sqlite3.connect(self.db_path)
        self.conn.execute("CREATE TABLE prediction_ledger(seq INTEGER PRIMARY KEY AUTOINCREMENT, entry_hash TEXT NOT NULL)")
        self.conn.execute("""CREATE TABLE ledger_anchors(
            seq INTEGER PRIMARY KEY AUTOINCREMENT,
            created_at INTEGER,
            ledger_seq INTEGER,
            ledger_count INTEGER,
            head_hash TEXT,
            alg TEXT,
            pub_key TEXT,
            sig TEXT,
            digest TEXT)""")
        self.conn.commit()
        for h in ("a"*64, "b"*64, HEAD, "c"*64):
            self.conn.execute("INSERT INTO prediction_ledger(entry_hash) VALUES (?)", (h,))
        self.conn.commit()
        with open(self.pinned_path, "w") as f:
            f.write("# pinned keys\n" + PUB + "  # test key\n")
        msg = f"signaldeck-ledger-anchor|v1|alg=ed25519|created_at=1790000000|ledger_seq=3|ledger_count=3|head={HEAD}"
        digest = hashlib.sha256(msg.encode() + b"\x1e" + SIG.encode() + b"\x1e" + PUB.encode()).hexdigest()
        self.expected_line = f"SIGNALDECK-LEDGER-ANCHOR v1 seq=3 count=3 ts=1790000000 digest={digest}"

    def tearDown(self):
        if hasattr(self, 'conn'):
            self.conn.close()
        self.tmp.cleanup()

    def sql(self, statement, params=()):
        self.conn.execute(statement, params)
        self.conn.commit()

    def anchor(self, head=HEAD, sig=SIG, pub=PUB, count=3, seq=3, digest=None):
        if digest is None:
            msg = f"signaldeck-ledger-anchor|v1|alg=ed25519|created_at=1790000000|ledger_seq={seq}|ledger_count={count}|head={head}"
            digest = hashlib.sha256(msg.encode() + b"\x1e" + sig.encode() + b"\x1e" + pub.encode()).hexdigest()
        self.conn.execute("""INSERT INTO ledger_anchors
            (created_at, ledger_seq, ledger_count, head_hash, alg, pub_key, sig, digest)
            VALUES (?,?,?,?,?,?,?,?)""",
            (1790000000, seq, count, head, "ed25519", pub, sig, digest))
        self.conn.commit()

    @unittest.skipUnless(OPENSSL, "openssl not installed")
    def test_valid_anchor_publishes(self):
        self.anchor()
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertEqual(line, self.expected_line)
        self.assertIsNone(reason)

    @unittest.skipUnless(OPENSSL, "openssl not installed")
    def test_forged_signature_with_consistent_digest_is_refused(self):
        forged = SIG[:-1] + ("0" if SIG[-1] != "0" else "1")
        self.anchor(sig=forged)
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("signature", reason.lower())

    def test_head_not_in_ledger_is_refused(self):
        self.anchor(head="d"*64)
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("reproduces", reason.lower())

    def test_rewritten_ledger_is_refused(self):
        self.anchor()
        self.sql("UPDATE prediction_ledger SET entry_hash=? WHERE seq=3", ("e"*64,))
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("reproduces", reason.lower())

    def test_missing_ledger_row_is_refused(self):
        self.anchor()
        self.sql("DELETE FROM prediction_ledger WHERE seq=3")
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("no longer exists", reason.lower())

    def test_row_count_mismatch_is_refused(self):
        self.anchor()
        self.sql("DELETE FROM prediction_ledger WHERE seq=1")
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("row count", reason.lower())

    def test_unpinned_key_is_refused(self):
        with open(self.pinned_path, "w") as f:
            f.write("f"*64 + "\n")
        self.anchor()
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("unpinned", reason.lower())

    def test_malformed_pinned_line_is_refused(self):
        with open(self.pinned_path, "a") as f:
            f.write("not-a-key\n")
        self.anchor()
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("pinned key file", reason)

    def test_hex_with_spaces_is_refused(self):
        # Go's hex.DecodeString refuses it, so the daemon would fail this anchor.
        self.anchor(sig=SIG[:64] + " " + SIG[65:])
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("malformed", reason)

    @unittest.skipUnless(OPENSSL, "openssl not installed")
    def test_stale_digest_is_refused(self):
        self.anchor(digest="0"*64)
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("digest", reason.lower())

    def test_no_openssl_fails_closed(self):
        self.anchor()
        fake_openssl = os.path.join(self.tmp.name, "no-such-openssl")
        line, reason = af.check(self.db_path, self.pinned_path, openssl=fake_openssl)
        self.assertIsNone(line)
        self.assertIn("openssl", reason.lower())

    @unittest.skipUnless(OPENSSL, "openssl not installed")
    def test_newest_anchor_is_the_one_checked(self):
        self.anchor()
        self.anchor(head="d"*64)  # newer, bad head
        line, reason = af.check(self.db_path, self.pinned_path)
        self.assertIsNone(line)
        self.assertIn("reproduces", reason.lower())

    @unittest.skipUnless(OPENSSL, "openssl not installed")
    def test_main_exit_codes(self):
        self.anchor()
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = af.main([self.db_path, self.pinned_path])
        self.assertEqual(rc, 0)
        self.assertEqual(out.getvalue().strip(), self.expected_line)
        self.assertEqual(err.getvalue(), "")
        # newer bad anchor
        self.anchor(head="d"*64)
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = af.main([self.db_path, self.pinned_path])
        self.assertEqual(rc, 1)
        self.assertEqual(out.getvalue().strip(), "")
        self.assertIn("anchor_fallback: refused:", err.getvalue())
        # no args
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = af.main([])
        self.assertEqual(rc, 2)
        self.assertEqual(out.getvalue().strip(), "")
        self.assertIn("usage", err.getvalue())

if __name__ == "__main__":
    unittest.main()
