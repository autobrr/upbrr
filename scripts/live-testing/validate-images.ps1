#Requires -Version 7.0
param([Parameter(Mandatory)][string]$ValidationDir)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'functions.ps1')
$script:RunDir = Assert-PrivatePath (Join-Path $ValidationDir 'image-pipeline') (Join-Path $env:LOCALAPPDATA 'upbrr-live-testing')
New-Item -ItemType Directory -Path $script:RunDir -Force | Out-Null
function Assert-ImageCheck([bool]$Condition, [string]$Code) { if (-not $Condition) { throw $Code } }

$metadata = @{ UploadHosts = @('ptscreens', 'pixhost', 'lostimg'); TrackerUploadHosts = @{ LIMITED = @('ptscreens', 'lostimg'); OWNER = @('lostimg') }; OwnedHosts = @{ lostimg = 'OWNER' } }
$config = @{ ImageHosting = @{ Host1 = 'ptscreens'; Host2 = 'pixhost'; Host3 = 'ptscreens'; Host4 = 'not-a-host' }; Trackers = @{ Trackers = @{ OWNER = @{ ImageHost = 'lostimg' }; OPEN = @{ ImageHost = 'lostimg' } } } }
Assert-ImageCheck ((@(Get-LiveImageCoverageHosts $metadata $config @('OPEN')) -join ',') -ceq 'ptscreens,pixhost') 'unrestricted_host_coverage_or_owner_filter_wrong'
Assert-ImageCheck ((@(Get-LiveImageCoverageHosts $metadata $config @('LIMITED')) -join ',') -ceq 'ptscreens') 'restricted_host_coverage_widened'
Assert-ImageCheck ((@(Get-LiveImageCoverageHosts $metadata $config @('OWNER')) -join ',') -ceq 'lostimg') 'owned_host_coverage_missing'

$script:Run = @{ budgets = @{ maxImages = 5 } }
$journalPath = Join-Path $script:RunDir 'image-effects.private.jsonl'
[IO.File]::WriteAllText($journalPath, '{"kind":"run"}' + "`n" + '{"kind":"upload_pending","sources":["a","b"]}' + "`n" + '{"kind":"uploaded","complete":false,"urls":[]}' + "`n")
Assert-ImageCheck ((Get-LiveImageBudgetRemaining) -eq 3) 'uncertain_dispatch_not_counted'

foreach ($wire in @(
  '{"preflight":{"results":[{"failures":[{"failure":{"Code":"rate_limited","Message":"Try later"}}]}]}}',
  '{"media":{"hostAttempts":[{"failures":[{"failure":{"Code":"upload_failed","Message":"Network unavailable"}}]}]}}',
  '{"descriptions":{"trackerResults":[{"failures":[{"failure":{"Code":"transport_error","Message":"Unavailable"}}]}]}}',
  '{"dryRun":{"reports":[{"failures":[{"failure":{"Code":"timeout","Message":"Unavailable"}}]}]}}'
)) {
  $script:RemoteStop = $false
  $failureCodes = @(Get-LiveFailureCodes ($wire | ConvertFrom-Json -AsHashtable))
  Assert-ImageCheck ($script:RemoteStop -and $failureCodes.Count -eq 1) 'wire_failure_not_reported_or_stopped'
}
$script:RemoteStop = $false
$failureCodes = @(Get-LiveFailureCodes ('{"media":{"failures":[{"failure":{"Code":"missing_prerequisite","Message":"Select a source"}}]}}' | ConvertFrom-Json -AsHashtable))
Assert-ImageCheck (-not $script:RemoteStop -and $failureCodes[0] -ceq 'missing_prerequisite') 'ordinary_policy_failure_stopped_remote_work'

