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

# POSIX TZ STRING, NOT AN IANA ZONE NAME — and then CHECKED.
#
# The Git Bash these tasks run under (C:\Program Files\Git\bin\bash.exe) ships
# NO tz database: `ls /usr/share/zoneinfo` does not exist. An unresolvable TZ
# does not error, it silently falls back to UTC — so `TZ=America/Los_Angeles`
# made every comparison below run seven or eight hours ahead of the window it
# was written for.
#
# Measured 2026-08-11 at the real 06:20 PT trigger instant:
#   TZ=America/Los_Angeles  -> hm=1320  -> `hm >= 1310` -> exit 0, NO COLLECTION
#   TZ=PST8PDT,M3.2.0/2,... -> hm=0620  -> collect
# The guard whose entire job is the window check was rejecting every scheduled
# fire, every weekday, and exiting 0 while doing it. Task Scheduler recorded
# 0x00000000 throughout. The window it DID accept was 23:20-06:10 PT.
#
# The POSIX form needs no database and carries the US DST rules with it
# (verified: -0800 in January, -0700 in July).
export TZ='PST8PDT,M3.2.0/2,M11.1.0/2'

# REFUSE RATHER THAN GUESS. A silent UTC fallback is exactly what hid the bug
# above, so if the offset is not Pacific, say so and exit non-zero — a task that
# fails loudly gets fixed; one that exits 0 does not.
off="$(date +%z)"
if [ "$off" != "-0700" ] && [ "$off" != "-0800" ]; then
    echo "market-open-guard: TZ did not resolve to Pacific (offset $off) — refusing to guess at the collection window" >&2
    exit 3
fi

dow="$(date +%u)"            # 1=Mon .. 7=Sun
hm="$(date +%H%M)"           # e.g. 0725
if [ "$dow" -ge 6 ]; then exit 0; fi
if [ "$hm" -lt 0620 ] || [ "$hm" -ge 1310 ]; then exit 0; fi

exec /bin/bash "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/signaldeck-ctl.sh" collect
