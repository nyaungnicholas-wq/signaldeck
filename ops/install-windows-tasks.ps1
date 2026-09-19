<# 
This script manages Windows Scheduled Tasks for the SignalDeck fleet.
Task definitions are lossless XML files exported by Export-ScheduledTask,
located in ops/tasks/*.xml (one file per task). 
It replaces an earlier version that translated macOS launchd .plist files,
which could not faithfully express Windows task settings.
By default the script only REPORTS what would change; no modifications are made.
To apply changes, run with -Install. Registering a task that uses an S4U principal
requires an elevated PowerShell session; otherwise you will see "Access is denied".
#>

[CmdletBinding()]
param(
    [switch]$Install,
    [switch]$Remove,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$xmlDir = Join-Path $repo 'ops\tasks'

function ConvertTo-ComparableTaskXml([string]$xml, [string]$userToken) {
    # Strip XML declaration (Export-ScheduledTask emits UTF-16 string with declaration)
    $xml = $xml -replace '^\s*<\?xml[^>]*\?>\s*', ''
    # Replace machine-specific SID with account name for comparison
    $xml = $xml -replace '(<UserId>)S-1-[^<]*(</UserId>)', ('$1' + $userToken + '$2')
    # Normalise line endings and trim whitespace
    $xml = ($xml -replace "`r`n", "`n").Trim()
    return $xml
}

# Verify task definitions exist
if (-not (Test-Path $xmlDir)) {
    Write-Output "FATAL: no task definitions in $xmlDir. Run ops\export-tasks.ps1 first."
    exit 1
}
$xmlFiles = Get-ChildItem $xmlDir -Filter *.xml | Sort-Object Name
if ($xmlFiles.Count -eq 0) {
    Write-Output "FATAL: no task definitions in $xmlDir. Run ops\export-tasks.ps1 first."
    exit 1
}

$userToken = "$env:USERDOMAIN\$env:USERNAME"
$tasks = @()
foreach ($file in $xmlFiles) {
    $taskName = $file.BaseName
    $raw = Get-Content $file.FullName -Raw
    $want = ConvertTo-ComparableTaskXml $raw $userToken
    $live = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    if ($live -eq $null) {
        $status = 'CREATE'
        $stateText = ''
        $refused = $false
    } else {
        $haveRaw = Export-ScheduledTask -TaskName $taskName
        $have = ConvertTo-ComparableTaskXml $haveRaw $userToken
        if ($have -ceq $want) {
            $status = 'ok'
            $stateText = $live.State
            $refused = $false
        } else {
            $status = 'UPDATE'
            $stateText = $live.State
            $refused = ($live.State -eq 'Running' -and -not $Force)
            if ($refused) {
                Write-Output "REFUSE $taskName differs but is RUNNING; re-run with -Force to replace it"
            }
        }
    }
    $tasks += [pscustomobject]@{
        TaskName = $taskName
        Status   = $status
        State    = $stateText
        WantXml  = $want
        Refused  = $refused
    }
}

if ($Remove) {
    $removed = 0
    foreach ($t in $tasks) {
        if (Get-ScheduledTask -TaskName $t.TaskName -ErrorAction SilentlyContinue) {
            Unregister-ScheduledTask -TaskName $t.TaskName -Confirm:$false
            Write-Output "REMOVED $($t.TaskName)"
            $removed++
        }
    }
    Write-Output "removed $removed task(s)"
    exit 0
}

if (-not $Install) {
    # Reporting mode
    foreach ($t in $tasks) {
        $state = if ($t.State) { $t.State } else { '' }
        $shown = if ($t.Refused) { 'REFUSE' } else { $t.Status }
        Write-Output ("{0,-8} {1,-34} {2}" -f $shown, $t.TaskName, $state)
    }
    # @(...) is load-bearing. In PowerShell 5.1 a Where-Object that matches a
    # single PSCustomObject returns that object, not a one-element array, and
    # $obj.Count is $null - which formats as an EMPTY STRING. Without the array
    # subexpression this summary printed "would install:  new,  changed, 17
    # already correct,  refused": every count that was 0 or 1 silently vanished,
    # and the one number that showed up was the only one nobody needed.
    $newCnt    = @($tasks | Where-Object {$_.Status -eq 'CREATE'}).Count
    $updCnt    = @($tasks | Where-Object {$_.Status -eq 'UPDATE' -and -not $_.Refused}).Count
    $okCnt     = @($tasks | Where-Object {$_.Status -eq 'ok'}).Count
    $refCnt    = @($tasks | Where-Object {$_.Refused}).Count
    Write-Output ("would install: {0} new, {1} changed, {2} already correct, {3} refused" -f $newCnt,$updCnt,$okCnt,$refCnt)
    Write-Output "Nothing was changed. Re-run with -Install to apply."
    exit 0
}

# Install mode
$okCnt  = @($tasks | Where-Object {$_.Status -eq 'ok'}).Count
$refCnt = @($tasks | Where-Object {$_.Refused}).Count
$new = $changed = $failed = 0
foreach ($t in $tasks) {
    if ($t.Status -eq 'CREATE' -or ($t.Status -eq 'UPDATE' -and -not $t.Refused)) {
        try {
            Register-ScheduledTask -TaskName $t.TaskName -Xml $t.WantXml -Force
            # Read-back verification
            $got = Get-ScheduledTask -TaskName $t.TaskName
            # LogonType
            $expectedLogonType = [regex]::Match($t.WantXml,'<LogonType>([^<]*)</LogonType>').Groups[1].Value
            if ($got.Principal.LogonType -ne $expectedLogonType) {
                throw ("LogonType is $($got.Principal.LogonType), expected $expectedLogonType")
            }
            # RunLevel. The Task Scheduler XML schema and the ScheduledTasks
            # cmdlets use DIFFERENT vocabularies for the same two values:
            #   XML <RunLevel>LeastPrivilege</RunLevel>   <-> Principal.RunLevel 'Limited'
            #   XML <RunLevel>HighestAvailable</RunLevel> <-> Principal.RunLevel 'Highest'
            # Only HighestAvailable was translated here, so a definition that
            # spelled LeastPrivilege out explicitly failed its own read-back with
            # "RunLevel is Limited, expected LeastPrivilege" AFTER registering
            # perfectly. The 19 exported tasks hid it because Export-ScheduledTask
            # omits the element entirely at the default level; the first two
            # hand-authored definitions did not.
            if ($t.WantXml -match '<RunLevel>([^<]*)</RunLevel>') {
                $expectedRunLevel = $matches[1]
                switch ($expectedRunLevel) {
                    'HighestAvailable' { $expectedRunLevel = 'Highest' }
                    'LeastPrivilege'   { $expectedRunLevel = 'Limited' }
                }
            } else {
                $expectedRunLevel = 'Limited'
            }
            if ($got.Principal.RunLevel -ne $expectedRunLevel) {
                throw ("RunLevel is $($got.Principal.RunLevel), expected $expectedRunLevel")
            }
            # Trigger count
            $expectedTriggers = [regex]::Matches($t.WantXml,'<(CalendarTrigger|TimeTrigger|LogonTrigger|BootTrigger|RegistrationTrigger|IdleTrigger|EventTrigger|SessionStateChangeTrigger)\b').Count
            $actualTriggers = $got.Triggers.Count
            if ($actualTriggers -ne $expectedTriggers) {
                throw ("Trigger count is $actualTriggers, expected $expectedTriggers")
            }
            # Success
            if ($t.Status -eq 'CREATE') { $new++ } else { $changed++ }
        } catch {
            $msg = $_.Exception.Message
            Write-Output ("FAIL   {0} {1}" -f $t.TaskName, $msg)
            if ($msg -like '*Access is denied*') {
                Write-Output "(registering an S4U task requires an elevated PowerShell)"
            }
            $failed++
        }
    }
}
$okCnt = ($tasks | Where-Object {$_.Status -eq 'ok'}).Count
$refCnt = ($tasks | Where-Object {$_.Refused}).Count
Write-Output ("installed: {0} new, {1} changed, {2} already correct, {3} refused, {4} failed" -f $new,$changed,$okCnt,$refCnt,$failed)
if ($failed -gt 0) { exit 1 } else { exit 0 }