import unittest
import subprocess
import shutil
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class MaintenanceLockContract(unittest.TestCase):
    """ops/.maintenance is how a planned database outage announces itself.
    On 2026-11-12 the daily refresh stopped the daemon for an 11-minute prune
    WITHOUT taking it, so daemon-guard restarted the daemon into "database is
    locked" twice, Anchor-Publish failed inside the window, and Check-Task-Health
    went red on a deliberate stop. These tests pin the contract across the four scripts.
    """

    def _read(self, rel: str) -> str:
        path = REPO / rel
        text = path.read_text(encoding="utf-8")
        return text.replace("\r\n", "\n")

    def test_refresh_takes_the_lock_before_stopping_the_daemon(self):
        text = self._read("ops/signaldeck-refresh.sh")
        self.assertIn('MAINT_LOCK="$SD/ops/.maintenance"', text)
        idx_lock = text.index('echo "refresh $$" > "$MAINT_LOCK"')
        idx_stop = text.index('log "daemon stopped for a clean prune"')
        self.assertGreaterEqual(idx_lock, 0)
        self.assertLess(idx_lock, idx_stop)

    def test_refresh_releases_only_its_own_lock(self):
        text = self._read("ops/signaldeck-refresh.sh")
        self.assertIn('release_maint(){', text)
        self.assertIn('[ "$(cat "$MAINT_LOCK" 2>/dev/null)" = "$own_maint" ]', text)
        self.assertIn('if [ ! -e "$MAINT_LOCK" ]; then', text)
        idx_sweep = text.index("last_full_sweep','$target')")
        # the standalone call after step 7, not the one inside on_interrupt
        self.assertNotEqual(text.find("\nrelease_maint\n", idx_sweep), -1,
                            "the lock is not released after the sweep is recorded")

    def test_daemon_guard_honours_the_refresh_as_a_holder(self):
        text = self._read("ops/daemon-guard.ps1")
        for line in text.splitlines():
            if line.lstrip().startswith("$LOCK_HOLDERS ="):
                self.assertIn("'signaldeck-refresh'", line)
                self.assertIn("'market-close'", line)
                return
        self.fail("Line starting with '$LOCK_HOLDERS =' not found")

    def test_anchor_publish_waits_for_the_lock_before_reading(self):
        text = self._read("ops/anchor-publish.sh")
        idx_maint = text.index('maint="$SD/ops/.maintenance"')
        idx_verify = text.index('/api/ledger/verify')
        self.assertLess(idx_maint, idx_verify)

    def test_task_health_does_not_judge_a_planned_stop(self):
        text = self._read("ops/check-task-health.ps1")
        self.assertIn('ops\\.maintenance', text)
        self.assertIn('TotalMinutes -lt 90', text)

    def test_release_maint_leaves_a_foreign_lock_alone(self):
        # The PATH lookup, passed explicitly: CreateProcess searches System32
        # first, so a bare "bash" would start the WSL launcher on Windows.
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("bash not available")
        text = self._read("ops/signaldeck-refresh.sh")
        release_line = None
        for line in text.splitlines():
            if line.strip().startswith("release_maint(){"):
                release_line = line.strip()
                break
        self.assertIsNotNone(release_line, "release_maint function not found")
        with tempfile.TemporaryDirectory() as d:
            # resolve(): an 8.3 short temp path (NICHOL~1) is not writable from Git Bash
            lock_path_posix = Path(d, "maint").resolve().as_posix()
            script = (
                f'MAINT_LOCK="{lock_path_posix}"\n'
                'own_maint="refresh 111"\n'
                f'echo "market-close" > "$MAINT_LOCK"\n'
                f'{release_line}\n'
                'release_maint\n'
                '[ -e "$MAINT_LOCK" ] && echo KEPT\n'
                'echo "refresh 222" > "$MAINT_LOCK"; own_maint="refresh 222"\n'
                'release_maint\n'
                '[ -e "$MAINT_LOCK" ] || echo REMOVED\n'
            )
            # Script as BYTES on stdin: a Windows command line mangles the quotes
            # in -c, and text-mode stdin would turn every \n into \r\n.
            result = subprocess.run(
                [bash, "-s"],
                input=script.encode("utf-8"),
                capture_output=True,
                timeout=30,
            )
            output = (result.stdout + result.stderr).decode("utf-8", "replace")
            self.assertIn("KEPT", output, msg=f"Expected KEPT in output:\n{output}")
            self.assertIn("REMOVED", output, msg=f"Expected REMOVED in output:\n{output}")


if __name__ == "__main__":
    unittest.main(verbosity=2)