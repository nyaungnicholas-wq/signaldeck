#!/bin/bash
# Guard against bare pkill/pgrep/jq invocations in ops scripts.
#
# Background: ops/market-close.sh called a bare `pkill -9 -x signaldeckd` on its
# hung-daemon escalation path. Under Windows Git Bash, `pkill`, `pgrep`, and `jq`
# DO NOT EXIST, so the escalation silently no-opped, the daemon stayed alive, and
# the backup script's is-daemon-alive safety check refused to run — the day's
# backup vanished while the scheduled task still exited 0. lib-portable.sh's
# header had already documented the identical pgrep defect being fixed; this one
# call site was missed. A one-site fix does not prevent the next one, so this test
# scans ALL ops scripts for bare invocations and asserts the shims still exist.
set -u

OPS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SELF="$(basename "${BASH_SOURCE[0]}")"
FAIL=0

for f in "$OPS"/*.sh; do
  base="$(basename "$f")"
  if [ "$base" = "lib-portable.sh" ] || [ "$base" = "$SELF" ]; then
    continue
  fi
  clean=1
  while IFS= read -r raw; do
    [ -z "$raw" ] && continue
    num="${raw%%:*}"
    [ -z "$num" ] && continue
    line="${raw#*:}"
    trimmed="$(printf '%s' "$line" | sed 's/^[[:space:]]*//')"
    case "$trimmed" in
      \#*) continue ;;
    esac
    case "$line" in
      *"command -v"*) continue ;;
    esac
    # Strip quoted string literals before matching. A log line that MENTIONS the
    # tool ("force-kill FAILED (no pkill, no taskkill)") is not an invocation of
    # it, and flagging the error message that documents the fix is how a guard
    # gets a `|| true` bolted onto it. Only code outside quotes can run a command.
    code="$(printf '%s' "$line" | sed -e "s/'[^']*'//g" -e 's/"[^"]*"//g')"
    if printf '%s' "$code" | grep -E '(^|[^[:alnum:]_])(pkill|pgrep|jq)([^[:alnum:]_]|$)' >/dev/null 2>&1; then
      printf 'FAIL %s:%s %s\n' "$base" "$num" "$line"
      FAIL=$((FAIL + 1))
      clean=0
    fi
  done <<EOF
$(grep -n -E '(pkill|pgrep|jq)' "$f")
EOF
  if [ "$clean" = 1 ]; then
    printf '  ok   %s\n' "$base"
  fi
done

for sym in 'sd_kill_hard()' 'sd_is_running()'; do
  if ! grep -n -F "$sym" "$OPS/lib-portable.sh" >/dev/null 2>&1; then
    printf 'FAIL lib-portable.sh missing %s\n' "$sym"
    FAIL=$((FAIL + 1))
  fi
done

if [ "$FAIL" = 0 ]; then
  printf 'all bare-pkill/pgrep/jq checks passed\n'
  exit 0
fi
printf '%d bare-pkill/pgrep/jq check(s) failed\n' "$FAIL"
exit 1