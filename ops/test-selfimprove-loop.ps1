# The loop's whole value rests on Run() reporting exit codes honestly. `exit 3`
# inside a PowerShell background job leaves State=Completed, so the obvious
# implementation marks every gate green and the loop "improves" a repo it never
# tested. This pins that behaviour.
#
#   pwsh -File ops/test-selfimprove-loop.ps1

. "$PSScriptRoot\selfimprove-loop.ps1" -SelfTestOnly

$ErrorActionPreference = 'Stop'   # a check that ERRORS must not be a check that vanishes

$fail = 0
$ran  = 0
function Check($label, $actual, $expect) {
  $script:ran += 1
  $script:fail += [int]($actual -ne $expect)
  "{0,-34} got={1,-6} want={2,-6} {3}" -f $label, $actual, $expect, $(if ($actual -eq $expect) { 'PASS' } else { 'FAIL' })
}

# If Run() fails to load, every Check below throws while evaluating its argument
# and never increments the counter -- the run then ends with "0 failures" and
# reports success having tested nothing. The expected count is the guard.
$EXPECTED_CHECKS = 21

Check 'native non-zero exit is RED'    (Run t 'cmd /c "exit 3"').Ok             $false
Check 'native zero exit is GREEN'      (Run t 'cmd /c "exit 0"').Ok             $true
Check 'thrown exception is RED'        (Run t 'throw "boom"').Ok                $false
Check 'real go build is GREEN'         (Run t 'cd daemon; go build ./...').Ok   $true
Check 'bogus go package is RED'        (Run t 'cd daemon; go build ./nope/...').Ok $false
Check 'timeout is RED'                 (Run t 'Start-Sleep 30' 2).Ok            $false
Check 'sentinel stripped from output'  ((Run t 'cmd /c "exit 3"').Output -match '__EXIT__') $false

# Every backlog item must carry a runnable verification, or the loop cannot tell
# done from claimed-done.
$text = Get-Content "$PSScriptRoot\IMPROVE_BACKLOG.md" -Raw
$open = [regex]::Matches($text, '(?ms)^## \[ \] (.+?)$(.*?)(?=^## |\z)')
$withVerify = @($open | Where-Object { $_.Groups[2].Value -match '(?m)^verify: `(.+)`\s*$' }).Count
Check 'every open backlog item verifiable' $withVerify $open.Count

# -UntilGoal stops when gates are green AND NextBacklogItem is null, so a parser
# that lied in EITHER direction would be serious: null while work remains makes
# the loop declare GOAL-MET having done nothing; non-null when the backlog is
# clear makes it grind on phantom work forever.
#
# This asserted "not null" while six items were open. All six are now closed, so
# that assertion had started failing for the best possible reason. What actually
# needs pinning is that the parser AGREES WITH THE FILE, whichever state it is in.
$openInFile = @([regex]::Matches($text, '(?m)^## \[ \] ')).Count
Check 'parser agrees with the backlog file' ($null -eq (NextBacklogItem)) ($openInFile -eq 0)

# The bug this exists to catch: a gate body calling `exit` terminates the job
# before Run() prints its sentinel, so Run() sees no verdict and reports RED.
# The whole battery read red on its first live run and the loop would have spent
# a day "fixing" code that was already correct. Stand-in commands did not catch
# it, so exercise the REAL bodies.
# String literals are stripped first: the py-tests gate PRINTS "GATE-FAIL: $m
# exit $LASTEXITCODE", and matching inside that text read as an exit call.
Check 'no gate body calls exit' (@(GateSpecs | Where-Object { ($_.c -replace '"[^"]*"', '' -replace "'[^']*'", '') -match '(^|;|\s)exit\s' }).Count) 0

# go-build is the cheapest real gate and is known green right now. If the gate
# plumbing breaks again, this goes red without waiting for a full sweep.
$goBuild = GateSpecs | Where-Object { $_.n -eq 'go-build' }
Check 'real go-build gate body is GREEN' (Run 'go-build' $goBuild.c).Ok $true

# --- ApplyEdits: the trust boundary where model output becomes source code ---
$NL = "`n"
function Blk($s, $r) { "<<<<<<< SEARCH$NL$s$NL=======$NL$r$NL>>>>>>> REPLACE" }

$file = "package main$NL${NL}func main() {$NL`tx := 1$NL`ty := 2$NL}$NL"

# The happy path. Note these three do NOT catch the worker draft's bugs -- it
# passes all of them. That was worth finding out: the assertions that bite are
# the insertion cases at the bottom, and without checking, this block would have
# been mistaken for coverage it does not provide.
$r = ApplyEdits $file (Blk "`tx := 1" "`tx := 42") 'test'
Check 'edit applies'                  ($null -ne $r)                     $true
Check 'leading tab preserved'         ($r -like "*`tx := 42*")           $true
Check 'unrelated lines untouched'     ($r -like "*`ty := 2*")            $true

# An anchor that appears twice must be refused, not guessed. Replacing the wrong
# one of two identical sites corrupts the file and can still pass a narrow verify.
$dupe = "a$NL`tsame$NL`tsame$NL"
Check 'ambiguous anchor refused'      (ApplyEdits $dupe (Blk "`tsame" "`tnew") 'test')  $null

# An anchor that matches nothing means the model retyped it from memory rather
# than copying it, so its picture of the file is already wrong.
Check 'missing anchor refused'        (ApplyEdits $file (Blk "`tz := 9" "`tz := 0") 'test') $null

# Prose with no blocks is not an edit, however confident it sounds.
Check 'no blocks refused'             (ApplyEdits $file "Sure! Here is the fix." 'test')   $null

# Anchors are source code full of regex metacharacters; they must match as
# literal bytes. If this ever goes through -replace or a regex, it breaks here.
$rx = "if (x[0] == `$y) {$NL"
Check 'regex metacharacters literal'  ((ApplyEdits $rx (Blk "if (x[0] == `$y) {" "if (ok) {") 'test') -like "*if (ok) {*") $true

# THE INSERTION CASE, and the one that actually bites. The worker's draft
# replaced first and then re-searched the MODIFIED text for the anchor, so any
# edit whose replacement CONTAINS its search text came back as "ambiguous".
# That is the dominant pattern -- the brief tells workers to insert by matching
# a line and replacing it with itself plus new lines -- so the draft would have
# rejected almost every insertion it was ever asked to make. Verified: the draft
# returns $null for all three shapes below; this implementation returns the text.
$ins = "`tx := 1$NL"
$got = ApplyEdits $ins (Blk "`tx := 1" "`tx := 1$NL`tx2 := 2") 'test'
Check 'insert after anchor works'     ($got -eq "`tx := 1$NL`tx2 := 2$NL")  $true

$got = ApplyEdits "`tx := 1$NL" (Blk "`tx := 1" "`t`tx := 1") 'test'
Check 're-indent (replacement contains search)' ($got -eq "`t`tx := 1$NL")   $true

$got = ApplyEdits "a$NL" (Blk "a" "a$NL") 'test'
Check 'append blank line'             ($got -eq "a$NL$NL")                  $true

if ($ran -ne $EXPECTED_CHECKS) {
  "`nONLY $ran OF $EXPECTED_CHECKS CHECKS RAN -- the rest errored out, so this run proves nothing"
  exit 1
}
if ($fail) { "`n$fail OF $ran CHECKS FAILED"; exit 1 }
"`nALL $ran CHECKS PASSED"
exit 0
