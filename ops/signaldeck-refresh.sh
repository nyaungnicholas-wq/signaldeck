#!/bin/bash
# SignalDeck DAILY FULL-UNIVERSE REFRESH SWEEP.
#
# Once per trading day, at the first opportunity after the US market close,
# reactivate the WHOLE known universe, let the pipeline pull that day's bars and
# recompute scores/predictions/rankings across everything, then prune back to
# the lean kept set. Net effect: the full universe's daily/weekly data stays
# current, but the CPU cost is ONE short burst per day instead of all-day load.
#
# Idempotent: only runs once per trading day (guarded by meta 'last_full_sweep').
# Triggered by com.signaldeck.daily-refresh (weekdays 13:15 PT, coalesced so it
# also fires on the first wake after close), or manually: `signaldeck refresh`.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB="$SD/data/signaldeck.db"
LOG="$SD/logs/refresh.log"
DOMAIN="gui/$(id -u)"
MAXWAIT="${SIGNALDECK_SWEEP_MAXWAIT:-3600}"   # cap the active window, seconds (default 60m)
KEEP="${SIGNALDECK_SWEEP_KEEP:-150}"          # broad breadth sample kept after the sweep
FORCE="${1:-}"                                 # pass "force" to ignore the once-per-day guard

log(){ echo "$(date '+%F %T') $*" >> "$LOG"; }
q(){ sqlite3 "$DB" "$1" 2>/dev/null; }                 # read
qw(){ sqlite3 -cmd ".timeout 60000" "$DB" "$1"; }      # write — waits up to 60s for the lock

