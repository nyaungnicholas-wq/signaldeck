<#
################################################################################
##                                                                            ##
##   S T O P  —  R E A D  T H I S  B E F O R E  R U N N I N G  A N Y T H I N G ##
##                                                                            ##
##   THE SECRETS BELOW ARE ALREADY DISCLOSED. TREAT THEM AS COMPROMISED AND    ##
##   ROTATE THEM. THIS SCRIPT DOES NOT AND CANNOT FIX THAT.                    ##
##                                                                            ##
##   Removing an ACL removes FUTURE access. It does not un-read what was       ##
##   already readable, it does not expire a key that was already copied, and   ##
##   it leaves no evidence either way — Windows object-access auditing is off  ##
##   by default, so there is NO log that would show whether the sandbox        ##
##   accounts ever opened these files. "No evidence of access" is not          ##
##   evidence of no access. The only action that actually restores the         ##
##   security property is rotating the credentials at the issuer.              ##
##                                                                            ##
##   ROTATE THESE (variable NAMES only — this script never reads, prints or    ##
##   logs any value, and neither should you when you rotate them):             ##
##                                                                            ##
##     daemon\.env                        SIGNALDECK_NVIDIA_KEY                ##
##                                        SIGNALDECK_TV_WEBHOOK_SECRET         ##
##     daemon\.env.bak-20260719-175045    SIGNALDECK_NVIDIA_KEY                ##
##                                        SIGNALDECK_TV_WEBHOOK_SECRET         ##
##                                        (an OLDER generation of the same     ##
##                                        two — rotating only the live .env    ##
##                                        leaves the backup's copies valid if  ##
##                                        they were never rotated since        ##
##                                        2026-07-19)                          ##
##                                                                            ##
##   And see the BLAST RADIUS section: the grant is not scoped to SignalDeck.  ##
##   It covers the ENTIRE Desktop tree, so ALPACA_KEY / ALPACA_SECRET and any  ##
##   other credential stored anywhere under C:\Users\Nicholas_N\Desktop is     ##
##   exposed by the same ACE and belongs on the same rotation list.            ##
##                                                                            ##
##   data\signaldeck.db is not a credential store, but it is the whole         ##
##   research record and it cannot be "rotated". Its exposure is a disclosure  ##
##   to accept or not, not something this script repairs.                      ##
##                                                                            ##
################################################################################

WHAT WAS MEASURED (2026-08-06, read-only `icacls` + Get-Acl, nothing modified)

  icacls "…\signaldeck\daemon\.env"
    Nicholas_Nyaung\CodexSandboxUsers:(I)(RX)
    NT AUTHORITY\SYSTEM:(I)(F)
    BUILTIN\Administrators:(I)(F)
    Nicholas_Nyaung\Nicholas_N:(I)(F)

  Identical output for daemon\.env.bak-20260719-175045 and data\signaldeck.db.

  Group: Nicholas_Nyaung\CodexSandboxUsers
    SID  S-1-5-21-209929897-3616501805-2598169953-1002
    Desc "Codex sandbox internal group (managed)"
    Members: Nicholas_Nyaung\CodexSandboxOffline, Nicholas_Nyaung\CodexSandboxOnline

THE THING THAT MAKES THE OBVIOUS FIX WRONG

  Every ACE on those files is marked (I) — INHERITED. Walking the ancestors:

    …\signaldeck\daemon      CodexSandboxUsers  ReadAndExecute  inherited=True
    …\signaldeck\data        CodexSandboxUsers  ReadAndExecute  inherited=True
    …\signaldeck             CodexSandboxUsers  ReadAndExecute  inherited=True
    …\Desktop\claude code    CodexSandboxUsers  ReadAndExecute  inherited=True
    C:\Users\Nicholas_N\Desktop
                             CodexSandboxUsers  ReadAndExecute  inherited=FALSE
                             flags: ContainerInherit, ObjectInherit
    C:\Users\Nicholas_N      (no CodexSandbox ACE)

  The one real ACE is on C:\Users\Nicholas_N\Desktop and it propagates to every
  file and folder beneath it.

  `icacls <file> /remove:g CodexSandboxUsers` REMOVES ONLY EXPLICIT ACEs. Run
  against these files it exits 0, prints "Successfully processed 1 files", and
  CHANGES NOTHING — the inherited ACE is still there on the next `icacls`. That
  is the failure mode this script exists to avoid: a remediation that reports
  success while the exposure is untouched. Inheritance must be broken on the
  target first (/inheritance:d), which converts the inherited ACEs to explicit
  copies that /remove:g can then delete.

