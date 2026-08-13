#!/bin/bash
# Checks the portability shims actually work on THIS machine.
#
# What they replaced, all of which were silently broken after the 2026-07-31
# move from macOS to Windows:
#   caffeinate  wrapped VACUUM INTO and both compressors -> no backup was made
#   sqlite3     CLI absent -> the VACUUM and every meta write would fail anyway
#   pgrep       absent -> "is the daemon live?" always answered NO, so the
#               offline backup's safety guard failed OPEN
#   osascript   absent -> every alert was a silent no-op
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$SD/ops/lib-portable.sh"

fails=0
check() { if [ "$2" = "1" ]; then echo "  ok   $1"; else echo "  FAIL $1"; fails=$((fails + 1)); fi; }

# sd_nosleep must run the command whether or not caffeinate exists.
out="$(sd_nosleep echo portable-ok 2>/dev/null || true)"
check "sd_nosleep runs its command" "$([ "$out" = "portable-ok" ] && echo 1 || echo 0)"
sd_nosleep false && rc=0 || rc=1
check "sd_nosleep propagates a non-zero status" "$rc"

# sd_sqlite must create and query a database with no sqlite3 CLI present.
TMPDB="$(mktemp -u)".db
sd_sqlite "$TMPDB" "CREATE TABLE t(a INTEGER); INSERT INTO t VALUES(42);" 2>/dev/null && rc=1 || rc=0
check "sd_sqlite executes DDL and DML" "$rc"
check "sd_sqlite created the file" "$([ -s "$TMPDB" ] && echo 1 || echo 0)"

# VACUUM INTO is the statement the backup depends on, and it fails inside an
# implicit transaction — the exact trap the Python fallback has to avoid.
VAC="$(mktemp -u)".db
sd_sqlite "$TMPDB" "VACUUM INTO '$VAC';" 2>/dev/null && rc=1 || rc=0
check "sd_sqlite can run VACUUM INTO (the backup's core statement)" "$rc"
check "VACUUM INTO produced a non-empty copy" "$([ -s "$VAC" ] && echo 1 || echo 0)"
rm -f "$TMPDB" "$VAC"

# A path with a SPACE is the normal case here: the repo lives under
# ".../claude code/...". `for lit in $(...)` word-split that in half, the
# MSYS->Windows conversion never matched, and the 13:10 market-close backup
# died with "unable to open database". Same defect class as the -File argument
# splitting this repo already fixed once.
SPACEDIR="$(mktemp -d)/dir with space"
mkdir -p "$SPACEDIR"
SPACEDB="$SPACEDIR/src.db"
sd_sqlite "$SPACEDB" "CREATE TABLE t(a INTEGER); INSERT INTO t VALUES(7);" 2>/dev/null && rc=1 || rc=0
check "sd_sqlite writes to a path containing a space" "$rc"
SPACEVAC="$SPACEDIR/copy.db"
sd_sqlite "$SPACEDB" "VACUUM INTO '$SPACEVAC';" 2>/dev/null && rc=1 || rc=0
check "VACUUM INTO works when the target path has a space" "$rc"
check "the spaced-path copy is non-empty" "$([ -s "$SPACEVAC" ] && echo 1 || echo 0)"
got="$(sd_sqlite_read "$SPACEVAC" "SELECT a FROM t;" 2>/dev/null | tr -d '[:space:]')"
check "sd_sqlite_read reads back through a spaced path (got '$got', want 7)" "$([ "$got" = "7" ] && echo 1 || echo 0)"
rm -rf "$SPACEDIR"


# sd_is_running must be right in both directions; a false NO is the dangerous
# one, because it lets a heavy job loose on a live database.
sd_is_running definitely-not-a-real-process-xyz && rc=0 || rc=1
check "sd_is_running says NO for a bogus name" "$rc"
if command -v powershell.exe >/dev/null 2>&1 || command -v pgrep >/dev/null 2>&1; then
  # Every platform this runs on has a live shell process to detect.
  self="bash"
  command -v pgrep >/dev/null 2>&1 || self="powershell"
  sd_is_running "$self" && rc=1 || rc=0
  check "sd_is_running says YES for a process that exists ($self)" "$rc"
