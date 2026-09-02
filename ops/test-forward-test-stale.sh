#!/usr/bin/env bash
# Runs the selftest embedded in ops/lib-forward-test.sh (honest forward-test STALE wording).
exec bash "$(dirname "$0")/lib-forward-test.sh" --selftest
