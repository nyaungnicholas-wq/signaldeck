<#
.SYNOPSIS
  Drives EIGHTY_PERCENT_SUPERPROMPT.md through OmniRoute, indefinitely, with
  ZERO Claude involvement.

.DESCRIPTION
  Claude's entire role is to have written this file and the protocol it carries.
  At runtime there are no Claude calls -- not one -- so the loop's Claude token
  consumption is 0 by construction rather than by a budget that has to be
  policed. That is the only honest way to enforce "Claude barely touches this":
  a local script cannot meter a remote model it never invokes, so it must simply
  never invoke it.

  Every cycle is one hypothesis, run end to end by local workers:

    1. PROPOSE  - a mechanism and an abstention rule, stated BEFORE any data is
                  touched (protocol section 3, steps 1-2).
    2. IMPLEMENT- a self-contained Python script that measures precision, the
                  issued-subset base rate, abstention rate and effective N.
    3. VERIFY   - the script must RUN. Output that does not execute is discarded;
                  a hypothesis is not evidence until a command produced its numbers.
    4. JUDGE    - the protocol's nine acceptance criteria are applied to the real
                  output. Almost everything is killed here, which is the loop
                  working rather than failing.
    5. RECORD   - kept or killed, the result is appended to the research journal
                  with its numbers. A search whose judgments cannot be counted is
                  an unverifiable null result.

  The protocol is passed VERBATIM as standing context on every worker call, so
  the traps in section 0 and the prohibitions in section 5 are in front of the
  model at all times rather than summarised away.

.PARAMETER Hours
  Wall-clock cap. 0 = run until stopped.

.PARAMETER MaxCycles
  Hard iteration cap, a runaway backstop rather than a target.

.EXAMPLE
  pwsh -File ops/eighty-loop.ps1 -Hours 24
#>
[CmdletBinding()]
param(
  [double]$Hours = 0,
  [int]$MaxCycles = 500,
  # BOTH stages run on the `code` lane (qwen3.6:27b) deliberately.
  #
  # The reason lane (deepseek-r1:32b, 19GB) TIMED OUT after 610 seconds against
  # a 11.4KB prompt while qwen3.6:27b (18GB) was already resident -- two models
  # that size cannot coexist, so every lane switch evicts and reloads, and under
  # that contention the call dies. omni then fell through to the PAID gateway
  # (mistral-large), which is both the wrong cost profile for a loop meant to run
  # for days and the reason no local token movement was visible.
  #
  # qwen3.6:27b stays resident, handled a 29KB prompt in 78-122s in testing, and
  # is a perfectly good reasoning model for this. One model, no thrash, no spend.
  [string]$Lane = 'code',
  # IMPLEMENT runs on the SAME lane as PROPOSE (qwen3.6:27b), reversing the
  # earlier choice of `fast`. Both halves of that choice were measured wrong:
  #
  # 1. "qwen2.5-coder:7b answered comparable asks in 20-50 seconds" was timing a
  #    harness that could not pass. With verify fixed and a gate that demands a
  #    real measurement, 60 genuine draws produced zero executable scripts --
  #    SyntaxError, unclosed parens, undefined cursors. Fast wrong is not fast.
  # 2. "qwen3.6:27b took over ten minutes" did not reproduce: 79 seconds COLD,
  #    generating correct working sqlite3 code on the first draw (2026-08-02).
  #
  # Sharing one model across both stages also removes the eviction thrash that
  # made the 27B call fail and silently fall through to the PAID gateway -- the
  # loop was billing Mistral for PROPOSE while claiming zero spend.
  [string]$CodeLane = 'code',
  # Pin the ACTUAL model rather than trusting a lane name -- same reasoning as
  # before (one model, no thrash), but the target changed. Ollama itself is now
  # killed and its autostart disabled machine-wide (2026-08-02, memory-hog
  # cleanup), so pinning a local tag here is no longer viable at all: every
  # call would just fail. Pin a single OmniRoute gateway model instead. This
  # loop now costs real router spend for the duration it runs -- that's an
  # accepted tradeoff of moving off Ollama, not an oversight.
  #
  # -Model still disables omni's fallback, which is still the point: a loop
  # meant to run for days must fail LOUDLY to worker-empty when the pinned
  # model errors, not silently drift onto a different (possibly pricier) one.
  # Set to '' to restore lane-based routing (now gateway-first by default too).
  #
  # 2026-08-04: repointed off mistral/devstral-latest, whose credentials are
  # dead -- it answers 404 to everything. Measured cost of not noticing: 8 runs,
  # 835 cycles, 827 of them worker-empty, 0 hypotheses judged.
  #
  # The replacement is an alias, not a concrete id, and that is a deliberate
  # reversal of the reasoning above. Every concrete gateway model was tried
  # against the REAL payload (this protocol, ~11.5KB, as standing context):
  # mistral/* and moonshot/* and xai/* are 404, gemini/* returns 429 under any
  # sustained use. Pinning one of those buys nominal reproducibility and zero
  # hypotheses, which is the trade this loop just made for eight runs.
  #
  # BE CLEAR ABOUT WHAT THIS COSTS. `served-by` records what omni was ASKED
  # for, not what the gateway chose: omni.ps1 line 171 echoes the requested id
  # ("via auto/coding"), and the response's real model is never surfaced. So
  # under an alias the concrete model behind a given hypothesis is NOT recorded,
  # and that is a genuine loss of provenance, not a wash.
  #
  # It is still the right trade today. A concrete pin bought exactly zero
  # hypotheses across 835 cycles, and provenance over nothing is worth nothing.
  # Revert to a concrete id the moment one is reliably reachable. The proper fix
  # is upstream -- omni.ps1 could log the `model` field the gateway returns,
  # which would make an alias fully attributable and this note obsolete.
  [string]$PinnedModel = 'auto/coding',
  [int]$CyclePauseSec = 30,
  # Consecutive empty cycles before the run gives up. "Fail loudly" was already
  # true -- worker-empty was logged all 827 times -- but loud into a log nobody
  # reads is indistinguishable from silence, and the loop ground on for ~7 hours
  # per run regardless. The comment at the diag capture below records the same
  # shape at 44 cycles; it got better diagnostics and still no brake. This is
  # the brake: five straight failures is a broken pin or a dead gateway, not a
  # bad run of luck, and stopping is what makes it visible.
  [int]$MaxConsecutiveEmpty = 5
)

