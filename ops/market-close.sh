#!/bin/bash
# Market-close sequence (13:10 PT weekdays): stop the stack, then take the
# daily backup OFFLINE — with the daemon down the 2GB+ VACUUM INTO has zero
# contention with the app (see ops/signaldeck-backup-offline.sh header).
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# pgrep does not exist under Git Bash, so both liveness checks below read as
# "already exited": the wait loop broke on its first pass and the escalation
# never fired, whatever the daemon was doing.
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"

# The daemon is stopped below on purpose, and ops/daemon-guard.ps1 now runs
# every 5 minutes to restart it whenever it is found down. Without a signal
# that THIS downtime is deliberate, the guard would restart the daemon in the
# middle of the VACUUM INTO and signaldeck-backup-offline.sh would skip the
# run outright (its safety check refuses to back up a live daemon) — we would
# silently trade a dead daemon for a missing backup.
#
# The trap is the load-bearing half. This script was seen dying mid-run
# (STATUS_CONTROL_C_EXIT, 2026-08-03); without it, that crash would hold the
# lock and keep the daemon down until someone noticed. daemon-guard.ps1 also
# expires the lock after 90m as a second net.
LOCK="$SD/ops/.maintenance"
release_lock() { rm -f "$LOCK"; }
trap release_lock EXIT INT TERM
: > "$LOCK"

/bin/bash "$SD/ops/signaldeck-ctl.sh" stop
# Graceful daemon shutdown (worker drain + WAL checkpoint) can take a minute —
# a fixed 8s sleep made the backup's is-daemon-alive safety check skip the run
# (seen live 2026-07-24). Wait for the process to actually exit, capped at 3m.
for _ in $(seq 1 90); do
  sd_is_running signaldeckd || break
  sleep 2
done
# Hung-drain escalation (seen live 2026-07-24: daemon sat 30+ min post-TERM,
# parked at 0% CPU — a stuck worker drain). SQLite under WAL is crash-safe,
# and a zombie daemon would also break the next morning's kickstart, so after
# the 3-minute grace we force-kill.
if sd_is_running signaldeckd; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: daemon ignored TERM for 3m — SIGKILL" >> "$SD/logs/backup-offline.log"
  # sd_kill_hard, not a raw `pkill -9`. pkill is absent under the Git Bash the
  # scheduled task runs (verified: `command -v pkill` fails there), so this line
  # was a `command not found` — the same class of defect the header above says
  # was fixed for pgrep, missed one call site down. The escalation therefore
  # no-opped, the daemon stayed up, and signaldeck-backup-offline.sh's
  # is-daemon-alive check then refused the run: the day's backup vanished with
  # the task still exiting 0. sd_kill_hard falls back to taskkill, which exists.
  if ! sd_kill_hard signaldeckd; then
    echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: force-kill FAILED (no pkill, no taskkill) — backup will be skipped" >> "$SD/logs/backup-offline.log"
  fi
  sleep 5
fi

# --- Backup + daemon restart: capture exit codes, do NOT swallow them ---
# A task that reports 0x00000000 while the day's backup silently vanished is
# indistinguishable from a healthy night. Measured 2026-08-11: Task Scheduler
# showed `SignalDeck Market-Close` LastResult 0x00000000 while
# logs/backup-offline.log had no entries at all for 2026-08-08 or 2026-08-09 —
# the task was green and the backups were missing. This file's exit code is the
# only signal Task Scheduler can see, so we propagate the backup's own exit
# code (1 on VACUUM failure, content-verification failure, missing python, or
# a backup-budget breach; also 0 after logging "SKIP" when the daemon was
# still running — the exact 2026-08-08/09 path). We deliberately do NOT add
# `set -e` here: the script must continue past a backup failure to restart
# the daemon, so a failed backup never leaves the daemon down.
/bin/bash "$SD/ops/signaldeck-backup-offline.sh"
backup_rc=$?

# Bring the stack back up. Before this line the script simply ENDED with the
# daemon stopped, so the 13:10 backup took SignalDeck down for the rest of the
# day, every weekday — the daemon exits 0, which Task Scheduler reads as
# success, so nothing ever restarted it.
#
# Drop the lock first, then kick the task (which runs daemon-guard.ps1 with
# native Windows paths, avoiding Git Bash path mangling). If schtasks is
# unavailable or fails, the 5-minute keepalive still recovers it — this only
# shortens the gap from minutes to seconds.
release_lock

