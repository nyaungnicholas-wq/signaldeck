#!/usr/bin/env bash
# Runs the selftest embedded in ops/lib-offsite-env.sh (daemon/.env as the single source of the offsite target).
exec bash "$(dirname "$0")/lib-offsite-env.sh" --selftest
