#!/bin/bash
# Self-test for the retention block in ops/signaldeck-cleanup.sh --retention-only.
# Guards log/holding file selection, enforce vs report modes, .env override,
# dangerous holding paths, invalid day values, window editing, and daily run inclusion.
set -uo pipefail
export LC_ALL=C
cd "$(dirname "$0")/.." || exit 2

# Prevent caller environment from leaking in
unset SIGNALDECK_RETENTION_ENFORCE SIGNALDECK_LOG_RETENTION_D SIGNALDECK_HOLDING_RETENTION_D

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/home"
export HOME="$W/home"

pass=0; fail=0
ok()   { printf '  ok    %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

SD="$W/sd tree"
TR="$W/trash"
FH="$W/fakehome"

make_fixtures() {
    rm -rf "$SD" "$TR" "$FH"
    mkdir -p "$SD/logs" "$SD/data/backups" "$SD/daemon" "$TR/dir" "$FH"

    # Logs candidates
    head -c 100 /dev/zero > "$SD/logs/old.log";            touch -d "40 days ago" "$SD/logs/old.log"
    head -c 200 /dev/zero > "$SD/logs/old.log.1.gz";       touch -d "40 days ago" "$SD/logs/old.log.1.gz"
    head -c 300 /dev/zero > "$SD/logs/old.log.2";          touch -d "40 days ago" "$SD/logs/old.log.2"
    head -c 400 /dev/zero > "$SD/logs/dead.err";           touch -d "40 days ago" "$SD/logs/dead.err"
    head -c 500 /dev/zero > "$SD/logs/dead.out";           touch -d "40 days ago" "$SD/logs/dead.out"
    head -c 600 /dev/zero > "$SD/logs/notes.md";           touch -d "40 days ago" "$SD/logs/notes.md"
    head -c 700 /dev/zero > "$SD/logs/events.jsonl";       touch -d "40 days ago" "$SD/logs/events.jsonl"

    # Logs protected
    head -c 10 /dev/zero > "$SD/logs/recent.log";          touch -d "5 days ago"  "$SD/logs/recent.log"
    head -c 10 /dev/zero > "$SD/logs/edge.log";            touch -d "29 days ago" "$SD/logs/edge.log"
    head -c 10 /dev/zero > "$SD/logs/web-release.json";    touch -d "40 days ago" "$SD/logs/web-release.json"
    head -c 10 /dev/zero > "$SD/logs/.restore-rehearsal-cursor"; touch -d "40 days ago" "$SD/logs/.restore-rehearsal-cursor"
    head -c 10 /dev/zero > "$SD/logs/daemon.pid";          touch -d "40 days ago" "$SD/logs/daemon.pid"
    head -c 10 /dev/zero > "$SD/logs/state.json";          touch -d "40 days ago" "$SD/logs/state.json"
    mkdir -p "$SD/logs/sub"
    head -c 10 /dev/zero > "$SD/logs/sub/deep.log";        touch -d "40 days ago" "$SD/logs/sub/deep.log"

    # Data protected
    head -c 10 /dev/zero > "$SD/data/signaldeck.db";       touch -d "40 days ago" "$SD/data/signaldeck.db"
    head -c 10 /dev/zero > "$SD/data/signaldeck.db-wal";   touch -d "40 days ago" "$SD/data/signaldeck.db-wal"
    head -c 10 /dev/zero > "$SD/data/signaldeck.db-shm";   touch -d "40 days ago" "$SD/data/signaldeck.db-shm"
    head -c 10 /dev/zero > "$SD/data/backups/signaldeck-20260101-000000.db"; touch -d "40 days ago" "$SD/data/backups/signaldeck-20260101-000000.db"

    # Outside protected, reachable only through a symlink in logs/ (-type f must
    # not follow it). Git Bash makes a copy unless MSYS asks for a native link;
    # a copy would be an ordinary old log, so it is dropped and the case skipped.
    head -c 10 /dev/zero > "$W/outside.log";               touch -d "40 days ago" "$W/outside.log"
    MSYS=winsymlinks:nativestrict ln -s "$W/outside.log" "$SD/logs/link.log" 2>/dev/null
    HAVE_LINK=1; [ -L "$SD/logs/link.log" ] || { HAVE_LINK=0; rm -f "$SD/logs/link.log"; }
    # The link itself old too, or -xtype f would skip it on its fresh lstat mtime
    [ "$HAVE_LINK" = 0 ] || touch -h -d "40 days ago" "$SD/logs/link.log"

    # Holding candidates
    head -c 1000 /dev/zero > "$TR/signaldeck-20250901-000000.db";          touch -d "40 days ago" "$TR/signaldeck-20250901-000000.db"
    head -c 50 /dev/zero > "$TR/signaldeck-20250901-000000.db.sha256";     touch -d "40 days ago" "$TR/signaldeck-20250901-000000.db.sha256"

    # Holding protected
    head -c 10 /dev/zero > "$TR/signaldeck-20261019-000000.db";            touch -d "3 days ago"  "$TR/signaldeck-20261019-000000.db"
    head -c 10 /dev/zero > "$TR/dir/inner.db";             touch -d "40 days ago" "$TR/dir/inner.db"
    touch -d "40 days ago" "$TR/dir"
    # Not something this script retires (on macOS this is the user's own Trash)
    head -c 10 /dev/zero > "$TR/vacation.jpg";             touch -d "40 days ago" "$TR/vacation.jpg"

    # Fakehome protected
    head -c 10 /dev/zero > "$FH/precious.txt";             touch -d "40 days ago" "$FH/precious.txt"
}

run_ret() {
    env SIGNALDECK_SD="$SD" SIGNALDECK_TRASH="$TR" "$@" bash ops/signaldeck-cleanup.sh --retention-only 2>&1
}

listed() {
    printf '%s\n' "$1" | sed -nE "s/^RETENTION $2 (.*) [0-9]+d [0-9]+\$/\1/p" | while IFS= read -r p; do basename "$p"; done | sort | tr '\n' ' '
}

candidates_present() {
    local candidates=(
        "$SD/logs/old.log"
        "$SD/logs/old.log.1.gz"
        "$SD/logs/old.log.2"
        "$SD/logs/dead.err"
        "$SD/logs/dead.out"
        "$SD/logs/notes.md"
        "$SD/logs/events.jsonl"
        "$TR/signaldeck-20250901-000000.db"
        "$TR/signaldeck-20250901-000000.db.sha256"
    )
    local count=0
    for p in "${candidates[@]}"; do
        [ -e "$p" ] && count=$((count+1))
    done
    printf '%s' "$count"
}

EXPECTED_LIST="dead.err dead.out events.jsonl notes.md old.log old.log.1.gz old.log.2 signaldeck-20250901-000000.db signaldeck-20250901-000000.db.sha256 "
REPORT_SUMMARY="RETENTION mode=report logs=7 holding=2 bytes=3850"
ENFORCE_SUMMARY="RETENTION mode=enforce logs=7 holding=2 bytes=3850"

echo "-- a. report mode lists exactly the old files, deletes nothing"
make_fixtures
out=$(run_ret)
check "would-remove list matches expected" "$(listed "$out" would-remove)" "$EXPECTED_LIST"
check "summary line present exactly once" "$(printf '%s\n' "$out" | grep -c -x "$REPORT_SUMMARY")" "1"
check "all 9 candidates still present" "$(candidates_present)" "9"
check "no removed lines" "$(printf '%s\n' "$out" | grep -c '^RETENTION removed')" "0"
check "old.log line ends with 40d 100" "$(printf '%s\n' "$out" | grep 'old.log' | grep -c ' 40d 100$')" "1"

echo "-- b. SIGNALDECK_RETENTION_ENFORCE=0 is still report"
make_fixtures
out=$(run_ret SIGNALDECK_RETENTION_ENFORCE=0)
check "all 9 candidates still present" "$(candidates_present)" "9"
check "summary mode=report" "$(printf '%s\n' "$out" | grep -c -x "$REPORT_SUMMARY")" "1"

echo "-- c. Enforce via environment (SIGNALDECK_RETENTION_ENFORCE=1)"
make_fixtures
out=$(run_ret SIGNALDECK_RETENTION_ENFORCE=1)
check "removed list matches expected" "$(listed "$out" removed)" "$EXPECTED_LIST"
check "zero candidates remain" "$(candidates_present)" "0"
missing_protected=0
for p in \
    "$SD/logs/recent.log" "$SD/logs/edge.log" "$SD/logs/web-release.json" \
    "$SD/logs/.restore-rehearsal-cursor" "$SD/logs/daemon.pid" "$SD/logs/state.json" \
    "$SD/logs/sub/deep.log" \
    "$SD/data/signaldeck.db" "$SD/data/signaldeck.db-wal" "$SD/data/signaldeck.db-shm" \
    "$SD/data/backups/signaldeck-20260101-000000.db" \
    "$W/outside.log" \
    "$TR/signaldeck-20261019-000000.db" "$TR/dir/inner.db" "$TR/vacation.jpg" \
    "$FH/precious.txt"; do
    [ -e "$p" ] || missing_protected=$((missing_protected+1))
done
check "no protected files missing" "$missing_protected" "0"
check "summary mode=enforce" "$(printf '%s\n' "$out" | grep -c -x "$ENFORCE_SUMMARY")" "1"
if [ "$HAVE_LINK" = 1 ]; then
    check "logs/link.log symlink not followed, not removed" "$([ -L "$SD/logs/link.log" ] && echo 1 || echo 0)" "1"
else
    echo "  skip  symlink case (this filesystem cannot make one)"
fi

echo "-- d. Enforce via daemon/.env"
make_fixtures
printf 'FOO=bar\nSIGNALDECK_RETENTION_ENFORCE=1\r\n' > "$SD/daemon/.env"
out=$(run_ret)
check "zero candidates remain" "$(candidates_present)" "0"
check "summary mode=enforce" "$(printf '%s\n' "$out" | grep -c -x "$ENFORCE_SUMMARY")" "1"

echo "-- e. daemon/.env with SIGNALDECK_RETENTION_ENFORCE=0 and no env flag"
make_fixtures
printf 'SIGNALDECK_RETENTION_ENFORCE=0\n' > "$SD/daemon/.env"
out=$(run_ret)
check "all 9 candidates still present" "$(candidates_present)" "9"
check "summary mode=report" "$(printf '%s\n' "$out" | grep -c -x "$REPORT_SUMMARY")" "1"

echo "-- f. Dangerous holding path / (REPORT MODE ONLY)"
make_fixtures
out=$(run_ret SIGNALDECK_TRASH=/)
check "SKIP holding line present" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP holding:')" "1"
check "summary holding=0" "$(printf '%s\n' "$out" | grep -c -x 'RETENTION mode=report logs=7 holding=0 bytes=2800')" "1"

echo "-- g. Holding path equal to \$HOME, in ENFORCE mode but contained"
make_fixtures
out=$(run_ret HOME="$FH" SIGNALDECK_TRASH="$FH" SIGNALDECK_RETENTION_ENFORCE=1)
check "SKIP holding line present" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP holding:')" "1"
check "precious.txt still exists" "$([ -e "$FH/precious.txt" ] && echo 1 || echo 0)" "1"
check "summary contains holding=0" "$(printf '%s\n' "$out" | grep -c 'holding=0')" "1"

echo "-- h. Holding path inside data, ENFORCE"
make_fixtures
out=$(run_ret SIGNALDECK_TRASH="$SD/data/backups" SIGNALDECK_RETENTION_ENFORCE=1)
check "SKIP holding line present" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP holding:')" "1"
check "backup db still exists" "$([ -e "$SD/data/backups/signaldeck-20260101-000000.db" ] && echo 1 || echo 0)" "1"

echo "-- i. Holding path equal to the repo root \$SD, ENFORCE"
make_fixtures
out=$(run_ret SIGNALDECK_TRASH="$SD" SIGNALDECK_RETENTION_ENFORCE=1)
check "SKIP holding line present" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP holding:')" "1"
missing_protected=0
for p in \
    "$SD/logs/recent.log" "$SD/logs/edge.log" "$SD/logs/web-release.json" \
    "$SD/logs/.restore-rehearsal-cursor" "$SD/logs/daemon.pid" "$SD/logs/state.json" \
    "$SD/logs/sub/deep.log" \
    "$SD/data/signaldeck.db" "$SD/data/signaldeck.db-wal" "$SD/data/signaldeck.db-shm" \
    "$SD/data/backups/signaldeck-20260101-000000.db"; do
    [ -e "$p" ] || missing_protected=$((missing_protected+1))
done
check "no protected files missing" "$missing_protected" "0"

echo "-- j. Invalid log day value"
make_fixtures
out=$(run_ret SIGNALDECK_LOG_RETENTION_D=abc SIGNALDECK_RETENTION_ENFORCE=1)
check "SKIP logs line present" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP logs:')" "1"
check "old.log still exists" "$([ -e "$SD/logs/old.log" ] && echo 1 || echo 0)" "1"
make_fixtures
out=$(run_ret SIGNALDECK_LOG_RETENTION_D=0 SIGNALDECK_RETENTION_ENFORCE=1)
check "SKIP logs line present" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP logs:')" "1"
check "old.log still exists" "$([ -e "$SD/logs/old.log" ] && echo 1 || echo 0)" "1"

echo "-- k. Window is editable: SIGNALDECK_LOG_RETENTION_D=3 (report mode)"
make_fixtures
out=$(run_ret SIGNALDECK_LOG_RETENTION_D=3)
check "recent.log appears in would-remove" "$(printf '%s\n' "$out" | grep -c 'recent.log')" "1"
check "edge.log appears in would-remove" "$(printf '%s\n' "$out" | grep -c 'edge.log')" "1"

echo "-- l. Daily run includes the block"
make_fixtures
out=$(env SIGNALDECK_SD="$SD" SIGNALDECK_TRASH="$TR" bash ops/signaldeck-cleanup.sh 2>&1)
check "report summary line appears" "$(printf '%s\n' "$out" | grep -c -x "$REPORT_SUMMARY")" "1"
check "all 9 candidates still present" "$(candidates_present)" "9"

echo "-- m. daemon/.env read like the daemon's parseDotEnv: last line wins"
make_fixtures
printf 'SIGNALDECK_RETENTION_ENFORCE=1\nSIGNALDECK_RETENTION_ENFORCE=0\n' > "$SD/daemon/.env"
out=$(run_ret)
check ".env =1 then =0: report" "$(printf '%s\n' "$out" | grep -c -x "$REPORT_SUMMARY")" "1"
check ".env =1 then =0: all 9 candidates still present" "$(candidates_present)" "9"

echo "-- n. a set environment value wins over daemon/.env"
make_fixtures
printf 'SIGNALDECK_RETENTION_ENFORCE=1\n' > "$SD/daemon/.env"
out=$(run_ret SIGNALDECK_RETENTION_ENFORCE=0)
check "env=0 over .env=1: report" "$(printf '%s\n' "$out" | grep -c -x "$REPORT_SUMMARY")" "1"
check "env=0 over .env=1: all 9 candidates still present" "$(candidates_present)" "9"

echo "-- o. values the daemon reads as 1 enforce here too"
for line in 'SIGNALDECK_RETENTION_ENFORCE=1 # on' 'SIGNALDECK_RETENTION_ENFORCE="1"' \
            "  SIGNALDECK_RETENTION_ENFORCE = '1'  " 'SIGNALDECK_RETENTION_ENFORCE=0 # off' \
            '# SIGNALDECK_RETENTION_ENFORCE=1' 'SIGNALDECK_RETENTION_ENFORCE_X=1'; do
    make_fixtures
    printf '%s\n' "$line" > "$SD/daemon/.env"
    case "$line" in *'=0'*|'#'*|*_X=*) want=report ;; *) want=enforce ;; esac
    check "[$line] -> $want" "$(run_ret | sed -n 's/^RETENTION mode=\([a-z]*\) .*/\1/p')" "$want"