$script:Lanes = @('blocked', 'local', 'hosted' | ForEach-Object { @{ laneId = $_; workflowId = "workflow-$_" } })
$script:Results = @(@{ laneId = 'local'; stage = 'media_ready'; status = 'pass' }, @{ laneId = 'hosted'; stage = 'image_host'; status = 'pass' })
Assert-ImageCheck ((Get-LiveRestartLane).laneId -ceq 'hosted') 'restart_skipped_hosted_evidence'
$script:Results = @($script:Results | Where-Object stage -NE 'image_host')
Assert-ImageCheck ((Get-LiveRestartLane).laneId -ceq 'local') 'restart_skipped_local_evidence'
$script:Results = @()
Assert-ImageCheck ((Get-LiveRestartLane).laneId -ceq 'blocked') 'restart_without_media_lost_identity_check'

# Exercise the runner's actual variant ordering and capture guard. A forced SAT
# preparation must precede the ordinary lane that owns local and hosted media.
$runnerAst = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'run.ps1'), [ref]$null, [ref]$null)
$variantStatement = @($runnerAst.FindAll({ param($node)
  $node -is [Management.Automation.Language.IfStatementAst] -and $node.Extent.Text.StartsWith('if ($script:Run.suite -in') -and $node.Extent.Text.Contains('$scenarios.dupe')
}, $true))
$captureStatement = @($runnerAst.FindAll({ param($node)
  $node -is [Management.Automation.Language.IfStatementAst] -and $node.Extent.Text.Contains("'source_capture_deferred_to_normal_lane'")
}, $true) | Sort-Object { $_.Extent.Text.Length } | Select-Object -First 1)
Assert-ImageCheck ($variantStatement.Count -eq 1 -and $captureStatement.Count -eq 1) 'runner_variant_binding_missing'
$variantBlock = [scriptblock]::Create($variantStatement[0].Extent.Text)
$captureBlock = [scriptblock]::Create($captureStatement[0].Extent.Text)
foreach ($mode in @('images', 'no-images', 'explicit-sat', 'ordinary')) {
  $script:Run = @{ suite = 'Full'; sat = $mode -eq 'explicit-sat'; budgets = @{ maxImages = $(if ($mode -eq 'no-images') { 0 } else { 20 }) } }
  $entry = @{ case = @{ case_id = $(if ($mode -eq 'ordinary') { 'ORDINARY' } else { 'PAIRED' }) } }
  $scenarios = @{ dupe = @('PAIRED') }
  $variants = @([bool]$script:Run.sat)
  . $variantBlock
  $allowedCaptures = @(); $script:Results = @(); $lane = @{ caseId = 'PAIRED'; laneId = 'paired' }
  foreach ($variant in $variants) { . $captureBlock; $allowedCaptures += $variant }
  $expectedVariants = switch ($mode) { 'images' { 'True,False' }; 'no-images' { 'True,False' }; 'explicit-sat' { 'True' }; default { 'False' } }
  $expectedCaptures = switch ($mode) { 'explicit-sat' { 'True' }; default { 'False' } }
  Assert-ImageCheck (($variants -join ',') -ceq $expectedVariants -and ($allowedCaptures -join ',') -ceq $expectedCaptures) "runner_generation_order_wrong_$mode"
}

$cliAst = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'check-cli.ps1'), [ref]$null, [ref]$null)
$cliSkipStatement = @($cliAst.FindAll({ param($node)
  $node -is [Management.Automation.Language.IfStatementAst] -and $node.Extent.Text.StartsWith('if ($baseline.skipRemoteDuplicates)')
}, $true))
Assert-ImageCheck ($cliSkipStatement.Count -eq 1) 'cli_skip_policy_binding_missing'
foreach ($skip in @($false, $true)) {
  $baseline = @{ skipRemoteDuplicates = $skip }; $arguments = @()
  . ([scriptblock]::Create($cliSkipStatement[0].Extent.Text))
  Assert-ImageCheck (($arguments -contains '--skip-dupe-check') -eq $skip) 'cli_skip_policy_changed'
}
$compareAst = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'compare.ps1'), [ref]$null, [ref]$null)
$compareStatement = @($compareAst.FindAll({ param($node)
  $node -is [Management.Automation.Language.ForEachStatementAst] -and $node.Extent.Text.StartsWith("foreach (`$field in @('sat'")
}, $true))
Assert-ImageCheck ($compareStatement.Count -eq 1) 'comparison_policy_binding_missing'
foreach ($changedPolicy in @('none', 'skipRemoteDuplicates', 'preferDeletableHosts')) {
  $baselineRun = @{ sat = $true; skipRemoteDuplicates = $false; preferDeletableHosts = $false; corpusSha256 = 'same'; rules = @() }
  $candidateRun = $baselineRun.Clone(); $mismatches = @()
  if ($changedPolicy -ne 'none') { $candidateRun[$changedPolicy] = $true }
  . ([scriptblock]::Create($compareStatement[0].Extent.Text))
  Assert-ImageCheck (($mismatches -join ',') -ceq $(if ($changedPolicy -eq 'none') { '' } else { $changedPolicy })) 'comparison_policy_difference_lost'
}

