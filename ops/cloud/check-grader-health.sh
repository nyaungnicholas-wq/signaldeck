#!/bin/bash
# Linux port of ops/check-grader-health.ps1, same checks
set -u

SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

MAX_AGE_MINUTES=1560
WAL_WARN_MB=128
WAL_CRIT_MB=256

while [[ $# -gt 0 ]]; do
    case "$1" in
        --max-age-minutes) MAX_AGE_MINUTES="$2"; shift 2 ;;
        --wal-warn-mb) WAL_WARN_MB="$2"; shift 2 ;;
        --wal-crit-mb) WAL_CRIT_MB="$2"; shift 2 ;;
        __selfcheck) SELFCHECK=1; shift ;;
        *) echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
done

DB="$SD/data/signaldeck.db"
WAL="$SD/data/signaldeck.db-wal"

if [[ -x "$SD/.venv/bin/python" ]]; then
    PY="$SD/.venv/bin/python"
elif command -v python3 >/dev/null 2>&1; then
    PY="python3"
else
    PY=""
fi

failed=false
reasons=()

set_unhealthy() {
    local msg="$1"
    echo "$msg"
    reasons+=("$msg")
    failed=true
}

heartbeat_verdict() {
    local age_min="$1"
    local max_min="$2"
    local uptime_min="$3"
    local ceiling
    ceiling=$(awk "BEGIN {print $max_min * 3}")
    if awk "BEGIN {exit !($age_min <= $max_min)}"; then
        echo fresh
        return
    fi
    if awk "BEGIN {exit !($uptime_min >= 0 && $uptime_min < $max_min && $age_min < $ceiling)}"; then
        echo expected-downtime
        return
    fi
    echo stale
}

if [[ "${SELFCHECK:-0}" -eq 1 ]]; then
    # fresh (age<=max)
    v=$(heartbeat_verdict 100 1560 10000)
    [[ "$v" == "fresh" ]] || { echo "FAIL fresh: got $v"; exit 1; }
    # expected-downtime (uptime<max and age<3*max)
    v=$(heartbeat_verdict 2000 1560 1000)
    [[ "$v" == "expected-downtime" ]] || { echo "FAIL expected-downtime: got $v"; exit 1; }
    # stale (uptime unknown -1)
    v=$(heartbeat_verdict 2000 1560 -1)
    [[ "$v" == "stale" ]] || { echo "FAIL stale uptime -1: got $v"; exit 1; }
    # stale (age>=3*max even with short uptime)
    v=$(heartbeat_verdict 5000 1560 100)
    [[ "$v" == "stale" ]] || { echo "FAIL stale age>=3*max: got $v"; exit 1; }
    # stale (uptime>=max)
    v=$(heartbeat_verdict 2000 1560 2000)
    [[ "$v" == "stale" ]] || { echo "FAIL stale uptime>=max: got $v"; exit 1; }
    echo "SELFCHECK OK"
    exit 0
fi

# HEARTBEAT CHECK
if [[ ! -f "$DB" ]]; then
    set_unhealthy "HEARTBEAT: database not found at $DB"
