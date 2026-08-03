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

# sd_nosleep runs its arguments, inhibiting sleep where the platform can.
# On macOS that is caffeinate; elsewhere the command simply runs.
sd_nosleep() {
  if command -v caffeinate >/dev/null 2>&1; then
    caffeinate -i "$@"
  else
    "$@"
  fi
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
  if command -v sqlite3 >/dev/null 2>&1; then
    sd_nosleep sqlite3 "$db" "$sql"
    return $?
  fi
  py="$(sd_py)"
  if [ -z "$py" ]; then
    return 127
  fi
  db="$(sd_winpath "$db")"
  if command -v cygpath >/dev/null 2>&1; then
    # Convert quoted absolute MSYS paths appearing inside the SQL.
    local lit conv
    for lit in $(printf '%s' "$sql" | grep -oE "'/[^']*'" | tr -d "'"); do
      conv="$(cygpath -w "$lit" | sed 's/\\/\\\\/g')"
      sql="${sql//\'$lit\'/\'$conv\'}"
    done
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
    pgrep -x "$name" >/dev/null 2>&1 && return 0
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
    com.stocktrader.*) printf 'StockTrader %s' "$(sd_titlecase "$leaf")" ;;
    *) printf 'SignalDeck %s' "$(sd_titlecase "$leaf")" ;;
  esac
}

sd_titlecase() {
  # daily-refresh -> Daily-Refresh
  printf '%s' "$1" | awk -F- '{for(i=1;i<=NF;i++){$i=toupper(substr($i,1,1)) substr($i,2)}; print}' OFS=-
}

sd_svc_start() {
  if command -v launchctl >/dev/null 2>&1; then
    launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/$1.plist" 2>/dev/null
    launchctl kickstart "gui/$(id -u)/$1" 2>/dev/null
    return 0
  fi
  schtasks //Run //TN "$(sd_task_name "$1")" >/dev/null 2>&1
}

sd_svc_stop() {
  if command -v launchctl >/dev/null 2>&1; then
    launchctl kill TERM "gui/$(id -u)/$1" 2>/dev/null
    return 0
  fi
  # /End stops what the task launched; it is the Scheduled Task equivalent of
  # SIGTERM to the job, and the daemon's own signal handler does the draining.
  schtasks //End //TN "$(sd_task_name "$1")" >/dev/null 2>&1
}

sd_svc_restart() { sd_svc_stop "$1"; sleep 2; sd_svc_start "$1"; }

# sd_kill_hard NAME — last-resort SIGKILL equivalent for a process that ignored
# the graceful stop. SQLite under WAL is crash-safe, so this is survivable; a
# zombie daemon is not, because it holds the port the next start needs.
sd_kill_hard() {
  if command -v pkill >/dev/null 2>&1; then
    pkill -9 -x "$1" 2>/dev/null && return 0
  fi
  if command -v taskkill >/dev/null 2>&1; then
    taskkill //F //IM "$1.exe" >/dev/null 2>&1 && return 0
  fi
  return 1
}

# sd_sqlite_read DB SQL — run a SELECT and print rows the way the sqlite3 CLI
# does: one row per line, columns joined by "|". sd_sqlite runs executescript,
# which returns nothing, so every `x=$(sqlite3 db "SELECT ...")` needs this
# instead. Callers parse the output as text, so the format has to match exactly.
sd_sqlite_read() {
  local db="$1" sql="$2" py
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$db" "$sql"
    return $?
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