$lane = @{ caseId = 'CASE-A'; laneId = 'lane-a'; trackerIds = @('OPEN') }
$validReport = @{ trackerId = 'OPEN'; status = 'completed'; uploadReleaseName = 'Example.2025.1080p-GRP'; fields = @(@{ key = 'name'; value = 'Example.2025.1080p-GRP' }); preparedOperationId = 'prepared-a'; torrentArtifactId = 'torrent-a'; torrentFingerprint = 'torrent-fingerprint'; semanticFingerprint = 'semantic-fingerprint'; clientInjection = @{ status = 'skipped' } }
foreach ($fault in @('none', 'full-two', 'excluded-failed', 'silent-omission', 'excluded-ready', 'excluded-waiting', 'excluded-no-failure', 'duplicate-outcome', 'extra-report', 'projection', 'payload', 'artifact', 'injection', 'no-seed', 'missing-report', 'skipped-report', 'target-mismatch', 'duplicate-report')) {
  $lane.trackerIds = @('OPEN')
  $current = @{ dryRun = @{ noSeed = $true; trackerIds = @('OPEN'); reports = @((ConvertTo-Json $validReport -Depth 20 | ConvertFrom-Json -AsHashtable)) }; projections = @{ projections = @(@{ trackerId = 'OPEN'; uploadReleaseName = 'Example.2025.1080p-GRP' }) } }
  if ($fault -in @('excluded-failed', 'silent-omission', 'excluded-ready', 'excluded-waiting', 'excluded-no-failure', 'duplicate-outcome')) {
    $lane.trackerIds += 'SECOND'
    $current.continuation = @{ trackerOutcomes = @(@{ trackerId = 'SECOND'; lifecycle = 'terminal'; disposition = 'failed'; failures = @(@{ failure = @{ Code = 'no_eligible_trackers' } }) }) }
  }
  switch ($fault) {
    'silent-omission' { $current.continuation.trackerOutcomes = @() }
    'excluded-ready' { $current.continuation.trackerOutcomes[0].lifecycle = 'ready'; $current.continuation.trackerOutcomes[0].disposition = 'none' }
    'excluded-waiting' { $current.continuation.trackerOutcomes[0].lifecycle = 'waiting'; $current.continuation.trackerOutcomes[0].disposition = 'needs_action' }
    'excluded-no-failure' { $current.continuation.trackerOutcomes[0].Remove('failures') }
    'duplicate-outcome' { $current.continuation.trackerOutcomes += $current.continuation.trackerOutcomes[0] }
    'extra-report' {
      $extraReport = ConvertTo-Json $validReport -Depth 20 | ConvertFrom-Json -AsHashtable; $extraReport.trackerId = 'SECOND'
      $current.dryRun.reports += $extraReport; $current.dryRun.trackerIds += 'SECOND'
      $current.projections.projections += @{ trackerId = 'SECOND'; uploadReleaseName = $validReport.uploadReleaseName }
    }
    'full-two' {
      $lane.trackerIds += 'SECOND'; $current.dryRun.trackerIds = @('SECOND', 'OPEN')
      $secondReport = ConvertTo-Json $validReport -Depth 20 | ConvertFrom-Json -AsHashtable; $secondReport.trackerId = 'SECOND'
      $current.dryRun.reports = @($secondReport, $current.dryRun.reports[0])
      $current.projections.projections += @{ trackerId = 'SECOND'; uploadReleaseName = $validReport.uploadReleaseName }
    }
    'projection' { $current.projections.projections[0].uploadReleaseName = 'Different.2025-GRP' }
    'payload' { $current.dryRun.reports[0].fields[0].value = 'Different.2025-GRP' }
    'artifact' { $current.dryRun.reports[0].torrentFingerprint = '' }
    'injection' { $current.dryRun.reports[0].clientInjection.status = 'completed' }
    'no-seed' { $current.dryRun.noSeed = $false }
    'missing-report' { $current.dryRun.reports = @() }
    'skipped-report' { $current.dryRun.reports += @{ trackerId = 'OPEN'; status = 'skipped' } }
    'target-mismatch' { $current.dryRun.trackerIds = @('SECOND') }
    'duplicate-report' { $current.dryRun.reports += $current.dryRun.reports[0] }
  }
  $script:Results = @()
  Record-LiveDryRunPayload $lane $current
  Assert-ImageCheck (($script:Results[0].status -eq 'pass') -eq ($fault -cin @('none', 'full-two', 'excluded-failed'))) "payload_fault_not_detected_$fault"
}

