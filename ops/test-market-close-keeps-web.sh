#!/bin/bash
# SD-38: market-close.sh must stop the daemon (the offline backup refuses a live
# one) and the ngrok tunnel, but NOT the web. It used to run
# `signaldeck-ctl.sh stop`, which also stopped the web - the public site - for
# nothing: the web never opens the database. Its boot-time catch-up on
# 2026-09-30 18:03 took the public site down ~4.5 min.
#
# Runs the REAL market-close.sh as a copy inside a temp repo where every other
# ops script, schtasks, powershell and python is a stub that records its call,
# so nothing here can start or stop a real task. The PATH check below refuses
# to run if a real schtasks would be reached.
set -u
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/ops" "$T/bin" "$T/logs" "$T/.venv/Scripts"
cp "$SRC/market-close.sh" "$T/ops/"
export CALLS="$T/calls"
: > "$CALLS"

cat > "$T/ops/lib-portable.sh" <<'EOF'
sd_svc_stop()   { echo "svc_stop $1" >> "$CALLS"; }
sd_is_running() { return 1; }
sd_kill_hard()  { echo "kill_hard $1" >> "$CALLS"; }
sd_notify()     { :; }
EOF
echo 'sd_ft_stale_line() { :; }' > "$T/ops/lib-forward-test.sh"
for s in signaldeck-ctl.sh signaldeck-backup-offline.sh; do
  printf '#!/bin/bash\necho "%s $*" >> "$CALLS"\n' "$s" > "$T/ops/$s"
done
for s in schtasks powershell; do
  printf '#!/bin/bash\necho "%s $*" >> "$CALLS"\n' "$s" > "$T/bin/$s"
  chmod +x "$T/bin/$s"
done
printf '#!/bin/bash\nexit 0\n' > "$T/.venv/Scripts/python.exe"
chmod +x "$T/.venv/Scripts/python.exe"

export PATH="$T/bin:$PATH"
if [ "$(command -v schtasks)" != "$T/bin/schtasks" ] || [ "$(command -v powershell)" != "$T/bin/powershell" ]; then
  echo "test-market-close-keeps-web: UNSAFE - a real schtasks/powershell would run; not running market-close.sh"
  exit 2
fi

/bin/bash "$T/ops/market-close.sh" > "$T/out" 2>&1
rc=$?

fails=0
check() { if [ "$2" = "1" ]; then echo "  ok   $1"; else echo "  FAIL $1"; fails=$((fails + 1)); fi; }
line_of() { grep -n -F -- "$1" "$CALLS" | head -1 | cut -d: -f1; }

check "the web is never stopped (no 'ctl stop', no web svc_stop)" \
  "$(grep -q -e 'signaldeck-ctl.sh stop' -e 'svc_stop com.signaldeck.web' "$CALLS" && echo 0 || echo 1)"
d=$(line_of 'svc_stop com.signaldeck.daemon'); b=$(line_of 'signaldeck-backup-offline.sh')
check "the daemon is stopped before the backup runs" \
  "$([ -n "$d" ] && [ -n "$b" ] && [ "$d" -lt "$b" ] && echo 1 || echo 0)"
check "the ngrok tunnel is still stopped" \
  "$(grep -q -F 'svc_stop com.signaldeck.tunnel' "$CALLS" && echo 1 || echo 0)"
r=$(line_of 'schtasks //Run //TN SignalDeck Daemon')
check "the daemon is restarted after the backup" \
  "$([ -n "$r" ] && [ -n "$b" ] && [ "$r" -gt "$b" ] && echo 1 || echo 0)"
check "market-close exits 0 when the backup and restart succeed (rc=$rc)" "$([ "$rc" = 0 ] && echo 1 || echo 0)"

if [ "$fails" -gt 0 ]; then
  echo "--- calls"; cat "$CALLS"; echo "--- output"; cat "$T/out"
  echo "test-market-close-keeps-web: FAILED ($fails)"
  exit 1
fi
echo "test-market-close-keeps-web: OK"