$ErrorActionPreference = 'Continue'
$repo = Split-Path $PSScriptRoot -Parent
Set-Location $repo

# Reuse the harness that is already tested rather than writing a second one:
# Run() reports exit codes honestly through a sentinel, which is the single
# property this loop cannot do without.
. "$PSScriptRoot\selfimprove-loop.ps1" -SelfTestOnly

$protocolPath = Join-Path $repo 'EIGHTY_PERCENT_SUPERPROMPT.md'
if (-not (Test-Path $protocolPath)) { throw "protocol missing: $protocolPath" }
$PROTOCOL = Get-Content $protocolPath -Raw

$omni     = Join-Path $env:USERPROFILE '.claude\scripts\omni.ps1'
$journal  = Join-Path $repo 'logs\eighty-research.md'
$events   = Join-Path $repo 'logs\eighty-events.jsonl'
$work     = Join-Path $repo 'research\eighty'
New-Item -ItemType Directory -Force (Split-Path $journal) | Out-Null
New-Item -ItemType Directory -Force $work | Out-Null

$deadline = if ($Hours -le 0) { [datetime]::MaxValue } else { (Get-Date).AddHours($Hours) }

function Ev([string]$event, [hashtable]$data = @{}) {
  $rec = @{ ts = (Get-Date).ToString('o'); event = $event } + $data
  # AppendLine comes from selfimprove-loop.ps1 (dot-sourced above): Add-Content
  # raises a non-terminating error under a file lock, which the empty catch here
  # never saw, so events vanished into the error stream.
  [void](AppendLine $events ($rec | ConvertTo-Json -Compress -Depth 6))
  Write-Host "[$((Get-Date).ToString('HH:mm:ss'))] $event $($data | ConvertTo-Json -Compress -Depth 3)"
}

# The measurement rules a CODE-GENERATION call actually needs. The full protocol
# governs judgment -- which hypotheses are worth testing and which results count
# as evidence -- and none of that changes a line of Python. Sending all 11KB of
# it alongside a 1.5KB code spec produced a 17-minute generation that timed out
# locally and then failed over to a gateway that also refused. Cycle 1 died there.
# PROPOSE and JUDGE still receive the protocol in full, because that is where it
# does its work.
$CODE_RULES = @"
THE ACTUAL SCHEMA. These are the real columns; there are no others. A generated
script that invented a column (predicted_class) died on the very first run,
because a model with no schema writes SQL against the database it imagines.

The first version of this block listed five tables and said "there are no
others". That was false -- the database has 101 -- and it cost 167 consecutive
cycles: PROPOSE invented mechanisms about insider clusters and earnings
surprises, IMPLEMENT was forbidden from touching them, and every script bailed
with INSUFFICIENT while writing comments like "we have no table for that" about
tables holding thousands of rows. A schema too narrow fails as surely as one
that is absent; it just fails politely.

PRICES AND LABELS
- bars(symbol_id, tf, ts, open, high, low, close, volume)  -- 13.2M rows.
  tf is '1d' | '1h' | '1m'. ts is a unix epoch integer.
  1d spans 2018-07-26..now over 1,777 symbols; 1h from 2025-06-23; 1m from
  2026-06-05 only. Anything longer than a few weeks must use 1d.
- symbols(id, symbol, market, name, active, added_at, stream, delisted_at)
  -- 1,780 rows. market is 'stocks' | 'crypto'. delisted_at is set only from
  2026-07-24; before that the universe is survivor-seeded.
- prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at,
  basis_epoch)  -- 397,769 rows. up is the realised direction and fwd_return the
  realised forward return: THESE ARE LABELS and are the safest label source.

