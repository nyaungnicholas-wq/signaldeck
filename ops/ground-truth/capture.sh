#!/usr/bin/env bash
# Capture ground truth to FILES, not terminal scrollback.
#
# Run this FIRST, before any investigation, and again after any deploy. Every
# agent brief should quote these facts verbatim so agents spend their budget
# investigating instead of rediscovering constants.
#
# Read-only: opens the DB with ?mode=ro, touches no service and no git state.
#
#   bash ops/ground-truth/capture.sh     -> ops/ground-truth/facts-<ts>.md
set -uo pipefail
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TS="$(date +%Y%m%d-%H%M%S)"
OUT="$SD/ops/ground-truth/facts-$TS.md"
# SQLite on Windows needs a native path (C:/...), not the Git Bash form (/c/...).
# Without this the queries return NOTHING and every section below renders empty —
# which is worse than an error, because an empty crux section reads like "no data"
# rather than "the script is broken".
SDWIN="$(cygpath -m "$SD" 2>/dev/null || echo "$SD")"
DB="file:$SDWIN/data/signaldeck.db?mode=ro"
mkdir -p "$SD/ops/ground-truth"

{
echo "# SignalDeck ground truth — $TS"

echo; echo '## Git'; echo '```'
echo "HEAD    $(git -C "$SD" rev-parse HEAD 2>/dev/null)"
echo "branch  $(git -C "$SD" rev-parse --abbrev-ref HEAD 2>/dev/null)"
echo "ahead   $(git -C "$SD" rev-list --count '@{u}'..HEAD 2>/dev/null || echo '?')"
git -C "$SD" status --porcelain 2>/dev/null | head -30
echo '```'

echo; echo '## Deploy gap — running binary vs HEAD'; echo '```'
RUNREV="$(curl -s -m 8 http://localhost:8322/api/version 2>/dev/null | sed -n 's/.*"revision":"\([^"]*\)".*/\1/p')"
HEADREV="$(git -C "$SD" rev-parse HEAD 2>/dev/null)"
echo "running  ${RUNREV:-<daemon not responding>}"
echo "HEAD     $HEADREV"
if [ -n "$RUNREV" ] && [ "$RUNREV" != "$HEADREV" ]; then
  echo "STALE    behind by $(git -C "$SD" rev-list --count "$RUNREV".."$HEADREV" 2>/dev/null || echo '?') commit(s)"
  echo "         ACTION CLASS = DEPLOY, not CODE."
  echo "         A restart does NOT close this. Only a rebuild does."
elif [ -n "$RUNREV" ]; then
  echo "MATCH    running == HEAD"
fi
echo '```'

echo; echo '## Processes and listeners'; echo '```'
powershell -NoProfile -Command "Get-Process -Name signaldeckd,tickstreamd -ErrorAction SilentlyContinue | Select-Object Name,Id,StartTime | Format-Table -AutoSize" 2>/dev/null | sed '/^[[:space:]]*$/d'
powershell -NoProfile -Command "Get-NetTCPConnection -State Listen | Where-Object {\$_.LocalPort -in 8321,8322,8323,8787} | Select-Object LocalPort,OwningProcess | Format-Table -AutoSize" 2>/dev/null | sed '/^[[:space:]]*$/d'
echo '```'

echo; echo '## Data (read-only)'; echo '```'
echo "db bytes $(stat -c %s "$SD/data/signaldeck.db" 2>/dev/null)"
sqlite3 "$DB" "SELECT 'symbols       '||COUNT(*)||' total, '||SUM(active)||' active' FROM symbols;" 2>/dev/null
sqlite3 "$DB" "SELECT 'outcomes      '||horizon||': '||COUNT(*)||' rows, '||SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END)||' resolved' FROM prediction_outcomes GROUP BY horizon;" 2>/dev/null
sqlite3 "$DB" "SELECT 'unresolved    '||COUNT(*) FROM prediction_outcomes WHERE resolved_at IS NULL;" 2>/dev/null
sqlite3 "$DB" "SELECT 'paper open    '||COUNT(*) FROM paper_positions;" 2>/dev/null
echo '```'

echo; echo '## CRUX — independent sample size, not row count'; echo
echo 'The row count is NOT the sample size. Roughly a thousand symbols share one'
echo 'market move per day, so trade-level n overstates independence by one to two'
echo 'orders of magnitude. Never quote an accuracy or return figure without the'
echo 'distinct-day count beside it. (2026-08-06: 165,456 rows were 33 days.)'
echo '```'
sqlite3 "$DB" "
WITH d AS (SELECT ts/86400 day,
                  AVG(CASE WHEN prob>0.5 THEN fwd_return ELSE -fwd_return END)*10000 bps
           FROM prediction_outcomes
           WHERE resolved_at IS NOT NULL AND up IS NOT NULL
             AND fwd_return IS NOT NULL AND horizon='1d'
           GROUP BY day)
SELECT 'INDEPENDENT   '||COUNT(*)||' days, mean '||ROUND(AVG(bps),2)||
       ' bps/day, profitable '||SUM(CASE WHEN bps>0 THEN 1 ELSE 0 END)||'/'||COUNT(*) FROM d;" 2>/dev/null
sqlite3 "$DB" "
SELECT 'trade-level   n='||COUNT(*)||', gross '||
       ROUND(AVG(CASE WHEN prob>0.5 THEN fwd_return ELSE -fwd_return END)*10000,2)||
       ' bps  <- NOT independent, do not quote alone'
FROM prediction_outcomes
WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND fwd_return IS NOT NULL AND horizon='1d';" 2>/dev/null
echo '```'

echo; echo '## Scheduled tasks whose last run failed'; echo '```'
# 267009 = SCHED_S_TASK_RUNNING and 267011 = SCHED_S_TASK_HAS_NOT_RUN are healthy
# states, not failures. Flagging a long-running daemon as failed every time would
# make this section noise, and a monitor nobody reads is worse than none.
powershell -NoProfile -Command "Get-ScheduledTask | Where-Object {\$_.TaskName -match 'SignalDeck'} | ForEach-Object { \$i = \$_ | Get-ScheduledTaskInfo; if (\$i.LastTaskResult -notin 0,267009,267011) { '{0}  last={1}  result={2} (0x{2:X8})' -f \$_.TaskName, \$i.LastRunTime, \$i.LastTaskResult } }" 2>/dev/null | sed '/^[[:space:]]*$/d'
echo '(empty = every task healthy; 267009=running, 267011=never run, both fine)'
echo '```'
} > "$OUT" 2>&1

echo "wrote $OUT"
