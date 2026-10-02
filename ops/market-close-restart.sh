#!/bin/bash
# Restarts the daemon after the market-close backup and exits with the
# schtasks result. Called by ops/market-close.sh twice over: as the backup's
# DB-phase hook (SIGNALDECK_BACKUP_DB_DONE_CMD, so the API is back BEFORE the
# ~8 min offsite upload, SD-57), and again after the backup only if the
# daemon still is not running (the hook failed or never ran).
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Bring the stack back up. Before this line the script simply ENDED with the
# daemon stopped, so the 13:10 backup took SignalDeck down for the rest of the
# day, every weekday — the daemon exits 0, which Task Scheduler reads as
# success, so nothing ever restarted it.
#
# Drop the lock first, then kick the task (which runs daemon-guard.ps1 with
# native Windows paths, avoiding Git Bash path mangling). If schtasks is
# unavailable or fails, the 5-minute keepalive still recovers it — this only
# shortens the gap from minutes to seconds.
rm -f "$SD/ops/.maintenance" # the market-close lock: guards may act again

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
exit "$daemon_rc"
