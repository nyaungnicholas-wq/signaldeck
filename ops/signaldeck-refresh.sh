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
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
DB="$SD/data/signaldeck.db"
LOG="$SD/logs/refresh.log"
DOMAIN="gui/$(id -u)"
MAXWAIT="${SIGNALDECK_SWEEP_MAXWAIT:-3600}"   # cap the active window, seconds (default 60m)
KEEP="${SIGNALDECK_SWEEP_KEEP:-150}"          # broad breadth sample kept after the sweep
FORCE="${1:-}"                                 # "force" ignores the once-per-day guard;
                                               # "prune-only" runs the fail-safe repair and exits
MINAGE="${2:-0}"                               # prune-only: only repair a marker at least this old (s)

log(){ echo "$(date '+%F %T') $*" >> "$LOG"; }
q(){ sd_sqlite_read "$DB" "$1" 2>/dev/null; }          # read
qw(){ sd_sqlite "$DB" "$1"; }                          # write — waits for the lock

# ── ATOMICITY ────────────────────────────────────────────────────────────────
# This sweep widens symbols.active from ~328 to ~2950 and narrows it back up to
# an hour later. For that hour the machine sits in the FAIL-DANGEROUS state:
# every worker scans 9x the intended set. Nothing here used to make that state
# recoverable, so when the 13:15 run on 2026-08-06 was killed
# (LastTaskResult 3221225786 = 0xC000013A, STATUS_CONTROL_C_EXIT) it left
# 2950/2950 active with no owner — dq-auditor "checked 2950 active symbols",
# downsampler "rolled up 2950 symbols" — and nothing noticed.
#
# Reordering cannot fix this. The prune ranks by 60-day dollar volume, and a
# symbol only HAS recent bars because it was active; ranking before reactivating
# ranks stale data. Nor can one SQL transaction fix it: the window spans an hour
# of work by a SEPARATE process (signaldeckd) that must SEE active=1 committed,
# so the widening must commit, and an hour-long write lock would deadlock the
# daemon it is waiting for. The widening is necessarily durable and visible.
#
# What is achievable is making the dangerous state self-healing. That needs
# three properties, and no one of them is sufficient:
#
#   1. DETECTABLE — "universe is wide" must be readable from the DB by any
#      process. Hence meta 'sweep_open', written in the SAME transaction as the
#      reactivation. Two atomic commits (open, close) mean the invariant
#      `universe wide  <=>  sweep_open present` cannot be broken by a kill at
#      any instruction: there is no interleaving that widens without marking.
#   2. REPAIRABLE WITHOUT THIS PROCESS — the keep criterion is a pure function
#      of the DB, so recovery must not need anything the dead run held in
#      memory. prune_sql() reads nothing from the run; react_ts, was_up and
#      $target are all irrelevant to it.
#   3. REPAIRED ON A SHORT CLOCK — a marker only inspected by tomorrow's sweep
#      still leaves ~24h of exposure, which is the incident we are fixing, just
#      slower. ops/daemon-guard.ps1 already ticks every 5 minutes and already
#      exists to self-heal STATUS_CONTROL_C_EXIT kills; it now calls
#      `signaldeck-refresh.sh prune-only`. Exposure: <=5 min, not <=24h.
#
# A trap is added as well, and is deliberately NOT the mechanism. daemon-guard.ps1's
# own header records why, from this same repo's experience: "a console-control
# kill does not run bash EXIT traps, so the lock outlives the holder". The trap
# only shortens the signal case from <=5 min to ~1s; correctness rests on 1-3.
BUSY="PRAGMA busy_timeout=120000;"

# prune_sql — the lean-set criterion, and the ONLY copy of it. Also clears the
# marker, in the same transaction as the narrowing, closing the window the same
# way opening it was closed. Note what it does NOT touch: last_full_sweep. A
# recovery prune must not claim the day was swept, because it was not.
prune_sql(){
  local cut=$(( $(date +%s) - 60*86400 ))
  cat <<SQL
$BUSY
BEGIN IMMEDIATE;
WITH broad AS (SELECT id FROM symbols WHERE active=1 AND market='stocks' AND id NOT IN (SELECT symbol_id FROM user_symbols)),
   liq AS (SELECT b.id, AVG(bar.close*bar.volume) dv FROM broad b JOIN bars bar ON bar.symbol_id=b.id AND bar.tf='1d' AND bar.ts>=$cut GROUP BY b.id),
   keep AS (SELECT id FROM liq ORDER BY dv DESC, id ASC LIMIT $KEEP)
   UPDATE symbols SET active=0 WHERE id IN (SELECT id FROM broad WHERE id NOT IN (SELECT id FROM keep));
DELETE FROM meta WHERE k='sweep_open';
COMMIT;
SQL
}

