# Holds a SYSTEM wake lock until this process is killed.
#
# ASCII ONLY. Task Scheduler and lib-portable.sh both invoke Windows PowerShell
# 5.1, which reads .ps1 as ANSI; one non-ASCII character makes the file a parse
# error.
#
# WHY THIS EXISTS. sd_nosleep wrapped the offline backup, the VACUUM INTO and
# both compressors in `caffeinate -i` on macOS so the machine could not sleep
# mid-write. On Windows caffeinate is not on PATH, so sd_nosleep fell through to
# running the command bare and the inhibition silently became nothing. Measured
# 2026-09-19: this machine's AC standby-idle is 600 seconds and `powercfg
# /requests` showed NO power request held by anything - so a backup that takes
# longer than the idle timeout could be suspended half-written, and the only
# evidence would be a backup that looks finished because the task exited 0.
#
# ES_CONTINUOUS (0x80000000) makes the state persist until this thread changes
# it or the process exits, and ES_SYSTEM_REQUIRED (0x00000001) is the part that
# keeps the SYSTEM awake. Deliberately NOT ES_DISPLAY_REQUIRED: the screen may
# sleep, the machine may not. Killing this process releases the lock, so the
# caller does not need to unwind anything if it dies.
$ErrorActionPreference = 'Stop'

Add-Type -Name NoSleep -Namespace SignalDeck -MemberDefinition @'
[DllImport("kernel32.dll", SetLastError = true)]
public static extern uint SetThreadExecutionState(uint esFlags);
'@

$ES_CONTINUOUS      = [uint32]'0x80000000'
$ES_SYSTEM_REQUIRED = [uint32]'0x00000001'

$prev = [SignalDeck.NoSleep]::SetThreadExecutionState($ES_CONTINUOUS -bor $ES_SYSTEM_REQUIRED)
if ($prev -eq 0) {
    Write-Error 'SetThreadExecutionState failed; sleep is NOT inhibited'
    exit 1
}
Write-Output 'NOSLEEP HELD'

# Idle until killed. The lock lives for the life of this process.
while ($true) { Start-Sleep -Seconds 3600 }
