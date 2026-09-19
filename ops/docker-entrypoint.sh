#!/bin/sh
# Start the daemon, wait for it to actually serve, then start the web app.
#
# Ordering matters and "sleep 5" does not cut it: on a cold volume the daemon
# creates 97 tables and backfills bars before it listens, which takes minutes.
# A web app that starts first proxies /api/* into a refused connection and
# renders an empty dashboard that looks like a data problem rather than a
# startup race.
set -eu

# THIS CONTAINER IS PUBLICATION, AND THE DAEMON CANNOT SEE THAT BY ITSELF.
#
# config.reachablePrivately() reads the DAEMON's bind address, which here is
# deliberately 127.0.0.1:8322 (fly.toml) so the daemon port is never exposed —
# while the web tier below binds 0.0.0.0 and Fly/Render publish it to the
# internet. So the heuristic evaluated "private" on exactly the deployment that
# is public: the same shape config.go's own comment says the tunnel case was
# fixed to prevent, arriving by a different route.
#
# What it silently opened, all three of which nobody chose:
#   * SIGNALDECK_OPEN_SIGNUP defaults to `private` -> registration open to the
#     internet (api/security.go asserts it "is false on any published
#     deployment"; on this topology it was true).
#   * SIGNALDECK_PUBLIC_READS defaults to `private` -> anonymous reads.
#   * ReachablePrivately() gates the datalicense 451 guard, so licensed vendor
#     bars became redistributable — the legal exposure SHIP_READINESS.md calls
#     "the serious one".
#
# ASSUME_TUNNEL is the supported, already-documented override for "serving a
# host you cannot reach from loopback IS publication". Set BEFORE the daemon
# starts, because config is read once at boot. An operator who genuinely wants
# anonymous reads still says so explicitly in fly.toml; this only stops the
# default from being decided by a bind address that describes the wrong tier.
export SIGNALDECK_ASSUME_TUNNEL=1

echo "signaldeck: starting daemon"
signaldeckd &
DAEMON_PID=$!

# If the daemon refuses to start — unattributable build, uncontracted schema —
# it exits immediately and we must not sit here waiting out the full timeout.
i=0
until wget -q -O /dev/null "http://127.0.0.1:8322/api/health" 2>/dev/null; do
  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    echo "signaldeck: daemon exited during startup — see its log above" >&2
    wait "$DAEMON_PID" || true
    exit 1
  fi
  i=$((i + 1))
  if [ "$i" -gt 600 ]; then           # 10 minutes: cold-volume backfill is slow
    echo "signaldeck: daemon never became healthy after 600s" >&2
    exit 1
  fi
  sleep 1
done
echo "signaldeck: daemon healthy after ${i}s"

# Readiness is stricter than health: it also fails when workers were refused at
# boot or credentials are absent. Report it, but do not block the demo on it —
# a daemon serving stale-but-real data is still worth showing.
if wget -q -O /dev/null "http://127.0.0.1:8322/api/ready" 2>/dev/null; then
  echo "signaldeck: daemon READY"
else
  echo "signaldeck: daemon is healthy but NOT ready — see /api/ready for reasons" >&2
fi

# THE GRADER'S SCHEDULE, which the image shipped without.
#
# ops/grade.sh was COPYed into the image and nothing ever called it. fly.toml
# installs no schedule either, so DEPLOY.md's instruction to cron a host-side
# `docker exec` was the only thing standing between a deployment and a
# permanently refused honesty page — a prerequisite living outside the artifact
# that claims to be self-contained. /api/accuracy is fail-closed on both an
# unreadable registry and a grader heartbeat older than GraderMaxAge (26h), so
# on a fresh volume the page never worked and on a seeded one it stopped
# working a day later, with /api/health still answering 200 throughout.
#
# Six hours gives four attempts inside that 26h window, so one refusal or one
# restart cannot expire the heartbeat on its own. grade.sh writes its own log
# and its own success/failure heartbeat; this only decides WHEN, and prints one
# line either way so a refusing grader and a grader nobody is running stop
# looking the same from outside.
#
# SIGNALDECK_GRADE_INTERVAL_SEC=0 disables it, for a host that genuinely does
# schedule the container from outside.
GRADE_INTERVAL="${SIGNALDECK_GRADE_INTERVAL_SEC:-21600}"
# A non-numeric value would make the comparison below fail under `set -e` and
# take the whole container down over a typo in an env var, so it falls back to
# the default and says so instead.
case "$GRADE_INTERVAL" in
  ''|*[!0-9]*)
    echo "signaldeck: SIGNALDECK_GRADE_INTERVAL_SEC='${GRADE_INTERVAL}' is not a number of seconds; using 21600" >&2
    GRADE_INTERVAL=21600
    ;;
esac
if [ "$GRADE_INTERVAL" -gt 0 ]; then
  echo "signaldeck: grading every ${GRADE_INTERVAL}s, first run now"
  (
    while :; do
      if /usr/local/bin/grade.sh >/dev/null 2>&1; then
        echo "signaldeck: grade OK"
      else
        # NOT an error about the model. grade.sh refuses on its own gates and
        # records why; this line only says a run finished without publishing.
        echo "signaldeck: grade did not publish — see /data/logs/grade.log" >&2
      fi
      sleep "$GRADE_INTERVAL"
    done
  ) &
else
  echo "signaldeck: in-container grading disabled (SIGNALDECK_GRADE_INTERVAL_SEC=0)" >&2
fi

cd /app/web
echo "signaldeck: starting web on ${PORT}"
exec npx next start -p "${PORT}" -H 0.0.0.0