else
    if [[ -z "$PY" ]]; then
        set_unhealthy "HEARTBEAT: unreadable (no python interpreter)"
    else
        out=$("$PY" - "$DB" <<'PY' 2>&1
import sqlite3, sys, urllib.parse

conn = None
try:
    path = sys.argv[1]
    conn = sqlite3.connect("file:" + urllib.parse.quote(path) + "?mode=ro", uri=True)
    row = conn.execute(
        "select success, finished_at, coalesce(error,'') from grader_heartbeats "
        "where task='accuracy_registry' order by finished_at desc limit 1"
    ).fetchone()
    print("%s|%s|%s" % row if row else "NONE")
except sqlite3.OperationalError:
    print("NONE")
except Exception as e:
    print("ERROR:%s" % e, file=sys.stderr)
    sys.exit(2)
finally:
    if conn is not None:
        conn.close()
PY
)
        py_exit=$?
        if [[ $py_exit -ne 0 ]]; then
            set_unhealthy "HEARTBEAT: unreadable ($out)"
        else
            line=$(echo "$out" | grep -v '^$' | tail -1)
            if [[ "$line" == "NONE" ]]; then
                set_unhealthy 'HEARTBEAT: none recorded'
            elif [[ "$line" =~ ^([^|]*)\|([^|]*)\|(.*)$ ]]; then
                success="${BASH_REMATCH[1]}"
                finished_at="${BASH_REMATCH[2]}"
                last_err="${BASH_REMATCH[3]}"
                if [[ "$success" != "1" ]]; then
                    set_unhealthy "HEARTBEAT: last run FAILED: $last_err"
                else
                    # Parse timestamp as UTC
                    if finished_at_utc=$(date -u -d "$finished_at" +%s 2>/dev/null); then
                        now_utc=$(date -u +%s)
                        age_min=$(awk "BEGIN {print ($now_utc - $finished_at_utc) / 60}")
                        if awk "BEGIN {exit !($age_min > $MAX_AGE_MINUTES)}"; then
                            # Get uptime in minutes
                            uptime_min=-1
                            if [[ -r /proc/uptime ]]; then
                                uptime_sec=$(awk '{print $1}' /proc/uptime 2>/dev/null)
                                if [[ -n "$uptime_sec" ]]; then
                                    uptime_min=$(awk "BEGIN {print $uptime_sec / 60}")
                                fi
                            fi
                            verdict=$(heartbeat_verdict "$age_min" "$MAX_AGE_MINUTES" "$uptime_min")
                            ceiling=$(awk "BEGIN {print $MAX_AGE_MINUTES * 3}")
                            case "$verdict" in
                                expected-downtime)
                                    printf 'HEARTBEAT: STALE (%.1f min, max %d) BUT EXPECTED - this machine has been up only %.1f min, so a full grading window has not elapsed since boot. The gap is DOWNTIME, not a grader fault. It becomes unhealthy once uptime passes %d min with no fresh heartbeat, or once age passes %.1f min regardless.\n' \
                                        "$age_min" "$MAX_AGE_MINUTES" "$uptime_min" "$MAX_AGE_MINUTES" "$ceiling"
                                    ;;
                                *)
                                    if awk "BEGIN {exit !($uptime_min < 0)}"; then
                                        why='uptime unreadable, which is not an excuse'
                                    elif awk "BEGIN {exit !($age_min >= $ceiling)}"; then
                                        why=$(printf 'age is past the %.1f min ceiling, which no amount of downtime excuses' "$ceiling")
                                    else
                                        why=$(printf 'this machine has been up %.1f min, longer than the %d min window, so the grader had its chance' "$uptime_min" "$MAX_AGE_MINUTES")
                                    fi
                                    printf 'HEARTBEAT: STALE (%.1f min, max %d) - %s\n' "$age_min" "$MAX_AGE_MINUTES" "$why"
                                    set_unhealthy "$(printf 'HEARTBEAT: STALE (%.1f min, max %d) - %s' "$age_min" "$MAX_AGE_MINUTES" "$why")"
                                    ;;
                            esac
                        else
                            printf 'HEARTBEAT: fresh (%.1f min)\n' "$age_min"
                        fi
                    else
                        set_unhealthy "HEARTBEAT: timestamp unparseable: $finished_at"
                    fi
                fi
            else
                set_unhealthy "HEARTBEAT: unexpected output: $line"
            fi
        fi
    fi
fi

# WAL CHECK
if [[ ! -f "$WAL" ]]; then
    echo 'WAL: absent (idle)'
else
    wal_bytes=$(stat -c %s "$WAL" 2>/dev/null || echo 0)
    wal_mb=$(awk "BEGIN {print $wal_bytes / 1048576}")
    if awk "BEGIN {exit !($wal_mb > $WAL_CRIT_MB)}"; then
        set_unhealthy "$(printf 'WAL: CRITICAL (%.1f MB)' "$wal_mb")"
    elif awk "BEGIN {exit !($wal_mb > $WAL_WARN_MB)}"; then
        printf 'WAL: warn (%.1f MB)\n' "$wal_mb"
    else
        printf 'WAL: ok (%.1f MB)\n' "$wal_mb"
    fi
fi

# LEDGER REVISIONS
if [[ -z "$PY" ]]; then
    set_unhealthy 'LEDGER REVISIONS: BROKEN (no python interpreter)'
else
    lrr_out=$("$PY" "$SD/tools/ledger_revision_reachability.py" 2>&1)
    lrr_exit=$?
    if [[ $lrr_exit -ne 0 ]]; then
        set_unhealthy 'LEDGER REVISIONS: BROKEN'
        echo "$lrr_out" | while IFS= read -r l; do echo "  $l"; done
    else
        last_line=$(echo "$lrr_out" | tail -1)
        echo "LEDGER REVISIONS: $last_line"
    fi
fi

# LEDGER MANIFEST
lp_out=$(bash "$SD/ops/ledger-provenance.sh" --diff 2>&1)
lp_exit=$?
last_line=$(echo "$lp_out" | tail -1)
echo "LEDGER MANIFEST: $last_line"
if [[ $lp_exit -ne 0 ]]; then
    echo "$lp_out" | head -n -1 | while IFS= read -r l; do echo "  $l"; done
fi
if [[ $lp_exit -gt 1 ]]; then
    set_unhealthy "LEDGER MANIFEST: $last_line"
fi

# LEDGER COVERAGE
max_unledgered=50
if [[ -z "$PY" ]]; then
    echo "LEDGER COVERAGE: unreadable (no python interpreter)"
