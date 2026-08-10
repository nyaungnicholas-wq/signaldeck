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
/bin/bash "$SD/ops/signaldeck-backup-offline.sh"

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
schtasks //Run //TN "SignalDeck Daemon" >/dev/null 2>&1 || true
