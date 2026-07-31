#!/bin/bash
# Market-hours guard for com.signaldeck.market-open.
#
# Runs at the 06:20 PT calendar trigger AND at login (RunAtLoad=true, added
# 2026-07-24): launchd's StartCalendarInterval catches jobs missed during
# SLEEP but not across a REBOOT — a login after 06:20 used to leave the stack
# dead all day. This guard starts collection only when it's actually a
# weekday inside the collection window (06:20–13:10 PT), so the login-time
# run is a no-op at night and on weekends.
set -u
export TZ=America/Los_Angeles

dow="$(date +%u)"            # 1=Mon .. 7=Sun
hm="$(date +%H%M)"           # e.g. 0725
if [ "$dow" -ge 6 ]; then exit 0; fi
if [ "$hm" -lt 0620 ] || [ "$hm" -ge 1310 ]; then exit 0; fi

exec /bin/bash "/Users/natalienyaung/claude code/signaldeck/ops/signaldeck-ctl.sh" collect
