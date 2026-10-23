#!/bin/bash
# Self-test for ops/cloud/deploy-linux.sh. Builds a throwaway git repo holding
# the real deploy script and the libraries it sources, puts stub go /
# golangci-lint / systemctl / curl / python3 / uname on PATH, and proves every
# refusal path fires - and that a refusal before the restart touches nothing.
# No real build, restart or network call happens. Prints SELFTEST OK.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$(cd "$HERE/../.." && pwd)"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
fails=0
ok() { echo "  ok   $1"; }
bad() { echo "  FAIL $1"; fails=$((fails + 1)); }

# --- stubs: behaviour comes from STUB_* variables, every call is logged ------
STUBS="$T/stubs"; mkdir -p "$STUBS"
LOG="$T/calls.log"
cat > "$STUBS/uname" <<'EOF'
#!/bin/bash
echo Linux
EOF
cat > "$STUBS/id" <<'EOF'
#!/bin/bash
if [ "${1:-}" = -u ]; then echo "${STUB_UID:-1000}"; else echo signaldeck; fi
EOF
cat > "$STUBS/go" <<'EOF'
#!/bin/bash
echo "go $* @ $PWD" >> "$STUB_LOG"
case "$1" in
  vet) exit "${STUB_VET_RC:-0}" ;;
  test) exit "${STUB_TEST_RC:-0}" ;;
  build)
    out=""; prev=""
    for a in "$@"; do [ "$prev" = -o ] && out="$a"; prev="$a"; done
    printf '#!/bin/bash\n[ "$1" = -publication-posture ] && exit "${STUB_POSTURE_RC:-0}"\nexit 0\n' > "$out"
    chmod +x "$out"
    exit "${STUB_BUILD_RC:-0}" ;;
esac
exit 0
EOF
cat > "$STUBS/golangci-lint" <<'EOF'
#!/bin/bash
echo "golangci-lint $* @ $PWD" >> "$STUB_LOG"
exit "${STUB_LINT_RC:-0}"
EOF
cat > "$STUBS/systemctl" <<'EOF'
#!/bin/bash
echo "systemctl $*" >> "$STUB_LOG"
case "$1" in
  cat) exit 0 ;;
  start|stop|restart) exit "${STUB_SYSTEMCTL_RC:-0}" ;;
esac
exit 0
EOF
cat > "$STUBS/curl" <<'EOF'
#!/bin/bash
url="${*: -1}"
echo "curl $url" >> "$STUB_LOG"
case "$url" in
  */api/health) exit 0 ;;
  */api/version) printf '{"revision":"%s","resolvable":%s}' "${STUB_REV:-}" "${STUB_RESOLVABLE:-true}"; exit 0 ;;
