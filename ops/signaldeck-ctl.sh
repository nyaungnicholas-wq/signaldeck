#!/bin/bash
# SignalDeck control — start/stop the stack on demand or on the market-hours schedule.
#
#   signaldeck-ctl.sh up       full stack (daemon + web, tunnel if registered), opens the dashboard
#   signaldeck-ctl.sh collect  data collection only (daemon, tunnel if registered) — market-open trigger uses this
#
# The tunnel is OPTIONAL and currently OFF by choice (Nicholas, 2026-08-11): it
# exists only to carry TradingView webhooks, it is not registered on this
# machine, and its absence is reported as a note rather than an error. See
# ops/TUNNEL_RESTORE_RUNBOOK.md to turn it back on. Any service that IS
# registered and fails to start makes `up`/`collect` exit non-zero.
#   signaldeck-ctl.sh stop     stop everything — market-close trigger uses this
#   signaldeck-ctl.sh status   show what is running
#   signaldeck-ctl.sh deploy   THE ONLY sanctioned source->running path (see ops/GO-LIVE.md)
#   signaldeck-ctl.sh launch   launchd's program for com.signaldeck.daemon: same preflight, then exec
#
# The service plists (daemon/web/tunnel) are loaded-idle at login (RunAtLoad=false),
# so nothing runs until this script kickstarts it — on the schedule or when you ask.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
DOMAIN="gui/$(id -u)"
LA="$HOME/Library/LaunchAgents"
DAEMON="com.signaldeck.daemon"
TUNNEL="com.signaldeck.tunnel"
WEB="com.signaldeck.web"

# launchd on macOS, Scheduled Tasks on Windows — see ops/lib-portable.sh.
# Without this every start/stop here was a no-op off macOS, which is why
# market-open and market-close could not drive the daemon on this machine.
# shellcheck source=lib-portable.sh
. "$REPO/ops/lib-portable.sh"

# API credential for this script's own calls.
#
# These curls used to be anonymous, which only worked because PublicReads
# defaulted OPEN. It no longer does: daemon/.env allowlists a public ngrok
# hostname, and tunnelConfigured() now reads that allowlist as proof of
# publication (config.go), so reads are closed by default and `up` / `deploy`
# would 401 without this. Same pattern as ops/anchor-publish.sh.
# ${SD_AUTH[@]+...} keeps `set -u` happy on bash 3.2.
SD_TOKEN=$(grep -m1 '^SIGNALDECK_API_TOKEN=' "$REPO/daemon/.env" 2>/dev/null | cut -d= -f2-)
SD_AUTH=()
[ -n "$SD_TOKEN" ] && SD_AUTH=(-H "Authorization: Bearer $SD_TOKEN")

# kick SERVICE [optional] — idempotent start that REPORTS what happened.
#
# START_FAILURES is what makes `up`/`collect` able to fail. Before this, every
# arm called kick and then printed a fixed success string, so a service that was
# not registered at all (the tunnel, measured 2026-08-11) produced the same
# output as a clean start.
#
# "optional" marks a service whose ABSENCE is a deliberate configuration, not a
# fault. The tunnel is off by choice (Nicholas, 2026-08-11 — see
# ops/TUNNEL_RESTORE_RUNBOOK.md); treating its absence as an error would make
# every market-open run red, which is the fastest way to make this output
# unreadable. An optional service that IS registered and then fails to start is
# still a real failure — absent and broken are different things.
START_FAILURES=0
kick() {
  local svc="$1" opt="${2:-}" rc=0
  sd_svc_start "$svc" || rc=$?
  case "$rc" in
    0) return 0 ;;
    2)
      if [ -n "$opt" ]; then
        echo "  note: $svc is not registered on this machine — skipping (optional)"
        return 0
      fi
      echo "  ERROR: $svc is not registered on this machine — cannot start it" >&2
      START_FAILURES=$((START_FAILURES + 1))
      return 1
      ;;
    *)
      echo "  ERROR: $svc is registered but failed to start" >&2
      START_FAILURES=$((START_FAILURES + 1))
      return 1
      ;;
  esac
}

# report_starts MESSAGE — print the success line only when nothing failed, and
# exit non-zero otherwise so a scheduled caller (market-open-guard.sh) sees it.
report_starts() {
  if [ "$START_FAILURES" -gt 0 ]; then
    echo "SignalDeck start INCOMPLETE — $START_FAILURES service(s) did not start" >&2
    return 1
  fi
  echo "$1"
}

