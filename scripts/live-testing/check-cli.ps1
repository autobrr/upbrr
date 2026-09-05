#Requires -Version 7.0
[CmdletBinding()]
param(
  [Parameter(Mandatory)][string]$BaselineRunDir,
  [Parameter(Mandatory)][ValidatePattern('^lane-[0-9]{4}$')][string]$LaneId
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'functions.ps1')
$script:RepoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$privateRoot = Join-Path $env:LOCALAPPDATA 'upbrr-live-testing'
$baselineDir = Assert-PrivatePath $BaselineRunDir $privateRoot
$baseline = Read-PrivateJson (Join-Path $baselineDir 'run.json')
$sourceProfile = Read-PrivateJson (Join-Path $baselineDir 'profile.private.json')
if ($baseline.state -ne 'cleaned' -or $baseline.runId -cne $sourceProfile.runId) { throw 'cli_check_requires_cleaned_baseline' }
if ((Get-FileHash -LiteralPath $sourceProfile.configPath).Hash -cne $baseline.profileConfigSha256) { throw 'cli_baseline_config_changed' }
$binary = Assert-PrivatePath $baseline.binaryPath $privateRoot
if ((Get-FileHash -LiteralPath $binary).Hash -cne $baseline.binarySha256) { throw 'cli_binary_identity_mismatch' }
$lanes = @(Read-PrivateJson (Join-Path $baselineDir 'lanes.private.json'))
$lane = @($lanes | Where-Object laneId -CEQ $LaneId)
if ($lane.Count -ne 1 -or -not $lane[0].workflowId) { throw 'cli_baseline_lane_missing' }
$lane = $lane[0]
$entries = @(Read-PrivateJson (Join-Path $baselineDir 'corpus.private.json'))
$entry = @($entries | Where-Object { $_.case.case_id -ceq $lane.caseId })
if ($entry.Count -ne 1 -or $entry[0].status -ne 'ready' -or (Get-SourceFingerprint $entry[0].case).fingerprint -cne $lane.sourceFingerprint) { throw 'cli_source_evidence_changed' }
if (@($lane.trackerIds).Count -eq 0 -or @($lane.trackerIds | Where-Object { $_ -cnotmatch '^[A-Z0-9]+$' }).Count -gt 0) { throw 'cli_tracker_scope_invalid' }
$snapshot = Assert-PrivatePath (Join-Path $baselineDir "snapshots/$LaneId.private.json") $privateRoot
$id = [datetime]::UtcNow.ToString('yyyyMMddTHHmmssZ') + '-cli-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$script:RunDir = Assert-PrivatePath (Join-Path $privateRoot "runs/$id") $privateRoot
$logDir = Assert-PrivatePath (Join-Path $privateRoot "builds/$id") $privateRoot
New-Item -ItemType Directory -Path $logDir -Force | Out-Null
$runnerLock = [IO.File]::Open((Join-Path $privateRoot 'runner.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
$initialized = $false; $script:Cleanup = $null; $receipt = $null; $exitCode = 2
$capturedOverrides = @{}
try {
  $node = (Get-Command node -CommandType Application | Select-Object -First 1).Source
  Invoke-OwnedProcess $node @('-e', "require('node:sqlite')") (Join-Path $logDir 'sqlite-availability') 30
  # Clone the captured baseline, without consulting a new source or settings from the caller's environment.
  foreach ($variable in @(Get-ChildItem Env: | Where-Object Name -Like 'UA_*')) {
    $capturedOverrides[$variable.Name] = $variable.Value
    [Environment]::SetEnvironmentVariable($variable.Name, $null, 'Process')
  }
  Invoke-OwnedProcess $binary @('live-test', 'init', '--run-dir', $script:RunDir, '--config', $sourceProfile.configPath) (Join-Path $logDir 'init')
  $profile = Read-PrivateJson (Join-Path $logDir 'init.stdout.private.log')
  $initialized = $true
  $script:Binary = $binary
  $script:Run = @{ version = 1; runId = $id; state = 'running'; binaryPath = $binary; binarySha256 = $baseline.binarySha256; baselineRunId = $baseline.runId; caseId = $lane.caseId; laneId = $LaneId; suite = 'CLIParity'; selectedTrackers = $lane.trackerIds; caseIds = @($lane.caseId); executionMode = $baseline.executionMode; gaps = @() }
  Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
  Write-PrivateJson (Join-Path $script:RunDir 'profile.private.json') $profile
  if ($profile.runId -cne $id -or $profile.runDir -cne $script:RunDir) { throw 'cli_profile_identity_mismatch' }
  $arguments = @('--live-test', '--live-test-max-images', '0', '--config', $profile.configPath, '--unattended', '--no-seed', '--trackers', ($lane.trackerIds -join ','))
  if ($lane.sat) { $arguments += '--sat' }
  if ($baseline.executionMode -eq 'debug') { $arguments += '--debug' }
  elseif ($baseline.executionMode -ne 'normal') { throw 'cli_execution_mode_invalid' }
  $arguments += @('--', $entry[0].case.input_path)
  $handle = Start-OwnedProcess $binary $arguments (Join-Path $script:RunDir 'cli')
  try {
    if (-not $handle.process.WaitForExit([int]$baseline.budgets.timeoutSeconds * 1000)) { throw 'cli_deadline_exceeded' }
    $cliExit = $handle.process.ExitCode
  } finally { Stop-OwnedProcess $handle }
  $receiptPath = Join-Path $script:RunDir 'cli-parity.json'
  Invoke-OwnedProcess $node @((Join-Path $PSScriptRoot 'read-cli-receipt.cjs'), $profile.dbPath, $snapshot, $receiptPath, $lane.workflowId) (Join-Path $script:RunDir 'inspect') 30
  $receipt = Read-PrivateJson $receiptPath
  $receipt.runId = $id; $receipt.baselineRunId = $baseline.runId; $receipt.caseId = $lane.caseId; $receipt.laneId = $LaneId; $receipt.cliExitCode = $cliExit
  if ($receipt.executionMode -cne $baseline.executionMode) { $receipt.status = 'fail'; $receipt.reason = 'cli_execution_mode_changed' }
  if ($receipt.status -eq 'pass') { $exitCode = 0 }
} catch {
  $receipt = @{ version = 1; status = 'inconclusive'; reason = 'cli_check_incomplete'; runId = $id; caseId = $lane.caseId; laneId = $LaneId }
  [IO.File]::WriteAllText((Join-Path $logDir 'error.private.log'), ($_ | Out-String))
  Write-Output 'status=inconclusive reason=cli_check_incomplete'
} finally {
  if ($initialized) {
    try { Invoke-RunCleanup; $script:Run.state = 'cleaned' }
    catch { $script:Run.state = 'cleanup_pending'; $exitCode = 2; $receipt.status = 'blocked'; $receipt.reason = 'cli_cleanup_unresolved' }
    $receipt.cleanup = $script:Cleanup
    Write-PrivateJson (Join-Path $script:RunDir 'cli-parity.json') $receipt
    Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
    Write-Output "run=$id status=$($receipt.status) reason=$($receipt.reason)"
  }
  foreach ($name in $capturedOverrides.Keys) { [Environment]::SetEnvironmentVariable($name, $capturedOverrides[$name], 'Process') }
  $runnerLock.Dispose()
}
exit $exitCode