fi

# sd_notify must never be silent: the log line is the channel that always works.
LOGF="$(mktemp)"
log() { echo "$*" >> "$LOGF"; }
sd_notify "TestTitle" "TestBody" 2>/dev/null || true
check "sd_notify always records the alert" "$(grep -q "TestTitle" "$LOGF" && echo 1 || echo 0)"
rm -f "$LOGF"

# sd_sqlite_read must return BARE fields. The native-Windows sqlite3 CLI
# terminates rows with CRLF, so every value it returned used to carry a
# trailing \r that printed invisibly and compared unequal to everything. It
# made `[ "${#rev}" -eq 40 ]` false for every commit id, which silently
# disarmed the pre-rebase hook: it read a ledger of 26,689 rows and reported
# nothing referenced. A shim whose two backends disagree on line endings is
# not a shim, so both must emit LF.
if command -v sqlite3 >/dev/null 2>&1 || [ -n "$(sd_py)" ]; then
  RDB="$(mktemp -u)"
  sd_sqlite "$RDB" "CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('abc');" >/dev/null 2>&1
  RAW="$(sd_sqlite_read "$RDB" "SELECT v FROM t;" 2>/dev/null)"
  check "sd_sqlite_read returns the value unpadded" "$([ "$RAW" = "abc" ] && echo 1 || echo 0)"
  check "sd_sqlite_read emits no carriage return" \
    "$(printf '%s' "$RAW" | tr -d '\r' | cmp -s - <(printf '%s' "$RAW") && echo 1 || echo 0)"
  check "sd_sqlite_read length is exact" "$([ "${#RAW}" -eq 3 ] && echo 1 || echo 0)"
  rm -f "$RDB"
fi

# sd_port_listening must answer about the PORT, not about a process name.
#
# signaldeck-ctl.sh judged the web app with `sd_is_running node`, and node is
# the most common process name on a dev box. Measured 2026-08-11: 3 unrelated
# node processes were running (Claude Code, the OmniRoute gateway) and NOTHING
# was listening on 8323 — `signaldeck-ctl.sh status` printed
# "com.signaldeck.web: running" while `curl http://localhost:8323/` was refused
# outright. The check could not report the web as down while any node existed,
# which on this machine is always.
#
# The daemon's own port is used as the live positive case when it is up, so
# this test asserts against real kernel state rather than a mock.
UNUSED_PORT=59137
check "sd_port_listening is false for a port nobody is on" \
  "$(sd_port_listening "$UNUSED_PORT" && echo 0 || echo 1)"
if sd_is_running signaldeckd; then
  check "sd_port_listening is true for the live daemon port 8322" \
    "$(sd_port_listening 8322 && echo 1 || echo 0)"
fi

# sd_svc_start must REPORT what happened, and must tell "absent" apart from
# "broken".
#
# It used to end in `schtasks //Run ... >/dev/null 2>&1` (macOS branch: an
# unconditional `return 0`) with every call site discarding the status, so
# `signaldeck-ctl.sh up` printed a fixed success string. Measured 2026-08-11:
# the SignalDeck Tunnel task did not exist, //Run on it exits 1, and every start
# path still reported success — which is how an unregistered service went
# unnoticed. The two failure modes need different answers from the caller
# (absent may be deliberate; broken never is), and //Run returns 1 for BOTH, so
# existence is probed separately.
NOSUCH="$(sd_svc_start com.signaldeck.definitelynotregistered >/dev/null 2>&1; echo $?)"
check "sd_svc_start reports 2 (NOT REGISTERED) for a service that does not exist" \
  "$([ "$NOSUCH" = "2" ] && echo 1 || echo 0)"
check "sd_svc_start does not report success for a service that does not exist" \
  "$([ "$NOSUCH" != "0" ] && echo 1 || echo 0)"

if [ "$fails" -gt 0 ]; then echo "$fails check(s) failed"; exit 1; fi
echo "all portability shim checks passed"
