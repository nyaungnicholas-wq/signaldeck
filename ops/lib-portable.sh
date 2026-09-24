# Portability shims for the ops scripts. Source this, do not execute it.
#
# These scripts were written on a Mac and moved to Windows on 2026-07-31. Three
# commands they lean on do not exist under Git Bash:
#
#   caffeinate  wraps the backup and both compressors. Missing, so the wrapped
#               command never ran -- `caffeinate -i sqlite3 ... VACUUM INTO`
#               failed as "command not found" and NO backup was produced.
#   sqlite3     the CLI is not installed either, so even unwrapped the VACUUM
#               and every meta write would have failed.
#   osascript   every "page a human" path (budget breach, restore-rehearsal
#               failure, grading refusal, no-transport warning) was a silent
#               no-op. An alert nobody can receive is not an alert.
#
# Each shim degrades to something real rather than pretending: no sleep
# inhibitor is fine, Python's bundled sqlite3 replaces the CLI, and a
# notification always reaches the log even when no desktop channel exists.

# Windows Python defaults to cp1252 for stdout, and every script here prints
# arrows, em dashes and box-drawing characters. nightly-bias.sh died on a single
# "→" with UnicodeEncodeError AFTER doing all its work — 32 bias-invariant tests
# passed and the run still reported failure. accuracy-registry.sh had already
# learned this and set it locally; setting it here means no script has to
# remember. Harmless on macOS/Linux, which are already UTF-8.
export PYTHONUTF8=1
export PYTHONIOENCODING=utf-8

# sd_py resolves a working interpreter once. Same resolution as the other
# scripts use; a candidate has to actually run, not merely be on PATH.
sd_py() {
  if [ -z "${SD_PY:-}" ]; then
    for cand in python3 python py; do
      if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
        SD_PY="$cand"; break
      fi
    done
  fi
  printf '%s' "${SD_PY:-}"
}

# SD_LIB_DIR is this library's own directory, so sd_nosleep can find its
# sidecar regardless of the caller's cwd.
SD_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# sd_nosleep runs its arguments, inhibiting sleep where the platform can.
# On macOS that is caffeinate; elsewhere the command simply runs.
sd_nosleep() {
  if command -v caffeinate >/dev/null 2>&1; then
    caffeinate -i "$@"
    return $?
  fi

  # WINDOWS. caffeinate is macOS-only, so this used to fall through to running
  # the command bare -- the inhibition silently became nothing on the move to
  # Windows, which is the same shape as the lost launchd log redirection: the
  # wrapper still looked like it was wrapping something. Measured 2026-09-19,
  # this machine's AC standby-idle is 600s, so a backup or a VACUUM INTO that
  # runs longer than the idle timeout could be suspended half-written while the
  # task still exits 0.
  #
  # A SIDECAR, not a wrapper: the keeper holds ES_CONTINUOUS|ES_SYSTEM_REQUIRED
  # for its own lifetime and the real command runs here under bash unchanged.
  # Passing "$@" through PowerShell would mean quoting an arbitrary argv across
  # two shells, which is how the arguments get mangled.
  local keeper="" rc=0
  local keeper_script="$SD_LIB_DIR/nosleep-keeper.ps1"
  if [ -f "$keeper_script" ] && command -v powershell >/dev/null 2>&1; then
    powershell -NoProfile -ExecutionPolicy Bypass -File "$(sd_winpath "$keeper_script")" >/dev/null 2>&1 &
    keeper=$!
  fi
  "$@"
  rc=$?
  # Releasing the lock is just killing the holder; nothing to unwind if we died.
  if [ -n "$keeper" ]; then kill "$keeper" >/dev/null 2>&1 || true; fi
  return $rc
}

# sd_winpath PATH — a path the NATIVE (non-MSYS) tools will resolve identically.
#
# Bash and Windows Python disagree about MSYS paths: to bash "/tmp" is
# C:/Users/<you>/AppData/Local/Temp, to Python it is C:\tmp. Handing an
# unconverted path to the Python fallback therefore writes the file somewhere
# the caller never looks — a backup that reports success and is not where the
# next step expects it. cygpath exists under Git Bash; elsewhere the path is
# already native.
sd_winpath() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s' "$1"
  fi
}

