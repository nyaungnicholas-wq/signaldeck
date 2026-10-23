#!/bin/bash
# Self-test for the Linux service-control paths with stub systemctl / curl /
# uname / pgrep: ops/lib-portable.sh's systemd branch (and that a non-Linux
# host never takes it), ops/market-close-restart.sh on Linux, and the decision
# logic of ops/cloud/daemon-guard.sh and ops/cloud/web-guard.sh. Nothing real
# is started or stopped. Prints SELFTEST OK.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$(cd "$HERE/../.." && pwd)"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
fails=0
ok() { echo "  ok   $1"; }
bad() { echo "  FAIL $1"; fails=$((fails + 1)); }
expect() { if eval "$2"; then ok "$1"; else bad "$1 [$2] out: $(printf '%s' "${OUT:-}" | tail -2 | tr '\n' '|')"; fi; }

S="$T/stubs"; mkdir -p "$S"; LOG="$T/calls.log"; FLAG="$T/restarted"
cat > "$S/uname" <<'EOF'
#!/bin/bash
echo "${STUB_OS:-Linux}"
EOF
cat > "$S/systemctl" <<'EOF'
#!/bin/bash
echo "systemctl $*" >> "$STUB_LOG"
case "$1" in
  cat) exit "${STUB_CAT_RC:-0}" ;;
  start) [ "${STUB_START_RC:-0}" -eq 0 ] && : > "$STUB_FLAG"; exit "${STUB_START_RC:-0}" ;;
  stop|reset-failed) exit 0 ;;
  is-active) exit "${STUB_ACTIVE_RC:-0}" ;;
  show) echo "${STUB_SINCE_US:-0}"; exit 0 ;;
esac
exit 0
EOF
cat > "$S/schtasks" <<'EOF'
#!/bin/bash
echo "schtasks $*" >> "$STUB_LOG"
exit 1
EOF
cat > "$S/pgrep" <<'EOF'
#!/bin/bash
case "$*" in *cloudflared*) [ "${STUB_TUNNEL:-no}" = yes ]; exit ;; esac
[ "${STUB_PGREP:-no}" = yes ]
EOF
cat > "$S/tasklist" <<'EOF'
#!/bin/bash
[ "${STUB_TUNNEL:-no}" = yes ] && echo cloudflared.exe
exit 0
EOF
cat > "$S/curl" <<'EOF'
#!/bin/bash
url="${*: -1}"
echo "curl $url" >> "$STUB_LOG"
case "$url" in
  */api/health) printf '%s' "${STUB_HEALTH:-200}" ;;
  */login) if [ -e "$STUB_FLAG" ]; then printf 200; else printf '%s' "${STUB_LOGIN:-200}"; fi ;;
  */api/version) printf '{"revision":"x","resolvable":true}' ;;