# prune_now REASON — run it, verify, retry once. Reports through the LOG only
# and deliberately prints nothing: the leading `PRAGMA busy_timeout` makes the
# sqlite3 CLI emit a result row ("120000") on stdout, so a caller writing
# `after=$(prune_now ...)` would capture "120000328". Callers re-read the count.
#
# Safe to call at ANY point in the sweep: if today's bars were never pulled,
# `liq` simply ranks on the previous 60 days, and if `liq` is empty the result
# is the user set alone — too LEAN, which is the harmless direction.
prune_now(){
  local reason="$1" after
  qw "$(prune_sql)" >/dev/null
  after=$(q 'SELECT count(*) FROM symbols WHERE active=1;')
  if [ "${after:-9999}" -gt 500 ]; then
    log "prune ($reason) returned $after active (DB busy?) — retrying in 5s"; sleep 5
    qw "$(prune_sql)" >/dev/null; after=$(q 'SELECT count(*) FROM symbols WHERE active=1;')
  fi
  log "prune ($reason): $after active"
}

# on_interrupt — FAST PATH ONLY. Armed just before the widening and left armed
# afterwards, where it is a no-op because the marker is gone. Do not mistake
# this for the safety mechanism: a taskkill /F, a STATUS_CONTROL_C_EXIT console
# kill, a power loss or a bluescreen all skip it, and the incident this fixes
# was exactly such a kill. It exists only so the ordinary Ctrl-C costs one
# second of exposure instead of five minutes.
on_interrupt(){
  local rc=$?
  trap - EXIT INT TERM HUP
  if [ -n "$(q "SELECT v FROM meta WHERE k='sweep_open';")" ]; then
    log "exiting (rc=$rc) with the universe wide open — pruning on the way out"
    prune_now "interrupt rc=$rc"
  fi
  exit "$rc"
}

# ── 0. FAIL-SAFE REPAIR, before anything else can exit past it. A marker left
#      in the DB means a previous run died with the universe wide open. This
#      block must sit ABOVE the once-per-day guard: the killed run of
#      2026-08-06 would otherwise be skipped by that guard for the rest of the
#      day while 2950 symbols stayed active.
stale=$(q "SELECT v FROM meta WHERE k='sweep_open';")
if [ -n "$stale" ]; then
  # The marker is the epoch the sweep opened. Anything non-numeric is treated
  # as ancient, so a corrupt value repairs rather than blocks.
  case "$stale" in ''|*[!0-9]*) age=999999999 ;; *) age=$(( $(date +%s) - stale )) ;; esac
  if [ "$age" -ge "$MINAGE" ]; then
    log "sweep_open set ${age}s ago — a sweep died with the universe wide open; repairing"
    prune_now "recovery, marker ${age}s old"
  else
    log "sweep_open set ${age}s ago, under the ${MINAGE}s ceiling — a sweep is still working"
  fi
fi
if [ "$FORCE" = "prune-only" ]; then
  [ -z "$stale" ] && log "prune-only: universe not open — nothing to do"
  exit 0
fi