# Exercise the actual multi-lane orchestration with local workflow doubles.
$script:Run = @{ suite = 'Full'; budgets = @{ screenshotCount = 3 } }
$captureLane = @{ trackerIds = @('NONE') }
$captureState = @{ projections = @{ projections = @(
  @{ trackerId = 'NONE'; artifacts = @{ screenshotCount = 0 } },
  @{ trackerId = 'IMAGES'; artifacts = @{ screenshotCount = 4 } }
) } }
Assert-ImageCheck ((Get-LiveMediaInstructions $captureLane $captureState).screenshotCount -eq 0) 'unselected_tracker_forced_screenshots'
$captureLane.trackerIds += 'IMAGES'
Assert-ImageCheck ((Get-LiveMediaInstructions $captureLane $captureState).screenshotCount -eq 4) 'mixed_content_requirements_lost'
$script:Run.suite = 'Screenshots'; $captureLane.trackerIds = @('NONE')
Assert-ImageCheck ((Get-LiveMediaInstructions $captureLane $captureState).screenshotCount -eq 3) 'explicit_capture_suite_ignored'
$script:Run.suite = 'Full'; $script:Run.imageHostCoverage = $true
Assert-ImageCheck ((Get-LiveMediaInstructions $captureLane $captureState).screenshotCount -eq 1) 'image_host_coverage_capture_missing'

