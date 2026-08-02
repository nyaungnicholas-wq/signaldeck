#!/bin/bash
# Market-close sequence (13:10 PT weekdays): stop the stack, then take the
# daily backup OFFLINE — with the daemon down the 2GB+ VACUUM INTO has zero
# contention with the app (see ops/signaldeck-backup-offline.sh header).
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
/bin/bash "$SD/ops/signaldeck-ctl.sh" stop
# Graceful daemon shutdown (worker drain + WAL checkpoint) can take a minute —
# a fixed 8s sleep made the backup's is-daemon-alive safety check skip the run
# (seen live 2026-07-24). Wait for the process to actually exit, capped at 3m.
for _ in $(seq 1 90); do
  pgrep -x signaldeckd >/dev/null 2>&1 || break
  sleep 2
done
# Hung-drain escalation (seen live 2026-07-24: daemon sat 30+ min post-TERM,
# parked at 0% CPU — a stuck worker drain). SQLite under WAL is crash-safe,
# and a zombie daemon would also break the next morning's kickstart, so after
# the 3-minute grace we force-kill.
if pgrep -x signaldeckd >/dev/null 2>&1; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: daemon ignored TERM for 3m — SIGKILL" >> "$SD/logs/backup-offline.log"
  pkill -9 -x signaldeckd
  sleep 5
fi
/bin/bash "$SD/ops/signaldeck-backup-offline.sh"
