#Requires -Version 7.0

function Get-LiveImageCoverageHosts($Metadata, $Config, [string[]]$TrackerIDs) {
  # Read only configured choices. The exported config must remain in memory;
  # credentials are neither evidence nor permission to probe every known host.
  $configured = @($Config.ImageHosting.Host1, $Config.ImageHosting.Host2, $Config.ImageHosting.Host3,
    $Config.ImageHosting.Host4, $Config.ImageHosting.Host5, $Config.ImageHosting.Host6)
  foreach ($tracker in $TrackerIDs) { $configured += $Config.Trackers.Trackers[$tracker].ImageHost }
  foreach ($candidate in @($configured | Where-Object { $_ -is [string] -and $_.Trim() } | ForEach-Object { $_.Trim().ToLowerInvariant() } | Select-Object -Unique)) {
    if ($candidate -cnotin $Metadata.UploadHosts) { continue }
    $owner = $Metadata.OwnedHosts[$candidate]
    foreach ($tracker in $TrackerIDs) {
      if ($owner -and $owner -cne $tracker) { continue }
      $allowed = @($Metadata.TrackerUploadHosts[$tracker])
      if (-not $Metadata.TrackerUploadHosts.ContainsKey($tracker) -or $candidate -cin $allowed) { $candidate; break }
    }
  }
}

function Get-LiveImageBudgetRemaining {
  $remaining = $script:Run.budgets.maxImages
  foreach ($line in @(Get-Content -LiteralPath (Join-Path $script:RunDir 'image-effects.private.jsonl') -ErrorAction Stop)) {
    if (-not $line.Trim()) { continue }
    $record = $line | ConvertFrom-Json -AsHashtable
    if ($record.kind -eq 'upload_pending') { $remaining -= @($record.sources).Count }
  }
  [Math]::Max(0, $remaining)
}

function Record-LiveDryRunPayload($Lane, $Current) {
  $reason = 'prepared_names_artifacts_and_no_seed_verified'
  $reports = @($Current.dryRun.reports | Where-Object status -EQ 'completed')
  if ($Current.dryRun.noSeed -ne $true -or $reports.Count -eq 0) { $reason = 'dry_run_completed_report_required' }
  $expectedTargets = @($Lane.trackerIds | Sort-Object -Unique)
  if ($expectedTargets.Count -eq 0 -or @($Current.dryRun.reports).Count -ne $reports.Count -or
      (($Current.dryRun.trackerIds | Sort-Object) -join ',') -cne ($expectedTargets -join ',') -or
      (($reports.trackerId | Sort-Object) -join ',') -cne ($expectedTargets -join ',')) {
    $reason = 'dry_run_complete_target_reports_required'
  }
  foreach ($report in $reports) {
    $projection = @($Current.projections.projections | Where-Object trackerId -CEQ $report.trackerId)
    if ($report.trackerId -cnotin $Lane.trackerIds -or $projection.Count -ne 1 -or
        -not $report.uploadReleaseName -or $report.uploadReleaseName -cne $projection[0].uploadReleaseName) {
      $reason = 'dry_run_projected_name_mismatch'
    }
    foreach ($field in @($report.fields | Where-Object key -CEQ 'name')) {
      if ($field.value -cne $report.uploadReleaseName) { $reason = 'dry_run_payload_name_mismatch' }
    }
    if (-not $report.preparedOperationId -or -not $report.torrentArtifactId -or -not $report.torrentFingerprint -or -not $report.semanticFingerprint) {
      $reason = 'dry_run_prepared_artifact_missing'
    }
    if ($report.clientInjection.status -ne 'skipped') { $reason = 'dry_run_client_injection_not_skipped' }
  }
  Add-Result $Lane.caseId $Lane.laneId 'dry_run_payload' $(if ($reason -eq 'prepared_names_artifacts_and_no_seed_verified') { 'pass' } else { 'fail' }) $reason @{ completedReports = $reports.Count }
}