done

echo "-- p. day windows come from daemon/.env too; the environment wins"
make_fixtures
printf 'SIGNALDECK_LOG_RETENTION_D=3\n' > "$SD/daemon/.env"
check ".env LOG_D=3 lists recent.log" "$(run_ret | grep -c 'would-remove .*/recent.log ')" "1"
check "env LOG_D=30 beats .env LOG_D=3" "$(run_ret SIGNALDECK_LOG_RETENTION_D=30 | grep -c 'recent.log')" "0"

echo "-- q. leading zeros are base 10, and 00 is still 0"
make_fixtures
out=$(run_ret SIGNALDECK_LOG_RETENTION_D=00 SIGNALDECK_RETENTION_ENFORCE=1)
check "00: SKIP logs line" "$(printf '%s\n' "$out" | grep -c '^RETENTION SKIP logs:')" "1"
check "00: recent.log kept" "$([ -e "$SD/logs/recent.log" ] && echo 1 || echo 0)" "1"
out=$(run_ret SIGNALDECK_LOG_RETENTION_D=08)
check "08: no bash error" "$(printf '%s\n' "$out" | grep -c 'value too great')" "0"
check "08 = 8 days: edge.log (29d) listed, recent.log (5d) not" \
    "$(printf '%s\n' "$out" | grep -c 'edge.log')$(printf '%s\n' "$out" | grep -c 'recent.log')" "10"