else
    cov_out=$("$PY" - "$DB" <<'PY' 2>&1
import sqlite3, sys, urllib.parse

LEDGER_START = 1783155600
conn = None
try:
    path = sys.argv[1]
    conn = sqlite3.connect("file:" + urllib.parse.quote(path) + "?mode=ro", uri=True)
    n = conn.execute(
        "select count(*) from predictions p "
        "left join prediction_ledger l on l.symbol_id=p.symbol_id "
        "  and l.horizon=p.horizon and l.bar_ts=p.ts "
        "where l.seq is null and p.n_used > 0 and p.ts >= ?",
        (LEDGER_START,)).fetchone()[0]
    total = conn.execute(
        "select count(*) from predictions where n_used > 0 and ts >= ?",
        (LEDGER_START,)).fetchone()[0]
    print("%d|%d" % (n, total))
except sqlite3.OperationalError as e:
    print("SKIP:%s" % e)
except Exception as e:
    print("ERROR:%s" % e, file=sys.stderr)
    sys.exit(2)
finally:
    if conn is not None:
        conn.close()
PY
)
    py_exit=$?
    if [[ $py_exit -ne 0 ]]; then
        echo "LEDGER COVERAGE: unreadable ($cov_out)"
    else
        line=$(echo "$cov_out" | grep -v '^$' | tail -1)
        if [[ "$line" == SKIP:* ]]; then
            echo "LEDGER COVERAGE: SKIPPED ($line)"
        elif [[ "$line" =~ ^([0-9]+)\|([0-9]+)$ ]]; then
            unledgered="${BASH_REMATCH[1]}"
            served_tot="${BASH_REMATCH[2]}"
            if [[ $unledgered -gt $max_unledgered ]]; then
                set_unhealthy "$(printf 'LEDGER COVERAGE: %d served prediction(s) of %d never reached the chain (max %d)' "$unledgered" "$served_tot" "$max_unledgered")"
            else
                printf 'LEDGER COVERAGE: %d unledgered of %d served (max %d)\n' "$unledgered" "$served_tot" "$max_unledgered"
            fi
        else
            echo "LEDGER COVERAGE: unexpected output ($line)"
        fi
    fi
fi

# LEDGER IDENTITIES
max_dup_identities=400
if [[ -z "$PY" ]]; then
    echo "LEDGER IDENTITIES: unreadable (no python interpreter)"
else
    dup_out=$("$PY" - "$DB" <<'PY' 2>&1
import sqlite3, sys, urllib.parse

conn = None
try:
    path = sys.argv[1]
    conn = sqlite3.connect("file:" + urllib.parse.quote(path) + "?mode=ro", uri=True)
    n = conn.execute(
        "select count(*) from (select symbol_id, horizon, bar_ts "
        "from prediction_ledger group by symbol_id, horizon, bar_ts "
        "having count(*) > 1)").fetchone()[0]
    conflicting = conn.execute(
        "select count(*) from (select symbol_id, horizon, bar_ts "
        "from prediction_ledger group by symbol_id, horizon, bar_ts "
        "having count(distinct cal_prob) > 1)").fetchone()[0]
    print("%d|%d" % (n, conflicting))
except sqlite3.OperationalError as e:
    print("SKIP:%s" % e)
except Exception as e:
    print("ERROR:%s" % e, file=sys.stderr)
    sys.exit(2)
finally:
    if conn is not None:
        conn.close()
PY
)
    py_exit=$?
    if [[ $py_exit -ne 0 ]]; then
        echo "LEDGER IDENTITIES: unreadable ($dup_out)"
    else
        line=$(echo "$dup_out" | grep -v '^$' | tail -1)
        if [[ "$line" == SKIP:* ]]; then
            echo "LEDGER IDENTITIES: SKIPPED ($line)"
        elif [[ "$line" =~ ^([0-9]+)\|([0-9]+)$ ]]; then
            dups="${BASH_REMATCH[1]}"
            conflicting="${BASH_REMATCH[2]}"
            if [[ $dups -gt $max_dup_identities ]]; then
                set_unhealthy "$(printf 'LEDGER IDENTITIES: %d superseded claim(s), %d disagreeing on cal_prob (max %d)' "$dups" "$conflicting" "$max_dup_identities")"
            else
                printf 'LEDGER IDENTITIES: %d superseded (%d conflicting), max %d\n' "$dups" "$conflicting" "$max_dup_identities"
            fi
        else
            echo "LEDGER IDENTITIES: unexpected output ($line)"
        fi
    fi
fi

if [[ "$failed" == true ]]; then
    echo 'RESULT: unhealthy'
    why="$(printf '%s; ' "${reasons[@]}")"
    if [[ -z "$why" ]]; then
        why='see the run output'
    fi
    bash "$SD/ops/cloud/alert.sh" 'SignalDeck grader health: UNHEALTHY' "$why" || true
    exit 1
fi
echo 'RESULT: ok'
exit 0