BLAST RADIUS — TWO DIFFERENT FIXES, PICK DELIBERATELY

  MODE A (default, -Apply): surgical. Breaks inheritance on the 5 target files
  only and drops the group from each. Everything else under Desktop keeps the
  grant, so whatever uses the Codex sandbox keeps working. Cost: those 5 files
  no longer inherit future ACL changes made at Desktop level.

  MODE B (-IncludeRootFix): removes the source ACE on C:\Users\Nicholas_N\Desktop.
  This revokes CodexSandboxUsers' read access to the ENTIRE Desktop tree — every
  project, not just SignalDeck. If the Codex CLI sandbox relies on that grant to
  read the user's code, MODE B breaks it. It is the correct fix if the grant was
  never intended; it is a self-inflicted outage if it was. That call belongs to
  the human, which is why it is a separate switch and not the default.

WHY -wal AND -shm ARE IN THE TARGET LIST

  data\signaldeck.db-wal is 67,108,864 bytes and data\signaldeck.db-shm is
  1,245,184 bytes (measured 2026-08-06). The WAL holds the most RECENT committed
  pages — the ones not yet checkpointed back into the main file. Locking down
  signaldeck.db and leaving the WAL readable would protect the old data and
  expose the new. They are separate files with their own (inherited) ACLs and
  SQLite recreates them, so this is a floor, not a fence: see LIMITS.

LIMITS — WHAT THIS SCRIPT DOES NOT ACHIEVE

  1. SQLite DELETES AND RECREATES -wal/-shm on checkpoint/close. A recreated
     file inherits from data\ again and the grant comes back. MODE A is
     therefore NOT durable for the sidecars; only MODE B (or an explicit deny on
     data\) is. This is stated here rather than papered over.
  2. New files added under Desktop always inherit the grant. MODE A protects the
     files named below and nothing else, forever.
  3. No elevation is required: Nicholas_N holds (F) on every target, which
     includes WRITE_DAC. If UAC prompts, something else is wrong — stop.
  4. The live daemon (pid 33844) holds data\signaldeck.db open. Windows checks
     access at OPEN time, so changing the ACL does not affect its existing
     handles and no restart is needed. This script performs NO database I/O and
     NO service action.

APPROVAL REQUIRED — DO NOT RUN THIS UNREVIEWED

  Drafted for BLOCKED-6 / SWARM 5b. It was NEVER executed: only read-only
  `icacls <path>` and Get-Acl were run to produce the measurements above. With
  no switches it is a DRY RUN — it enumerates and prints the exact commands it
  WOULD run, and modifies nothing.

  Dry run (safe, read-only):   .\strip-sandbox-acl.ps1
  Apply mode A:                .\strip-sandbox-acl.ps1 -Apply
  Apply mode A + B:            .\strip-sandbox-acl.ps1 -Apply -IncludeRootFix
  Verify at any time:          .\strip-sandbox-acl.ps1 -Verify
  Undo:                        .\strip-sandbox-acl.ps1 -Undo
#>

[CmdletBinding()]
param(
    # Actually modify ACLs. Without it this script only reads and prints.
    [switch]$Apply,
    # ALSO remove the source ACE on C:\Users\Nicholas_N\Desktop (MODE B).
    [switch]$IncludeRootFix,
    # Re-run the read-only checks and report pass/fail per target.
    [switch]$Verify,
    # Restore: icacls /reset on each target (and re-grant at Desktop for MODE B).
    [switch]$Undo
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Apply -and $Undo) { throw "-Apply and -Undo are mutually exclusive." }

# Reference the group by SID, not by name. A renamed group would silently make a
# name-based /remove:g a no-op — the same class of quiet failure as the
# inherited-ACE trap above. icacls accepts *<SID> anywhere it accepts a name.
$GroupSid  = '*S-1-5-21-209929897-3616501805-2598169953-1002'
$GroupName = 'Nicholas_Nyaung\CodexSandboxUsers'   # for display only

$Repo = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
$RootGrantPath = 'C:\Users\Nicholas_N\Desktop'

$Targets = @(
    "$Repo\daemon\.env"
    "$Repo\daemon\.env.bak-20260719-175045"
    "$Repo\data\signaldeck.db"
    "$Repo\data\signaldeck.db-wal"
    "$Repo\data\signaldeck.db-shm"
)