esac
exit 0
EOF
chmod +x "$S"/*
export PATH="$S:$PATH" STUB_LOG="$LOG" STUB_FLAG="$FLAG"

# fixture tree: real scripts under test, stub refresh + asset check
F="$T/sd"; mkdir -p "$F/ops/cloud" "$F/data" "$F/logs" "$F/daemon" "$F/bin" "$F/web/.next"
cp "$SRC/ops/lib-portable.sh" "$SRC/ops/lib-deploy.sh" "$SRC/ops/market-close-restart.sh" "$F/ops/"
cp "$SRC/ops/cloud/daemon-guard.sh" "$SRC/ops/cloud/web-guard.sh" "$SRC/ops/cloud/alert.sh" "$F/ops/cloud/"
printf '#!/bin/bash\necho "refresh $*" >> "$STUB_LOG"\n' > "$F/ops/signaldeck-refresh.sh"
printf '#!/bin/bash\n[ -e "$STUB_FLAG" ] && exit 0\nexit "${STUB_ASSETS:-0}"\n' > "$F/ops/cloud/web-assets-check.sh"
printf '#!/bin/bash\n[ "$1" = -publication-posture ] && exit "${STUB_POSTURE:-0}"\n' > "$F/bin/signaldeckd"
chmod +x "$F/bin/signaldeckd"
echo build-1 > "$F/web/.next/BUILD_ID"
reset() { : > "$LOG"; rm -f "$FLAG" "$F/ops/.maintenance" "$F/logs/daemon-guard.strikes" "$F/logs/web-guard-linux.state" "$F/data/.stop-request"; }
called() { grep -q "^$1" "$LOG"; }

echo "Linux ops self-test"

# --- lib-portable.sh systemd branch -------------------------------------------
reset; ( . "$F/ops/lib-portable.sh"; sd_svc_start com.signaldeck.daemon ); rc=$?
expect "sd_svc_start daemon -> systemctl start, rc 0" '[ $rc -eq 0 ] && called "systemctl start signaldeck-daemon.service"'
reset; ( . "$F/ops/lib-portable.sh"; STUB_CAT_RC=1 sd_svc_start com.signaldeck.web ); rc=$?
expect "a unit that is not installed reads as not registered (2)" '[ $rc -eq 2 ] && ! called "systemctl start"'
reset; ( . "$F/ops/lib-portable.sh"; STUB_START_RC=1 sd_svc_start com.signaldeck.daemon ); rc=$?
expect "a unit that will not start is 1" '[ $rc -eq 1 ]'
reset; ( . "$F/ops/lib-portable.sh"; STUB_PGREP=yes sd_svc_stop com.signaldeck.daemon ); rc=$?
expect "sd_svc_stop daemon -> systemctl stop, and NO stop file (Restart=always would undo it)" \
  '[ $rc -eq 0 ] && called "systemctl stop signaldeck-daemon.service" && [ ! -e "$F/data/.stop-request" ]'
reset; ( . "$F/ops/lib-portable.sh"; sd_svc_start com.signaldeck.tunnel ); rc=$?
expect "a service with no unit (the ngrok tunnel) is 2 via the schtasks branch" '[ $rc -eq 2 ] && ! called systemctl'
reset; ( . "$F/ops/lib-portable.sh"; STUB_OS=MINGW64_NT-10.0 sd_svc_start com.signaldeck.daemon ); rc=$?
expect "off Linux the systemd branch is never taken (Windows path unchanged)" '! called systemctl && called "schtasks //Query"'

# --- market-close-restart.sh on Linux ------------------------------------------
reset; : > "$F/ops/.maintenance"
bash "$F/ops/market-close-restart.sh"; rc=$?
expect "market-close-restart starts the unit, drops the lock, exits 0" \
  '[ $rc -eq 0 ] && called "systemctl start signaldeck-daemon.service" && [ ! -e "$F/ops/.maintenance" ] && ! called schtasks'
reset; STUB_START_RC=1 bash "$F/ops/market-close-restart.sh"; rc=$?
expect "a failed restart exits non-zero and is logged" '[ $rc -ne 0 ] && grep -q "daemon restart FAILED" "$F/logs/backup-offline.log"'

# --- daemon-guard.sh -------------------------------------------------------------
UP="$T/uptime"; echo "100000.00 0" > "$UP"; export SD_UPTIME_FILE="$UP"
NOW_US=100000000000
reset; : > "$F/ops/.maintenance"
OUT="$(bash "$F/ops/cloud/daemon-guard.sh")"; rc=$?
expect "guard: a fresh lock stands down and touches nothing" '[ $rc -eq 0 ] && [ ! -s "$LOG" ]'
reset; : > "$F/ops/.maintenance"; touch -d '3 hours ago' "$F/ops/.maintenance"
OUT="$(bash "$F/ops/cloud/daemon-guard.sh")"; rc=$?
expect "guard: a stale lock with no holder is removed" '[ ! -e "$F/ops/.maintenance" ] && called "refresh prune-only 0"'
reset; OUT="$(STUB_ACTIVE_RC=3 bash "$F/ops/cloud/daemon-guard.sh")"; rc=$?
expect "guard: a down daemon is reset-failed and started" \
  '[ $rc -eq 0 ] && called "systemctl reset-failed signaldeck-daemon.service" && called "systemctl start signaldeck-daemon.service"'
reset; OUT="$(STUB_ACTIVE_RC=3 STUB_TUNNEL=yes STUB_POSTURE=3 bash "$F/ops/cloud/daemon-guard.sh")"; rc=$?
expect "guard: a down daemon that reads unpublished behind a tunnel is NOT started" \
  '[ $rc -ne 0 ] && ! called "systemctl start" && grep -q "NOT restarted" "$F/logs/health-alerts.log"'
reset
for i in 1 2; do OUT="$(STUB_HEALTH=000 STUB_SINCE_US=$((NOW_US - 1800000000)) bash "$F/ops/cloud/daemon-guard.sh")"; done
expect "guard: two silent health checks are strikes, not a restart" '[ "$(cat "$F/logs/daemon-guard.strikes")" = 2 ] && ! called "systemctl stop"'
OUT="$(STUB_HEALTH=000 STUB_SINCE_US=$((NOW_US - 1800000000)) bash "$F/ops/cloud/daemon-guard.sh")"
expect "guard: the third strike restarts the daemon" 'called "systemctl stop signaldeck-daemon.service" && called "systemctl start signaldeck-daemon.service" && [ ! -e "$F/logs/daemon-guard.strikes" ]'
reset; for i in 1 2 3; do OUT="$(STUB_HEALTH=000 STUB_SINCE_US=$((NOW_US - 300000000)) bash "$F/ops/cloud/daemon-guard.sh")"; done
expect "guard: no strikes count in the first 20 minutes of uptime" '[ "$(cat "$F/logs/daemon-guard.strikes")" = 0 ] && ! called "systemctl stop"'
reset; echo 2 > "$F/logs/daemon-guard.strikes"; OUT="$(STUB_HEALTH=503 bash "$F/ops/cloud/daemon-guard.sh")"
expect "guard: any HTTP answer (even 503) clears the strikes" '[ ! -e "$F/logs/daemon-guard.strikes" ]'

# --- web-guard.sh -----------------------------------------------------------------
reset; : > "$F/ops/.maintenance"; OUT="$(STUB_LOGIN=000 bash "$F/ops/cloud/web-guard.sh")"
expect "web guard: a fresh lock stands down" '! called systemctl'
reset; OUT="$(STUB_LOGIN=000 bash "$F/ops/cloud/web-guard.sh")"; rc=$?
expect "web guard: a web not answering /login is restarted" '[ $rc -eq 0 ] && called "systemctl start signaldeck-web.service"'
reset; OUT="$(STUB_ASSETS=1 bash "$F/ops/cloud/web-guard.sh")"; rc=$?
expect "web guard: an incomplete build is restarted once and the fix is seen" '[ $rc -eq 0 ] && called "systemctl stop signaldeck-web.service"'
reset; echo build-1 > "$F/logs/web-guard-linux.state"; OUT="$(STUB_ASSETS=1 bash "$F/ops/cloud/web-guard.sh")"; rc=$?
expect "web guard: a build a restart already failed to fix is suppressed" '[ $rc -ne 0 ] && ! called systemctl'
reset; OUT="$(STUB_ASSETS=2 bash "$F/ops/cloud/web-guard.sh")"; rc=$?
expect "web guard: an unmeasurable check (exit 2) restarts nothing" '[ $rc -eq 0 ] && ! called systemctl'

[ "$fails" -eq 0 ] || { echo "linux ops self-test: $fails FAILED"; exit 1; }
echo "SELFTEST OK"