stopsvc() { sd_svc_stop "$1"; }

# build_from_head — THE single implementation of the provenance-preserving
# build, shared by `deploy` (interactive) and `launch` (the launchd program).
# Neither path can drift from the other because neither has its own copy.
# Refuses (non-zero) rather than producing a binary of unknown provenance:
#   clean tree -> manifest check -> `git archive HEAD` extract -> build with the
#   commit stamped via -ldflags -> install to bin/signaldeckd.
# Prints nothing on success except progress; all output goes to stderr so the
# caller can exec the result.
build_from_head() {
  cd "$REPO" || return 2
  local dirty rev tmp LDPKG
  # The tree must be clean EXCEPT for the nightly-regenerated docs listed in
  # ops/generated-docs.txt. Those cannot change the binary: the build extracts
  # `git archive HEAD` (the COMMIT, never the working tree), and no Go source
  # embeds a doc (the only go:embed targets are schema.sql and result.json).
  # The refusal below protects operator INTENT — "the edit I just made got
  # deployed" — and a machine-regenerated doc carries no operator intent to
  # protect. Everything else still refuses; a missing or unreadable allowlist
  # exempts NOTHING; and porcelain lines the exact-match parser cannot claim
  # (renames "R old -> new", quoted paths) fail CLOSED by never matching.
  dirty="$(git status --porcelain | awk -v listfile="$REPO/ops/generated-docs.txt" '
    BEGIN {
      n = 0
      while ((getline line < listfile) > 0) {
        sub(/\r$/, "", line)
        if (line ~ /^[ \t]*(#|$)/) continue
        allow[n++] = line
      }
      close(listfile)
    }
    {
      path = substr($0, 4)
      for (i = 0; i < n; i++) if (allow[i] == path) next
      print
    }')"
  if [ -n "$dirty" ]; then
    echo "REFUSED: working tree is not clean." >&2
    printf '%s\n' "$dirty" | head -20 >&2
    echo "Commit or stash first — a running daemon must be reproducible from a commit." >&2
    echo "(nightly-generated docs from ops/generated-docs.txt are exempt and not counted above)" >&2
    return 1
  fi
  if [ -n "$(git status --porcelain)" ]; then
    echo "NOTE: proceeding past uncommitted NIGHTLY-GENERATED docs (ops/generated-docs.txt):" >&2
    git status --porcelain | head -12 >&2
    echo "The binary builds from git archive HEAD; none of these paths can reach it." >&2
    echo "Commit them when convenient — a HAND-built binary still stamps +dirty and refuses to start." >&2
  fi
  if ! /bin/bash "$REPO/ops/manifest-check.sh" >&2; then
    echo "REFUSED: load-bearing paths are missing from git." >&2
    return 1
  fi
  rev="$(git rev-parse HEAD)"
  tmp="$(mktemp -d)"
  git archive HEAD | tar -x -C "$tmp" || { echo "REFUSED: git archive failed." >&2; rm -rf "$tmp"; return 1; }
  echo "build: signaldeckd from the extracted commit $rev (not the working tree)" >&2
  # git archive drops .git, so the toolchain can embed no vcs.revision. Stamp
  # the commit we extracted explicitly — the tree IS that commit by
  # construction. lineage only honours this when no vcs stamp is present.
  LDPKG="github.com/nyaungnicholas-wq/signaldeck/internal/lineage.ldflagsRev"
  # Windows needs the .exe suffix: the Scheduled Task launches bin/signaldeckd.exe,
  # and a deploy that wrote an extensionless bin/signaldeckd left the task still
  # pointing at the OLD binary — a deploy that reports success and changes
  # nothing is worse than one that fails.
  local exe=""
  case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe=".exe" ;; esac
  if ! (cd "$tmp/daemon" && PATH="$PATH:$HOME/.local/go-sdk/go/bin" \
          go build -ldflags "-X $LDPKG=$rev" -o "$tmp/signaldeckd$exe" ./cmd/signaldeckd) >&2; then
    echo "REFUSED: build from the extracted commit failed." >&2
    rm -rf "$tmp"; return 1
  fi
  mkdir -p "$REPO/bin"
  # A running daemon holds its own image open on Windows, so install(1) fails
  # with "File exists" (Unix silently replaces the inode instead). Stop first,
  # and move the old binary aside rather than deleting it so a failed install
  # leaves something to roll back to.
  # ops/daemon-guard.ps1 restarts the daemon whenever it finds it down, every
  # 5 minutes. That is what keeps a stopped daemon from staying stopped all
  # day, but it also races THIS function: the guard can re-launch the daemon
  # between the stop below and the install, and Windows then fails the install
  # with "File exists" because the running image is held open. Hold the same
  # maintenance lock the backup uses so the guard stands down for the swap.
  local lock="$REPO/ops/.maintenance"
  : > "$lock"
  if [ -n "$exe" ] && sd_is_running signaldeckd; then
    sd_svc_stop com.signaldeck.daemon
    for _ in $(seq 1 20); do sd_is_running signaldeckd || break; sleep 1; done
    sd_is_running signaldeckd && sd_kill_hard signaldeckd
    sleep 1
  fi
  [ -f "$REPO/bin/signaldeckd$exe" ] && mv -f "$REPO/bin/signaldeckd$exe" "$REPO/bin/signaldeckd$exe.prev"
  install -m 755 "$tmp/signaldeckd$exe" "$REPO/bin/signaldeckd$exe" || { rm -f "$lock"; rm -rf "$tmp"; return 1; }
  rm -f "$lock"
  rm -rf "$tmp"
  BUILT_REV="$rev"
  return 0
}
running() {
  if command -v launchctl >/dev/null 2>&1; then
    launchctl print "$DOMAIN/$1" 2>/dev/null | awk -F'= ' '/[^a-z]pid = /{print $2; exit}'
    return
  fi
  # Windows: report the daemon's own pid rather than the task's, since that is
  # what every caller here actually wants to know.
  case "$1" in
    *.daemon) sd_is_running signaldeckd && echo "up" ;;
    # The PORT, not the process name. `sd_is_running node` matched any node on
    # the box — see sd_port_listening in lib-portable.sh for the measurement.
    *.web)    sd_port_listening 8323 && echo "up" ;;
    *)        schtasks //Query //TN "$(sd_task_name "$1")" 2>/dev/null | grep -qi running && echo "up" ;;
  esac
}