$BackupDir = Join-Path $PSScriptRoot ("acl-backup-" + (Get-Date -Format 'yyyyMMdd-HHmmss'))

function Show-Acl([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { Write-Host "  (absent) $Path"; return }
    Write-Host "  $Path"
    icacls "$Path" | ForEach-Object { Write-Host "    $_" }
}

# Test-Grant is the verification primitive: does this path grant the group
# anything, by any route (explicit OR inherited)? Get-Acl is used rather than
# parsing icacls text because the display name can be localised.
function Test-Grant([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return $null }   # nothing to grant
    $sid = $GroupSid.TrimStart('*')
    foreach ($ace in (Get-Acl -LiteralPath $Path).Access) {
        try { $id = $ace.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value }
        catch { $id = $ace.IdentityReference.Value }
        if ($id -eq $sid) { return $ace }
    }
    return $null
}

function Invoke-Step([string]$Description, [scriptblock]$Command, [string]$Printed) {
    Write-Host ""
    Write-Host "  # $Description"
    Write-Host "  $Printed"
    if (-not $Apply -and -not $Undo) { Write-Host "  [dry run — not executed]"; return }
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "FAILED (exit $LASTEXITCODE): $Printed" }
}

# ── 0. Always enumerate first (read-only) ────────────────────────────────────
Write-Host "=== CURRENT STATE (read-only) ==="
Write-Host "group: $GroupName  sid: $($GroupSid.TrimStart('*'))"
foreach ($t in $Targets) { Show-Acl $t }
Write-Host ""
Write-Host "source of the inherited grant:"
Show-Acl $RootGrantPath

if ($Verify) {
    Write-Host ""
    Write-Host "=== VERIFY ==="
    $bad = 0
    foreach ($t in $Targets) {
        $ace = Test-Grant $t
        if ($null -eq $ace) {
            Write-Host "  PASS  no grant to the sandbox group : $t"
        } else {
            $bad++
            Write-Host "  FAIL  still granted ($($ace.FileSystemRights), inherited=$($ace.IsInherited)) : $t"
        }
    }
    $rootAce = Test-Grant $RootGrantPath
    if ($null -ne $rootAce) {
        Write-Host "  NOTE  the source ACE on $RootGrantPath is still present (expected unless MODE B was run)."
        Write-Host "        Any NEW file created under Desktop — including a SQLite -wal/-shm recreated"
        Write-Host "        on the next checkpoint — inherits it again."
    }
    Write-Host ""
    Write-Host ("verify result: {0}" -f $(if ($bad -eq 0) { "clean" } else { "$bad target(s) still exposed" }))
    Write-Host "REMINDER: a clean verify means future access is blocked. It says NOTHING about"
    Write-Host "past access. Rotate SIGNALDECK_NVIDIA_KEY and SIGNALDECK_TV_WEBHOOK_SECRET."
    return
}

