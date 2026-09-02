#!/usr/bin/env bash
# lib-forward-test.sh - Determine if forward test data is stale
#
# Wording defect (2026-09-01): Original market-close.sh used COALESCE to fake a session
# date when forward_test_daily was empty, producing false alerts like "newest recorded session 2026-08-23 is 8d old".
# This function replaces that logic with honest wording: distinguishes between no rows and stale rows.
#
# Intended call site in market-close.sh:
#   msg="$(sd_ft_stale_line "$SD/data/signaldeck.db" 2026-08-23 "$FT_QUIET_DAYS")"
#   [ -n "$msg" ] && { echo "$(date '+%Y-%m-%dT%H:%M:%S') market-close: forward-test $msg" >> "$SD/logs/forward-test.log"; sd_notify "SignalDeck forward test" "$msg. prereg seq 87 is not accruing evidence."; }

declare -f sd_sqlite >/dev/null 2>&1 || . "$(dirname "${BASH_SOURCE[0]}")/lib-portable.sh"

sd_ft_stale_line() {
    local db="$1" registered_date="$2" quiet_days="$3"
    local today newest_raw newest today_sec reg_sec new_sec N age

    today=$(date -u +%F) || return 0
    newest_raw=$(sd_sqlite "$db" "SELECT MAX(session) FROM forward_test_daily;" 2>/dev/null) || return 0
    newest=$(echo "$newest_raw" | tr -d '\r' | sed 's/^ *//;s/ *$//')
    if [ -z "$newest" ] || [ "$newest" = "None" ] || [ "$newest" = "NULL" ]; then
        today_sec=$(date -u -d "$today" +%s 2>/dev/null) || return 0
        reg_sec=$(date -u -d "$registered_date" +%s 2>/dev/null) || return 0
        N=$(( (today_sec - reg_sec) / 86400 ))
        [ "$N" -gt "$quiet_days" ] && printf 'STALE: no session graded yet — forward_test_daily is empty %sd after registration %s\n' "$N" "$registered_date"
    else
        today_sec=$(date -u -d "$today" +%s 2>/dev/null) || return 0
        new_sec=$(date -u -d "$newest" +%s 2>/dev/null) || return 0
        age=$(( (today_sec - new_sec) / 86400 ))
        [ "$age" -gt "$quiet_days" ] && printf 'STALE: newest graded session is %s (%sd old)\n' "$newest" "$age"
    fi
    return 0
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    if [ "$#" -eq 1 ] && [ "$1" = "--selftest" ]; then
        temp_dir=$(mktemp -d) || exit 1
        trap 'rm -rf "$temp_dir"' EXIT
        today=$(date -u +%F)
        day3=$(date -u -d "-3 days" +%F)
        day10=$(date -u -d "-10 days" +%F)
        day20=$(date -u -d "-20 days" +%F)
        day2=$(date -u -d "-2 days" +%F)
        py=$(sd_py)
        # python.exe cannot open an MSYS /tmp path; hand it a mixed C:/... path
        w() { cygpath -m "$1" 2>/dev/null || printf %s "$1"; }
        [ -z "$py" ] && { echo "FAIL: no python found"; exit 1; }

        # Create empty DB
        db_a="$temp_dir/a.db"
        "$py" -c "import sqlite3; c=sqlite3.connect('$(w "$db_a")'); c.execute('CREATE TABLE forward_test_daily(session TEXT, eligible INTEGER)'); c.commit(); c.close()" || exit 1

        # Create DB with 3-day-old row
        db_b="$temp_dir/b.db"
        "$py" -c "import sqlite3; c=sqlite3.connect('$(w "$db_b")'); c.execute('CREATE TABLE forward_test_daily(session TEXT, eligible INTEGER)'); c.execute('INSERT INTO forward_test_daily VALUES (?,1)',('$day3',)); c.commit(); c.close()" || exit 1

        # Create DB with 10-day-old row
        db_c="$temp_dir/c.db"
        "$py" -c "import sqlite3; c=sqlite3.connect('$(w "$db_c")'); c.execute('CREATE TABLE forward_test_daily(session TEXT, eligible INTEGER)'); c.execute('INSERT INTO forward_test_daily VALUES (?,1)',('$day10',)); c.commit(); c.close()" || exit 1

        # Case 1: empty DB, registered 20d ago, quiet 7d -> should stale
        expected1="STALE: no session graded yet — forward_test_daily is empty 20d after registration $day20"
        out1=$(sd_ft_stale_line "$db_a" "$day20" 7 2>/dev/null) || { echo "FAIL: case1: sd_ft_stale_line failed"; exit 1; }
        if [ "$out1" != "$expected1" ]; then
            echo "FAIL: case1: expected '$expected1', got '$out1'"
            exit 1
        fi

        # Case 2: empty DB, registered 2d ago, quiet 7d -> should be empty
        expected2=""
        out2=$(sd_ft_stale_line "$db_a" "$day2" 7 2>/dev/null) || { echo "FAIL: case2: sd_ft_stale_line failed"; exit 1; }
        if [ "$out2" != "$expected2" ]; then
            echo "FAIL: case2: expected empty string, got '$out2'"
            exit 1
        fi

        # Case 3: 3-day-old row, registered 20d ago, quiet 7d -> should be empty (fresh)
        expected3=""
        out3=$(sd_ft_stale_line "$db_b" "$day20" 7 2>/dev/null) || { echo "FAIL: case3: sd_ft_stale_line failed"; exit 1; }
        if [ "$out3" != "$expected3" ]; then
            echo "FAIL: case3: expected empty string, got '$out3'"
            exit 1
        fi

        # Case 4: 10-day-old row, registered 20d ago, quiet 7d -> should stale with exact msg
        expected4="STALE: newest graded session is $day10 (10d old)"
        out4=$(sd_ft_stale_line "$db_c" "$day20" 7 2>/dev/null) || { echo "FAIL: case4: sd_ft_stale_line failed"; exit 1; }
        if [ "$out4" != "$expected4" ]; then
            echo "FAIL: case4: expected '$expected4', got '$out4'"
            exit 1
        fi

        echo "PASS: forward-test stale wording (4 cases)"
        exit 0
    fi
fi