# ── 1. target trading day (PT): today if weekday & >=13:05, else prior weekday ──
dow=$(date +%u); hnow=$((10#$(date +%H)*60 + 10#$(date +%M)))
target=$(date +%F)
if [ "$dow" -ge 6 ] || { [ "$dow" -le 5 ] && [ "$hnow" -lt 785 ]; }; then
  d=1; while [ "$d" -le 7 ]; do
    [ "$(sd_dow_days_ago $d)" -le 5 ] && { target=$(sd_days_ago $d); break; }
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
#
#      Three changes here, all load-bearing:
#
#      (a) qw, not q. `q` is sd_sqlite_read — the READ helper, with stderr
#          discarded — and it was being used to perform the single most
#          consequential WRITE in the file. On its Python backend that write is
#          NEVER COMMITTED (default isolation_level opens an implicit
#          transaction and con.close() discards it); on its sqlite3-CLI backend
#          it carries no busy_timeout and simply loses to the daemon, which is
#          still running at this point and holds the writer. Either way it fails
#          in silence. logs/refresh.log:
#             2026-08-03 13:15:04 reactivated full universe: 329 active
#             2026-08-05 13:15:03 reactivated full universe: 329 active
#          329 is the LEAN set. On those days the "full-universe refresh" did
#          not refresh the full universe, and the log said so without anyone
#          reading it that way.
#      (b) the marker commits WITH the widening. See ATOMICITY above.
#      (c) the result is CHECKED. A step whose failure mode is "silently does
#          nothing" must not also be a step nobody verifies.
was_up=$(sd_is_running signaldeckd && echo yes || echo no)
trap on_interrupt EXIT INT TERM HUP
qw "$BUSY
BEGIN IMMEDIATE;
INSERT INTO meta(k,v) VALUES('sweep_open', CAST(strftime('%s','now') AS TEXT))
  ON CONFLICT(k) DO UPDATE SET v=excluded.v;
UPDATE symbols SET active=1 WHERE market='stocks';
COMMIT;" >/dev/null
if [ -z "$(q "SELECT v FROM meta WHERE k='sweep_open';")" ]; then
  # Nothing committed, so nothing was widened either — that is the whole point
  # of putting them in one transaction. The universe is still lean; leave it.
  trap - EXIT INT TERM HUP
  log "reactivation did not commit (DB locked?) — sweep aborted, universe left lean at $(q 'SELECT count(*) FROM symbols WHERE active=1;') active"
  exit 1
fi
log "reactivated full universe: $(q 'SELECT count(*) FROM symbols WHERE active=1;') active"

# ── 3. ensure the daemon is running (background priority via its plist) ──
if [ "$was_up" = "no" ]; then
  sd_svc_start com.signaldeck.daemon
  for i in $(seq 1 30); do sd_is_running signaldeckd && break; sleep 1; done
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
sd_svc_stop com.signaldeck.daemon
for i in $(seq 1 40); do sd_is_running signaldeckd || break; sleep 1; done
if sd_is_running signaldeckd; then sd_kill_hard signaldeckd; sleep 1; fi
log "daemon stopped for a clean prune"

# ── 6. prune back to the lean kept set. The criterion now lives in prune_sql()
#      and nowhere else, because the recovery paths (step 0, the trap, and
#      ops/daemon-guard.ps1) must narrow to the SAME set this does — a second
#      copy that drifts would make recovery a different universe than a
#      completed sweep. The marker clears inside that same transaction.
prune_now "end of sweep"
after=$(q 'SELECT count(*) FROM symbols WHERE active=1;')

# ── 7. mark this trading day done. Deliberately NOT part of prune_sql: the
#      recovery paths share the prune but must not claim the day was swept,
#      or a run killed at 13:16 would suppress the retry that repairs it.
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
      -d "$("$(sd_py)" -c 'import json,sys; print(json.dumps({"content": sys.argv[1][:1900]}))' "$msg")" \
      "$disc" >/dev/null 2>&1
  fi
  if [ -n "$hook" ]; then
    curl -sS -m 10 -H 'Content-Type: application/json' \
      -d "$("$(sd_py)" -c 'import json,sys,time; print(json.dumps({"title":"SignalDeck storage","body":sys.argv[1],"kind":"storage","ts":int(time.time())}))' "$msg")" \
      "$hook" >/dev/null 2>&1
  fi
}
# Same suffix + rebuild idiom as ops/restore-rehearsal.sh, and for the same
# reason: this pointed at $SD/daemon/bin/sdmaint, which is neither where the
# binary is built (bin/ at the repo root) nor runnable on Windows without the
# .exe suffix. A stale macOS build does sit at daemon/bin/sdmaint, and it is
# not -x here — so from the move to Windows onward every sweep logged
# "sdmaint not built ... skipping storage report" and the nightly storage
# budget went unchecked. data/ reached 27 GB against a 12 GB backups budget
# with nothing paging about it, which is precisely the "13GB surprise" the
# block above says it exists to prevent.
SDMAINT_EXE=""
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) SDMAINT_EXE=".exe" ;; esac
SDMAINT="$SD/bin/sdmaint$SDMAINT_EXE"
if [ ! -x "$SDMAINT" ]; then
  GO="$HOME/.local/go-sdk/go/bin/go"
  [ -x "$GO" ] || GO="$(command -v go || true)"
  if [ -n "$GO" ] && [ -x "$GO" ]; then
    (cd "$SD/daemon" && "$GO" build -o "$SDMAINT" ./cmd/sdmaint) >/dev/null 2>&1
  fi
fi
if [ -x "$SDMAINT" ]; then
  report=$(cd "$SD" && "$SDMAINT" storage-report -db "$DB" \
    -budget-db-mb "${SIGNALDECK_BUDGET_DB_MB:-4096}" \
    -budget-wal-mb "${SIGNALDECK_BUDGET_WAL_MB:-512}" \
    -budget-backups-mb "${SIGNALDECK_BUDGET_BACKUPS_MB:-12288}" \
    -budget-sidecars-mb "${SIGNALDECK_BUDGET_SIDECARS_MB:-4096}" \
    -budget-archive-mb "${SIGNALDECK_BUDGET_ARCHIVE_MB:-2048}" \
    -budget-logs-mb "${SIGNALDECK_BUDGET_LOGS_MB:-512}" 2>&1)
  rc=$?
  printf '%s\n' "$report" >> "$LOG"
  if [ "$rc" -ne 0 ]; then
    over=$(printf '%s\n' "$report" | grep -E 'OVER BUDGET')
    log "STORAGE OVER BUDGET — paging"
    notify_remote "SignalDeck storage budget exceeded (nightly sweep, $target):
$over
Full report in logs/refresh.log"
    sd_notify "SignalDeck storage" "A storage surface outgrew its budget — see logs/refresh.log."
  else
    log "storage report: all surfaces within budget"
  fi
else
  log "sdmaint missing at $SDMAINT and could not be rebuilt — skipping storage report (build: cd daemon && go build -o ../bin/sdmaint ./cmd/sdmaint)"
fi

# ── 8. restart the daemon only if it was already running before the sweep
#      (a manual refresh during market hours); otherwise leave it off. ──
if [ "$was_up" = "yes" ]; then
  sd_svc_start com.signaldeck.daemon
  log "daemon restarted (was up before the sweep)"
fi
log "=== sweep done for $target (active=$after) ==="