function Invoke-LiveAPI($Method, $Body = @{}, [int]$ExpectedStatus = 200) {
  switch ($Method) {
    'GetImageHostPolicyMetadata' { return $metadata }
    'GetConfig' {
      if ($script:ImageScenario -eq 'coverage-none-no-hosts') { return '{"ImageHosting":{"Host1":"lostimg"},"Trackers":{"Trackers":{}}}' }
      return (ConvertTo-Json $config -Depth 20)
    }
    'GetReleaseWorkflow' { return $script:ImageStates[$Body.workflowId] }
    'UploadReleaseWorkflowImages' {
      $current = $script:ImageStates[$Body.workflowId]
      Assert-ImageCheck ($Body.expectedRevision -eq $current.workflow.revision -and $Body.media.revision -eq $current.media.revision) 'stale_image_authority'
      if (-not $Body.host -and $current.media.imageRequirementsPrepared) { return $current }
      $hostID = $(if ($Body.host) { $Body.host } else { 'ptscreens' })
      $script:ImageDispatches += @{ lane = $Body.workflowId; host = $hostID; explicitHost = [bool]$Body.host; count = @($Body.artifactIds).Count }
      [IO.File]::AppendAllText($journalPath, (ConvertTo-Json @{ kind = 'upload_pending'; sources = @($Body.artifactIds) } -Compress) + "`n")
      $current.workflow.revision++; $current.media.revision++
      if ($script:ImageScenario -eq 'feedback' -and $Body.workflowId -eq 'lane-a') {
        $current.media.requiredActions = @(@{ id = 'reconcile-a'; kind = 'reconcile'; status = 'pending' })
        return $current
      }
      foreach ($artifactID in $Body.artifactIds) { $current.media.artifacts += @{ id = "hosted-$hostID-$artifactID"; kind = 'hosted_image'; host = $hostID; url = 'https://images.example.test/a.png' } }
      if (-not $Body.host) { $current.media.imageRequirementsPrepared = $true }
      return $current
    }
    default { throw 'unexpected_image_test_api' }
  }
}
function Wait-Workflow($Current, $SnapshotPath) { $Current }
function Save-Feedback($Lane, $Current, $Goal) { if (@(Get-PendingActions $Current).Count -gt 0) { $script:ImageFeedback += $Lane.laneId } }
function Continue-Lane($Lane, $Goal, $Current, $Intent) {
  Assert-ImageCheck ($Intent.noSeed -eq $true -and $Intent.skipRemoteDuplicates -eq [bool]$script:Run.skipRemoteDuplicates -and $Intent.interaction -eq 'unattended') 'image_dry_run_authority_changed'
  $expectedCount = [int]$Current.projections.projections[0].artifacts.screenshotCount
  if ($expectedCount -gt 0) { $expectedCount = [Math]::Max($expectedCount, $script:Run.budgets.screenshotCount) }
  elseif ($script:Run.imageHostCoverage) { $expectedCount = 1 }
  Assert-ImageCheck ($Intent.media.screenshotCount -eq $expectedCount -and $Intent.media.purpose -ceq 'final' -and $Intent.media.captureDvdMenus -eq $false) 'image_dry_run_media_options_changed'
  $script:ImageGoals += "$($Lane.laneId):$Goal"
  if ($Goal -eq 'dry_run') { $Current.dryRun = @{ noSeed = $true; status = 'completed'; trackerIds = $Lane.trackerIds; reports = @($validReport) } }
  else { $Current.descriptions = @{ status = $(if ($Current.projections.projections[0].artifacts.description) { 'completed' } else { 'skipped' }) } }
  $Current
}
function Invoke-BrowserCheck($Phase) { $script:ImageBrowserLanes = @((Read-PrivateJson (Join-Path $script:RunDir 'browser.private.json')).lanes.laneId) }

