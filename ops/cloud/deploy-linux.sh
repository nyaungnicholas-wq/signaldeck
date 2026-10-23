#!/bin/bash
# deploy-linux.sh - the Linux (systemd) counterpart of `ops/signaldeck-ctl.sh
# deploy`: THE sanctioned source->running path on the cloud VM (ops/cloud/README.md).
#
#   sudo -u signaldeck bash ops/cloud/deploy-linux.sh            deploy HEAD
#   sudo -u signaldeck bash ops/cloud/deploy-linux.sh --dry-run  every gate, no install/restart
#
# Same guarantees as ctl deploy, each step can only REFUSE (exit non-zero):
#   1. clean tree (ops/generated-docs.txt exempt, same rule) + manifest check
#   2. `git archive HEAD` into a temp dir - the COMMIT, never the working tree
#   3. go vet, golangci-lint and go test on that extracted commit. Stricter than
#      ctl deploy, which tests the working tree and only warns without lint.
#   4. build with the commit stamped via -ldflags, publication posture asked of
#      the NEW binary before anything is stopped (AUD-09)
#   5. install beside the old binary (bin/signaldeckd.prev kept), restart
#   6. /api/version revision == HEAD and resolvable=true
#   7. a finished worker_runs row stamped with HEAD (rows, not just the process)
# Success is printed only after 7. Self-test: ops/cloud/test-deploy-linux.sh.
set -u
set -o pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib-portable.sh
. "$REPO/ops/lib-portable.sh"
# shellcheck source=../lib-deploy.sh
. "$REPO/ops/lib-deploy.sh"

DRY=0
case "${1:-}" in
  "") ;;
  --dry-run) DRY=1 ;;
  *) echo "usage: $(basename "$0") [--dry-run]" >&2; exit 2 ;;
esac

API="http://127.0.0.1:8322"
ROWS_WAIT="${SD_DEPLOY_ROWS_WAIT:-180}"   # seconds to wait for a stamped worker run
LDPKG="github.com/nyaungnicholas-wq/signaldeck/internal/lineage.ldflagsRev"

refuse() { echo "deploy REFUSED: $*" >&2; exit 1; }
unverified() {
  echo "deploy UNVERIFIED: $*" >&2
  echo "  Do not treat this as deployed. Roll back: mv -f $REPO/bin/signaldeckd.prev $REPO/bin/signaldeckd && systemctl restart signaldeck-daemon.service" >&2
  exit 1
}

[ "$(uname -s)" = Linux ] || refuse "this is the Linux path; on Windows use ops/signaldeck-ctl.sh deploy"
command -v systemctl >/dev/null 2>&1 || refuse "systemctl not found; this script drives the units in ops/cloud/systemd"
[ "$(id -u)" -ne 0 ] || refuse "run as the service user (sudo -u signaldeck ...), never root: root-owned bin/ and caches break the units"
command -v golangci-lint >/dev/null 2>&1 || refuse "golangci-lint is not installed; CI's daemon job runs it (README, provisioning)"
cd "$REPO" || refuse "cannot cd to $REPO"

# 1. clean tree + manifest
dirty="$(sd_dirty_excluding_generated "$REPO")"
if [ -n "$dirty" ]; then
  printf '%s\n' "$dirty" | head -20 >&2
  refuse "working tree is not clean. Commit or stash first - a running daemon must be reproducible from a commit."
fi
bash "$REPO/ops/manifest-check.sh" >&2 || refuse "load-bearing paths are missing from git (ops/manifest-check.sh)"
rev="$(git rev-parse HEAD)" || refuse "git cannot name HEAD"

# 2. the commit, extracted
tmp="$(mktemp -d)"
lock="$REPO/ops/.maintenance"
took_lock=0
cleanup() { rm -rf "$tmp"; [ "$took_lock" -eq 1 ] && rm -f "$lock"; }
trap cleanup EXIT
mkdir -p "$tmp/src" "$tmp/isolated-root"
git archive HEAD | tar -x -C "$tmp/src" || refuse "git archive HEAD failed"
echo "deploy: HEAD=$rev extracted to a temp dir; every check below runs on the commit" >&2

