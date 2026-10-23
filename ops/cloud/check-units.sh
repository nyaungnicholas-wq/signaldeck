#!/bin/bash
# Replaces ops/check-task-health.ps1 and the report mode of ops/install-windows-tasks.ps1
# Read-only health check for the SignalDeck systemd fleet on Linux.
# Exits 0 if all checks pass, 1 otherwise. Produces human-readable findings.
# Usage: check-units.sh [--unit-dir DIR]

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UNIT_DIR="/etc/systemd/system"
SYSTEMCTL_CMD="${SYSTEMCTL:-systemctl}"
SS_CMD="${SS:-ss}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --unit-dir)
            UNIT_DIR="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 1
            ;;
    esac
done

findings=()

# 1. Drift check
repo_dir="${SCRIPT_DIR}/systemd"
if [[ -d "$repo_dir" ]]; then
    for f in "$repo_dir"/signaldeck-*.service "$repo_dir"/signaldeck-*.timer; do
        [[ -e "$f" ]] || continue
        name="$(basename "$f")"
        installed="${UNIT_DIR}/${name}"
        if [[ ! -e "$installed" ]]; then
            findings+=("MISSING ${name}")
        elif ! cmp -s "$f" "$installed"; then
            findings+=("DRIFT ${name}")
        fi
    done
    for f in "$UNIT_DIR"/signaldeck-*.service "$UNIT_DIR"/signaldeck-*.timer; do
        [[ -e "$f" ]] || continue
        name="$(basename "$f")"
        repo_file="${repo_dir}/${name}"
        if [[ ! -e "$repo_file" ]]; then
            findings+=("ORPHAN ${name}")
        fi
    done
fi

# 2. Timers check
if [[ -d "$repo_dir" ]]; then
    for f in "$repo_dir"/signaldeck-*.timer; do
        [[ -e "$f" ]] || continue
        name="$(basename "$f")"
        if ! "$SYSTEMCTL_CMD" is-enabled --quiet "$name"; then
            findings+=("DISABLED ${name}")
        fi
        if ! "$SYSTEMCTL_CMD" is-active --quiet "$name"; then
            findings+=("INACTIVE ${name}")
        fi
    done
fi

# 3. Failed units
while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    unit="$(echo "$line" | awk '{print $1}')"
    findings+=("FAILED ${unit}")
done < <("$SYSTEMCTL_CMD" list-units --state=failed --no-legend --plain 'signaldeck-*' 2>/dev/null || true)

# 4. Last result for services with matching timers
if [[ -d "$repo_dir" ]]; then
    for f in "$repo_dir"/signaldeck-*.service; do
        [[ -e "$f" ]] || continue
        name="$(basename "$f")"
        timer_name="${name%.service}.timer"
        if [[ -e "${repo_dir}/${timer_name}" ]]; then
            result="$("$SYSTEMCTL_CMD" show -p Result --value "$name" 2>/dev/null || echo "")"
            if [[ -n "$result" && "$result" != "success" ]]; then
                findings+=("LAST-RESULT ${name} ${result}")
            fi
        fi
    done
fi

# 5. Ports check
port_tries="${CHECK_UNITS_PORT_TRIES:-4}"
port_sleep="${CHECK_UNITS_PORT_SLEEP:-30}"
for port in 8322 8323; do
    success=0
    for ((i=1; i<=port_tries; i++)); do
        if "$SS_CMD" -ltnH "sport = :${port}" 2>/dev/null | grep -q .; then
            success=1
            break
        fi
        if (( i < port_tries )); then
            sleep "$port_sleep"
        fi
    done
    if (( success == 0 )); then
        findings+=("PORT-DOWN ${port}")
    fi
done

# 6. Clock check
if command -v timedatectl >/dev/null 2>&1; then
    ntp_sync="$(timedatectl show -p NTPSynchronized --value 2>/dev/null || echo "")"
    if [[ "$ntp_sync" != "yes" ]]; then
        findings+=("CLOCK-UNSYNCED")
    fi
else
    findings+=("CLOCK-UNKNOWN")
fi

# Output findings
for finding in "${findings[@]}"; do
    echo "  ${finding}"
done

# Alert if findings exist and alert.sh exists
if (( ${#findings[@]} > 0 )) && [[ -f "${SCRIPT_DIR}/alert.sh" ]]; then
    joined="$(printf '%s; ' "${findings[@]}")"
    bash "${SCRIPT_DIR}/alert.sh" "check-units" "${joined}" || true
fi

# Final status
if (( ${#findings[@]} == 0 )); then
    echo "check-units: HEALTHY"
    exit 0
else
    echo "check-units: UNHEALTHY (${#findings[@]} finding(s))"
    exit 1
fi