# ── UNDO ─────────────────────────────────────────────────────────────────────
if ($Undo) {
    Write-Host ""
    Write-Host "=== UNDO ==="
    Write-Host "Every ACE on these files was inherited before the change (all four entries"
    Write-Host "were marked (I)), so /reset — which discards explicit ACEs and re-inherits"
    Write-Host "from the parent — restores the original state exactly, INCLUDING the"
    Write-Host "sandbox group's read access."
    foreach ($t in $Targets) {
        if (-not (Test-Path -LiteralPath $t)) { continue }
        Invoke-Step "restore inherited ACL on $(Split-Path $t -Leaf)" `
            { icacls "$t" /reset } `
            "icacls `"$t`" /reset"
    }
    Write-Host ""
    Write-Host "  # MODE B undo — ONLY if -IncludeRootFix was used. Re-grants the whole Desktop tree."
    Write-Host "  icacls `"$RootGrantPath`" /grant `"$($GroupSid):(OI)(CI)(RX)`""
    if ($IncludeRootFix) {
        Invoke-Step "re-grant at the Desktop root (MODE B undo)" `
            { icacls "$RootGrantPath" /grant "$($GroupSid):(OI)(CI)(RX)" } `
            "icacls `"$RootGrantPath`" /grant `"$($GroupSid):(OI)(CI)(RX)`""
    }
    Write-Host ""
    Write-Host "Undo does not un-rotate a rotated key, and it should not: if you rotated"
    Write-Host "after the exposure — which you should have — leave the new values in place."
    return
}

# ── 1. Back up the exact security descriptors before touching anything ───────
Write-Host ""
Write-Host "=== BACKUP (SDDL per target) ==="
Write-Host "  $BackupDir"
if ($Apply) { New-Item -ItemType Directory -Force -Path $BackupDir | Out-Null }
foreach ($t in $Targets) {
    if (-not (Test-Path -LiteralPath $t)) { continue }
    $dest = Join-Path $BackupDir ((Split-Path $t -Leaf) + '.sddl')
    Write-Host "  (Get-Acl '$t').Sddl > '$dest'"
    if ($Apply) { (Get-Acl -LiteralPath $t).Sddl | Set-Content -LiteralPath $dest -Encoding UTF8 }
}
Write-Host "  # restore one by hand if /reset is not enough:"
Write-Host "  #   `$a = Get-Acl -LiteralPath <path>"
Write-Host "  #   `$a.SetSecurityDescriptorSddlForm((Get-Content <path>.sddl -Raw).Trim())"
Write-Host "  #   Set-Acl -LiteralPath <path> -AclObject `$a"

# ── 2. MODE A — the surgical fix, per target ─────────────────────────────────
Write-Host ""
Write-Host "=== MODE A — strip the grant on the 5 target files ==="
Write-Host "Two commands per file, IN THIS ORDER. The first is not optional: without it"
Write-Host "the second exits 0 and changes nothing, because the ACE is inherited."
foreach ($t in $Targets) {
    if (-not (Test-Path -LiteralPath $t)) {
        Write-Host ""
        Write-Host "  # SKIP (file absent): $t"
        continue
    }
    Invoke-Step "1/2 break inheritance (copies the inherited ACEs down as explicit ones)" `
        { icacls "$t" /inheritance:d } `
        "icacls `"$t`" /inheritance:d"
    Invoke-Step "2/2 drop the sandbox group's now-explicit ACE" `
        { icacls "$t" /remove:g "$GroupSid" } `
        "icacls `"$t`" /remove:g `"$GroupSid`""
}

# ── 3. MODE B — the root fix, gated ──────────────────────────────────────────
Write-Host ""
Write-Host "=== MODE B — remove the SOURCE ACE (whole Desktop tree) ==="
if ($IncludeRootFix) {
    Write-Host "ENABLED. This revokes sandbox read access to EVERYTHING under $RootGrantPath,"
    Write-Host "not just SignalDeck. If the Codex CLI sandbox depends on it, it stops working."
    Invoke-Step "remove the explicit ACE at the Desktop root" `
        { icacls "$RootGrantPath" /remove:g "$GroupSid" } `
        "icacls `"$RootGrantPath`" /remove:g `"$GroupSid`""
} else {
    Write-Host "NOT enabled (default). The command, for review:"
    Write-Host "  icacls `"$RootGrantPath`" /remove:g `"$GroupSid`""
    Write-Host "Leaving it in place means: the 5 files above are protected, and every other"
    Write-Host "file under Desktop — plus any SQLite -wal/-shm recreated later — is not."
}

# ── 4. Verify ────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "=== VERIFY (re-run any time with -Verify) ==="
Write-Host "  icacls `"$Repo\daemon\.env`""
Write-Host "  # expected: NO Nicholas_Nyaung\CodexSandboxUsers line; the SYSTEM /"
Write-Host "  # Administrators / Nicholas_N entries remain, now without the (I) marker."
foreach ($t in $Targets) {
    $ace = Test-Grant $t
    if ($null -eq $ace) { Write-Host "  PASS  $t" }
    else { Write-Host "  FAIL  $t  ($($ace.FileSystemRights), inherited=$($ace.IsInherited))" }
}

Write-Host ""
Write-Host "################################################################################"
Write-Host "# ACLs are only half of it, and it is the half that does not matter yet.       #"
Write-Host "# ROTATE SIGNALDECK_NVIDIA_KEY AND SIGNALDECK_TV_WEBHOOK_SECRET AT THE ISSUER, #"
Write-Host "# in daemon\.env AND in daemon\.env.bak-20260719-175045, plus any credential   #"
Write-Host "# stored anywhere else under C:\Users\Nicholas_N\Desktop (ALPACA_KEY /         #"
Write-Host "# ALPACA_SECRET included). Until then the exposure is unremediated.            #"
Write-Host "################################################################################"