RAW OBSERVATIONS you may use as inputs
- insider_trades(accession, symbol_id, insider, title, code, shares, price,
  value, tx_ts, filed_ts)  -- 5,678 rows, 609 symbols, 2008-03..2026-07.
  code: A=award S=sale P=purchase F=tax M=option-exercise.
  AS-OF: tx_ts is when the trade happened, filed_ts when it became public.
  Only filed_ts is knowable at decision time. Using tx_ts is lookahead.
- filings(id, symbol_id, form, filed_ts, title, url, label)  -- 86,643 rows,
  903 symbols, but ONLY 2026-02-05..now. form: 424B2, 4, 8-K, 144, 3, 6-K.
- short_volume(symbol_id, day, short_vol, short_exempt, total_vol, short_pct)
  -- 25,435 rows, 1,040 symbols, ONLY 2026-05-20..2026-07-31. day is 'YYYY-MM-DD'.
- news(id, symbol_id, ts, headline, url, source, sentiment, score, rationale,
  lex_score, lex_ver, lex_polar, lex_hedged)  -- 322,719 rows, 741 symbols,
  2012-04..now.
- sentiment_features(symbol_id, day, n_polar, n_all, mean_score, pos, neg,
  hedged, ver)  -- 41,625 rows, 695 symbols, 2012-04..now. day is 'YYYY-MM-DD'.
- stocktwits_sentiment(symbol_id, ts, bullish, bearish, untagged, total)
  -- 41,670 rows.
- macro_series(series, ts, value)  -- 104,543 rows. FRED series keyed by name.
- fundamentals(symbol_id, metric, value, as_of, fetched_at)  -- 4,989 rows.
  KEY/VALUE, not columns: metric is 'EPS' | 'Revenues' | 'SharesOutstanding' |
  'EntityPublicFloat' | 'CIK' | 'LatestFilingDate'. AS-OF: as_of is the period,
  fetched_at is when we learned it. Only fetched_at is knowable in advance.
- inst_holdings(cik, manager, period, symbol_id, cusip, name, value, shares)
  -- 48,805 rows. 13F. AS-OF: period is the quarter END; 13Fs are filed up to 45
  days later, and this table does NOT record the filing date. Treating period as
  knowable is a 45-day lookahead. Prefer another input unless you lag it >=45d.
- anomalies(id, symbol_id, ts, kind, z, detail, hour_bucket)  -- 3,949 rows.

MODEL OUTPUTS -- self-reference hazard, read this before using them
scores(symbol_id, horizon, ts, score, components) 1.9M; composite_scores(
symbol_id, ts, horizon, score, curve_pct, edge, payload) 296k; features(id,
symbol_id, horizon, ts, version, vec) 324k; expectancy(symbol_id, horizon,
state_key, n, mean_fwd, median_fwd, hit_rate, stdev, updated_at) 87k;
rankings(ts, symbol_id, score, rank, ret1m, ret3m) 269k;
tv_ratings(symbol_id, ts, reco_all, reco_ma, reco_other, rsi, close_px, label)
626k but ONLY 2026-07-11..now, far too short for most horizons.
These are THIS SYSTEM'S OWN predictions, not observations. A hypothesis whose
input is a model output and whose label is that model's outcome measures the
model against itself. If you use one, say so in the mechanism and expect the
judge to weigh it accordingly.
- regime_outcomes(id, symbol_id, kind, ts, day, horizon_days, regime, conviction,
  historical_accuracy, rank, resolved_at, actual, correct, naive_label, revision,
  basis_epoch)  -- 24,957 rows. correct and resolved_at are NULL on EVERY row:
  nothing has resolved yet, so this cannot supply labels. Do not use it for them.

Never reference a table or column not listed above. If the data a hypothesis
needs genuinely is not here, say so in one line and stop -- but check this list
first, because the last 167 scripts declared data missing that was present.

Measurement rules this script must obey:
- Open data/signaldeck.db READ-ONLY. Never write to it.
- As-of discipline: every input must be computable at the decision timestamp.
  No value from a bar at or after the label window may inform a call.
- Hold out the most recent 20% as a sealed era and report it separately.
- Count independent observations, not rows: one (symbol, UTC day) is one
  observation, however many forecasts resolve on it.
- Report the base rate of the predicted class WITHIN the issued subset. A
  precision at or near that base rate is unskilled classification, not an edge.
- Never fabricate. If the data is insufficient, print INSUFFICIENT=1 and exit 0.
"@