case "${1:-status}" in
  up)
    kick "$DAEMON"; kick "$TUNNEL" optional; kick "$WEB"
    report_starts "SignalDeck up — daemon :8322, web :8323. Dashboard: http://127.0.0.1:8323" || exit 1
    # Wait for the daemon to answer, then pre-warm the dashboard cache before
    # opening the browser — the first cold /api/dashboard build after boot can
    # take 20-60s while workers catch up; warming here means the page you see
    # loads from cache in ~40ms. Cap the wait so the browser always opens.
    echo "warming dashboard cache (first build after boot is the slow one)..."
    for _ in $(seq 1 30); do
      curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8322/api/health && break
      sleep 1
    done
    curl -sf -o /dev/null --max-time 120 -H "X-Signaldeck: 1" ${SD_AUTH[@]+"${SD_AUTH[@]}"} http://127.0.0.1:8322/api/dashboard || true
    open "http://127.0.0.1:8323" 2>/dev/null || true
    ;;
  collect)
    kick "$DAEMON"; kick "$TUNNEL" optional
    report_starts "SignalDeck collecting — daemon up (no web UI)" || exit 1
    ;;
  stop)
    stopsvc "$WEB"; stopsvc "$TUNNEL"; stopsvc "$DAEMON"
    echo "SignalDeck stopped"
    ;;
  deploy)
    # THE ONLY SANCTIONED PATH FROM SOURCE TO RUNNING SYSTEM.
    #
    # The defect this closes: the running binary was built from whatever the
    # working tree happened to contain, so every sampling-correctness mechanism
    # credited in review could be — and was — absent from the process actually
    # producing rows. A review then measures an artifact rather than the system.
    #
    # Order matters; each step can only REFUSE, never soften a result:
    #   1. clean tree      — a dirty tree means the source under review is not
    #                        the source anyone else can obtain.
    #   2. tests pass      — no deploy of a build whose own tests fail.
    #   3. build from      — `git archive HEAD` into a temp dir. This is the
    #      the COMMIT        step that makes the claim "the binary is the commit"
    #                        true by construction rather than by assertion.
    #   4. restart
    #   5. verify          — GET /api/version and require the running revision
    #                        to equal the commit just built AND resolvable=true
    #                        (embedded vcs stamp present and not dirty).
    # Failure at any step exits non-zero and prints why; success is only
    # reported after step 5 agrees.
    cd "$REPO" || exit 2
    rev="$(git rev-parse HEAD)"
    echo "deploy: HEAD=$rev — running daemon tests"
    if ! (cd "$REPO/daemon" && PATH="$PATH:$HOME/.local/go-sdk/go/bin" go test ./...); then
      echo "deploy REFUSED: daemon tests failed."
      exit 1
    fi
    BUILT_REV=""
    build_from_head || { echo "deploy REFUSED (see above)."; exit 1; }
    rev="$BUILT_REV"
    # -k means "kill first": deploy must land on the NEW binary, so restart
    # rather than merely start an already-running daemon.
    sd_svc_restart "$DAEMON"
    for _ in $(seq 1 60); do
      curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8322/api/health && break
      sleep 1
    done
    ver="$(curl -sf --max-time 5 -H 'X-Signaldeck: 1' ${SD_AUTH[@]+"${SD_AUTH[@]}"} http://127.0.0.1:8322/api/version)" || {
      echo "deploy UNVERIFIED: /api/version did not answer. Do not treat this as deployed."
      exit 1
    }
    got="$(printf '%s' "$ver" | sed -n 's/.*"revision"[ ]*:[ ]*"\([^"]*\)".*/\1/p')"
    resolvable="$(printf '%s' "$ver" | sed -n 's/.*"resolvable"[ ]*:[ ]*\([a-z]*\).*/\1/p')"
    if [ "$got" != "$rev" ] || [ "$resolvable" != "true" ]; then
      echo "deploy UNVERIFIED: running revision '$got' (resolvable=$resolvable)"
      echo "                   does not match the deployed commit '$rev'."
      echo "$ver"
      exit 1
    fi
    echo "deploy VERIFIED: daemon is running commit $rev (resolvable)."
    ;;
  launch)
    # THE launchd program for com.signaldeck.daemon. launchd used to exec
    # bin/signaldeckd directly, which meant the production start path had no
    # provenance gate at all — whatever binary happened to be on disk (built
    # from a dirty tree, `vcs.modified=true`) collected rows the grader then
    # refuses as unresolvable. This routes launchd THROUGH the same preflight
    # deploy uses instead of around it, and exits non-zero rather than exec'ing
    # when any step refuses. The daemon's own startup check is the last line of
    # defence; this one stops an unattributable binary from ever being written.
    BUILT_REV=""
    build_from_head || { echo "launch REFUSED: not starting an unattributable build." >&2; exit 1; }
    echo "launch: exec signaldeckd built from commit $BUILT_REV" >&2
    exe=""
    case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe=".exe" ;; esac
    exec "$REPO/bin/signaldeckd$exe"
    ;;
  refresh)
    # run the daily full-universe refresh sweep now (ignores the once-per-day guard)
    exec /bin/bash "$REPO/ops/signaldeck-refresh.sh" force
    ;;
  status)
    for s in "$DAEMON" "$TUNNEL" "$WEB"; do
      if command -v launchctl >/dev/null 2>&1; then
        if launchctl print "$DOMAIN/$s" >/dev/null 2>&1; then
          p="$(running "$s")"
          [ -n "$p" ] && echo "$s: running (pid $p)" || echo "$s: loaded-idle (stopped)"
        else
          echo "$s: not loaded"
        fi
      elif schtasks //Query //TN "$(sd_task_name "$s")" >/dev/null 2>&1; then
        [ -n "$(running "$s")" ] && echo "$s: running" || echo "$s: registered-idle (stopped)"
      elif [ "$s" = "$TUNNEL" ]; then
        # OFF BY CHOICE (Nicholas, 2026-08-11), not a misconfiguration. The old
        # message pointed at install-windows-tasks.ps1, which SKIPS this task by
        # design (it requires a .sh in ProgramArguments and the plist names the
        # ngrok binary directly), so following that advice changed nothing and
        # made a deliberate state look broken.
        echo "$s: not registered — OFF by choice (webhooks only; ops/TUNNEL_RESTORE_RUNBOOK.md to enable)"
      else
        echo "$s: no scheduled task — run ops/install-windows-tasks.ps1"
      fi
    done
    ;;
  *) echo "usage: $(basename "$0") {up|collect|stop|status|refresh|deploy|launch}"; exit 1;;
esac
