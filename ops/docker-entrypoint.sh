#!/bin/sh
# Start the daemon, wait for it to actually serve, then start the web app.
#
# Ordering matters and "sleep 5" does not cut it: on a cold volume the daemon
# creates 97 tables and backfills bars before it listens, which takes minutes.
# A web app that starts first proxies /api/* into a refused connection and
# renders an empty dashboard that looks like a data problem rather than a
# startup race.
set -eu

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

cd /app/web
echo "signaldeck: starting web on ${PORT}"
exec npx next start -p "${PORT}" -H 0.0.0.0