# One worker call. $context defaults to the full protocol; the code stage passes
# the compact rules instead.
function Ask([string]$task, [string]$lane, [string]$outFile, [int]$timeoutSec = 900,
  [string]$context = $null, [string]$verify = '', [int]$samples = 1) {
  if (-not $context) { $context = $PROTOCOL }
  $prompt = @"
$context

=== END OF CONTEXT. YOUR TASK FOLLOWS. ===

$task
"@
  $tmp = Join-Path $env:TEMP "eighty-$([guid]::NewGuid().ToString('N').Substring(0,8)).txt"
  # Capture omni's own diagnostics instead of discarding them. Out-Null here
  # meant 44 consecutive cycles logged a bare "worker-empty" while omni was
  # printing the actual cause -- a local model timing out and falling through to
  # the gateway -- to a stream nobody read.
  $diag = ''
  # Prompt goes through a file, never the command line: PowerShell re-parses
  # `-File` arguments and any "-word" in the protocol text binds as a parameter.
  $pf = Join-Path $env:TEMP "eighty-prompt-$([guid]::NewGuid().ToString('N').Substring(0,8)).txt"
  Set-Content -Path $pf -Value $prompt -Encoding UTF8
  try {
    # NOT $args -- that is an automatic variable and assigning it inside a
    # function with a param block is a footgun that reads as working code.
    $omniArgs = @('-NoProfile', '-File', $omni, '-PromptFile', $pf, '-TimeoutSec', $timeoutSec)
    if ($PinnedModel -ne '') { $omniArgs += '-Model', $PinnedModel }
    else                    { $omniArgs += '-Task', $lane }
    if ($verify -ne '') {
      # omni.ps1 refuses -Verify without -Out ("it runs against the written
      # file") and refuses -Samples>1 without -Verify. Omitting -Out here made
      # every verified call exit 2 before reaching a model.
      $tmp = $outFile
      # -Attempts is omni's re-brief count (it feeds the failure back into the
      # next try); -Samples is independent draws within one attempt. 3x5 was
      # never actually exercised -- the verify command was broken until now --
      # so this is the first run where retrying can converge on anything.
      $omniArgs += '-Out', $tmp, '-Verify', $verify, '-Samples', $samples, '-Attempts', 6
    } else {
      $omniArgs += '-Out', $tmp
    }
    $diag = (& powershell $omniArgs 2>&1 | Out-String)
    $omniExit = $LASTEXITCODE
  } catch { Ev 'worker-error' @{ err = "$_" }; return $null }
  if ($diag -match 'via\s+(\S+)') { Ev 'served-by' @{ model = $Matches[1] } }

  if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -lt 10) {
    # Keep the line that NAMES the cause, not just the last two lines. Taking
    # the tail captured PowerShell's error trailer ("WriteErrorException,
    # omni.ps1") and dropped omni's own "WARNING: <model> @ <url> failed: ...
    # (429) Too Many Requests" -- the only line that distinguishes a dead
    # credential from a rate limit from a timeout. Diagnosing the 827-cycle
    # outage meant re-running the call by hand purely to see this line.
    $lines = @($diag -split "`n" | Where-Object { $_ -match '\S' } | ForEach-Object { $_.Trim() })
    $cause = @($lines | Where-Object { $_ -match 'WARNING:|failed:|\(\d{3}\)' })
    Ev 'worker-empty' @{ diag = ((@($cause) + @($lines | Select-Object -Last 1) |
          Select-Object -Unique -First 3) -join ' | ') }
    return $null
  }

  # omni already stripped fences and BOM and PROVED the artifact runs, so the
  # post-processing below would only re-mangle a file that is already correct.
  #
  # EXCEPT when it proved the opposite. omni calls Write-Artifact BEFORE each
  # verify, so -Out exists and is non-empty even when every attempt FAILED and
  # omni exited 1. Sizing the file is therefore not evidence it runs -- the
  # first version of this branch logged 'implement-verified' over a script that
  # died on a NameError one second later. The exit code is the only honest
  # signal, so a failed verify returns null and the cycle dies as 'no-code'
  # rather than parading an unverified artifact as verified.
  if ($verify -ne '') {
    if ($omniExit -ne 0) {
      Ev 'verify-exhausted' @{ exit = $omniExit
        diag = (($diag -split "`n" | Where-Object { $_ -match '\S' } |
                 Select-Object -Last 6) -join ' | ') }
      return $null
    }
    Ev 'implement-verified' @{ bytes = (Get-Item $tmp).Length }
    return (Get-Content $tmp -Raw)
  }

  # omni writes a UTF-8 BOM and models fence their output despite instructions.
  $bytes = [IO.File]::ReadAllBytes($tmp)
  if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
    $bytes = $bytes[3..($bytes.Length - 1)]
  }
  $text = ([Text.Encoding]::UTF8.GetString($bytes) -replace "`r`n", "`n")
  $text = (($text -split "`n") | Where-Object { $_ -notmatch '^\s*```' }) -join "`n"
  Remove-Item $tmp -ErrorAction SilentlyContinue
  if ($outFile) { [IO.File]::WriteAllText($outFile, $text, (New-Object Text.UTF8Encoding $false)) }
  return $text
}

# maxCycles and model are logged because their absence made a real diagnosis
# impossible: every recent run ended at cycles=2 with a 24h deadline and no way
# to tell from the log whether that was -MaxCycles 2 or a defect in the guard.
Ev 'loop-start' @{ protocol = 'EIGHTY_PERCENT_SUPERPROMPT.md'; lane = $Lane; codeLane = $CodeLane
                   deadline = $(if ($Hours -le 0) { 'none' } else { $deadline.ToString('o') })
                   maxCycles = $MaxCycles
                   model = $(if ($PinnedModel -eq '') { "lane:$Lane" } else { $PinnedModel })
                   claudeCalls = 0 }

