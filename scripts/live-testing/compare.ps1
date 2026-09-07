#Requires -Version 7.0
[CmdletBinding()]
param(
  [Parameter(Mandatory)][string]$BaselineRunDir,
  [Parameter(Mandatory)][string]$CandidateRunDir
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'functions.ps1')
$privateRoot = Join-Path $env:LOCALAPPDATA 'upbrr-live-testing'
$baselineDir = Assert-PrivatePath $BaselineRunDir $privateRoot
$candidateDir = Assert-PrivatePath $CandidateRunDir $privateRoot
if ($baselineDir.Equals($candidateDir, [StringComparison]::OrdinalIgnoreCase)) { throw 'comparison_requires_distinct_runs' }
$baseline = Read-PrivateJson (Join-Path $baselineDir 'report.json')
$candidate = Read-PrivateJson (Join-Path $candidateDir 'report.json')
$baselineRun = Read-PrivateJson (Join-Path $baselineDir 'run.json')
$candidateRun = Read-PrivateJson (Join-Path $candidateDir 'run.json')
foreach ($report in @($baseline, $candidate)) {
  if ($report.version -ne 1 -or $report.runId -cnotmatch '^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$') { throw 'comparison_report_invalid' }
}
if ($baseline.runId -cne $baselineRun.runId -or $candidate.runId -cne $candidateRun.runId) { throw 'comparison_run_identity_mismatch' }

$mismatches = @()
foreach ($field in @('suite', 'executionMode', 'configFingerprint', 'selectedTrackers', 'cases')) {
  if (-not $baseline.ContainsKey($field) -or -not $candidate.ContainsKey($field) -or
      (ConvertTo-Json -InputObject $baseline[$field] -Compress) -cne (ConvertTo-Json -InputObject $candidate[$field] -Compress)) { $mismatches += $field }
}
foreach ($field in @('sat', 'corpusSha256', 'rules')) {
  if (-not $baselineRun.ContainsKey($field) -or -not $candidateRun.ContainsKey($field) -or
      (ConvertTo-Json -InputObject $baselineRun[$field] -Depth 20 -Compress) -cne (ConvertTo-Json -InputObject $candidateRun[$field] -Depth 20 -Compress)) { $mismatches += $field }
}
foreach ($field in @('maxImages', 'maxRequests', 'timeoutSeconds', 'screenshotCount')) {
  if (-not $baseline.budgets.ContainsKey($field) -or -not $candidate.budgets.ContainsKey($field) -or
      $baseline.budgets[$field] -ne $candidate.budgets[$field]) { $mismatches += $field }
}
foreach ($tool in @('ffmpeg', 'ffprobe')) {
  if (-not $baselineRun.tools[$tool].binarySha256 -or $baselineRun.tools[$tool].binarySha256 -cne $candidateRun.tools[$tool].binarySha256) { $mismatches += "${tool}Version" }
}

function Index-Observations($Report) {
  $index = @{}
  foreach ($row in $Report.results) {
    if (($row.caseId -and $row.caseId -cnotmatch '^[A-Z0-9]+(?:-[A-Z0-9]+)*$') -or
        $row.stage -cnotmatch '^[a-z][a-z0-9_]{0,80}$' -or
        $row.status -notin @('pass', 'fail', 'blocked', 'needs_input', 'inconclusive', 'not_applicable') -or
        $row.reason -cnotmatch '^[a-z][a-z0-9_]{0,80}$') { throw 'comparison_observation_invalid' }
    # Lane numbers are process-local; pair independent client-lookup variants explicitly.
    $key = '{0}|{1}|{2}' -f $row.caseId, $row.stage, [bool]$row.evidence.sat
    if (-not $index.ContainsKey($key)) { $index[$key] = @() }
    $index[$key] += @{ status = $row.status; reason = $row.reason }
  }
  $index
}
$before = Index-Observations $baseline
$after = Index-Observations $candidate
$changes = @()
foreach ($key in @(@($before.Keys) + @($after.Keys) | Sort-Object -Unique)) {
  $left = @($before[$key] | Sort-Object status, reason)
  $right = @($after[$key] | Sort-Object status, reason)
  if ((ConvertTo-Json -InputObject $left -Depth 5 -Compress) -cne (ConvertTo-Json -InputObject $right -Depth 5 -Compress)) {
    $parts = $key.Split('|')
    $changes += @{ caseId = $parts[0]; stage = $parts[1]; sat = $parts[2] -eq 'True'; baseline = $left; candidate = $right }
  }
}
$cleanupComplete = $baseline.cleanup.state -eq 'complete' -and $candidate.cleanup.state -eq 'complete'
$comparison = @{
  version = 1; baselineRunId = $baseline.runId; candidateRunId = $candidate.runId
  inputsComparable = $mismatches.Count -eq 0; mismatchedInputs = $mismatches
  cleanupComplete = $cleanupComplete; observationsChanged = $changes.Count; changes = $changes
  interpretation = 'Observed stage changes require evidence review; live search results are time-dependent. Equal outcomes do not prove equivalent decisions or image quality.'
}
$outputDir = Assert-PrivatePath (Join-Path $privateRoot ('comparisons/' + [guid]::NewGuid().ToString('N'))) $privateRoot
New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
Write-PrivateJson (Join-Path $outputDir 'comparison.json') $comparison
$lines = @('# Live testing comparison', '', "Baseline: $($baseline.runId). Candidate: $($candidate.runId).", '', "Comparable inputs: $($comparison.inputsComparable). Cleanup complete: $cleanupComplete. Changed observations: $($changes.Count).", '', $comparison.interpretation)
if ($mismatches.Count -gt 0) { $lines += @('', 'Inputs requiring reconciliation: ' + ($mismatches -join ', ')) }
foreach ($change in $changes) {
  $old = @($change.baseline | ForEach-Object { "$($_.status):$($_.reason)" }) -join ', '
  $new = @($change.candidate | ForEach-Object { "$($_.status):$($_.reason)" }) -join ', '
  $lines += @('', "$($change.caseId) / $($change.stage) / sat=$($change.sat): $old -> $new")
}
[IO.File]::WriteAllText((Join-Path $outputDir 'comparison.md'), ($lines -join "`n"))
Write-Output "comparison=$outputDir comparable=$($comparison.inputsComparable) changed=$($changes.Count) cleanupComplete=$cleanupComplete"
if ($mismatches.Count -gt 0 -or -not $cleanupComplete) { exit 2 }