# sd_sqlite DB SQL — execute SQL against DB, preferring the sqlite3 CLI and
# falling back to Python's bundled sqlite3 module. Returns the tool's status.
#
# The SQL is rewritten too: statements like VACUUM INTO carry a second path as
# a literal, and that one needs the same conversion as the database path.
sd_sqlite() {
  local db="$1" sql="$2" py
  # Path conversion belongs to BOTH backends, so it happens before either is
  # chosen. Every backend here is a native Windows binary that cannot open a
  # "/c/..." MSYS path, and the MSYS argument mangler does not help: it
  # rewrites arguments that are themselves path-like, not a path embedded in a
  # quoted SQL string such as VACUUM INTO '/c/...'.
  #
  # This conversion used to sit inside the Python branch only. The sqlite3-CLI
  # branch above it was unreachable on this machine because no sqlite3 CLI was
  # installed — until one was added on 2026-08-04 so the restore rehearsal
  # could verify a backup. That prerequisite silently switched sd_sqlite onto
  # the unconverted path and every backup began failing with
  #   Error in 2nd command line argument: unable to open database: /c/Users/...
  # while the daily job still reported only a generic "VACUUM INTO error".
  # A dormant bug that an unrelated install switches on is exactly why the two
  # backends must not each carry their own copy of this.
  db="$(sd_winpath "$db")"
  if command -v cygpath >/dev/null 2>&1; then
    # Convert quoted absolute MSYS paths appearing inside the SQL.
    #
    # Read LINE BY LINE, never `for lit in $(...)`. This repo lives under
    # "…/claude code/…" and word-splitting cut that path in half, so the
    # substitution never matched and Python received a raw MSYS path it cannot
    # open. Measured live 2026-08-03: the 13:10 market-close backup died with
    #   sqlite3.OperationalError: unable to open database:
    #   /c/Users/…/claude code/…/signaldeck-20260803-131005.db
    # which is the same defect class as the -File argument splitting this repo
    # already fixed once. A path with a space is the normal case here.
    local lit conv
    while IFS= read -r lit; do
      [ -z "$lit" ] && continue
      conv="$(cygpath -w "$lit" | sed 's/\\/\\\\/g')"
      sql="${sql//\'$lit\'/\'$conv\'}"
    done <<EOF
$(printf '%s' "$sql" | grep -oE "'/[^']*'" | sed "s/^'//; s/'\$//")
EOF
  fi
  # Backends, in preference order, both now receiving converted paths.
  #
  # `-cmd ".timeout"` is NOT optional. The Python branch below opens with
  # timeout=120; the CLI branch had no equivalent, so it failed the instant the
  # DB was locked instead of waiting. Measured 2026-08-12: the offline backup
  # wrote its 4.7GB file at 17:53 while the daemon was down, then lost the
  # `backup_last_ts` meta write at 17:57 to
  #   Error in 2nd command line argument: database is locked
  # because the daemon had come back up in between. That failure is downgraded
  # to a WARN, so the backup reported success while the key the in-daemon
  # failsafe gates on was never updated — and 5 hours later that failsafe took a
  # REDUNDANT full VACUUM INTO backup against a live daemon, which is the exact
  # contention its 30h gate exists to prevent.
  #
  # Use a dot-command rather than `PRAGMA busy_timeout=...;` prepended to $sql:
  # the pragma prints its value on stdout, which would corrupt any caller
  # reading the result. Same reason this belongs HERE and not at the call sites:
  # a property only one backend has is precisely the drift this file exists to
  # stop, as the path-conversion comment above already learned once.
  if command -v sqlite3 >/dev/null 2>&1; then
    sd_nosleep sqlite3 -cmd ".timeout 120000" "$db" "$sql"
    return $?
  fi
  py="$(sd_py)"
  if [ -z "$py" ]; then
    return 127
  fi
  # VACUUM INTO cannot run inside a transaction, so autocommit is required —
  # isolation_level=None. Without it Python opens an implicit transaction and
  # the statement fails with "cannot VACUUM from within a transaction".
  SD_SQL="$sql" sd_nosleep "$py" -c '
import os, sqlite3, sys
con = sqlite3.connect(sys.argv[1], isolation_level=None, timeout=120)
try:
    con.executescript(os.environ["SD_SQL"])
finally:
    con.close()
' "$db"
}