# RUN THE PROVENANCE PREFLIGHT ON THIS PATH TOO.
#
# The stale-binary check lives in ops/daemon-guard.ps1, which only the 5-minute
# Keepalive reaches. Every other way the daemon starts skips it: the AtLogOn
# trigger, RestartOnFailure, sd_svc_start, and this line. Evidence it is not
# theoretical -- "SignalDeck Daemon" last ran 2026-08-21 21:01 while the last
# entry in logs/daemon-provenance.log was 2026-08-20 14:20, and every logged
# entry ends in :03 seconds, the Keepalive's tick offset.
#
# This does NOT redirect the restart through the guard. Kicking the Keepalive
# instead would be the fuller fix, but this exact line exists BECAUSE the 13:10
# backup once took SignalDeck down for the rest of the day, every weekday, and
# MultipleInstances=IgnoreNew means a kick can be swallowed. Trading a verified
# seconds-long gap for an unverified one is not a repair.
#
# So: observe rather than reroute. -CheckOnly writes logs/daemon-provenance.log
# and cannot start, stop or modify anything, and its output is captured here
# instead of the console S4U does not have. Never fatal -- a stale binary is a
# thing to KNOW at market close, not a reason to leave the daemon down.
if [ -x "$(command -v powershell)" ] || command -v powershell >/dev/null 2>&1; then
  powershell -NoProfile -ExecutionPolicy Bypass -File "$(cygpath -w "$SD/ops/run-daemon-with-provenance.ps1" 2>/dev/null || echo "$SD/ops/run-daemon-with-provenance.ps1")" -CheckOnly \
    >> "$SD/logs/daemon-provenance.log" 2>&1 \
    || echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: provenance preflight reported a problem (non-fatal; see logs/daemon-provenance.log)" >> "$SD/logs/backup-offline.log"
fi

schtasks //Run //TN "SignalDeck Daemon" >/dev/null 2>&1
daemon_rc=$?

if [ "$daemon_rc" -ne 0 ]; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: daemon restart FAILED (schtasks rc=$daemon_rc) — 5-minute keepalive should still recover it" >> "$SD/logs/backup-offline.log"
fi

# Grade the pre-registered forward test (prereg kind forward-test-registration,
# testId confluence-long-liquid-2026-08). It runs AFTER the daemon is back up so
# the write takes the same busy_timeout path as everything else, rather than
# racing the restart for the lock.
#
# It grades only sessions whose forward return has already resolved, so running
# at market close picks up yesterday's session and never half-grades today's.
#
# No --start here, deliberately. The window boundary belongs to the registration
# record (prereg seq 87, 2026-08-23T00:14:36Z) and now lives in the grader as
# REGISTERED_START, which also refuses to --commit with any other value.
#
# It used to be a constant in THIS file, hand-coupled to the record. That is the
# shape that rots: every confluence bucket is stamped at exactly 00:00:00 UTC, so
# a bucket dated 2026-08-23 sits 14m36s BEFORE the record that registered it, and
# a FT_START naming the LOCAL filing date (2026-08-22) would have admitted it and
# graded a pre-registration session as forward evidence. One constant in one place
# cannot disagree with itself.
#
# Deliberately cannot fail this script. A research grade is not a reason to
# report the market-close backup as broken, and letting it mask a backup or
# daemon failure would be strictly worse than missing one session.
#
# NOT failing this script is not the same as nobody being told. Every run is
# stamped, and both ways this can die now raise sd_notify:
#
#   * the grader exits non-zero, and
#   * the grader exits ZERO and records nothing, run after run.
#
# The second is the one that actually threatens the test. Nothing read this log
# before, so the single live experiment in the repo could have sat at zero
# sessions until November and every surface would have stayed green — the
# silent-success shape this repo keeps rediscovering. An unstamped success line
# also made a SKIPPED run and a zero-session run identical on disk.
echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: forward-test grading starting" >> "$SD/logs/forward-test.log"
if ! "$SD/.venv/Scripts/python.exe" "$SD/tools/forward_test.py" \
     --db "$SD/data/signaldeck.db" --commit --verdict \
     >> "$SD/logs/forward-test.log" 2>&1; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: forward-test grading FAILED (non-fatal, market-close result unaffected)" >> "$SD/logs/forward-test.log"
  sd_notify "SignalDeck forward test" "Grading FAILED — prereg seq 87 recorded nothing today. See logs/forward-test.log."
fi

# Liveness, not just exit status. FT_QUIET_DAYS is a GRACE period, not the
# cadence: a session is only gradable once its forward return resolves, which
# measured 2-4 days behind the session itself, and the book does not produce a
# gradable long entry every single day. Seven days is comfortably past both and
# still far short of a lost month.
#
# The floor is the registration date, so the alert works before the first row
# exists — the state this was actually in when it was written (window open since
# 2026-08-23, forward_test_daily empty). Waiting for a first row to go stale
# would have meant the alert could never fire on the failure that mattered most.
FT_QUIET_DAYS=7
# The wording lives in ops/lib-forward-test.sh. An EMPTY forward_test_daily used to
# be reported as "newest recorded session 2026-08-23 is Nd old" via COALESCE -- a
# session that was never graded (2026-09-01). Firing on an empty table is still
# deliberate; only the sentence changed. 2026-08-23 is the registered start
# (tools/forward_test.py REGISTERED_START).
. "$SD/ops/lib-forward-test.sh"
ft_msg="$(sd_ft_stale_line "$SD/data/signaldeck.db" 2026-08-23 "$FT_QUIET_DAYS")"
if [ -n "${ft_msg:-}" ]; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: forward-test $ft_msg" >> "$SD/logs/forward-test.log"
  sd_notify "SignalDeck forward test" "$ft_msg. prereg seq 87 is not accruing evidence."
fi

if [ "$backup_rc" -ne 0 ]; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: backup FAILED (rc=$backup_rc) — Task Scheduler result will be non-zero" >> "$SD/logs/backup-offline.log"
  exit "$backup_rc"
fi

# Backup succeeded. If the daemon restart failed, surface that; otherwise 0.
exit "$daemon_rc"
