param(
    [string]$OutFile = '',
    [string]$Prefix = 'SignalDeck',
    [switch]$SelfCheck,
    [switch]$Prune,
    [string]$XmlDir = ''
)
if ($XmlDir -eq '') {
    $XmlDir = Join-Path (Split-Path -Parent $PSScriptRoot) 'ops\tasks'
}
function ConvertTo-Psd1String([string]$s) {
    if ($null -eq $s) { $s = '' }
    return "'" + $s.Replace("'", "''") + "'"
}
function New-Psd1Document([object[]]$tasks, [string]$prefix) {
    $lines = New-Object System.Collections.Generic.List[string]
    $lines.Add('@{')
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
function ConvertTo-PortableTaskXml([string]$xml, [string]$userToken) {
    # STRIP the XML declaration; do not rewrite it to UTF-8.
    #
    # Export-ScheduledTask emits encoding="UTF-16" because it returns a .NET
    # string, which is UTF-16 in memory. We store the file as UTF-8, so leaving
    # that declaration makes the file lie - but REWRITING it to UTF-8 is worse:
    # Register-ScheduledTask -Xml takes the text back as a UTF-16 string, sees a
    # declaration claiming UTF-8, and refuses with
    #     The task XML is malformed. (1,40)::ERROR: unable to switch the encoding
    # Measured 2026-09-19, both ways round. A declaration-less document is valid
    # XML, registers cleanly, and cannot disagree with the file it is stored in.
    $xml = $xml -replace '^\s*<\?xml[^>]*\?>\s*', ''
    # A raw SID is opaque in a diff and wrong on a rebuilt machine.
    $xml = $xml -replace '(<UserId>)S-1-[^<]*(</UserId>)', ('$1' + $userToken + '$2')
    return $xml
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
    # f
    $inputXml = '<?xml version="1.0" encoding="UTF-16"?><Task><Principals><Principal id="Author"><UserId>S-1-5-21-111-222-333-1001</UserId><LogonType>S4U</LogonType></Principal></Principals></Task>'
    $userToken = 'CONTOSO\alice'
    $result = ConvertTo-PortableTaskXml -xml $inputXml -userToken $userToken
    # The declaration must be GONE, not rewritten. A declaration of any kind is
    # what makes Register-ScheduledTask -Xml refuse the string with
    # "unable to switch the encoding", so this assertion is the registerability
    # of every exported file, not a cosmetic preference.
    if ($result -match '<\?xml') { Write-Error "Assertion f failed: xml declaration survived; Register-ScheduledTask -Xml will reject this"; exit 1 }
    if ($result -match 'UTF-16') { Write-Error "Assertion f failed: still contains UTF-16"; exit 1 }
    if (-not ($result.StartsWith('<Task'))) { Write-Error "Assertion f failed: result must start at <Task"; exit 1 }
    if (-not ($result -match '<UserId>CONTOSO\\alice</UserId>')) { Write-Error "Assertion f failed: UserId not replaced"; exit 1 }
    if ($result -match 'S-1-5-21') { Write-Error "Assertion f failed: SID still present"; exit 1 }
    if (-not ($result -match '<LogonType>S4U</LogonType>')) { Write-Error "Assertion f failed: LogonType missing"; exit 1 }
    try { [xml]$result | Out-Null } catch { Write-Error "Assertion f failed: not valid XML"; exit 1 }
    # g
    $inputXml2 = '<Task><Principals><Principal id="Author"><UserId>DOMAIN\bob</UserId></Principal></Principals></Task>'
    $result2 = ConvertTo-PortableTaskXml -xml $inputXml2 -userToken 'CONTOSO\alice'
    if (-not ($result2 -match '<UserId>DOMAIN\\bob</UserId>')) { Write-Error "Assertion g failed: UserId changed"; exit 1 }
    if ($result2 -match 'CONTOSO') { Write-Error "Assertion g failed: token appeared"; exit 1 }
    # h
    $result3 = ConvertTo-PortableTaskXml -xml $inputXml -userToken $userToken
    $result3again = ConvertTo-PortableTaskXml -xml $result3 -userToken $userToken
    if ($result3 -ne $result3again) { Write-Error "Assertion h failed: not idempotent"; exit 1 }
    Write-Host "SELFCHECK OK"
    exit 0
}
else {
    if ($OutFile -eq '') { $OutFile = Join-Path (Split-Path -Parent $PSScriptRoot) 'ops\tasks.psd1' }
    $ErrorActionPreference = 'Stop'
    try {
        $tasks = Get-ScheduledTask -TaskName "$Prefix*" | Sort-Object TaskName
        if (-not (Test-Path $XmlDir)) { New-Item -ItemType Directory -Path $XmlDir | Out-Null }
        # DO NOT blanket-delete the definitions here.
        #
        # This used to wipe every *.xml before re-exporting, so that a task
        # removed from the fleet did not leave a stale file. But a file with no
        # LIVE task is not necessarily stale - it is also how a PENDING task is
        # staged, which is exactly what 'SignalDeck Tunnel' and 'SignalDeck
        # Tunnel Keepalive' are while ngrok has no authtoken. Running the export
        # would have silently deleted both hand-authored definitions and the
        # only symptom would be an installer that suddenly had nothing to do.
        # -Prune restores the old behaviour when an orphan really should go.
        if ($Prune) {
            # A definition with no live task is NOT necessarily an orphan - it is
            # also how a task is staged ahead of its install. Measured 2026-09-19:
            # the first -Prune deleted 'SignalDeck Tunnel' and 'SignalDeck Tunnel
            # Keepalive', which were waiting on an ngrok authtoken, and one of them
            # was not yet committed. ops	asks\.pending names the deliberate ones.
            $keep = @{}
            foreach ($t in $tasks) { $keep[$t.TaskName] = $true }
            $pendingFile = Join-Path $XmlDir '.pending'
            if (Test-Path $pendingFile) {
                foreach ($line in (Get-Content $pendingFile)) {
                    $n = $line.Trim()
                    if ($n -and -not $n.StartsWith('#')) { $keep[$n] = $true }
                }
            }
            foreach ($f in (Get-ChildItem -Path $XmlDir -Filter *.xml -File)) {
                if (-not $keep.ContainsKey($f.BaseName)) {
                    Remove-Item -Force $f.FullName
                    Write-Host "PRUNED $($f.Name) (no live task, not in .pending)"
                }
            }
        }
        $lines = New-Psd1Document $tasks $Prefix
        Set-Content -Path $OutFile -Value $lines -Encoding ASCII
        foreach ($t in $tasks) {
            $rawXml = Export-ScheduledTask -TaskName $t.TaskName
            $userToken = "$env:USERDOMAIN\$env:USERNAME"
            $normalizedXml = ConvertTo-PortableTaskXml -xml $rawXml -userToken $userToken
            $xmlPath = Join-Path $XmlDir ($t.TaskName + '.xml')
            [System.IO.File]::WriteAllText($xmlPath, $normalizedXml, (New-Object System.Text.UTF8Encoding($false)))
        }
        Write-Host "EXPORTED $($tasks.Count) tasks to $OutFile and $($tasks.Count) xml files to $XmlDir"
    }
    catch {
        Write-Error $_
        exit 1
    }
}