# 3. the checks CI's daemon job runs. SIGNALDECK_ROOT isolates the tests from
# this checkout's daemon/.env (internal/config: rung 1 is obeyed verbatim).
(cd "$tmp/src/daemon" && go vet ./...) >&2 || refuse "go vet failed"
(cd "$tmp/src/daemon" && golangci-lint run ./...) >&2 || refuse "golangci-lint failed - CI's daemon job would go red"
(cd "$tmp/src/daemon" && SIGNALDECK_ROOT="$tmp/isolated-root" go test -timeout 40m ./...) >&2 || refuse "daemon tests failed"

# 4. build + publication posture (before anything is stopped)
(cd "$tmp/src/daemon" && CGO_ENABLED=0 go build -ldflags "-X $LDPKG=$rev" -o "$tmp/signaldeckd" ./cmd/signaldeckd) >&2 \
  || refuse "build from the extracted commit failed"
sd_check_publication_posture "$tmp/signaldeckd" "$REPO/daemon" "$(sd_tunnel_running)" >&2 || exit 1

if [ "$DRY" -eq 1 ]; then
  echo "deploy DRY RUN: every gate passed for $rev. Nothing was installed or restarted (would install $REPO/bin/signaldeckd and restart signaldeck-daemon.service)."
  exit 0
fi

# 5. install + restart. The lock tells ops/cloud/daemon-guard.sh to stand down.
# Someone else's lock (the market-close backup, a web release) means the daemon
# is down or being swapped on purpose: restarting it now would wreck that job.
[ -e "$lock" ] && refuse "$lock exists - another maintenance job is running (market-close backup, web release). Retry after it finishes; delete the file only if you know it is stale."
: > "$lock"; took_lock=1
mkdir -p "$REPO/bin"
sd_install_binary "$tmp/signaldeckd" "$REPO/bin/signaldeckd" || refuse "install failed; the previous binary is untouched or at bin/signaldeckd.prev"
sd_svc_restart com.signaldeck.daemon || unverified "systemctl could not restart signaldeck-daemon.service (journalctl -u signaldeck-daemon)"
rm -f "$lock"; took_lock=0

# 6. the running process is the commit
for _ in $(seq 1 60); do
  curl -sf -o /dev/null --max-time 2 "$API/api/health" && break
  sleep 1
done
SD_TOKEN="$(grep -m1 '^SIGNALDECK_API_TOKEN=' "$REPO/daemon/.env" 2>/dev/null | cut -d= -f2-)"
SD_AUTH=()
[ -n "$SD_TOKEN" ] && SD_AUTH=(-H "Authorization: Bearer $SD_TOKEN")
ver="$(curl -sf --max-time 5 -H 'X-Signaldeck: 1' ${SD_AUTH[@]+"${SD_AUTH[@]}"} "$API/api/version")" \
  || unverified "/api/version did not answer"
got="$(printf '%s' "$ver" | sed -n 's/.*"revision"[ ]*:[ ]*"\([^"]*\)".*/\1/p')"
resolvable="$(printf '%s' "$ver" | sed -n 's/.*"resolvable"[ ]*:[ ]*\([a-z]*\).*/\1/p')"
if [ "$got" != "$rev" ] || [ "$resolvable" != "true" ]; then
  unverified "running revision '$got' (resolvable=$resolvable) does not match the deployed commit '$rev'"
fi

# 7. rows, not just the process (CLAUDE.md pre-flight)
py="$(sd_py)"
[ -n "$py" ] || unverified "no working python to read worker_runs"
stamped=0
deadline=$((SECONDS + ROWS_WAIT))
while :; do
  stamped="$("$py" -c '
import os, sqlite3, sys, urllib.parse
uri = "file:" + urllib.parse.quote(os.path.abspath(sys.argv[1])) + "?mode=ro"
con = sqlite3.connect(uri, uri=True, timeout=30)
print(con.execute("select count(*) from worker_runs where revision=? and finished_at is not null", (sys.argv[2],)).fetchone()[0])
' "$REPO/data/signaldeck.db" "$rev" 2>/dev/null)"
  [ "${stamped:-0}" -gt 0 ] 2>/dev/null && break
  [ "$SECONDS" -ge "$deadline" ] && unverified "no finished worker_runs row stamped $rev within ${ROWS_WAIT}s"
  sleep 2
done
echo "deploy VERIFIED: daemon is running commit $rev (resolvable); $stamped worker run(s) already stamped with it."