esac
exit 7
EOF
cat > "$STUBS/python3" <<'EOF'
#!/bin/bash
[ "${1:-}" = -c ] && [ "${2:-}" = "import sys" ] && exit 0
echo "python3 rows ${*: -1}" >> "$STUB_LOG"
echo "${STUB_ROWS:-1}"
EOF
# A tunnel is "running" only when STUB_TUNNEL=yes, whichever probe lib-deploy picks.
cat > "$STUBS/pgrep" <<'EOF'
#!/bin/bash
[ "${STUB_TUNNEL:-no}" = yes ]
EOF
cat > "$STUBS/tasklist" <<'EOF'
#!/bin/bash
[ "${STUB_TUNNEL:-no}" = yes ] && echo cloudflared.exe
exit 0
EOF
chmod +x "$STUBS"/*

# --- the throwaway repo: real scripts under test, stub everything else -------
R="$T/root/signaldeck"
mkdir -p "$R/ops/cloud" "$R/daemon/internal/api" "$R/daemon/cmd/signaldeckd"
cp "$SRC/ops/lib-portable.sh" "$SRC/ops/lib-deploy.sh" "$SRC/ops/manifest-check.sh" \
   "$SRC/ops/generated-docs.txt" "$R/ops/"
cp "$SRC/ops/cloud/deploy-linux.sh" "$R/ops/cloud/"
for f in PREREGISTRATION.md REPRODUCE.md PREDICTION_PROCESS.md; do echo stub > "$R/$f"; done
echo 'version: "2"' > "$R/daemon/.golangci.yml"
echo 'package api' > "$R/daemon/internal/api/version.go"
echo 'package main' > "$R/daemon/cmd/signaldeckd/main.go"
echo stub > "$R/ops/signaldeck-ctl.sh"
printf 'data/\nbin/\nlogs/\nops/.maintenance\n' > "$R/.gitignore"
git -C "$R" init -q
git -C "$R" -c user.email=t@t -c user.name=t add -A
git -C "$R" -c user.email=t@t -c user.name=t commit -qm fixture
REV="$(git -C "$R" rev-parse HEAD)"

# run NAME [VAR=value ...] -> sets RC and OUT; fresh call log per run
run() {
  : > "$LOG"
  rm -f "$R/bin/signaldeckd" "$R/bin/signaldeckd.prev" "$R/ops/.maintenance"
  OUT="$(env PATH="$STUBS:$PATH" STUB_LOG="$LOG" STUB_REV="$REV" SD_DEPLOY_ROWS_WAIT=2 "$@" \
        bash "$R/ops/cloud/deploy-linux.sh" ${DEPLOY_ARGS:-} 2>&1)"
  RC=$?
}
restarted() { grep -qE '^systemctl (stop|start|restart) signaldeck-daemon.service' "$LOG"; }
check_refused() { # NAME EXPECTED_TEXT  -- refused BEFORE anything was stopped
  if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q "$2" && ! restarted && [ ! -e "$R/bin/signaldeckd" ]; then
    ok "$1"
  else
    bad "$1 (rc=$RC, restarted=$(restarted && echo yes || echo no)): $(printf '%s' "$OUT" | tail -3)"
  fi
}
check_unverified() { # NAME EXPECTED_TEXT -- restarted, then refused to call it deployed
  if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q "deploy UNVERIFIED: .*$2" && restarted; then
    ok "$1"
  else
    bad "$1 (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
  fi
}

echo "deploy-linux.sh self-test"

# 1. success: build ran on the extracted commit, stamped with HEAD, restarted, verified
run env
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q "deploy VERIFIED: daemon is running commit $REV" \
   && [ -x "$R/bin/signaldeckd" ] && restarted \
   && grep -q "^go build -ldflags -X .*ldflagsRev=$REV " "$LOG" \
   && ! grep -E '^go (vet|test|build) ' "$LOG" | grep -qF "@ $R/daemon" \
   && grep -q '^golangci-lint run' "$LOG" && grep -q '^go test ' "$LOG" && grep -q '^go vet ' "$LOG" \
   && [ ! -e "$R/ops/.maintenance" ]; then
  ok "clean HEAD deploys, verified against /api/version and worker_runs"
else
  bad "success path (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 2. dirty tree
echo dirt > "$R/daemon/uncommitted.go"
run env
check_refused "dirty tree refuses" "working tree is not clean"
rm -f "$R/daemon/uncommitted.go"

# 3-5. the CI checks, each on its own
run env STUB_VET_RC=1;  check_refused "go vet failure refuses" "go vet failed"
run env STUB_LINT_RC=1; check_refused "golangci-lint failure refuses" "golangci-lint failed"
run env STUB_TEST_RC=1; check_refused "failing test refuses" "daemon tests failed"

# 6. build failure
run env STUB_BUILD_RC=1; check_refused "build failure refuses" "build from the extracted commit failed"

# 7. publication posture: a tunnel is up and the new binary reads as unpublished
run env STUB_TUNNEL=yes STUB_POSTURE_RC=3; check_refused "unpublished posture behind a tunnel refuses" "reads the daemon as unpublished"

# 8. root
run env STUB_UID=0; check_refused "running as root refuses" "never root"

# 9. someone else's maintenance lock
: > "$LOG"; rm -f "$R/bin/signaldeckd"
: > "$R/ops/.maintenance"
OUT="$(env PATH="$STUBS:$PATH" STUB_LOG="$LOG" STUB_REV="$REV" bash "$R/ops/cloud/deploy-linux.sh" 2>&1)"; RC=$?
check_refused "an existing maintenance lock refuses" "another maintenance job"
[ -e "$R/ops/.maintenance" ] && ok "the other job's lock is left in place" || bad "deploy removed another job's lock"
rm -f "$R/ops/.maintenance"

# 10-13. after the restart: the running process must be the commit, with rows
run env STUB_REV=0000000000000000000000000000000000000000; check_unverified "revision mismatch is UNVERIFIED" "does not match the deployed commit"
run env STUB_RESOLVABLE=false; check_unverified "unresolvable build is UNVERIFIED" "resolvable=false"
run env STUB_ROWS=0; check_unverified "no worker_runs row stamped HEAD is UNVERIFIED" "no finished worker_runs row"
run env STUB_SYSTEMCTL_RC=1; check_unverified "failed restart is UNVERIFIED" "could not restart"

# 14. dry run: every gate, nothing installed or restarted
DEPLOY_ARGS=--dry-run run env
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q "deploy DRY RUN: every gate passed for $REV" \
   && ! restarted && [ ! -e "$R/bin/signaldeckd" ] && grep -q '^go test ' "$LOG"; then
  ok "--dry-run runs every gate and changes nothing"
else
  bad "dry run (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi
DEPLOY_ARGS=--dry-run run env STUB_TEST_RC=1; check_refused "--dry-run still refuses a failing test" "daemon tests failed"

[ "$fails" -eq 0 ] || { echo "deploy-linux self-test: $fails FAILED"; exit 1; }
echo "SELFTEST OK"
