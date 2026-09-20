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
        $tokenized2 = ConvertTo-TaskTokens -Xml $tokenized -Repo $Repo
        if ($tokenized2 -ne $tokenized) { Write-Host 'g failed'; exit 1 }
        if (-not ($tokenized -like '*{{REPO}}*')) { Write-Host 'h1 failed'; exit 1 }
        if ($tokenized -like '*{{USERPROFILE}}\Desktop*') { Write-Host 'h2 failed'; exit 1 }
        $upperXml = $originalXml.ToUpperInvariant()
        $tokenizedUpper = ConvertTo-TaskTokens -Xml $upperXml -Repo $Repo
        if (-not ($tokenizedUpper -like '*{{USERPROFILE}}*')) { Write-Host 'i1 failed'; exit 1 }
        if ($tokenizedUpper -like '*C:\USERS*') { Write-Host 'i2 failed'; exit 1 }
        Write-Host 'SELFCHECK OK'
        exit 0
    } finally {
        $env:USERPROFILE = $origUserProfile
        $env:USERDOMAIN = $origUserDomain
        $env:USERNAME = $origUserName
    }
}