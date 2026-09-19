param(
    [string]$OutFile = '',
    [string]$Prefix = 'SignalDeck',
    [switch]$SelfCheck
)
function ConvertTo-Psd1String([string]$s) {
    if ($null -eq $s) { $s = '' }
    return "'" + $s.Replace("'", "''") + "'"
}
function New-Psd1Document([object[]]$tasks, [string]$prefix) {
    $lines = New-Object System.Collections.Generic.List[string]
    $lines.Add('@{')
    # Real UTC with a Z. Get-Date -Format o returns LOCAL time with an offset,
    # which is not what a field called GeneratedUtc may say.
    $lines.Add("    GeneratedUtc = '$([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ'))'")
    $lines.Add("    Prefix = $(ConvertTo-Psd1String $prefix)")
    $lines.Add('    Tasks = @(')
    foreach ($task in $tasks) {
        $lines.Add('        @{')
        $lines.Add("            TaskName = $(ConvertTo-Psd1String $task.TaskName)")
        $firstAction = if ($task.Actions -and $task.Actions.Count -gt 0) { $task.Actions[0] } else { $null }
        $execute = if ($firstAction -and $null -ne $firstAction.Execute) { $firstAction.Execute.ToString() } else { '' }
        $lines.Add("            Execute = $(ConvertTo-Psd1String $execute)")
        $arguments = if ($firstAction -and $null -ne $firstAction.Arguments) { $firstAction.Arguments.ToString() } else { '' }
        $lines.Add("            Arguments = $(ConvertTo-Psd1String $arguments)")
        $workingDir = if ($firstAction -and $null -ne $firstAction.WorkingDirectory) { $firstAction.WorkingDirectory.ToString() } else { '' }
        $lines.Add("            WorkingDirectory = $(ConvertTo-Psd1String $workingDir)")
        $etl = if ($null -ne $task.Settings -and $null -ne $task.Settings.ExecutionTimeLimit) { $task.Settings.ExecutionTimeLimit.ToString() } else { '' }
        $lines.Add("            ExecutionTimeLimit = $(ConvertTo-Psd1String $etl)")
        $principal = $task.Principal
        $logonType = if ($principal -and $null -ne $principal.LogonType) { $principal.LogonType.ToString() } else { '' }
        $lines.Add("            LogonType = $(ConvertTo-Psd1String $logonType)")
        $runLevel = if ($principal -and $null -ne $principal.RunLevel) { $principal.RunLevel.ToString() } else { '' }
        $lines.Add("            RunLevel = $(ConvertTo-Psd1String $runLevel)")
        $userId = if ($principal -and $null -ne $principal.UserId) { $principal.UserId.ToString() } else { '' }
        $lines.Add("            UserId = $(ConvertTo-Psd1String $userId)")
        $enabled = if ($null -ne $task.Settings -and $null -ne $task.Settings.Enabled) { [bool]$task.Settings.Enabled } else { $false }
        $enabledToken = if ($enabled) { "`$true" } else { "`$false" }
        $lines.Add("            Enabled = $enabledToken")
        $triggerNames = @()
        if ($task.Triggers) {
            foreach ($trig in $task.Triggers) {
                $triggerNames += $trig.CimClass.CimClassName
            }
        }
        if ($triggerNames.Count -eq 0) {
            $triggersStr = '@()'
        } else {
            $elements = $triggerNames | ForEach-Object { ConvertTo-Psd1String $_ }
            $triggersStr = "@($($elements -join ','))"
        }
        $lines.Add("            Triggers = $triggersStr")
        $lines.Add('        }')
    }
    $lines.Add('    )')
    $lines.Add('}')
    return $lines.ToArray()
}
if ($SelfCheck) {
    function Assert-Equal($actual, $expected, $message) {
        if ($actual -ne $expected) {
            Write-Error "Assertion failed: $message. Expected '$expected', got '$actual'."
            exit 1
        }
    }
    if ((ConvertTo-Psd1String "plain") -ne "'plain'") { Write-Error "Assertion failed: plain string."; exit 1 }
    if ((ConvertTo-Psd1String "it's") -ne "'it''s'") { Write-Error "Assertion failed: apostrophe."; exit 1 }
    if ((ConvertTo-Psd1String "") -ne "''") { Write-Error "Assertion failed: empty string."; exit 1 }
    $fakeTask = [pscustomobject]@{
        TaskName = "Bob's Task"
        State = 'Ready'
        Actions = @(
            [pscustomobject]@{
                Execute = 'sh'
                Arguments = "-lc '" + "/c/path with space/x.sh" + "' >> '" + "/c/logs/y.log" + "' 2>&1"
                WorkingDirectory = '/c'
            }
        )
        Settings = [pscustomobject]@{
            ExecutionTimeLimit = 'PT1H'
            Enabled = $true
        }
        Principal = [pscustomobject]@{
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Bob'
        }
        Triggers = @(
            [pscustomobject]@{
                CimClass = [pscustomobject]@{
                    CimClassName = 'MSFT_TaskDailyTrigger'
                }
            }
        )
    }
    $lines = New-Psd1Document @($fakeTask) "SignalDeck"
    $tempPath = [System.IO.Path]::GetTempFileName()
    try {
        Set-Content -Path $tempPath -Value $lines -Encoding ASCII
        $imported = Import-PowerShellDataFile -Path $tempPath
        $importedTask = $imported.Tasks[0]
        if ($importedTask.Arguments -ne $fakeTask.Actions[0].Arguments) { Write-Error "Assertion failed: Arguments roundtrip."; exit 1 }
        if ($importedTask.TaskName -ne $fakeTask.TaskName) { Write-Error "Assertion failed: TaskName roundtrip."; exit 1 }
        if ($importedTask.Enabled -ne [bool]$fakeTask.Settings.Enabled) { Write-Error "Assertion failed: Enabled roundtrip."; exit 1 }
        if ($importedTask.Triggers.Count -ne $fakeTask.Triggers.Count) { Write-Error "Assertion failed: Triggers count roundtrip."; exit 1 }
    }
    finally {
        if (Test-Path $tempPath) { Remove-Item $tempPath -Force }
    }
    Write-Host "SELFCHECK OK"
    exit 0
}
else {
    if ($OutFile -eq '') { $OutFile = Join-Path (Split-Path -Parent $PSScriptRoot) 'ops\tasks.psd1' }
    $ErrorActionPreference = 'Stop'
    try {
        $tasks = Get-ScheduledTask -TaskName "$Prefix*" | Sort-Object TaskName
        $lines = New-Psd1Document $tasks $Prefix
        Set-Content -Path $OutFile -Value $lines -Encoding ASCII
        Write-Host "EXPORTED $($tasks.Count) tasks to $OutFile"
    }
    catch {
        Write-Error $_
        exit 1
    }
}