if (-not (Test-Path $journal)) {
  @"
# Eighty-percent research journal

Every hypothesis this loop has tested, kept or killed, with the numbers that
decided it. Written by local workers via OmniRoute; no Claude call participates
in this loop.

A killed hypothesis is evidence. The expected outcome of a disciplined search is
that almost everything here is killed.

"@ | Set-Content $journal
}

$cycle = 0
# Reset by any cycle that produces a hypothesis; trips the abort at
# $MaxConsecutiveEmpty. See the parameter's comment for what it cost not to
# have this.
$script:consecutiveEmpty = 0

# WHERE THE NUMBERING STARTS. Scripts were named h{cycle}.py, and $cycle resets
# to 1 on every run, so each run overwrote the previous one's scripts from h0001
# up. The 2026-08-04 00:04 run reached cycle 179 and destroyed the entire 58-file
# corpus from 2026-08-02 in the process; the only reason the loss is partial is
# that logs/eighty-research.md keeps the hypothesis text and the numbers.
#
# What it destroyed is the reproducibility, which is the part that matters here:
# the protocol's own acceptance criteria require a result to reproduce from a
# cold clone, and a journal entry citing research/eighty/h0007.py is worth
# nothing once h0007.py holds a different hypothesis from a later run.
#
# Continue from the highest number already on disk instead. Computed ONCE, so a
# long run numbers contiguously, and D4 pads rather than truncates so passing
# 9999 widens the name instead of colliding.
$hBase = 0
foreach ($f in Get-ChildItem -LiteralPath $work -Filter 'h*.py' -File -ErrorAction SilentlyContinue) {
  if ($f.BaseName -match '^h(\d+)$') {
    $n = [int]$Matches[1]
    if ($n -gt $hBase) { $hBase = $n }
  }
}
Ev 'numbering' @{ startsAfter = $hBase }
while ((Get-Date) -lt $deadline -and $cycle -lt $MaxCycles) {
  $cycle++
  Ev 'cycle-start' @{ cycle = $cycle }

  # --- 1. PROPOSE ---------------------------------------------------------
  # Dedup context. This used to be the last 120 RAW lines of the journal, and a
  # journal entry is ~10 lines, so it reached 5 of 321 entries -- 1.6% of the
  # corpus. That is why 32 of 262 hypotheses proposed news sentiment: anything
  # older than five cycles was invisible, including a refutation the daemon had
  # already recorded at IC -0.0051 over 22,755 observations.
  #
  # One line per past hypothesis instead: verdict plus the mechanism head. The
  # mechanism is what a duplicate is recognisable BY, and the compact form fits
  # the entire corpus in less prompt than 120 raw lines cost. Budget-capped at
  # ~24KB (the loop is tested to 29KB) by dropping the OLDEST entries first,
  # because a recent proposal is the one most likely to be repeated.
  $prior = if (Test-Path $journal) {
    $digest = New-Object System.Collections.Generic.List[string]
    $pending = ''
    foreach ($line in (Get-Content -LiteralPath $journal)) {
      if ($line -match '^##\s*Cycle\s*(\d+)\s*-\s*([A-Z]+)') {
        $pending = '{0}/{1}' -f $Matches[1], $Matches[2]
      } elseif ($pending -and $line -match '^MECHANISM:\s*(.+)$') {
        $m = $Matches[1].Trim()
        if ($m.Length -gt 90) { $m = $m.Substring(0, 90) }
        $digest.Add("[$pending] $m")
        $pending = ''
      }
    }
    while ((($digest -join "`n").Length -gt 24000) -and $digest.Count -gt 1) {
      $digest.RemoveAt(0)
    }
    if ($digest.Count) { $digest -join "`n" } else { '(none yet)' }
  } else { '(none yet)' }

  $hypothesis = Ask @"
Propose ONE new hypothesis, in at most 12 lines.

WHAT THIS DATABASE ACTUALLY HOLDS. Propose only what these can test. Until
2026-08-04 this stage was given no inventory at all, so it proposed mechanisms
about 13D filings, buyback announcements, lockup expiries and index additions --
none of which exist here -- and all 167 resulting scripts died without computing
anything. A hypothesis this data cannot address is not a bold hypothesis, it is
a wasted cycle.

  daily bars 2018-07..now, 1,777 symbols (hourly only from 2025-06, minute from
    2026-06 -- so anything beyond a few weeks must be daily)
  realised labels: direction and forward return per (symbol, horizon)
  insider transactions 2008..2026, 609 symbols, with BOTH trade and disclosure
    dates (only the disclosure date is knowable in advance)
  SEC filings 2026-02..now ONLY, 903 symbols: 8-K, Form 4, 144, 424B2
  short volume 2026-05-20..2026-07-31 ONLY, 1,040 symbols
  news headlines + daily sentiment aggregates 2012..now, ~700 symbols
  StockTwits bullish/bearish counts
  FRED macro series
  fundamentals as key/value (EPS, Revenues, SharesOutstanding, public float)
  13F institutional holdings by quarter (filed up to 45 days after period end)
  this system's own scores, rankings and expectancy tables -- usable, but a
    hypothesis built on them is measuring the system against itself

  NOT here: intraday tick/quote data, options, index membership history,
    analyst estimates, earnings dates, dividends, corporate actions,
    congressional trades.

Prefer a mechanism whose data spans years over one whose data spans weeks: a
21-day horizon tested on ten weeks of short-volume data cannot reach the
independent-observation floor no matter how good the idea is.

Required, in this order:
  MECHANISM:  one sentence naming an economic reason this should work (a
              liquidity constraint, a flow imbalance, a participant behaviour, a
              slow-diffusing information source). A pattern with no mechanism is
              a coincidence you have not caught yet.
  HORIZON:    the prediction horizon.
  UNIVERSE:   which symbols, defined without hindsight.
  ENTRY:      the exact condition under which a call is ISSUED.
  ABSTAIN:    the exact condition under which no call is made. State this now,
              before any data is touched. Abstention is what buys precision.
  CLAIM:      the precision band you are committing to in advance.

Do NOT repeat a hypothesis already present in this journal tail:
$prior

Output the six labelled lines and nothing else.
"@ $Lane $null 900

  if (-not $hypothesis) {
    $script:consecutiveEmpty++
    Ev 'no-hypothesis' @{ consecutive = $script:consecutiveEmpty; limit = $MaxConsecutiveEmpty }
    if ($script:consecutiveEmpty -ge $MaxConsecutiveEmpty) {
      # Stop rather than grind. The worker is not producing hypotheses, which
      # means the pinned model is unreachable or rejecting the protocol -- and
      # neither gets better by asking 400 more times.
      Ev 'aborted-worker-dead' @{
        consecutive = $script:consecutiveEmpty
        model       = $(if ($PinnedModel -eq '') { "lane:$Lane" } else { $PinnedModel })
        hint        = 'check the pinned model answers: omni.ps1 -Model <id> -Prompt "Reply OK"'
      }
      Write-Output ("eighty-loop ABORTED: {0} consecutive cycles produced no hypothesis. " -f $script:consecutiveEmpty +
                    "The pinned model ({0}) is not answering. Verify it, then restart." -f $(if ($PinnedModel -eq '') { "lane:$Lane" } else { $PinnedModel }))
      exit 1
    }
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }
  $script:consecutiveEmpty = 0
  Ev 'hypothesis' @{ cycle = $cycle; head = (($hypothesis -split "`n")[0]) }

  # --- 2. IMPLEMENT -------------------------------------------------------
  $script = Join-Path $work ("h{0:D4}.py" -f ($hBase + $cycle))
  # The verify command travels to omni.ps1 as a `powershell -File` ARGUMENT, and
  # -File re-parses arguments: quotes are stripped and the value is cut at the
  # first space, so `python "C:\...\Desktop\claude code\...\h0001.py"` arrived as
  # `python C:\Users\Nicholas_N\Desktop\claude` and every verify died with
  # "python: can't open file or directory" -- which omni then reported as a
  # failing artifact rather than a broken command. Same defect class as the
  # -PromptFile fix. A repo-RELATIVE path has no spaces, so it needs no quotes
  # and survives the hop intact; omni inherits this process's cwd, which
  # Set-Location pinned to $repo at startup.
  $scriptRel = "research\eighty\" + ("h{0:D4}.py" -f ($hBase + $cycle))
  $code = Ask @"
Write a SELF-CONTAINED Python 3 script that tests exactly this hypothesis:

$hypothesis

Hard requirements:
- Read ONLY from the read-only SQLite database at data/signaldeck.db, opened as
  sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True). Never write to it.
