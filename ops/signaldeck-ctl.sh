#!/bin/bash
# SignalDeck control — start/stop the stack on demand or on the market-hours schedule.
#
#   signaldeck-ctl.sh up       full stack (daemon + tunnel + web), opens the dashboard
#   signaldeck-ctl.sh collect  data collection only (daemon + tunnel) — market-open trigger uses this
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

kick() {
  # ensure loaded, then start (idempotent)
  launchctl bootstrap "$DOMAIN" "$LA/$1.plist" 2>/dev/null
  launchctl kickstart "$DOMAIN/$1" 2>/dev/null
}
stopsvc() { launchctl kill TERM "$DOMAIN/$1" 2>/dev/null; }

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
  dirty="$(git status --porcelain | wc -l | tr -d ' ')"
  if [ "$dirty" != "0" ]; then
    echo "REFUSED: working tree is not clean ($dirty path(s))." >&2
    git status --porcelain | head -20 >&2
    echo "Commit or stash first — a running daemon must be reproducible from a commit." >&2
    return 1
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
  if ! (cd "$tmp/daemon" && PATH="$PATH:$HOME/.local/go-sdk/go/bin" \
          go build -ldflags "-X $LDPKG=$rev" -o "$tmp/signaldeckd" ./cmd/signaldeckd) >&2; then
    echo "REFUSED: build from the extracted commit failed." >&2
    rm -rf "$tmp"; return 1
  fi
  mkdir -p "$REPO/bin"
  install -m 755 "$tmp/signaldeckd" "$REPO/bin/signaldeckd" || { rm -rf "$tmp"; return 1; }
  rm -rf "$tmp"
  BUILT_REV="$rev"
  return 0
}
running() { launchctl print "$DOMAIN/$1" 2>/dev/null | awk -F'= ' '/[^a-z]pid = /{print $2; exit}'; }

case "${1:-status}" in
  up)
    kick "$DAEMON"; kick "$TUNNEL"; kick "$WEB"
    echo "SignalDeck up — daemon :8322, web :8323, tunnel. Dashboard: http://127.0.0.1:8323"
    # Wait for the daemon to answer, then pre-warm the dashboard cache before
    # opening the browser — the first cold /api/dashboard build after boot can
    # take 20-60s while workers catch up; warming here means the page you see
    # loads from cache in ~40ms. Cap the wait so the browser always opens.
    echo "warming dashboard cache (first build after boot is the slow one)..."
    for _ in $(seq 1 30); do
      curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8322/api/health && break
      sleep 1
    done
    curl -sf -o /dev/null --max-time 120 -H "X-Signaldeck: 1" http://127.0.0.1:8322/api/dashboard || true
    open "http://127.0.0.1:8323" 2>/dev/null || true
    ;;
  collect)
    kick "$DAEMON"; kick "$TUNNEL"
    echo "SignalDeck collecting — daemon + tunnel up (no web UI)"
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
    kick "$DAEMON"
    launchctl kickstart -k "$DOMAIN/$DAEMON" >/dev/null 2>&1
    for _ in $(seq 1 60); do
      curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8322/api/health && break
      sleep 1
    done
    ver="$(curl -sf --max-time 5 -H 'X-Signaldeck: 1' http://127.0.0.1:8322/api/version)" || {
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
    exec "$REPO/bin/signaldeckd"
    ;;
  refresh)
    # run the daily full-universe refresh sweep now (ignores the once-per-day guard)
    exec /bin/bash "$REPO/ops/signaldeck-refresh.sh" force
    ;;
  status)
    for s in "$DAEMON" "$TUNNEL" "$WEB"; do
      if launchctl print "$DOMAIN/$s" >/dev/null 2>&1; then
        p="$(running "$s")"
        [ -n "$p" ] && echo "$s: running (pid $p)" || echo "$s: loaded-idle (stopped)"
      else
        echo "$s: not loaded"
      fi
    done
    ;;
  *) echo "usage: $(basename "$0") {up|collect|stop|status|refresh|deploy|launch}"; exit 1;;
esac