# sd_port_listening PORT — true when something is LISTENING on that TCP port.
#
# Exists because "is a process with this name alive" is the wrong question for a
# server. `sd_is_running node` was how signaldeck-ctl.sh judged the web app, and
# node is the most common process name on a developer box: measured 2026-08-11
# there were 3 unrelated node processes running (Claude Code, the OmniRoute
# gateway) and NOTHING listening on 8323, and `signaldeck-ctl.sh status` printed
# "com.signaldeck.web: running" while `curl http://localhost:8323/` was refused
# outright. The check could not report the web as down while any node existed —
# which, on this machine, is always.
#
# ops/signaldeck-web-task.ps1 already asks the correct question with
# Get-NetTCPConnection; this makes the same answer available to the shell.
sd_port_listening() {
  local port="$1"
  if command -v powershell.exe >/dev/null 2>&1; then
    [ "$(powershell.exe -NoProfile -NonInteractive -Command \
        "@(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue).Count" \
        2>/dev/null | tr -d '\r\n ')" != "0" ] && return 0
    return 1
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 && return 0
    return 1
  fi
  if command -v netstat >/dev/null 2>&1; then
    netstat -an 2>/dev/null | grep -qE "[:.]$port[[:space:]].*LISTEN" && return 0
    return 1
  fi
  # No way to tell. Say NO: unlike sd_is_running (whose callers are guarding a
  # destructive VACUUM and must fail safe by assuming the daemon is UP), the
  # only caller here is a STATUS report, where the dangerous answer is a
  # confident "running" for something that is not.
  return 1
}

# sd_is_running NAME — true when a process by that name is alive.
#
# `pgrep` is absent under Git Bash, so `pgrep -x signaldeckd >/dev/null 2>&1`
# simply failed and read as "the daemon is NOT running". That is the guard
# stopping the offline backup from running a multi-gigabyte VACUUM INTO against
# a live database — inoperative, and failing OPEN, which is the wrong direction
# for a safety check.
sd_is_running() {
  local name="$1"
  if command -v pgrep >/dev/null 2>&1; then
    # Try "$name.exe" too. pgrep is absent under Git Bash today, so this branch
    # is dormant on Windows — and a dormant branch that an unrelated install
    # switches on is exactly how this file was broken once before: adding a
    # sqlite3 CLI on 2026-08-04 for the restore rehearsal silently moved
    # sd_sqlite onto an untested path and every backup began failing (see the
    # path-conversion comment above). Installing procps here would activate
    # this branch, and `pgrep -x signaldeckd` cannot match a Windows process
    # named signaldeckd.exe — so it would answer "not running" for a LIVE
    # daemon and let the offline backup VACUUM INTO against it, which is the
    # precise fail-open direction this function exists to prevent.
    #
    # Checking both names is safe on macOS, where nothing is called *.exe and
    # the second test simply never matches. It must NOT fall through to the
    # branches below on a miss: on macOS those are all absent and the final
    # fail-safe `return 0` would then report every dead process as running.
    pgrep -x "$name" >/dev/null 2>&1 && return 0
    pgrep -x "$name.exe" >/dev/null 2>&1 && return 0
    return 1
  fi
  if command -v powershell.exe >/dev/null 2>&1; then
    [ "$(powershell.exe -NoProfile -NonInteractive -Command \
        "(Get-Process '$name' -ErrorAction SilentlyContinue | Measure-Object).Count" 2>/dev/null | tr -d '\r\n ')" \
        != "0" ] && return 0
    return 1
  fi
  if command -v tasklist >/dev/null 2>&1; then
    tasklist //FI "IMAGENAME eq $name.exe" 2>/dev/null | grep -qi "$name" && return 0
    return 1
  fi
  # No way to tell. Say YES: for the callers here the dangerous answer is a
  # false "not running", which lets a heavy job loose on a live database.
  return 0
}