- The full table and column inventory is in the schema block above. Use it as
  the authority; this line used to name five tables and contradicted it, which
  is what made 167 scripts declare present data missing.
- Respect as-of discipline: every input must be computable at the decision
  timestamp. No value from a bar at or after the label window may inform a call.
- Hold out the most recent 20% of the sample as a sealed era and report it
  separately from the rest.
- PRINT exactly these lines at the end, one per line, with real computed numbers:
    ISSUED=<count of calls issued>
    OPPORTUNITIES=<count of decision points considered>
    PRECISION=<hits/issued as a decimal>
    BASE_RATE=<base rate of the predicted class WITHIN the issued subset>
    DISTINCT_DAYS=<distinct UTC days on which a call was issued>
    EFFECTIVE_N=<issued count divided by the measured design effect>
    SEALED_PRECISION=<precision on the sealed era>
  Two of those are INVARIANTS, not just definitions, and a run that violates
  either is discarded before it is judged:
    DISTINCT_DAYS counts days among the ISSUED calls only, never among the
      opportunities considered. It therefore can never exceed ISSUED. A draw
      that reported DISTINCT_DAYS=19 against ISSUED=15 had counted every day it
      looked at rather than every day it acted.
    EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are
      not independent, so the design effect is always greater than 1 and the
      effective sample is always smaller than the raw count. Setting
      EFFECTIVE_N=ISSUED asserts perfect independence, which is never true here.
- If there is insufficient data, print INSUFFICIENT=1 and exit 0. Never fabricate.
  But CHECK THE SCHEMA BLOCK FIRST: 167 consecutive scripts took this exit while
  declaring data missing that the database actually held.
- Standard library plus sqlite3 only. No pandas, no numpy, no network.
- Must run to completion in under 10 minutes.

