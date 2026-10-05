"""Retention checks for ops/signaldeck-cleanup.sh.

On 2026-10-04 14:05 the cleanup kept a 0-byte signaldeck-20261003-200108.db (a
db-backup run cut off by a shutdown) as one of its "2 newest" and moved the
newest sha256-recorded, offsite-verified backup signaldeck-20261002-131010.db to
the Trash. The script now retires 0-byte .db files without counting them and
never retires the newest .db that has a .sha256 sidecar. These tests run the
real script against a scratch tree.
"""

import os
import shutil
import subprocess
import tempfile
import time
import unittest


def find_bash():
    if os.name == "nt":
        path = r"C:\Program Files\Git\bin\bash.exe"
        return path if os.path.exists(path) else None
    else:
        return shutil.which("bash")


SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "ops", "signaldeck-cleanup.sh")


# Fixture times sit well in the past: the script ignores a 0-byte file under an
# hour old (it may still be being written), so a fixed epoch would turn these
# cases into future-dated files as the calendar caught up with it.
BASE = int(time.time()) - 30 * 86400


class CleanupRetentionTest(unittest.TestCase):
    def setUp(self):
        bash = find_bash()
        if bash is None:
            self.skipTest("bash not available")
        self.bash = bash

    def run_cleanup(self, files, keep=None):
        root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, root, True)

        ops_dir = os.path.join(root, "ops")
        data_backups_dir = os.path.join(root, "data", "backups")
        logs_dir = os.path.join(root, "logs")
        trash_dir = os.path.join(root, "trash")
        os.makedirs(ops_dir)
        os.makedirs(data_backups_dir)
        os.makedirs(logs_dir)
        os.makedirs(trash_dir)

        script_path = os.path.join(ops_dir, "signaldeck-cleanup.sh")
        shutil.copyfile(SCRIPT, script_path)

        for name, content, mtime in files:
            path = os.path.join(data_backups_dir, name)
            with open(path, "wb") as f:
                f.write(content)
            os.utime(path, (mtime, mtime))

        env = dict(os.environ, SIGNALDECK_TRASH=trash_dir.replace("\\", "/"))
        cmd = [self.bash, script_path.replace("\\", "/")]
        if keep is not None:
            cmd.append(str(keep))
        result = subprocess.run(
            cmd,
            env=env,
            capture_output=True,
            text=True,
            timeout=120,
        )
        self.assertEqual(
            result.returncode,
            0,
            msg=f"Script failed:\nSTDOUT:\n{result.stdout}\nSTDERR:\n{result.stderr}",
        )

        backups = set(os.listdir(data_backups_dir))
        trash = set(os.listdir(trash_dir))
        return result.stdout, backups, trash

    def test_incident_2026_10_04(self):
        B = BASE
        files = [
            ("signaldeck-20261001-131127.db", b"x", B),
            ("signaldeck-20261002-131010.db", b"good", B + 24 * 3600),
            ("signaldeck-20261002-131010.db.sha256", b"sha", B + 24 * 3600),
            ("signaldeck-20261003-200108.db", b"", B + 55 * 3600),
            ("signaldeck-20261003-200839.db", b"copy", B + 56 * 3600),
        ]
        stdout, backups, trash = self.run_cleanup(files)
        expected_backups = {
            "signaldeck-20261002-131010.db",
            "signaldeck-20261002-131010.db.sha256",
            "signaldeck-20261003-200839.db",
        }
        expected_trash = {
            "signaldeck-20261003-200108.db",
            "signaldeck-20261001-131127.db",
        }
        self.assertEqual(backups, expected_backups, msg=f"stdout:\n{stdout}")
        self.assertEqual(trash, expected_trash, msg=f"stdout:\n{stdout}")
        self.assertNotIn(
            "signaldeck-20261003-200108.db",
            backups,
            msg=f"stdout:\n{stdout}",
        )

    def test_newest_verified_survives_newer_unverified_copies(self):
        B = BASE
        files = [
            ("signaldeck-a.db", b"v", B),
            ("signaldeck-a.db.sha256", b"s", B),
            ("signaldeck-b.db", b"1", B + 3600),
            ("signaldeck-c.db", b"2", B + 7200),
            ("signaldeck-d.db", b"3", B + 10800),
        ]
        stdout, backups, trash = self.run_cleanup(files)
        expected_backups = {"signaldeck-a.db", "signaldeck-a.db.sha256", "signaldeck-c.db", "signaldeck-d.db"}
        expected_trash = {"signaldeck-b.db"}
        self.assertEqual(backups, expected_backups, msg=f"stdout:\n{stdout}")
        self.assertEqual(trash, expected_trash, msg=f"stdout:\n{stdout}")

    def test_keep_count_without_sidecars(self):
        B = BASE
        files = [
            ("signaldeck-1.db", b"1", B),
            ("signaldeck-2.db", b"2", B + 3600),
            ("signaldeck-3.db", b"3", B + 7200),
            ("signaldeck-4.db", b"4", B + 10800),
        ]
        stdout, backups, trash = self.run_cleanup(files, keep=2)
        expected_backups = {"signaldeck-3.db", "signaldeck-4.db"}
        expected_trash = {"signaldeck-1.db", "signaldeck-2.db"}
        self.assertEqual(backups, expected_backups, msg=f"stdout:\n{stdout}")
        self.assertEqual(trash, expected_trash, msg=f"stdout:\n{stdout}")

    def test_empty_file_never_counted_as_generation(self):
        B = BASE
        files = [
            ("signaldeck-old.db", b"old", B),
            ("signaldeck-new.db", b"new", B + 3600),
            ("signaldeck-empty.db", b"", B + 7200),
        ]
        stdout, backups, trash = self.run_cleanup(files, keep=2)
        expected_backups = {"signaldeck-old.db", "signaldeck-new.db"}
        expected_trash = {"signaldeck-empty.db"}
        self.assertEqual(backups, expected_backups, msg=f"stdout:\n{stdout}")
        self.assertEqual(trash, expected_trash, msg=f"stdout:\n{stdout}")


if __name__ == "__main__":
    unittest.main()