mkbak() { head -c 10 /dev/zero > "$SD/data/backups/$1"; touch -d "$2" "$SD/data/backups/$1"; }

echo "-- r. a bad argument is refused before any backup moves"
for arg in --retention_only --retention 0 00 abc -1 "2 3"; do
    make_fixtures
    mkbak signaldeck-20261020-000000.db "1 day ago"; mkbak signaldeck-20261018-000000.db "2 days ago"
    mkbak signaldeck-20261016-000000.db "3 days ago"
    # $arg unquoted on purpose: "2 3" is two arguments
    env SIGNALDECK_SD="$SD" SIGNALDECK_TRASH="$TR" bash ops/signaldeck-cleanup.sh $arg >/dev/null 2>&1; rc=$?
    check "[$arg] exits 2, all 4 backups still in data/backups" "$rc $(ls "$SD/data/backups" | grep -c '\.db$')" "2 4"
done

echo "-- s. a backup retired today gets the full holding window, even if it is old"
make_fixtures
rm -f "$SD/data/backups/"*
mkbak signaldeck-20261020-000000.db "1 day ago"; mkbak signaldeck-20261020-000000.db.sha256 "1 day ago"
mkbak signaldeck-20261018-000000.db "2 days ago"
mkbak signaldeck-20250910-000000.db "40 days ago"; mkbak signaldeck-20250910-000000.db.sha256 "40 days ago"
out=$(env SIGNALDECK_SD="$SD" SIGNALDECK_TRASH="$TR" SIGNALDECK_RETENTION_ENFORCE=1 bash ops/signaldeck-cleanup.sh 2>&1)
check "40-day-old backup retired" "$(printf '%s\n' "$out" | grep -c '^  trash  signaldeck-20250910-000000.db$')" "1"
check "retired .db and sidecar survive the same run's sweep" "$(ls "$TR" | grep -c '^signaldeck-20250910-000000\.db')" "2"
check "older holding entries still swept in that run" "$(candidates_present)" "0"

echo "----------------------------------------------------------------------"
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ] && echo "CLEANUP RETENTION SELF-TEST OK"
exit "$fail"