Output the raw Python file only. No markdown fences, no commentary.
"@ $CodeLane $script 1200 $CODE_RULES -verify "python ops\verify_hypothesis.py $scriptRel" -samples 5

  if (-not $code) { Ev 'no-code'; Start-Sleep -Seconds $CyclePauseSec; continue }

  # --- 3. VERIFY: it must actually run -----------------------------------
  $r = Run "h$cycle" "python `"$script`"" 900
  if (-not $r.Ok) {
    $tail = ($r.Output -split "`n" | Where-Object { $_ -match '\S' } | Select-Object -Last 12) -join ' | '
    Ev 'script-failed' @{ cycle = $cycle; tail = $tail }
    Add-Content $journal "`n## Cycle $cycle - KILLED (script did not run)`n`n$hypothesis`n`n``````$(($r.Output -split "`n" | Select-Object -Last 6) -join "`n")```````n"
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }
  Ev 'script-ran' @{ cycle = $cycle }

  # --- 3b. SANITY: kill structurally impossible output in code -----------
  # A script that RUNS is not a script that MEASURED. The first generated one
  # printed OPPORTUNITIES=-113750, PRECISION=0.0000 on 247,360 issued calls, and
  # EFFECTIVE_N identical to ISSUED -- a negative population, a precision no
  # rule could produce, and a design effect never applied. Numbers like that are
  # arithmetically impossible, not merely unpromising, and deciding that costs a
  # regex rather than a 27B judgment call. Do it here so the judge only ever
  # sees output that could be real.
  $out = $r.Output
  function Num([string]$key) {
    if ($out -match "(?m)^$key=(-?[\d.]+)\s*$") { return [double]$Matches[1] }
    return $null
  }
  $issued = Num 'ISSUED'; $opps = Num 'OPPORTUNITIES'; $prec = Num 'PRECISION'
  $effN = Num 'EFFECTIVE_N'; $days = Num 'DISTINCT_DAYS'
  # A script that reports INSUFFICIENT has not produced impossible output; it has
  # produced a FINDING -- that this database cannot support the hypothesis. Those
  # were being lumped in with arithmetic that could not occur (the first generated
  # script printed OPPORTUNITIES=-113750) and journaled under the same heading,
  # "output not arithmetically possible".
  #
  # That mislabel is not cosmetic. PROPOSE reads the journal tail and is told not
  # to repeat what it finds there, so 238 entries were teaching it "that script
  # was broken" when the fact was "this data is too thin for that mechanism" --
  # the one lesson that would stop it proposing the same shape again. The
  # 2026-08-04 14:02 cycle earned this distinction: it queried insider_trades
  # correctly, respected the disclosure-date discipline, and found 16 qualifying
  # symbol-days against a floor of 30. Sixteen is a perfectly possible number.
  $insufficient = $out -match '(?m)^INSUFFICIENT=1\s*$'
  $insane = @()
  # Only a script CLAIMING to have measured owes us metrics. Demanding them from
  # one that correctly declined to measure is what produced the doubled reason
  # string "reported INSUFFICIENT; did not print ISSUED/PRECISION".
  if (-not $insufficient -and ($null -eq $issued -or $null -eq $prec)) {
    $insane += 'did not print ISSUED/PRECISION'
  }
  if ($null -ne $opps -and $opps -lt 0) { $insane += "OPPORTUNITIES is negative ($opps)" }
  if ($null -ne $issued -and $null -ne $opps -and $opps -gt 0 -and $issued -gt $opps) {
    $insane += "ISSUED ($issued) exceeds OPPORTUNITIES ($opps)"
  }
  if ($null -ne $prec -and ($prec -lt 0 -or $prec -gt 1)) { $insane += "PRECISION out of [0,1] ($prec)" }
  if ($null -ne $issued -and $null -ne $effN -and $issued -gt 0 -and $effN -ge $issued) {
    $insane += 'EFFECTIVE_N is not below ISSUED, so no design effect was applied'
  }
  if ($null -ne $days -and $null -ne $issued -and $days -gt $issued) {
    $insane += "DISTINCT_DAYS ($days) exceeds ISSUED ($issued)"
  }
  # Impossible arithmetic outranks insufficiency: a script printing INSUFFICIENT
  # alongside a negative population is broken, not merely short of data.
  if ($insane.Count -gt 0) {
    Ev 'insane-output' @{ cycle = $cycle; reasons = ($insane -join '; ') }
    Add-Content $journal @"

## Cycle $cycle - KILLED (output not arithmetically possible)  ($(Get-Date -Format 'yyyy-MM-dd HH:mm'))

$hypothesis

**Rejected before judging:** $($insane -join '; ')

``````
$(($out -split "`n" | Select-Object -Last 10) -join "`n")
``````
"@
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }

  # A real finding, recorded as one. The protocol's own line is that a killed
  # hypothesis is evidence; a mechanism this database demonstrably cannot test is
  # evidence about the DATA, and it is the kind PROPOSE most needs to read back.
  if ($insufficient) {
    Ev 'insufficient-data' @{ cycle = $cycle }
    Add-Content $journal @"

## Cycle $cycle - KILLED (data cannot support this hypothesis)  ($(Get-Date -Format 'yyyy-MM-dd HH:mm'))

$hypothesis

**Rejected before judging:** the script ran correctly and reported INSUFFICIENT --
this database does not hold enough of what the mechanism needs. The hypothesis was
not wrong; it was untestable HERE. Do not propose this shape again without naming
a data source that would change the answer.

``````
$(($out -split "`n" | Select-Object -Last 10) -join "`n")
``````
"@
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }

  # --- 4. JUDGE against the protocol's criteria --------------------------
  # The judge saw the protocol and this run's numbers and nothing else, so it
  # could not tell a fresh idea from one this corpus had already killed. Reuse
  # the same digest the proposer gets, filtered to KILLED, rather than building
  # a second source of truth that can drift from it.
  # Budget separately from $prior. Measured 2026-08-06: 232 of 232 retained
  # entries are KILLED, so an unbounded filter hands the judge the WHOLE ~24KB
  # digest on top of the 11.5KB protocol, the hypothesis and the run output --
  # past the ~29KB this loop is tested at. The proposer needs breadth to avoid
  # repeating anything; the judge only needs enough to recognise THIS mechanism,
  # so give it the most recent kills and cap hard.
  $refutedList = @($prior -split "`n" | Where-Object { $_ -match '^\[\d+/KILL' })
  $refuted = ($refutedList | Select-Object -Last 90) -join "`n"
  while ($refuted.Length -gt 8000 -and $refutedList.Count -gt 1) {
    $refutedList = $refutedList | Select-Object -Skip 1
    $refuted = ($refutedList | Select-Object -Last 90) -join "`n"
  }
  if (-not $refuted) { $refuted = '(nothing killed yet)' }

  $verdict = Ask @"