# ── service control ────────────────────────────────────────────────────────
#
# launchd labels are the canonical service names in this repo. On Windows the
# equivalent is a Scheduled Task; the mapping is mechanical, so it lives here
# instead of being duplicated in every caller.
#
#   com.signaldeck.daemon  ->  "SignalDeck Daemon"
#   com.signaldeck.web     ->  "SignalDeck Web"
sd_task_name() {
  local leaf="${1##*.}"
  case "$1" in
    com.tickstream.*) printf 'TickStream %s' "$(sd_titlecase "$leaf")" ;;
    # ops/install-sibling-tasks.ps1 registers this one as "TraderHud Daemon",
    # not the "StockTrader Hud" the generic rule below derives. The registered
    # name is the truth — a mapping that disagrees with it addresses a task that
    # does not exist, which schtasks reports and this library used to discard.
    com.stocktrader.hud) printf 'TraderHud Daemon' ;;
    com.stocktrader.*) printf 'StockTrader %s' "$(sd_titlecase "$leaf")" ;;
    *) printf 'SignalDeck %s' "$(sd_titlecase "$leaf")" ;;
  esac
}

sd_titlecase() {
  # daily-refresh -> Daily-Refresh
  printf '%s' "$1" | awk -F- '{for(i=1;i<=NF;i++){$i=toupper(substr($i,1,1)) substr($i,2)}; print}' OFS=-
}

# sd_svc_start SERVICE — start a service, and REPORT WHETHER IT STARTED.
#
# Exit codes, because "absent" and "broken" need different answers from the
# caller and used to be indistinguishable:
#   0  started (or already running)
#   1  the service exists but could not be started
#   2  the service is NOT REGISTERED on this machine
#
# This used to end in `schtasks //Run ... >/dev/null 2>&1` with stdout, stderr
# and — at every call site — the exit status all discarded, while the macOS
# branch returned 0 unconditionally whether or not launchctl did anything. So
# `signaldeck-ctl.sh up` printed "SignalDeck up - daemon :8322, web :8323,
# tunnel" as a fixed string. Measured 2026-08-11: the `SignalDeck Tunnel` task
# does not exist on this machine, `schtasks //Run` on it exits 1 with "ERROR:
# The system cannot find the file specified.", and every start path still
# reported success. That is how a service nobody had registered went unnoticed.
#
# Existence is probed with //Query rather than inferred from //Run's status,
# because //Run returns 1 for both "no such task" and "task exists but refused"
# (measured), and those are not the same problem.
sd_svc_start() {
  local task
  task="$(sd_task_name "$1")"
  schtasks //Query //TN "$task" >/dev/null 2>&1 || return 2
  schtasks //Run //TN "$task" >/dev/null 2>&1 || return 1
  return 0
}

sd_svc_stop() {
  # /End is NOT a SIGTERM. It is TerminateProcess: the daemon's signal handler
  # never runs, so every in-flight worker run was left 'orphaned' and swept at
  # the next boot. Measured 2026-09-23: nine boots in three clusters, each one a
  # Market-Close or Daily-Refresh /End, 3-35 orphaned runs per kill, and
  # congress-poller orphaned twice in a row (53h without a good run). So the
  # daemon is asked first through the stop file it polls (daemon/cmd/signaldeckd/
  # stopfile.go), which cancels the same context SIGTERM would; /End below is
  # only the fallback for a drain that has not finished in SD_STOP_GRACE seconds
  # (150: ShutdownGrace 75s + run-journal close 30s + the WAL checkpoint).
  if [ "$1" = com.signaldeck.daemon ] && sd_is_running signaldeckd; then
    : > "$SD_LIB_DIR/../data/.stop-request"
    local waited=0
    while [ "$waited" -lt "${SD_STOP_GRACE:-150}" ] && sd_is_running signaldeckd; do
      sleep 3; waited=$((waited + 3))
    done
  fi
  #
  # A task that does not EXIST is reported as 2, the same contract sd_svc_start
  # already honours. Ending a task that merely is not RUNNING stays 0 — callers
  # routinely stop services without checking first, and turning that into a
  # failure would break all of them.
  #
  # The distinction is the point: a label whose name no longer matches its
  # registration produces exactly the same observable as a service that was
  # already stopped — nothing happens, quietly, forever. This function used to
  # discard schtasks' status entirely, so there was no observable at all.
  local task
  task="$(sd_task_name "$1")"
  schtasks //Query //TN "$task" >/dev/null 2>&1 || return 2
  schtasks //End //TN "$task" >/dev/null 2>&1
  return 0
}

