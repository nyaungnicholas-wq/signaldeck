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

if [ "$fails" -gt 0 ]; then echo "$fails check(s) failed"; exit 1; fi
echo "all portability shim checks passed"