function Invoke-LiveImageChecks($BrowserHandoff) {
  $decoded = @($script:Results | Where-Object { $_.stage -eq 'image_decode' -and $_.status -eq 'pass' } | Select-Object -ExpandProperty laneId -Unique)
  $lanes = @($script:Lanes | Where-Object { $_.laneId -cin $decoded -and -not $_.pendingFeedback })
  if ($lanes.Count -eq 0) { Add-Result '' '' 'image_host' 'blocked' 'local_decode_required'; return }
  $coverage = @{}
  $coverageTargets = @{}
  if ($script:Run.imageHostCoverage) {
    $metadata = Invoke-LiveAPI 'GetImageHostPolicyMetadata'
    # GetConfig returns an encrypted JSON string. Extract only host IDs; never persist it.
    $config = (Invoke-LiveAPI 'GetConfig') | ConvertFrom-Json -AsHashtable
    foreach ($lane in $lanes) { $coverageTargets[$lane.laneId] = @(Get-LiveImageCoverageHosts $metadata $config $lane.trackerIds) }
    $config = $null
  }
  $hostedLanes = @()
  foreach ($lane in $lanes) {
    if ($script:RemoteStop) { Add-Result $lane.caseId $lane.laneId 'image_host' 'blocked' 'remote_work_stopped'; continue }
    $script:Results = @($script:Results | Where-Object {
      $_.laneId -cne $lane.laneId -or $_.stage -notin @('image_host', 'image_host_coverage', 'descriptions_ready', 'dry_run', 'dry_run_payload')
    })
    Write-Host "case=$($lane.caseId) stage=image_host decision=checking"
    $stage = 'image_host'
    $failureCodes = @()
    try {
      $current = Invoke-LiveAPI 'GetReleaseWorkflow' @{ workflowId = $lane.workflowId }
      if (@(Get-PendingActions $current).Count -gt 0) { Save-Feedback $lane $current 'media_ready'; Add-Result $lane.caseId $lane.laneId $stage 'needs_input' 'typed_action_required'; continue }
      $requiredCount = $script:Run.budgets.screenshotCount
      foreach ($projection in @($current.projections.projections | Where-Object { $_.trackerId -cin $lane.trackerIds })) {
        $requiredCount = [Math]::Max($requiredCount, [int]$projection.artifacts.screenshotCount)
      }
      $local = @($current.media.artifacts | Where-Object { $_.kind -eq 'screenshot' -and $_.selected } | Sort-Object order | Select-Object -First $requiredCount)
      if ($local.Count -eq 0) { throw 'selected_local_images_required' }
      if ($local.Count -lt $requiredCount) { throw 'selected_local_images_insufficient' }
      $retained = @($current.media.artifacts | Where-Object kind -EQ 'hosted_image')
      $requirementsPrepared = $current.media.imageRequirementsPrepared -and $retained.Count -ge $local.Count
      # The production command revalidates and reuses hosted images on resume.
      # Its journal guard still enforces the remaining budget if dispatch is needed.
      if (-not $requirementsPrepared -and (Get-LiveImageBudgetRemaining) -lt $local.Count) { throw 'image_budget_insufficient_for_lane' }
      $command = @{ workflowId = $current.workflow.id; expectedRevision = $current.workflow.revision; media = @{ id = $current.media.id; revision = $current.media.revision }; artifactIds = @($local.id); idempotencyKey = [guid]::NewGuid().ToString('N') }
      $current = Wait-Workflow (Invoke-LiveAPI 'UploadReleaseWorkflowImages' $command -ExpectedStatus 202) (Join-Path $script:RunDir "snapshots/$($lane.laneId).private.json")
      $failureCodes = @(Get-LiveFailureCodes $current)
      if (@(Get-PendingActions $current).Count -gt 0) { Save-Feedback $lane $current 'media_ready'; Add-Result $lane.caseId $lane.laneId $stage 'needs_input' 'typed_action_required'; continue }
      $hosted = @($current.media.artifacts | Where-Object kind -EQ 'hosted_image')
      if (-not $current.media.imageRequirementsPrepared -or $hosted.Count -lt $local.Count) { throw 'required_hosting_incomplete' }
      Add-Result $lane.caseId $lane.laneId 'image_host' 'pass' 'required_images_hosted' @{ selected = $local.Count; hosted = $hosted.Count }
      $hostedLanes += $lane
      if ($script:Run.imageHostCoverage) {
        foreach ($hostID in @($hosted.host | Where-Object { $_ -cin $metadata.UploadHosts } | Select-Object -Unique)) {
          if ($coverage.ContainsKey($hostID)) { continue }
          $coverage[$hostID] = $true
          Add-Result $lane.caseId $lane.laneId 'image_host_coverage' 'pass' 'required_upload_covered_host' @{ host = $hostID }
        }
      }
      foreach ($hostID in $coverageTargets[$lane.laneId]) {
        if ($script:RemoteStop) { break }
        if ($coverage.ContainsKey($hostID)) { continue }
        $coverage[$hostID] = $true
        if ((Get-LiveImageBudgetRemaining) -lt 1) {
          Add-Result $lane.caseId $lane.laneId 'image_host_coverage' 'blocked' 'image_budget_exhausted' @{ host = $hostID }
          continue
        }
        $stage = 'image_host_coverage'
        $command = @{ workflowId = $current.workflow.id; expectedRevision = $current.workflow.revision; media = @{ id = $current.media.id; revision = $current.media.revision }; artifactIds = @($local[0].id); host = $hostID; idempotencyKey = [guid]::NewGuid().ToString('N') }
        $current = Wait-Workflow (Invoke-LiveAPI 'UploadReleaseWorkflowImages' $command -ExpectedStatus 202) (Join-Path $script:RunDir "snapshots/$($lane.laneId).private.json")
        $failureCodes = @(Get-LiveFailureCodes $current)
        if (@(Get-PendingActions $current).Count -gt 0) {
          Save-Feedback $lane $current 'media_ready'
          Add-Result $lane.caseId $lane.laneId 'image_host_coverage' 'needs_input' 'typed_action_required' @{ host = $hostID }
          break
        }
        $uploaded = @($current.media.artifacts | Where-Object { $_.kind -eq 'hosted_image' -and $_.host -ceq $hostID }).Count
        Add-Result $lane.caseId $lane.laneId 'image_host_coverage' $(if ($uploaded -gt 0) { 'pass' } else { 'blocked' }) $(if ($uploaded -gt 0) { 'configured_host_uploaded' } else { 'configured_host_upload_incomplete' }) @{ host = $hostID }
      }
      if (@(Get-PendingActions $current).Count -gt 0 -or $script:RemoteStop) { continue }
      if ($script:Run.suite -notin @('Smoke', 'Full')) { continue }
      $intent = @{ executionMode = $script:Run.executionMode; interaction = 'unattended'; trackerIds = $lane.trackerIds; noSeed = $true; skipRemoteDuplicates = $false; descriptions = @{ options = @{ NoSeed = $true; InteractionMode = 'unattended' }; imageHost = @{} } }
      foreach ($stage in @('descriptions_ready', 'dry_run')) {
        $current = Continue-Lane $lane $stage $current $intent
        if ((Record-Stage $lane $current $stage) -ne 'pass') { break }
        if ($stage -eq 'dry_run') { Record-LiveDryRunPayload $lane $current }
      }
    } catch {
      $reason = $_.Exception.Message
      if ($reason -cnotmatch '^[a-z][a-z0-9_]{0,80}$') { $reason = 'image_or_dry_run_check_incomplete' }
      Add-Result $lane.caseId $lane.laneId $stage 'blocked' $reason @{ failureCodes = $failureCodes }
      [IO.File]::AppendAllText((Join-Path $script:RunDir 'errors.private.log'), ($_ | Out-String))
      if ($reason -in @('request_budget_exhausted', 'workflow_deadline_exceeded', 'api_request_failed')) { $script:RemoteStop = $true }
    }
  }
  if ($hostedLanes.Count -gt 0 -and -not $script:RemoteStop) {
    $BrowserHandoff.hostedOnly = $true
    $BrowserHandoff.lanes = $hostedLanes
    $BrowserHandoff.remainingRequests = [Math]::Max(0, $script:Run.budgets.maxRequests - $script:RequestCount)
    Write-PrivateJson (Join-Path $script:RunDir 'browser.private.json') $BrowserHandoff
    try { $null = Invoke-BrowserCheck 'hosted' }
    catch { Add-Result '' '' 'hosted_preview' 'fail' 'hosted_preview_failed' }
  }
  if ($script:Run.suite -in @('Smoke', 'Full')) {
    foreach ($lane in $lanes) {
      if (@($script:Results | Where-Object { $_.laneId -ceq $lane.laneId -and $_.stage -eq 'dry_run' }).Count -eq 0) {
        Add-Result $lane.caseId $lane.laneId 'dry_run' 'blocked' 'hosting_or_description_prerequisites_unfulfilled'
      }
    }
  }
}