foreach ($scenario in @('full', 'skip-dupes', 'coverage', 'coverage-none', 'coverage-none-budget', 'coverage-none-no-hosts', 'budget', 'feedback', 'stopped', 'resume', 'projected-count', 'insufficient-selection', 'zero-image-budget', 'description-only', 'images-only')) {
  $script:ImageScenario = $scenario
  $script:ImageStates = @{}; $script:ImageDispatches = @(); $script:ImageGoals = @(); $script:ImageFeedback = @(); $script:ImageBrowserLanes = @()
  $script:Results = @(); $script:Lanes = @(); $script:RemoteStop = $scenario -eq 'stopped'; $script:RequestCount = 0
  $script:Run = @{ suite = 'Full'; executionMode = 'normal'; skipRemoteDuplicates = $scenario -eq 'skip-dupes'; imageHostCoverage = $scenario -in @('coverage', 'coverage-none', 'coverage-none-budget', 'coverage-none-no-hosts'); budgets = @{ maxImages = $(if ($scenario -in @('budget', 'resume')) { 3 } else { 20 }); screenshotCount = 3; maxRequests = 100 } }
  if ($scenario -in @('zero-image-budget', 'description-only', 'coverage-none-budget')) { $script:Run.budgets.maxImages = 0 }
  [IO.File]::WriteAllText($journalPath, '{"kind":"run"}' + "`n")
  foreach ($name in @('a', 'b', 'pending', 'undecoded')) {
    $script:Lanes += @{ caseId = "CASE-$name"; laneId = "lane-$name"; workflowId = "lane-$name"; trackerIds = @('OPEN'); pendingFeedback = $name -eq 'pending' }
    $script:ImageStates["lane-$name"] = @{ workflow = @{ id = "lane-$name"; revision = 1 }; media = @{ id = "media-$name"; revision = 1; artifacts = @(0..2 | ForEach-Object { @{ id = "screen-$name-$_"; kind = 'screenshot'; selected = $true; order = $_ } }) }; projections = @{ projections = @(@{ trackerId = 'OPEN'; uploadReleaseName = $validReport.uploadReleaseName; artifacts = @{ screenshotCount = 3; imageHosting = $true; description = $scenario -ne 'images-only' } }) } }
    if ($name -ne 'undecoded') { Add-Result "CASE-$name" "lane-$name" 'image_decode' 'pass' 'synthetic_decode' }
  }
  if ($scenario -in @('zero-image-budget', 'description-only')) {
    $script:ImageStates['lane-a'].projections.projections[0].artifacts = @{ screenshotCount = 0; imageHosting = $false; description = $scenario -eq 'description-only' }
    $script:ImageStates['lane-a'].media.artifacts = @()
    $script:Results = @($script:Results | Where-Object laneId -CNE 'lane-a')
  }
  if ($scenario -in @('coverage-none', 'coverage-none-budget', 'coverage-none-no-hosts')) {
    foreach ($current in $script:ImageStates.Values) { $current.projections.projections[0].artifacts = @{ screenshotCount = 0; imageHosting = $false; description = $false } }
  }
  if ($scenario -in @('projected-count', 'insufficient-selection')) {
    foreach ($name in @('a', 'b')) {
      $current = $script:ImageStates["lane-$name"]
      $current.projections.projections[0].artifacts = @{ screenshotCount = 4 }
      if ($scenario -eq 'projected-count') { $current.media.artifacts += @{ id = "screen-$name-3"; kind = 'screenshot'; selected = $true; order = 3 } }
    }
  }
  if ($scenario -eq 'resume') {
    $current = $script:ImageStates['lane-a']
    $current.media.imageRequirementsPrepared = $true
    $current.media.artifacts += @(0..2 | ForEach-Object { @{ id = "hosted-$_"; kind = 'hosted_image'; host = 'ptscreens' } })
    [IO.File]::AppendAllText($journalPath, '{"kind":"upload_pending","sources":["a","b","c"]}' + "`n")
    Add-Result 'CASE-a' 'lane-a' 'descriptions_ready' 'needs_input' 'previous_typed_action'
    Add-Result 'CASE-a' 'lane-a' 'dry_run' 'blocked' 'hosting_or_description_prerequisites_unfulfilled'
  }
  foreach ($deferred in @('sat', 'deferred', 'covered')) {
    $script:Lanes += @{ caseId = 'CASE-a'; laneId = "lane-$deferred"; workflowId = "lane-$deferred"; trackerIds = @('OPEN'); sat = $deferred -eq 'sat' }
    $script:ImageStates["lane-$deferred"] = $script:ImageStates['lane-a']
    if ($deferred -ne 'sat') {
      $reason = $(if ($deferred -eq 'deferred') { 'source_capture_deferred_to_normal_lane' } else { 'source_capture_covered_in_another_lane' })
      Add-Result 'CASE-a' "lane-$deferred" 'media_ready' 'not_applicable' $reason
    }
  }
  Invoke-LiveImageChecks @{}
  Assert-ImageCheck (@($script:ImageGoals | Where-Object { $_ -match '^lane-(sat|deferred|covered):' }).Count -eq 0) 'deferred_lane_resumed_content'
  Assert-ImageCheck (@($script:ImageDispatches | Where-Object lane -IN @('lane-sat', 'lane-deferred', 'lane-covered')).Count -eq 0) 'deferred_lane_uploaded'
  Assert-ImageCheck (@($script:Results | Where-Object { $_.laneId -in @('lane-sat', 'lane-deferred', 'lane-covered') -and $_.stage -ne 'media_ready' }).Count -eq 0) 'deferred_lane_reported_content_blocker'
  Assert-ImageCheck (@($script:ImageDispatches | Where-Object lane -IN @('lane-pending', 'lane-undecoded')).Count -eq 0) 'pending_or_undecoded_lane_uploaded'
  $expected = switch ($scenario) { 'coverage' { 3 }; 'coverage-none-budget' { 0 }; 'coverage-none-no-hosts' { 0 }; 'stopped' { 0 }; 'resume' { 0 }; 'insufficient-selection' { 0 }; 'budget' { 1 }; 'zero-image-budget' { 0 }; 'description-only' { 0 }; default { 2 } }
  Assert-ImageCheck ($script:ImageDispatches.Count -eq $expected) "image_dispatch_count_wrong_$scenario"
  $dryRuns = @($script:ImageGoals | Where-Object { $_.EndsWith(':dry_run') })
  $expectedDryRuns = switch ($scenario) { 'budget' { 1 }; 'resume' { 1 }; 'feedback' { 1 }; 'stopped' { 0 }; 'insufficient-selection' { 0 }; 'zero-image-budget' { 1 }; 'description-only' { 1 }; default { 2 } }
  Assert-ImageCheck ($dryRuns.Count -eq $expectedDryRuns) "eligible_dry_run_coverage_wrong_$scenario"
  if ($scenario -eq 'coverage') { Assert-ImageCheck (@($script:ImageDispatches | Where-Object host -EQ 'pixhost').Count -eq 1) 'extra_host_not_exercised_once' }
  if ($scenario -eq 'coverage-none') {
    Assert-ImageCheck (@($script:ImageDispatches | Where-Object { -not $_.explicitHost -or $_.count -ne 1 }).Count -eq 0) 'image_free_coverage_dispatched_required_host_plan'
    Assert-ImageCheck (($script:ImageDispatches.host -join ',') -ceq 'ptscreens,pixhost' -and ($script:ImageBrowserLanes -join ',') -ceq 'lane-a') 'image_free_coverage_hosts_or_preview_missing'
  }
  if ($scenario -eq 'coverage-none-budget') { Assert-ImageCheck (@($script:Results | Where-Object reason -EQ 'image_budget_exhausted').Count -eq 2) 'image_free_coverage_bypassed_budget' }
  if ($scenario -eq 'coverage-none-no-hosts') { Assert-ImageCheck (@($script:Results | Where-Object { $_.stage -eq 'image_host_coverage' -and $_.status -eq 'not_applicable' -and $_.reason -eq 'no_configured_compatible_hosts' }).Count -eq 3) 'empty_host_coverage_silently_omitted' }
  if ($scenario -eq 'feedback') { Assert-ImageCheck (($script:ImageFeedback -join ',') -ceq 'lane-a' -and $dryRuns[0] -ceq 'lane-b:dry_run') 'unknown_feedback_not_preserved' }
  if ($scenario -eq 'budget') { Assert-ImageCheck (@($script:Results | Where-Object { $_.laneId -eq 'lane-b' -and $_.reason -eq 'image_budget_insufficient_for_lane' }).Count -eq 1) 'budget_exhaustion_not_reported' }
  if ($scenario -eq 'resume') {
    Assert-ImageCheck ((Get-LiveImageBudgetRemaining) -eq 0 -and $dryRuns[0] -ceq 'lane-a:dry_run') 'resume_reuploaded_or_skipped_prepared_lane'
    Assert-ImageCheck (@($script:Results | Where-Object { $_.laneId -eq 'lane-a' -and $_.status -in @('blocked', 'needs_input') }).Count -eq 0) 'resume_kept_superseded_blockers'
  }
  if ($scenario -eq 'projected-count') { Assert-ImageCheck (@($script:ImageDispatches | Where-Object count -NE 4).Count -eq 0) 'workflow_required_screenshot_count_ignored' }
  if ($scenario -eq 'insufficient-selection') { Assert-ImageCheck (@($script:Results | Where-Object reason -EQ 'selected_local_images_insufficient').Count -eq 2) 'insufficient_selected_screenshots_uploaded' }
  if ($scenario -in @('zero-image-budget', 'description-only')) {
    Assert-ImageCheck ($dryRuns[0] -ceq 'lane-a:dry_run' -and $script:ImageBrowserLanes.Count -eq 0) 'zero_image_lane_did_not_bypass_hosting'
    Assert-ImageCheck (@($script:Results | Where-Object { $_.laneId -eq 'lane-b' -and $_.reason -eq 'image_budget_insufficient_for_lane' }).Count -eq 1) 'image_lane_budget_gate_bypassed'
  }
}
$script:Run.suite = 'Full'; $script:ImageGoals = @()
$resumeLane = @{ caseId = 'CASE-NONE'; laneId = 'lane-none'; trackerIds = @('NONE') }
$resumeState = @{ workflow = @{ id = 'lane-none' }; media = @{ status = 'skipped' }; projections = @{ projections = @(@{ trackerId = 'NONE'; artifacts = @{ screenshotCount = 0; description = $false } }) } }
$null = Resume-Lane $resumeLane $resumeState 'duplicates_decided'
Assert-ImageCheck (($script:ImageGoals -join ',') -ceq 'lane-none:media_ready') 'no_content_resume_forced_media'
foreach ($deferred in @('sat', 'deferred', 'covered')) {
  $script:ImageGoals = @()
  $deferredLane = @($script:Lanes | Where-Object laneId -CEQ "lane-$deferred")[0]
  $null = Resume-Lane $deferredLane $resumeState 'duplicates_decided'
  Assert-ImageCheck ($script:ImageGoals.Count -eq 0) 'feedback_resume_captured_deferred_lane'
}
$handoffAssignment = $runnerAst.Find({ param($node)
  $node -is [Management.Automation.Language.AssignmentStatementAst] -and $node.Left.Extent.Text -ceq '$browserHandoff'
}, $true)
$handoffTable = $handoffAssignment.Find({ param($node) $node -is [Management.Automation.Language.HashtableAst] }, $true)
$handoffLanes = @($handoffTable.KeyValuePairs | Where-Object { $_.Item1.Value -ceq 'lanes' })
$ownedLanes = @(. ([scriptblock]::Create($handoffLanes[0].Item2.Extent.Text)))
Assert-ImageCheck (($ownedLanes.laneId -join ',') -ceq 'lane-a,lane-b,lane-pending,lane-undecoded') 'browser_handoff_lost_lane_ownership_or_pending_feedback'
$script:Lanes = @(@{ caseId = 'EXPLICIT'; laneId = 'explicit-sat'; sat = $true }); $script:Results = @()
Assert-ImageCheck (Test-LiveContentLane $script:Lanes[0]) 'explicit_sat_content_disabled'
$feedbackStatement = @($runnerAst.FindAll({ param($node)
  $node -is [Management.Automation.Language.IfStatementAst] -and $node.Extent.Text.StartsWith('if ($feedback.goal -in')
}, $true))
Assert-ImageCheck ($feedbackStatement.Count -eq 1) 'feedback_media_binding_missing'
foreach ($goal in @('prepared', 'trackers_assessed', 'duplicates_decided', 'media_ready', 'descriptions_ready', 'dry_run')) {
  foreach ($count in @(0, 5)) {
    foreach ($coverage in @($false, $true)) {
      $script:Run = @{ suite = 'Full'; imageHostCoverage = $coverage; budgets = @{ screenshotCount = 3 } }
      $feedback = @{ goal = $goal }; $intent = @{}; $lane = $resumeLane
      $laterGoal = $goal -in @('media_ready', 'descriptions_ready', 'dry_run')
      $current = $(if ($laterGoal) { @{ projections = @{ projections = @(@{ trackerId = 'NONE'; artifacts = @{ screenshotCount = $count } }) } } } else { @{} })
      . ([scriptblock]::Create($feedbackStatement[0].Extent.Text))
      if ($laterGoal) {
        $expectedCount = $(if ($count -gt 0) { 5 } elseif ($coverage) { 1 } else { 0 })
        Assert-ImageCheck ($intent.media.screenshotCount -eq $expectedCount) 'feedback_media_count_changed'
      } else { Assert-ImageCheck (-not $intent.ContainsKey('media')) 'early_feedback_requires_missing_projections' }
    }
  }
}
Write-Host 'PASS: image coverage respects compatibility, budgets, decoded/feedback authority, all eligible dry runs, and prepared payload identity.'
