# NO param() BLOCK. This file is DOT-SOURCED, and a dot-sourced script's param
# block is declared in the CALLER'S scope - so `param([switch]$SelfCheck)` here
# silently reset export-tasks.ps1's own -SelfCheck to $false, and
# `export-tasks.ps1 -SelfCheck` ran a REAL EXPORT instead of its self-check.
# Measured 2026-09-19. The self-check is gated on an explicit argument and on
# NOT being dot-sourced (InvocationName is '.' when it is).

function Get-TaskTokenMap([string]$Repo) {
    return [ordered]@{
        '{{REPO}}'        = $Repo
        '{{USERPROFILE}}' = $env:USERPROFILE
        '{{USER}}'        = "$env:USERDOMAIN\$env:USERNAME"
    }
}

function ConvertTo-TaskTokens([string]$Xml, [string]$Repo) {
    $map = Get-TaskTokenMap -Repo $Repo
    foreach ($entry in $map.GetEnumerator()) {
        $token = $entry.Key
        $value = $entry.Value
        if ([string]::IsNullOrEmpty($value)) { continue }
        $escaped = [regex]::Escape($value)
        $Xml = [regex]::Replace($Xml, $escaped, { param($m) $token }, [System.Text.RegularExpressions.RegexOptions]::IgnoreCase)
    }
    return $Xml
}

function Expand-TaskTokens([string]$Xml, [string]$Repo) {
    $map = Get-TaskTokenMap -Repo $Repo
    foreach ($entry in $map.GetEnumerator()) {
        $token = $entry.Key
        $value = $entry.Value
        if ([string]::IsNullOrEmpty($value)) { continue }
        $Xml = $Xml.Replace($token, $value)
    }
    return $Xml
}

if ($MyInvocation.InvocationName -ne '.' -and $args.Count -gt 0 -and $args[0] -eq '__selfcheck') {
    try {
        $origUserProfile = $env:USERPROFILE
        $origUserDomain = $env:USERDOMAIN
        $origUserName = $env:USERNAME
        $env:USERPROFILE = 'C:\Users\alice'
        $env:USERDOMAIN = 'CONTOSO'
        $env:USERNAME = 'alice'
        $Repo = 'C:\Users\alice\Desktop\claude code\signaldeck'
        $originalXml = @"
<Task>
    <Command>C:\Users\alice\AppData\Local\Microsoft\WinGet\Links\ngrok.exe</Command>
    <Arguments>-File "C:\Users\alice\Desktop\claude code\signaldeck\ops\tunnel-guard.ps1"</Arguments>
    <WorkingDirectory>C:\Users\alice\Desktop\claude code\signaldeck</WorkingDirectory>
    <UserId>CONTOSO\alice</UserId>
</Task>
"@
        $tokenized = ConvertTo-TaskTokens -Xml $originalXml -Repo $Repo
        if (-not ($tokenized -like '*{{REPO}}\ops\tunnel-guard.ps1*')) { Write-Host 'a failed'; exit 1 }
        if (-not ($tokenized -like '*{{USERPROFILE}}\AppData\Local\Microsoft\WinGet\Links\ngrok.exe*')) { Write-Host 'b failed'; exit 1 }
        if (-not ($tokenized -like '*<UserId>{{USER}}</UserId>*')) { Write-Host 'c failed'; exit 1 }
        if ($tokenized -like '*C:\Users\alice*') { Write-Host 'd failed'; exit 1 }
        if ($tokenized -like '*CONTOSO*') { Write-Host 'e failed'; exit 1 }
        $expanded = Expand-TaskTokens -Xml $tokenized -Repo $Repo
        if ($expanded -ne $originalXml) { Write-Host 'f failed'; exit 1 }
        $twice = ConvertTo-TaskTokens -Xml $tokenized -Repo $Repo
        if ($twice -ne $tokenized) { Write-Host 'g failed'; exit 1 }
        if (-not ($tokenized -like '*{{REPO}}*')) { Write-Host 'h1 failed'; exit 1 }
        if ($tokenized -like '*{{USERPROFILE}}\Desktop*') { Write-Host 'h2 failed'; exit 1 }
        $upperXml = $originalXml.ToUpperInvariant()
        $tokenizedUpper = ConvertTo-TaskTokens -Xml $upperXml -Repo $Repo
        if (-not ($tokenizedUpper -like '*{{USERPROFILE}}*')) { Write-Host 'i1 failed'; exit 1 }
        if ($tokenizedUpper -like '*C:\USERS*') { Write-Host 'i2 failed'; exit 1 }
        # case j: real install-windows-tasks.ps1 -Install against a temp copy of ops\
        # with ScheduledTasks cmdlets shadowed by stub functions.
        # Fails if any XML handed to Register-ScheduledTask still contains '{{'.
        $global:SdScRegistered = New-Object System.Collections.ArrayList
        function Get-ScheduledTask { [CmdletBinding()] param([string]$TaskName) }
        function Export-ScheduledTask { throw 'selfcheck must not export a task' }
        function Unregister-ScheduledTask { throw 'selfcheck must not unregister a task' }
        function Register-ScheduledTask { [CmdletBinding()] param([string]$TaskName, [string]$Xml, [switch]$Force) [void]$global:SdScRegistered.Add($Xml) }
        # SAFETY GATE: ensure stubs are in place
        foreach ($name in @('Get-ScheduledTask','Export-ScheduledTask','Unregister-ScheduledTask','Register-ScheduledTask')) {
            if ((Get-Command $name).CommandType -ne 'Function') {
                Write-Host "j unsafe: $name is not stubbed"
                exit 1
            }
        }
        $scTmp = Join-Path ([IO.Path]::GetTempPath()) ('sd-selfcheck-' + [guid]::NewGuid().ToString('N'))
        $scOps = Join-Path $scTmp 'ops'
        New-Item -ItemType Directory -Path (Join-Path $scOps 'tasks') -Force | Out-Null
        try {
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'install-windows-tasks.ps1') -Destination $scOps
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'lib-tasks.ps1') -Destination $scOps
            $scXml = @(Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot 'tasks') -Filter *.xml)
            foreach ($xmlFile in $scXml) {
                Copy-Item -LiteralPath $xmlFile.FullName -Destination (Join-Path $scOps 'tasks')
            }
            & (Join-Path $scOps 'install-windows-tasks.ps1') -Install *>&1 | Out-Null
            if ($scXml.Count -eq 0 -or $global:SdScRegistered.Count -ne $scXml.Count) {
                Write-Host ("j1 failed: registered {0} of {1}" -f $global:SdScRegistered.Count, $scXml.Count)
                exit 1
            }
            foreach ($registeredXml in $global:SdScRegistered) {
                if ($registeredXml.Contains('{{')) {
                    Write-Host 'j2 failed: XML handed to Register-ScheduledTask still has a {{token}}'
                    exit 1
                }
            }
        } finally {
            Remove-Item -LiteralPath $scTmp -Recurse -Force -ErrorAction SilentlyContinue
        }
        Write-Host 'SELFCHECK OK'
        exit 0
    } finally {
        $env:USERPROFILE = $origUserProfile
        $env:USERDOMAIN = $origUserDomain
        $env:USERNAME = $origUserName
    }
}