# ── 1. target trading day (PT): today if weekday & >=13:05, else prior weekday ──
dow=$(date +%u); hnow=$((10#$(date +%H)*60 + 10#$(date +%M)))
target=$(date +%F)
if [ "$dow" -ge 6 ] || { [ "$dow" -le 5 ] && [ "$hnow" -lt 785 ]; }; then
  d=1; while [ "$d" -le 7 ]; do
    [ "$(date -v-${d}d +%u)" -le 5 ] && { target=$(date -v-${d}d +%F); break; }
    d=$((d+1))
  done
fi
if [ "$FORCE" != "force" ] && [ "$(q "SELECT v FROM meta WHERE k='last_full_sweep';")" = "$target" ]; then
  log "already swept $target — skip"; exit 0
fi

# ── load-guard: never kick off a heavy full-universe reactivation while the Mac
#    is already slammed (e.g. a video render). It would make things far worse and
#    the sweep itself can stall under the thrash. Defer — the trigger retries on
#    the next wake/day. Override with `force`.
LOAD_MAX="${SIGNALDECK_SWEEP_LOAD_MAX:-24}"
load1=$(uptime | sed -E 's/.*averages?: ([0-9.]+).*/\1/' | cut -d. -f1)
if [ "$FORCE" != "force" ] && [ "${load1:-0}" -gt "$LOAD_MAX" ]; then
  log "machine busy (1-min load ${load1} > ${LOAD_MAX}) — deferring today's sweep"; exit 0
fi
log "=== sweep start (trading day $target, maxwait ${MAXWAIT}s) ==="

# ── 2. reactivate the WHOLE known universe BEFORE (re)starting the daemon, so ──
#      the daemon's startup universe-poller pass covers the full set.
was_up=$(pgrep -f 'bin/signaldeckd' >/dev/null && echo yes || echo no)
q "UPDATE symbols SET active=1 WHERE market='stocks';"
log "reactivated full universe: $(q 'SELECT count(*) FROM symbols WHERE active=1;') active"

# ── 3. ensure the daemon is running (background priority via its plist) ──
if [ "$was_up" = "no" ]; then
  launchctl kickstart "$DOMAIN/com.signaldeck.daemon" 2>/dev/null
  for i in $(seq 1 30); do pgrep -f 'bin/signaldeckd' >/dev/null && break; sleep 1; done
fi
react_ts=$(date +%s)

# ── 4. wait until the key workers each finish a pass over the full set ──
workers_done(){
  q "SELECT count(DISTINCT worker) FROM worker_runs
     WHERE worker IN ('universe-poller','signal-runner','composite-scorer','prediction-runner','ranking-runner')
       AND started_at>=$react_ts AND status='ok';"
}
deadline=$((react_ts+MAXWAIT))
while [ "$(date +%s)" -lt "$deadline" ]; do
  [ "$(workers_done)" -ge 5 ] && { log "all 5 key workers refreshed the full set"; break; }
  sleep 30
done
log "processing window: $(($(date +%s)-react_ts))s elapsed"

# ── 5. stop the daemon ALWAYS before pruning, so the re-prune writes to a FREE db.
#      (the daemon holds the single SQLite writer while backfilling; pruning under
#      that lock stalls — which is exactly how a past sweep got stuck. SIGKILL
#      fallback guarantees it's down.) We restart it in step 8 only if it was up.
launchctl kill TERM "$DOMAIN/com.signaldeck.daemon" 2>/dev/null
for i in $(seq 1 40); do pgrep -f 'bin/signaldeckd' >/dev/null || break; sleep 1; done
if pgrep -f 'bin/signaldeckd' >/dev/null; then pkill -9 -f 'bin/signaldeckd'; sleep 1; fi
log "daemon stopped for a clean prune"

# ── 6. prune back to lean kept set; qw() waits for the lock; verify + retry once ──
NOW=$(date +%s); CUT=$((NOW-60*86400))
reprune="WITH broad AS (SELECT id FROM symbols WHERE active=1 AND market='stocks' AND id NOT IN (SELECT symbol_id FROM user_symbols)),
   liq AS (SELECT b.id, AVG(bar.close*bar.volume) dv FROM broad b JOIN bars bar ON bar.symbol_id=b.id AND bar.tf='1d' AND bar.ts>=$CUT GROUP BY b.id),
   keep AS (SELECT id FROM liq ORDER BY dv DESC, id ASC LIMIT $KEEP)
   UPDATE symbols SET active=0 WHERE id IN (SELECT id FROM broad WHERE id NOT IN (SELECT id FROM keep));"
qw "$reprune"
after=$(q 'SELECT count(*) FROM symbols WHERE active=1;')
if [ "${after:-9999}" -gt 500 ]; then
  log "re-prune returned $after active (DB busy?) — retrying in 5s"; sleep 5
  qw "$reprune"; after=$(q 'SELECT count(*) FROM symbols WHERE active=1;')
fi
log "pruned back to $after active"

# ── 7. mark this trading day done ──
qw "INSERT INTO meta(k,v) VALUES('last_full_sweep','$target') ON CONFLICT(k) DO UPDATE SET v='$target';"

# ── 7b. nightly storage budget report — sdmaint storage-report prints per-table
#      dbstat MB + WAL/backup/log sizes vs declared budgets and exits non-zero
#      when any surface outgrew its budget. Paged through the same remote
#      transports the daemon's internal/notify uses (daemon/.env), so a growth
#      regression is a page the night it starts instead of a 13GB surprise.
#      Best-effort: a missing binary or notify failure never fails the sweep. ──
notify_remote() {
  local msg="$1" env="$SD/daemon/.env" tok chat disc hook
  [ -f "$env" ] || return 0
  tok=$(sed -n 's/^SIGNALDECK_TELEGRAM_BOT_TOKEN=//p' "$env" | tail -1)
  chat=$(sed -n 's/^SIGNALDECK_TELEGRAM_CHAT_ID=//p' "$env" | tail -1)
  disc=$(sed -n 's/^SIGNALDECK_DISCORD_WEBHOOK=//p' "$env" | tail -1)
  hook=$(sed -n 's/^SIGNALDECK_WEBHOOK_URL=//p' "$env" | tail -1)
  if [ -n "$tok" ] && [ -n "$chat" ]; then
    curl -sS -m 10 -X POST "https://api.telegram.org/bot${tok}/sendMessage" \
      --data-urlencode "chat_id=${chat}" --data-urlencode "text=${msg}" >/dev/null 2>&1
  fi
  if [ -n "$disc" ]; then
    curl -sS -m 10 -H 'Content-Type: application/json' \
      -d "$(python3 -c 'import json,sys; print(json.dumps({"content": sys.argv[1][:1900]}))' "$msg")" \
      "$disc" >/dev/null 2>&1
  fi
  if [ -n "$hook" ]; then
    curl -sS -m 10 -H 'Content-Type: application/json' \
      -d "$(python3 -c 'import json,sys,time; print(json.dumps({"title":"SignalDeck storage","body":sys.argv[1],"kind":"storage","ts":int(time.time())}))' "$msg")" \
      "$hook" >/dev/null 2>&1
  fi
}
SDMAINT="$SD/daemon/bin/sdmaint"
if [ -x "$SDMAINT" ]; then
  report=$(cd "$SD" && "$SDMAINT" storage-report -db "$DB" \
    -budget-db-mb "${SIGNALDECK_BUDGET_DB_MB:-4096}" \
    -budget-wal-mb "${SIGNALDECK_BUDGET_WAL_MB:-512}" \
    -budget-backups-mb "${SIGNALDECK_BUDGET_BACKUPS_MB:-12288}" \
    -budget-logs-mb "${SIGNALDECK_BUDGET_LOGS_MB:-512}" 2>&1)
  rc=$?
  printf '%s\n' "$report" >> "$LOG"
  if [ "$rc" -ne 0 ]; then
    over=$(printf '%s\n' "$report" | grep -E 'OVER BUDGET')
    log "STORAGE OVER BUDGET — paging"
    notify_remote "SignalDeck storage budget exceeded (nightly sweep, $target):
$over
Full report in logs/refresh.log"
    osascript -e "display notification \"A storage surface outgrew its budget — see logs/refresh.log.\" with title \"SignalDeck storage\"" >/dev/null 2>&1
  else
    log "storage report: all surfaces within budget"
  fi
else
  log "sdmaint not built at $SDMAINT — skipping storage report (build: cd daemon && go build -o bin/sdmaint ./cmd/sdmaint)"
fi

# ── 8. restart the daemon only if it was already running before the sweep
#      (a manual refresh during market hours); otherwise leave it off. ──
if [ "$was_up" = "yes" ]; then
  launchctl kickstart "$DOMAIN/com.signaldeck.daemon" 2>/dev/null
  log "daemon restarted (was up before the sweep)"
fi
log "=== sweep done for $target (active=$after) ==="