sd_svc_restart() { sd_svc_stop "$1"; sleep 2; sd_svc_start "$1"; }

# sd_kill_hard NAME — last-resort SIGKILL equivalent for a process that ignored
# the graceful stop. SQLite under WAL is crash-safe, so this is survivable; a
# zombie daemon is not, because it holds the port the next start needs.
#
# SD_KILL_HARD_REASON is set on failure and is the POINT of this function's
# contract. It used to return a bare 1 for two unrelated situations -- no kill
# utility on PATH, and a utility that ran and was refused -- so market-close.sh
# logged "force-kill FAILED (no pkill, no taskkill)" without having established
# either clause. Measured 2026-09-12 on this box: taskkill IS on PATH, and the
# real cause is permission. signaldeckd.exe runs as a SERVICE in session 0 while
# the backup task runs unelevated in the user session, which cannot even read
# that process's owner, let alone terminate it. The log blamed a missing tool
# for an access-denied, which sends the reader to fix the wrong thing.
sd_kill_hard() {
  SD_KILL_HARD_REASON=""
  local found=0 err=""
  if command -v pkill >/dev/null 2>&1; then
    found=1
    err=$(pkill -9 -x "$1" 2>&1) && return 0
  fi
  if command -v taskkill >/dev/null 2>&1; then
    found=1
    err=$(taskkill //F //IM "$1.exe" 2>&1) && return 0
  fi
  if [ "$found" -eq 0 ]; then
    SD_KILL_HARD_REASON="no kill utility on PATH (neither pkill nor taskkill)"
  else
    # Collapse to one line; taskkill's refusal is multi-line and the log is
    # append-only shared with every other backup message.
    SD_KILL_HARD_REASON="kill utility ran and failed: $(printf '%s' "$err" | tr '
' '  ' | sed 's/  */ /g')"
  fi
  return 1
}

# sd_sqlite_read DB SQL — run a SELECT and print rows the way the sqlite3 CLI
# does: one row per line, columns joined by "|". sd_sqlite runs executescript,
# which returns nothing, so every `x=$(sqlite3 db "SELECT ...")` needs this
# instead. Callers parse the output as text, so the format has to match exactly.
sd_sqlite_read() {
  local db="$1" sql="$2" py
  # STRIP CR. The sqlite3 CLI installed here (WinGet, a native Windows build)
  # terminates rows with CRLF, so every field a caller reads ends in \r while
  # LOOKING correct in any output you print. That is a silent-wrong-answer bug,
  # not a cosmetic one: `[ "${#rev}" -eq 40 ]` failed on every 40-hex commit id,
  # string compares against literals never matched, and the pre-rebase hook
  # built on this helper reported "nothing referenced" for a ledger holding
  # 26,689 rows. The Python fallback already emits bare LF, so normalising here
  # makes the two backends agree — which is this file's whole purpose.
  # `-cmd ".timeout"` for the same reason as sd_sqlite: the Python fallback
  # opens with timeout=120 and the CLI had no equivalent, so a read racing the
  # daemon failed instantly rather than waiting. On this path that is a
  # silent-wrong-answer bug — the pre-rebase and reference-transaction hooks
  # read through here, and a lock-time failure makes them report "nothing
  # referenced", which is the same shape as the CRLF defect described above.
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 -cmd ".timeout 120000" "$db" "$sql" | tr -d '\r'
    return "${PIPESTATUS[0]}"
  fi
  py="$(sd_py)"
  [ -z "$py" ] && return 127
  SD_SQL="$sql" "$py" -c '
import os, sqlite3, sys
con = sqlite3.connect(sys.argv[1], timeout=120)
try:
    for row in con.execute(os.environ["SD_SQL"]):
        print("|".join("" if v is None else str(v) for v in row))
finally:
    con.close()
' "$(sd_winpath "$db")"
}

# sd_days_ago N — YYYY-MM-DD N days back. `date -v-1d` is BSD/macOS only; GNU
# date needs `-d "1 day ago"`, and getting this wrong silently returned today's
# date, which would have made the daily sweep pick the wrong trading day.
sd_days_ago() {
  if date -v-1d +%F >/dev/null 2>&1; then
    date -v-"$1"d +%F
  else
    date -d "$1 days ago" +%F
  fi
}

# sd_dow_days_ago N — day-of-week (1=Mon..7=Sun) N days back.
sd_dow_days_ago() {
  if date -v-1d +%u >/dev/null 2>&1; then
    date -v-"$1"d +%u
  else
    date -d "$1 days ago" +%u
  fi
}

# sd_notify TITLE BODY — surface a message on whatever channel exists, and
# ALWAYS record it. The log line is the point: it is the one channel that works
# everywhere, so a missing desktop notifier can no longer make an alert vanish.
sd_notify() {
  local title="$1" body="$2"
  if command -v osascript >/dev/null 2>&1; then
    osascript -e "display notification \"$body\" with title \"$title\"" >/dev/null 2>&1
  elif command -v powershell.exe >/dev/null 2>&1; then
    # Balloon tip via the tray icon: non-modal, so an unattended cron run is
    # never left blocking on a dialog nobody will click.
    SD_T="$title" SD_B="$body" powershell.exe -NoProfile -NonInteractive -Command '
      Add-Type -AssemblyName System.Windows.Forms
      $n = New-Object System.Windows.Forms.NotifyIcon
      $n.Icon = [System.Drawing.SystemIcons]::Warning
      $n.BalloonTipTitle = $env:SD_T; $n.BalloonTipText = $env:SD_B
      $n.Visible = $true; $n.ShowBalloonTip(10000); Start-Sleep -Seconds 11; $n.Dispose()
    ' >/dev/null 2>&1 &
  fi
  if command -v log >/dev/null 2>&1 || type log >/dev/null 2>&1; then
    log "NOTIFY: $title — $body"
  else
    printf '%s NOTIFY: %s — %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$title" "$body" >&2
  fi
}

# sd_dirty_excluding_generated — porcelain lines, minus ops/generated-docs.txt.
#
# ONE spelling of this rule, shared by ops/signaldeck-ctl.sh (build_from_head)
# and ops/docker-build.sh. It used to live only in the former, so the two deploy
# paths disagreed about what "clean" means: docker-build.sh had a blanket
# `wc -l != 0` and refused on the nine grader-regenerated docs that the native
# path correctly ignores. That is not a theoretical drift -- it made the
# container path unbuildable on any day the nightly grader had run, which is
# every day.
#
# The exemption exists because those paths cannot change the binary: the build
# extracts `git archive HEAD` (the COMMIT, never the working tree), and the only
# go:embed targets in the daemon are schema.sql and result.json. The refusal
# protects operator INTENT -- "the edit I just made got deployed" -- and a
# machine-regenerated doc carries no operator intent to protect.
#
# Fails CLOSED in every ambiguous case: a missing or unreadable allowlist
# exempts NOTHING, and porcelain lines the exact-match parser cannot claim
# (renames "R old -> new", quoted paths with spaces) never match an allow entry
# and so are reported as dirty.
#
# Usage:  dirty="$(sd_dirty_excluding_generated "$REPO")"
sd_dirty_excluding_generated() {
  local repo="${1:-.}"
  git -C "$repo" status --porcelain | awk -v listfile="$repo/ops/generated-docs.txt" '
    BEGIN {
      n = 0
      while ((getline line < listfile) > 0) {
        sub(/\r$/, "", line)
        if (line ~ /^[ \t]*(#|$)/) continue
        allow[n++] = line
      }
      close(listfile)
    }
    {
      path = substr($0, 4)
      for (i = 0; i < n; i++) if (allow[i] == path) next
      print
    }'
}