Here is a hypothesis and the REAL measured output of the script that tested it.

HYPOTHESIS:
$hypothesis

MEASURED OUTPUT:
$($r.Output -split "`n" | Select-Object -Last 40 | Out-String)

Apply the protocol's acceptance criteria to these numbers ONLY. Do not assume
any number that is not printed above; if something needed is missing, that is a
KILL for insufficient evidence.

Answer in at most 10 lines:
  VERDICT: KEEP or KILL
  REASON: one sentence citing the specific number that decided it
  PRECISION / BASE_RATE / EDGE: the three figures, or 'not reported'
  CRITERIA_MET: how many of the nine, listing which failed

Remember: precision alone is never evidence. A precision at or near the issued
subset base rate is unskilled classification and must be KILLED however high it
looks.

PRIOR VERDICTS ON THIS CORPUS. This is a SEPARATE test from the numeric criteria
above -- do not let it change how you read the numbers. If this hypothesis's
MECHANISM was already killed below, that is an independent KILL reason, and you
should name the earlier cycle. A mechanism that keeps being re-proposed and
re-killed costs a cycle every time and inflates the search this corpus has run.
Judge the numbers on the numbers; judge the novelty on this list.
$refuted
"@ $Lane $null 900

  if (-not $verdict) { $verdict = '(judge produced nothing - treated as KILL)' }
  $kept = $verdict -match 'VERDICT:\s*KEEP'
  Ev $(if ($kept) { 'KEPT' } else { 'killed' }) @{ cycle = $cycle }

  # SELECTION HISTORY. Stamp the artifact with how many hypotheses this corpus
  # had already tried when this one was written. A KEEP promoted out of here
  # otherwise arrives in the strategy grid looking like one candidate among 48,
  # when it actually survived a search of several hundred -- the divisor would be
  # wrong by roughly 5x, in the direction that makes things look significant.
  # The grid lane corrects multiplicity properly (SPA, StepM, Bonferroni over a
  # 528 divisor); it can only do that if the number travels with the candidate.
  if (Test-Path -LiteralPath $script) {
    $stamp = @"
# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: $($hBase + $cycle - 1)
# cycle_index: $cycle
# verdict: $(if ($verdict -match 'VERDICT:\s*(KEEP|KILL)') { $Matches[1] } else { 'UNKNOWN' })
# This hypothesis was selected from the corpus above. Any multiplicity
# correction applied downstream MUST use corpus_size_at_generation, not the
# size of whatever family it is promoted into.

"@
    $body = [IO.File]::ReadAllText($script)
    if ($body -notmatch '(?m)^# SELECTION HISTORY') {
      [IO.File]::WriteAllText($script, $stamp + $body, (New-Object Text.UTF8Encoding $false))
    }
  }

  # --- 5. RECORD, kept or killed -----------------------------------------
  Add-Content $journal @"

## Cycle $cycle - $(if ($kept) { 'KEPT' } else { 'KILLED' })  ($(Get-Date -Format 'yyyy-MM-dd HH:mm'))

$hypothesis

**Measured**
``````
$(($r.Output -split "`n" | Select-Object -Last 12) -join "`n")
``````

**Verdict**
$verdict

Script: ``research/eighty/$(Split-Path $script -Leaf)``
"@

  Start-Sleep -Seconds $CyclePauseSec
}

Ev 'loop-end' @{ cycles = $cycle; claudeCalls = 0 }
