#Requires -Version 7.0
[CmdletBinding(DefaultParameterSetName = 'New')]
param(
  [Parameter(ParameterSetName = 'New')][ValidateSet('Smoke', 'Screenshots', 'Dupe', 'Full')][string]$Suite = 'Smoke',
  [Parameter(ParameterSetName = 'New')][ValidatePattern('^[A-Z0-9]+$')][string]$Tracker,
  [Parameter(ParameterSetName = 'New')][string[]]$CaseId,
  [Parameter(ParameterSetName = 'New')][switch]$Sat,
  [Parameter(ParameterSetName = 'New')][switch]$DebugCoverage,
  [Parameter(ParameterSetName = 'New')][switch]$UploadImages,
  [Parameter(ParameterSetName = 'New')][switch]$ImageHostCoverage,
  [Parameter(ParameterSetName = 'New')][ValidateRange(1, 500)][int]$MaxImages = 3,
  [Parameter(ParameterSetName = 'New')][ValidateRange(1, 10)][int]$ScreenshotCount = 3,
  [Parameter(ParameterSetName = 'New')][ValidateRange(10, 2000)][int]$MaxRequests = 200,
  [Parameter(ParameterSetName = 'New')][ValidateRange(30, 3600)][int]$TimeoutSeconds = 900,
  [Parameter(ParameterSetName = 'New')][string]$Config,
  [Parameter(ParameterSetName = 'New')][string]$Corpus = (Join-Path $env:LOCALAPPDATA 'upbrr-live-testing/media-corpus.private.json'),
  [Parameter(ParameterSetName = 'New')][switch]$ValidateOnly,
  [Parameter(Mandatory, ParameterSetName = 'Resume')][ValidatePattern('^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$')][string]$ResumeRun,
  [Parameter(Mandatory, ParameterSetName = 'Cleanup')][ValidatePattern('^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$')][string]$CleanupRun
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'functions.ps1')
$script:RepoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$privateRoot = Join-Path $env:LOCALAPPDATA 'upbrr-live-testing'
$script:BaseURL = 'http://127.0.0.1:7480'
$script:Results = @(); $script:Feedback = @(); $script:Lanes = @()
$script:RequestCount = 0; $script:RemoteStop = $false
$script:Cleanup = @{ deleted = 0; retained = 0; pending = 0; unknown = 0; failed = 0; state = 'not_started' }
$server = $null; $runnerLock = $null; $initialized = $false; $keepForFeedback = $false
$exitCode = 0; $script:Run = $null; $script:RunDir = $null

try {
  if ($PSCmdlet.ParameterSetName -eq 'New') {
    if ($ImageHostCoverage) { $UploadImages = $true }
    $Corpus = Assert-PrivatePath $Corpus $privateRoot
    $scenariosSHA256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'scenarios.json')).Hash
    $scenarios = Read-PrivateJson (Join-Path $PSScriptRoot 'scenarios.json')
    $selected = switch ($Suite) { 'Smoke' { $scenarios.smoke }; 'Dupe' { $scenarios.dupe }; default { $scenarios.screenshots } }
    if ($CaseId) {
      $selected = @($CaseId | Select-Object -Unique)
    }
    $corpusSHA256 = (Get-FileHash -LiteralPath $Corpus).Hash
    $entries = @(Read-Corpus $Corpus $selected)
    if ((Get-FileHash -LiteralPath $Corpus).Hash -cne $corpusSHA256) { throw 'corpus_changed_during_run' }
    # The production temporary layout uses a sanitized basename, not source identity.
    foreach ($group in @($entries | Where-Object status -EQ 'ready' | Group-Object { Get-BDInfoTempName $_.case })) {
      if (@($group.Group | Where-Object { $_.case.bdmv_selection }).Count -gt 0 -and
          @($group.Group.stat.input_path_hash | Select-Object -Unique).Count -gt 1) { throw 'bdinfo_temp_name_collision_use_separate_runs' }
    }
    Write-Host ('Live testing: suite={0} cases={1} trackerScope={2} images={3}' -f $Suite, $entries.Count, $(if ($Tracker) { $Tracker } else { 'config_defaults' }), $(if ($UploadImages) { $MaxImages } else { 0 }))
    if ($ValidateOnly) {
      foreach ($entry in $entries) { Write-Host ('case={0} status={1} reason={2}' -f $entry.case.case_id, $entry.status, $entry.reason) }
      if (@($entries | Where-Object status -eq 'blocked').Count -gt 0) { exit 2 }
      exit 0
    }
  }

  New-Item -ItemType Directory -Path $privateRoot -Force | Out-Null
  $runnerLock = [IO.File]::Open((Join-Path $privateRoot 'runner.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
  if ($PSCmdlet.ParameterSetName -eq 'New') {
    # A pending run must be resumed or explicitly cleaned before creating another profile.
    foreach ($manifest in @(Get-ChildItem -LiteralPath (Join-Path $privateRoot 'runs') -Filter run.json -File -Recurse -ErrorAction SilentlyContinue)) {
      $previous = Read-PrivateJson $manifest.FullName
      if ($previous.state -notin @('cleaned', 'validation_failed')) { throw 'previous_run_requires_resume_or_cleanup' }
    }
    $runID = [datetime]::UtcNow.ToString('yyyyMMddTHHmmssZ') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 10)
    $script:RunDir = Assert-PrivatePath (Join-Path $privateRoot "runs/$runID") $privateRoot
    $buildDir = Assert-PrivatePath (Join-Path $privateRoot "builds/$runID") $privateRoot
    New-Item -ItemType Directory -Path $buildDir -Force | Out-Null
    if ((Get-PSDrive -Name ([IO.Path]::GetPathRoot($buildDir).TrimEnd('\', ':'))).Free -lt 5GB) { throw 'workspace_capacity_low' }
    foreach ($tool in @('go', 'node', 'pnpm', 'pwsh', 'git', 'ffmpeg', 'ffprobe')) {
      if (-not (Get-Command $tool -CommandType Application -ErrorAction SilentlyContinue)) { throw 'required_tool_missing' }
    }
    if (-not (Test-Path -LiteralPath (Join-Path $script:RepoRoot 'webui/node_modules/@playwright/test/cli.js'))) { throw 'playwright_dependency_missing' }
    $candidateBefore = Get-CandidateState $buildDir
    $bdinfoScannerFingerprint = Get-BDInfoScannerFingerprint
    Write-Host 'Building embedded production candidate and running deterministic live-test safety gates.'
    Invoke-OwnedProcess (Get-ToolPath 'pnpm') @('--dir', 'webui', 'run', 'build') (Join-Path $buildDir 'frontend')
    Invoke-OwnedProcess (Get-ToolPath 'pwsh') @('-NoProfile', '-File', (Join-Path $script:RepoRoot 'scripts/sync-webui-assets.ps1')) (Join-Path $buildDir 'assets')
    Invoke-OwnedProcess (Get-ToolPath 'go') @('test', '-race', '-timeout', '5m', './internal/livetest') (Join-Path $buildDir 'safety-profile') 360
    Invoke-OwnedProcess (Get-ToolPath 'go') @('test', '-race', '-timeout', '20m', './cmd/upbrr', './pkg/api', './internal/core', './internal/webserver', './internal/releaseworkflow', './internal/torrentclient', './internal/trackers', './internal/imagehosting', './internal/configstore', './internal/services/db', './internal/livetest', '-run', 'LiveTest|LiveTesting|LiveImages') (Join-Path $buildDir 'safety') 1500
    $buildIdentifier = 'live-' + $runID
    $builtBinary = Join-Path $buildDir 'upbrr.exe'
    Invoke-OwnedProcess (Get-ToolPath 'go') @('build', '-tags=', '-ldflags', "-X main.buildIdentifier=$buildIdentifier", '-o', $builtBinary, './cmd/upbrr') (Join-Path $buildDir 'backend')
    if ((Get-BDInfoScannerFingerprint) -cne $bdinfoScannerFingerprint) { throw 'bdinfo_scanner_changed_during_build' }
    $candidateAfter = Get-CandidateState $buildDir
    if ((ConvertTo-Json $candidateBefore -Depth 20 -Compress) -cne (ConvertTo-Json $candidateAfter -Depth 20 -Compress)) { throw 'candidate_changed_during_build' }
    if ((Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'scenarios.json')).Hash -cne $scenariosSHA256) { throw 'scenarios_changed_during_run' }
    if ((Get-FileHash -LiteralPath $Corpus).Hash -cne $corpusSHA256) { throw 'corpus_changed_during_run' }
    $initArgs = @('live-test', 'init', '--run-dir', $script:RunDir)
    if ($Config) { $initArgs += @('--config', $Config) }
    if ($UploadImages) { $initArgs += '--prefer-deletable-hosts' }
    Invoke-OwnedProcess $builtBinary $initArgs (Join-Path $buildDir 'init')
    $profile = Read-PrivateJson (Join-Path $buildDir 'init.stdout.private.log')
    $initialized = $true
    if ($profile.runId -cne $runID -or $profile.runDir -cne $script:RunDir -or @($profile.defaultTrackers).Count -eq 0) { throw 'profile_identity_invalid' }
    # Record recoverable ownership immediately after init, before copying tools or collecting optional metadata.
    $script:Binary = $builtBinary
    $script:Run = @{ version = 1; runId = $runID; state = 'running'; binaryPath = $builtBinary; binarySha256 = (Get-FileHash -LiteralPath $builtBinary).Hash; buildIdentifier = $buildIdentifier; buildLogs = $buildDir; suite = $Suite; caseIds = @($selected); selectedTrackers = @($profile.defaultTrackers); executionMode = $(if ($DebugCoverage) { 'debug' } else { 'normal' }); gaps = @($scenarios.gaps) }
    Write-PrivateJson (Join-Path $script:RunDir 'profile.private.json') $profile
    Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
    $trackers = @($profile.defaultTrackers)
    if ($Tracker) { $trackers = @($Tracker) }
    foreach ($id in $trackers) { if ($id -cnotmatch '^[A-Z0-9]+$') { throw 'profile_tracker_id_invalid' } }
    New-Item -ItemType Directory -Path (Join-Path $script:RunDir 'tools'), (Join-Path $script:RunDir 'snapshots') -Force | Out-Null
    $script:Binary = Join-Path $script:RunDir 'tools/upbrr.exe'
    Copy-Item -LiteralPath $builtBinary -Destination $script:Binary
    $versions = @{}
    foreach ($tool in @('go', 'node', 'pnpm', 'ffmpeg', 'ffprobe', 'mediainfo', 'BDInfo')) {
      $available = Get-Command $tool -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
      if (-not $available) { $versions[$tool] = @{ available = $false }; continue }
      if ($tool -eq 'BDInfo') {
        $versions[$tool] = @{ available = $true; binarySha256 = (Get-FileHash -LiteralPath $available.Source).Hash; version = [Diagnostics.FileVersionInfo]::GetVersionInfo($available.Source).FileVersion }
        continue
      }
      $versionArgs = switch ($tool) { 'go' { @('version') }; { $_ -in @('ffmpeg', 'ffprobe') } { @('-version') }; default { @('--version') } }
      try {
        Invoke-OwnedProcess $available.Source $versionArgs (Join-Path $buildDir "version-$tool") 30
        $versions[$tool] = @{ available = $true; binarySha256 = (Get-FileHash -LiteralPath $available.Source).Hash; version = (Get-Content -LiteralPath (Join-Path $buildDir "version-$tool.stdout.private.log") -TotalCount 1) }
      } catch { $versions[$tool] = @{ available = $true; version = 'probe_failed' } }
    }
    $assets = @(Get-ChildItem -LiteralPath (Join-Path $script:RepoRoot 'webui/dist') -File -Recurse | Sort-Object FullName | ForEach-Object {
      @{ path = [IO.Path]::GetRelativePath((Join-Path $script:RepoRoot 'webui/dist'), $_.FullName); sha256 = (Get-FileHash -LiteralPath $_.FullName).Hash }
    })
    $script:Run = @{
      version = 1; runId = $runID; state = 'running'; createdAt = [datetime]::UtcNow.ToString('o')
      buildIdentifier = $buildIdentifier; binaryPath = $script:Binary; binarySha256 = (Get-FileHash -LiteralPath $script:Binary).Hash
      candidate = $candidateAfter; frontend = $assets; tools = $versions; rules = @(Get-RuleState)
      configFingerprint = $profile.sourceFingerprint; profileConfigSha256 = (Get-FileHash -LiteralPath $profile.configPath).Hash
      configDefaultTrackers = @($profile.defaultTrackers); selectedTrackers = $trackers; trackerScope = $(if ($Tracker) { 'explicit' } else { 'config_defaults' })
      suite = $Suite; caseIds = @($selected); corpusPath = $Corpus; corpusSha256 = $corpusSHA256
      sat = [bool]$Sat; executionMode = $(if ($DebugCoverage) { 'debug' } else { 'normal' })
      imageHostCoverage = [bool]$ImageHostCoverage; preferDeletableHosts = [bool]$UploadImages
      budgets = @{ maxImages = $(if ($UploadImages) { $MaxImages } else { 0 }); maxRequests = $MaxRequests; timeoutSeconds = $TimeoutSeconds; screenshotCount = $ScreenshotCount }
      buildLogs = $buildDir; expectedSafetyDenials = 0; requests = 0; gaps = @($scenarios.gaps)
    }
    Write-PrivateJson (Join-Path $script:RunDir 'profile.private.json') $profile
    Write-PrivateJson (Join-Path $script:RunDir 'corpus.private.json') $entries
    Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
  } else {
    $runID = $(if ($ResumeRun) { $ResumeRun } else { $CleanupRun })
    $script:RunDir = Assert-PrivatePath (Join-Path $privateRoot "runs/$runID") $privateRoot
    $script:Run = Read-PrivateJson (Join-Path $script:RunDir 'run.json')
    $profile = Read-PrivateJson (Join-Path $script:RunDir 'profile.private.json')
    $script:Binary = Assert-PrivatePath $script:Run.binaryPath $privateRoot
    if ($script:Run.runId -cne $runID -or $profile.runId -cne $runID -or (Get-FileHash -LiteralPath $script:Binary).Hash -cne $script:Run.binarySha256) { throw 'saved_run_identity_mismatch' }
    if ($CleanupRun) {
      $initialized = $true
      $script:Run.state = 'cleanup_pending'
      Invoke-RunCleanup
      $script:Run.state = 'cleaned'
      Write-Host "run=$runID cleanup=complete"
    } else {
      if ($script:Run.state -in @('cleaned', 'cleanup_pending') -or (Test-Path -LiteralPath (Join-Path $script:RunDir 'cleanup-started'))) { throw 'cleaned_run_cannot_resume' }
      Stop-RecordedServer $script:RunDir
      $currentCandidate = Get-CandidateState $script:RunDir
      if ((ConvertTo-Json $currentCandidate -Depth 20 -Compress) -cne (ConvertTo-Json $script:Run.candidate -Depth 20 -Compress)) { throw 'candidate_changed_new_run_required' }
      if ((Get-FileHash -LiteralPath $script:Run.corpusPath).Hash -cne $script:Run.corpusSha256 -or (Get-FileHash -LiteralPath $profile.configPath).Hash -cne $script:Run.profileConfigSha256 -or (ConvertTo-Json @(Get-RuleState) -Depth 20 -Compress) -cne (ConvertTo-Json $script:Run.rules -Depth 20 -Compress)) { throw 'feedback_evidence_changed' }
      $entries = @(Read-PrivateJson (Join-Path $script:RunDir 'corpus.private.json'))
      foreach ($entry in $entries) {
        if ($entry.stat -and (Get-SourceFingerprint $entry.case).fingerprint -cne $entry.stat.fingerprint) { throw 'feedback_source_changed' }
      }
      $script:Feedback = @(Read-PrivateJson (Join-Path $script:RunDir 'feedback.private.json'))
      $script:Lanes = @(Read-PrivateJson (Join-Path $script:RunDir 'lanes.private.json'))
      $script:Results = @(Read-PrivateJson (Join-Path $script:RunDir 'results.private.json'))
      $script:RequestCount = $script:Run.requests
      $initialized = $true
    }
  }

  if (-not $CleanupRun) {
    $started = Start-VerifiedServer $profile
    $server = $started.handle; $processRecord = $started.process; $info = $started.info
    Set-RunTrackerScope (Invoke-LiveAPI 'ListTrackerCatalog')
    Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
    Write-Host "run=$runID ready=verified intendedTrackers=$($script:Run.selectedTrackers.Count) availableTrackers=$($script:Run.availableTrackers.Count) unavailableTrackers=$($script:Run.unavailableTrackers.Count) mode=$($script:Run.executionMode)"
    $initialCounters = $info.testRuntime
    # One separately counted refusal proves the outer guard. It has valid request shape but no existing workflow.
    Invoke-LiveAPI 'ContinueReleaseWorkflow' @{ idempotencyKey = [guid]::NewGuid().ToString('N'); goal = 'uploaded'; intent = @{ preparation = @{ SourcePath = 'live-test-negative-control' }; noSeed = $false } } -ExpectedStatus 403
    $script:Run.expectedSafetyDenials++
    Add-Result '' '' 'submission_negative_control' 'pass' 'http_403_expected'

    if ($ResumeRun) {
      Write-PrivateJson (Join-Path $script:RunDir ('feedback-input-' + [guid]::NewGuid().ToString('N') + '.private.json')) $script:Feedback
      foreach ($feedback in @($script:Feedback)) {
        if (-not $feedback.authority) { continue }
        $lane = @($script:Lanes | Where-Object laneId -CEQ $feedback.laneId)[0]
        if (-not $lane -or $lane.sourceFingerprint -cne $feedback.sourceFingerprint) { throw 'feedback_lane_mismatch' }
        $current = Invoke-LiveAPI 'GetReleaseWorkflow' @{ workflowId = $feedback.authority.workflowId }
        if ((Resolve-FeedbackAuthority $lane $feedback $current) -eq 'refreshed') {
          Add-Result $lane.caseId $lane.laneId 'feedback' 'needs_input' 'changed_evidence_requires_acceptance'
          continue
        }
        if (@($feedback.answers).Count -eq 0) { continue }
        if (-not $feedback.acceptedAt -or -not $feedback.rationale) { throw 'feedback_acceptance_receipt_missing' }
        $actions = @(Get-PendingActions $current)
        foreach ($answer in $feedback.answers) {
          $action = @($actions | Where-Object id -CEQ $answer.actionId)[0]
          $saved = @($feedback.requiredActions | Where-Object id -CEQ $answer.actionId)[0]
          if (-not $action -or -not $saved -or $answer.workflowRevision -ne $current.workflow.revision -or (Get-ActionSemantics @($action)) -cne (Get-ActionSemantics @($saved))) { throw 'feedback_action_stale' }
          if ($action.kind -in @('approve_upload', 'authenticate_tracker', 'provide_two_factor', 'reconcile_submission')) { throw 'feedback_action_not_permitted' }
        }
        $intent = @{ executionMode = $script:Run.executionMode; interaction = 'unattended'; trackerIds = $lane.trackerIds; noSeed = $true; skipRemoteDuplicates = $false; media = @{ screenshotCount = $script:Run.budgets.screenshotCount; purpose = 'final'; captureDvdMenus = $false } }
        if ($lane.preparation) {
          $intent.preparation = $lane.preparation
          $intent.preparation.Instructions = $current.factInstructions.instructions
          if (@($actions | Where-Object kind -EQ 'reprepare').Count -gt 0) { $intent.preparation.Force = $true }
        }
        $current = Continue-Lane $lane $feedback.goal $current $intent $feedback.answers
        foreach ($completedGoal in @('prepared', 'trackers_assessed', 'duplicates_decided', 'media_ready')) {
          if ($completedGoal -eq $feedback.goal) { break }
          $null = Record-Stage $lane $current $completedGoal
        }
        $status = Record-Stage $lane $current $feedback.goal
        if (@(Get-PendingActions $current).Count -eq 0) {
          $script:Results = @($script:Results | Where-Object { $_.laneId -cne $lane.laneId -or $_.stage -ne 'feedback' })
          $script:Feedback = @($script:Feedback | Where-Object laneId -CNE $lane.laneId)
          if ($status -eq 'pass') { $current = Resume-Lane $lane $current $feedback.goal }
        }
      }
    } else {
      $capturedCases = @{}
      foreach ($entry in $entries) {
        if ($entry.status -ne 'ready') {
          Add-Result $entry.case.case_id '' 'preflight' $entry.status $entry.reason
          $script:Feedback += @{ caseId = $entry.case.case_id; status = $entry.status; topic = $entry.reason; sourceFingerprint = $entry.stat.fingerprint; requiredActions = @(); answers = @(); rationale = $null }
          continue
        }
        $variants = @([bool]$script:Run.sat)
        if ($script:Run.suite -in @('Dupe', 'Full') -and -not $script:Run.sat -and $entry.case.case_id -in $scenarios.dupe) {
          $variants = @($true, $false)
        }
        # Registered configured defaults stay in their original order. Unavailable intended defaults
        # remain in the run report with a blocked result for each selected case.
        $laneTrackers = ,@($script:Run.availableTrackers)
        $representative = $entry.case.case_id -in @($scenarios.smoke + $scenarios.dupe)
        foreach ($variant in $variants) {
          foreach ($trackerChoice in $laneTrackers) {
            $ids = @($trackerChoice)
            $lane = @{ laneId = 'lane-' + ($script:Lanes.Count + 1).ToString('D4'); caseId = $entry.case.case_id; trackerIds = $ids; sat = $variant; sourceFingerprint = $entry.stat.fingerprint; workflowId = ''; goal = 'prepared' }
            $script:Lanes += $lane
            try {
              if ((Get-SourceFingerprint $entry.case).fingerprint -cne $entry.stat.fingerprint) { throw 'source_changed_during_run' }
              if ($script:RemoteStop) { throw 'remote_work_stopped' }
              $intent = @{
                executionMode = $script:Run.executionMode; interaction = 'unattended'; trackerIds = $ids; noSeed = $true; skipRemoteDuplicates = $false
                preparation = @{ SourcePath = $entry.case.input_path; Intent = 'preview'; Search = @{ Skip = $variant }; Controls = @{ Interaction = 'unattended' }; Force = $variant }
              }
              $identity = Get-CaseIdentityOverrides $entry.case
              $lane.expectedIdentity = $identity.Clone()
              if ($identity.Count -gt 0) { $intent.preparation.Instructions = @{ Identity = $identity } }
              $playlists = @(Get-CaseBDMVPlaylists $entry.case)
              if ($playlists.Count -gt 0) {
                if (-not $intent.preparation.Instructions) { $intent.preparation.Instructions = @{} }
                $intent.preparation.Instructions.Playlist = @{ Set = $true; Selected = $playlists; UseAll = $false }
                # Recollect into this profile instead of reusing cloned prepared facts.
                $intent.preparation.Force = $true
                $lane.expectedPlaylists = $playlists
              }
              $bdinfoCache = Restore-BDInfoReports $entry $profile $privateRoot $bdinfoScannerFingerprint
              $current = Continue-Lane $lane 'prepared' $null $intent
              if ($bdinfoCache) {
                $savedBDInfo = Save-BDInfoReports $bdinfoCache $entry $privateRoot $script:Run.binarySha256
                $lane.bdinfo = @{ sourceFingerprint = $bdinfoCache.sourceFingerprint; scannerFingerprint = $bdinfoCache.scannerFingerprint; restored = $bdinfoCache.restored; reports = @($savedBDInfo.reports) }
                if ($savedBDInfo) { Add-Result $lane.caseId $lane.laneId 'bdinfo_cache' 'pass' $(if ($bdinfoCache.restored) { 'reports_restored' } else { 'reports_saved' }) }
              }
              $status = Record-Stage $lane $current 'prepared'
              if (-not $current.release -or $status -eq 'fail') { continue }
              $intent.Remove('preparation')
              $current = Continue-Lane $lane 'trackers_assessed' $current $intent
              $status = Record-Stage $lane $current 'trackers_assessed'
              if (-not $current.projections) { continue }
              if ($script:Run.suite -ne 'Screenshots' -and ($script:Run.suite -ne 'Full' -or $representative)) {
                $current = Continue-Lane $lane 'duplicates_decided' $current $intent
                $status = Record-Stage $lane $current 'duplicates_decided'
                if (-not $current.dupes) { continue }
              } else { Add-Result $lane.caseId $lane.laneId 'duplicates_decided' 'not_applicable' 'local_capture_scope' }
              if ($script:Run.suite -eq 'Dupe') { continue }
              # Keep the ordinary lane's captured generation current for deferred
              # browser checks, image uploads, and dry runs, including zero-image runs.
              if ($variant -and $variants -contains $false) {
                Add-Result $lane.caseId $lane.laneId 'media_ready' 'not_applicable' 'source_capture_deferred_to_normal_lane'
                continue
              }
              if ($capturedCases.ContainsKey($lane.caseId)) {
                Add-Result $lane.caseId $lane.laneId 'media_ready' 'not_applicable' 'source_capture_covered_in_another_lane'
                continue
              }
              $intent.media = @{ screenshotCount = $script:Run.budgets.screenshotCount; purpose = 'final'; captureDvdMenus = $false }
              $current = Continue-Lane $lane 'media_ready' $current $intent
              $null = Record-Stage $lane $current 'media_ready'
              if ($current.dupes -and $script:Run.suite -eq 'Screenshots') {
                $script:Results = @($script:Results | Where-Object { $_.laneId -cne $lane.laneId -or $_.stage -cne 'duplicates_decided' })
                $null = Record-Stage $lane $current 'duplicates_decided'
              }
              if (@($current.media.artifacts | Where-Object kind -EQ 'screenshot').Count -gt 0) { $capturedCases[$lane.caseId] = $true }
              # Uploading is a separate explicit command after local browser decoding below.
            } catch {
              $reason = $_.Exception.Message
              if ($reason -cnotmatch '^[a-z][a-z0-9_]{0,80}$') { $reason = 'lane_execution_failed' }
              Add-Result $lane.caseId $lane.laneId $lane.goal 'blocked' $reason
              [IO.File]::AppendAllText((Join-Path $script:RunDir 'errors.private.log'), ($_ | Out-String))
              if ($reason -in @('request_budget_exhausted', 'workflow_deadline_exceeded', 'api_request_failed')) { $script:RemoteStop = $true }
            }
          }
        }
      }
    }

    Write-PrivateJson (Join-Path $script:RunDir 'feedback.private.json') $script:Feedback
    Write-PrivateJson (Join-Path $script:RunDir 'lanes.private.json') $script:Lanes
    foreach ($lane in $script:Lanes) {
      $lane.pendingFeedback = @($script:Feedback | Where-Object laneId -CEQ $lane.laneId).Count -gt 0
      $source = @($entries | Where-Object { $_.case.case_id -ceq $lane.caseId })[0]
      $video = @($source.case.probe.streams | Where-Object codec_type -EQ 'video' | Select-Object -First 1)
      if ($video.Count -gt 0 -and $video[0].width -gt 0 -and $video[0].height -gt 0) {
        $sar = 1.0
        if ($video[0].sample_aspect_ratio -match '^([0-9]+):([1-9][0-9]*)$') { $sar = [double]$Matches[1] / [double]$Matches[2] }
        $lane.sourceDisplayAspect = [double]$video[0].width * $sar / [double]$video[0].height
      }
    }
    # Browser handoff contains session authority and is always private, including its output.
    $cookies = @($script:Session.Cookies.GetCookies([uri]$script:BaseURL) | ForEach-Object { @{ name = $_.Name; value = $_.Value; domain = $_.Domain; path = $_.Path; httpOnly = $_.HttpOnly; secure = $_.Secure; sameSite = 'Lax' } })
    $browserHandoff = @{ runId = $runID; buildIdentifier = $script:Run.buildIdentifier; executionMode = $script:Run.executionMode; imageUploadLimit = $script:Run.budgets.maxImages; requireUploadControls = $script:Run.suite -in @('Smoke', 'Full') -or $script:Run.budgets.maxImages -gt 0; remainingRequests = [Math]::Max(0, $script:Run.budgets.maxRequests - $script:RequestCount); baseURL = $script:BaseURL; cookies = $cookies; process = $processRecord; lanes = $script:Lanes }
    Write-PrivateJson (Join-Path $script:RunDir 'browser.private.json') $browserHandoff
    try {
      $browserReceipt = Invoke-BrowserCheck
      foreach ($lane in $script:Lanes | Where-Object workflowId) {
        $latest = Read-PrivateJson (Join-Path $script:RunDir "snapshots/$($lane.laneId).private.json")
        if (@(Get-PendingActions $latest).Count -gt 0) {
          Save-Feedback $lane $latest $lane.goal
          $lane.pendingFeedback = $true
        }
        if ($latest.media.status -in @('ready', 'completed') -and @($browserReceipt.results | Where-Object { $_.laneId -ceq $lane.laneId -and $_.stage -eq 'image_decode' -and $_.status -eq 'pass' }).Count -gt 0) {
          foreach ($row in $script:Results | Where-Object { $_.laneId -ceq $lane.laneId -and $_.stage -eq 'media_ready' -and $_.reason -eq 'local_capture_requires_decode' }) {
            $row.status = 'pass'; $row.reason = 'retained_local_capture_decoded'
          }
        }
      }
      Add-Result '' '' 'embedded_browser' 'pass' 'identity_and_banner_verified'
    } catch { Add-Result '' '' 'embedded_browser' 'fail' 'browser_check_failed'; $script:RemoteStop = $true }

    if ($script:Run.budgets.maxImages -gt 0 -and -not $script:RemoteStop) {
      Invoke-LiveImageChecks $browserHandoff
    } else {
      Add-Result '' '' 'image_host' 'not_applicable' 'image_uploads_not_authorized_or_remote_stopped'
      if ($script:Run.suite -in @('Smoke', 'Full')) { Add-Result '' '' 'dry_run' 'blocked' 'hosting_or_duplicate_prerequisites_unfulfilled' }
      else { Add-Result '' '' 'dry_run' 'not_applicable' 'outside_selected_suite' }
    }
    $finalInfo = Invoke-LiveAPI 'GetApplicationInfo' -Poll
    Assert-Runtime $finalInfo
    $effects = $finalInfo.testRuntime
    $unexpected = ($effects.trackerSubmission.requestsDenied - $initialCounters.trackerSubmission.requestsDenied - 1) + ($effects.clientMutation.requestsDenied - $initialCounters.clientMutation.requestsDenied) + $effects.trackerSubmission.mutationCallsDenied + $effects.clientMutation.mutationCallsDenied
    $remoteCalls = $effects.trackerSubmission.remoteCallsStarted + $effects.trackerSubmission.remoteCallsSucceeded + $effects.clientMutation.remoteCallsStarted + $effects.clientMutation.remoteCallsSucceeded
    $script:Run.effects = @{ trackerSubmission = $effects.trackerSubmission; clientMutation = $effects.clientMutation; expectedNegativeDenialsThisSession = 1; unexpectedDenialsThisSession = $unexpected }
    Add-Result '' '' 'forbidden_effects' $(if ($unexpected -eq 0 -and $remoteCalls -eq 0) { 'pass' } else { 'fail' }) $(if ($unexpected -eq 0 -and $remoteCalls -eq 0) { 'zero_forbidden_calls' } else { 'unexpected_policy_effect' })
    # One owned restart checks persistence without replaying choices or performing new remote work.
    $restartLane = @(Get-LiveRestartLane)
    if ($restartLane.Count -gt 0) {
      Write-Host 'Checking one server restart with the original binary, profile, and image budget.'
      $lane = $restartLane[0]
      $beforeRestart = Invoke-LiveAPI 'GetReleaseWorkflow' @{ workflowId = $lane.workflowId } -Poll
      Write-PrivateJson (Join-Path $script:RunDir 'restart-before.private.json') $beforeRestart
      Stop-OwnedProcess $server; $server = $null
      $restarted = Start-VerifiedServer $profile
      $server = $restarted.handle; $processRecord = $restarted.process
      $afterRestart = Invoke-LiveAPI 'GetReleaseWorkflow' @{ workflowId = $lane.workflowId } -Poll
      Write-PrivateJson (Join-Path $script:RunDir 'restart-after.private.json') $afterRestart
      if ($afterRestart.workflow.id -cne $beforeRestart.workflow.id) { throw 'restart_workflow_identity_changed' }
      $script:Results = @($script:Results | Where-Object { $_.laneId -cne $lane.laneId -or $_.stage -notin @('server_restart', 'restart_authority', 'restart_media', 'restart_media_decode') })
      Add-Result $lane.caseId $lane.laneId 'server_restart' 'pass' 'same_profile_runtime_policy_and_workflow_verified'
      $beforeMedia = @($beforeRestart.media.artifacts | Sort-Object id | ForEach-Object { [ordered]@{ id = $_.id; kind = $_.kind; selected = $_.selected; order = $_.order; index = $_.index; timestamp = $_.timestampSeconds; width = $_.width; height = $_.height; url = $_.url } })
      $afterMedia = @($afterRestart.media.artifacts | Sort-Object id | ForEach-Object { [ordered]@{ id = $_.id; kind = $_.kind; selected = $_.selected; order = $_.order; index = $_.index; timestamp = $_.timestampSeconds; width = $_.width; height = $_.height; url = $_.url } })
      $sameMedia = (ConvertTo-Json -InputObject $beforeMedia -Depth 20 -Compress) -ceq (ConvertTo-Json -InputObject $afterMedia -Depth 20 -Compress)
      $restartActions = @(Get-PendingActions $afterRestart)
      if ($restartActions.Count -gt 0) { Save-Feedback $lane $afterRestart $lane.goal; Add-Result $lane.caseId $lane.laneId 'restart_authority' 'needs_input' 'restart_requires_fresh_typed_action' }
      else { Add-Result $lane.caseId $lane.laneId 'restart_authority' 'pass' 'no_pending_recovery_actions' }
      Add-Result $lane.caseId $lane.laneId 'restart_media' $(if ($sameMedia -and $beforeMedia.Count -gt 0) { 'pass' } elseif ($restartActions.Count -gt 0) { 'needs_input' } elseif ($beforeMedia.Count -eq 0) { 'inconclusive' } else { 'fail' }) $(if ($sameMedia -and $beforeMedia.Count -gt 0) { 'artifact_identity_selection_and_order_retained' } elseif ($restartActions.Count -gt 0) { 'media_revalidation_requires_typed_action' } elseif ($beforeMedia.Count -eq 0) { 'no_retained_media_to_compare' } else { 'media_changed_without_recovery_action' })
      if ($sameMedia -and @($afterRestart.media.artifacts | Where-Object kind -EQ 'screenshot').Count -gt 0) {
        $browserHandoff.hostedOnly = $false; $browserHandoff.restartOnly = $true; $browserHandoff.process = $processRecord; $browserHandoff.lanes = @($lane)
        $browserHandoff.cookies = @($script:Session.Cookies.GetCookies([uri]$script:BaseURL) | ForEach-Object { @{ name = $_.Name; value = $_.Value; domain = $_.Domain; path = $_.Path; httpOnly = $_.HttpOnly; secure = $_.Secure; sameSite = 'Lax' } })
        $browserHandoff.remainingRequests = [Math]::Max(0, $script:Run.budgets.maxRequests - $script:RequestCount)
        Write-PrivateJson (Join-Path $script:RunDir 'browser.private.json') $browserHandoff
        try {
          $null = Invoke-BrowserCheck 'restart'
        } catch { Add-Result $lane.caseId $lane.laneId 'restart_media_decode' $(if ($restartActions.Count -gt 0) { 'needs_input' } else { 'fail' }) $(if ($restartActions.Count -gt 0) { 'private_resources_require_revalidation' } else { 'restart_browser_check_failed' }) }
      }
      $restartInfo = Invoke-LiveAPI 'GetApplicationInfo' -Poll
      Assert-Runtime $restartInfo
      $script:Run.restartEffects = @{ trackerSubmission = $restartInfo.testRuntime.trackerSubmission; clientMutation = $restartInfo.testRuntime.clientMutation; expectedNegativeDenialsThisSession = 0 }
      $restartForbidden = 0
      foreach ($counter in @($restartInfo.testRuntime.trackerSubmission, $restartInfo.testRuntime.clientMutation)) {
        $restartForbidden += $counter.requestsDenied + $counter.mutationCallsDenied + $counter.remoteCallsStarted + $counter.remoteCallsSucceeded
      }
      Add-Result '' '' 'restart_forbidden_effects' $(if ($restartForbidden -eq 0) { 'pass' } else { 'fail' }) $(if ($restartForbidden -eq 0) { 'zero_forbidden_calls_after_restart' } else { 'unexpected_policy_effect_after_restart' })
    } else { Add-Result '' '' 'server_restart' 'inconclusive' 'no_workflow_to_recover' }
    $keepForFeedback = @($script:Feedback | Where-Object { $_.authority -and $_.status -eq 'needs_input' }).Count -gt 0
    Write-PrivateJson (Join-Path $script:RunDir 'feedback.private.json') $script:Feedback
    $script:Results = @($script:Results | Where-Object stage -NE 'runner')
  }
} catch {
  $exitCode = 2
  $reason = $_.Exception.Message
  if ($reason -cnotmatch '^[a-z][a-z0-9_]{0,80}$') { $reason = 'runner_preflight_or_execution_failed' }
  Write-Host "status=blocked reason=$reason"
  Add-Result '' '' 'runner' 'blocked' $reason
  if (Test-Path -LiteralPath $privateRoot) { [IO.File]::WriteAllText((Join-Path $privateRoot 'last-runner-error.private.log'), ($_ | Out-String)) }
  if (-not $initialized -and $PSCmdlet.ParameterSetName -eq 'New' -and $buildDir -and (Test-Path -LiteralPath $buildDir)) {
    Write-PrivateJson (Join-Path $buildDir 'preflight-report.json') @{
      version = 1; runId = $runID; status = 'blocked'; stage = 'initialization'; reason = $reason
      suite = $Suite; cases = @($selected); trackerScope = $(if ($Tracker) { $Tracker } else { 'config_defaults' })
      trackerSubmissions = 0; clientMutations = 0; imageUploads = 0; serverStarted = $false
    }
    Write-Host "preflightReceipt=$buildDir"
  }
  if ($script:RunDir -and (Test-Path -LiteralPath $script:RunDir)) { [IO.File]::AppendAllText((Join-Path $script:RunDir 'errors.private.log'), ($_ | Out-String)) }
  # Resumption failures must preserve authority and cleanup ownership for operator reconciliation.
  if ($ResumeRun) { $keepForFeedback = $true }
} finally {
  if ($server) { Stop-OwnedProcess $server; $server = $null }
  if ($initialized -and $script:Run) {
    if (-not $CleanupRun) {
      $keepForFeedback = $keepForFeedback -or @($script:Feedback | Where-Object { $_.authority -and $_.status -eq 'needs_input' }).Count -gt 0
      if ($keepForFeedback) {
        $script:Run.state = 'needs_input'; $script:Cleanup.state = 'deferred_for_feedback'
        if ($script:Run.budgets.maxImages -gt 0) { $script:Cleanup.retained = $null; $script:Cleanup.pending = $null; $script:Cleanup.unknown = $null; $script:Cleanup.failed = $null }
      }
      else {
        try { Invoke-RunCleanup; $script:Run.state = 'cleaned'; $script:Cleanup.state = 'complete' }
        catch { $script:Run.state = 'cleanup_pending'; $script:Cleanup.state = 'unresolved'; $exitCode = 2; Add-Result '' '' 'cleanup' 'blocked' 'cleanup_unresolved' }
      }
    }
    $script:Run.requests = $script:RequestCount
    $script:Run.updatedAt = [datetime]::UtcNow.ToString('o')
    Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
    if ($CleanupRun) {
      if (Test-Path -LiteralPath (Join-Path $script:RunDir 'results.private.json')) { $script:Results = @(Read-PrivateJson (Join-Path $script:RunDir 'results.private.json')) }
    }
    if ($script:Cleanup.state -eq 'complete') { $script:Results = @($script:Results | Where-Object stage -NE 'cleanup') }
    Write-PrivateJson (Join-Path $script:RunDir 'results.private.json') $script:Results
    $counts = @{}
    foreach ($state in @('pass', 'fail', 'blocked', 'needs_input', 'inconclusive', 'not_applicable')) { $counts[$state] = @($script:Results | Where-Object status -EQ $state).Count }
    $overall = 'inconclusive'
    if ($counts.fail -gt 0) { $overall = 'fail' }
    elseif ($counts.needs_input -gt 0 -or $keepForFeedback) { $overall = 'needs_input' }
    elseif ($counts.blocked -gt 0 -or $script:Cleanup.state -eq 'unresolved') { $overall = 'blocked' }
    elseif ($counts.inconclusive -eq 0 -and $counts.pass -gt 0 -and $script:Cleanup.state -eq 'complete') { $overall = 'pass' }
    $report = @{ version = 1; runId = $runID; status = $overall; suite = $script:Run.suite; executionMode = $script:Run.executionMode; buildIdentifier = $script:Run.buildIdentifier; binarySha256 = $script:Run.binarySha256; configFingerprint = $script:Run.configFingerprint; configDefaultTrackers = $script:Run.configDefaultTrackers; selectedTrackers = $script:Run.selectedTrackers; availableTrackers = $script:Run.availableTrackers; unavailableTrackers = $script:Run.unavailableTrackers; trackerScope = $script:Run.trackerScope; cases = $script:Run.caseIds; budgets = $script:Run.budgets; requests = $script:Run.requests; counts = $counts; effects = $script:Run.effects; restartEffects = $script:Run.restartEffects; cleanup = $script:Cleanup; gaps = $script:Run.gaps; results = $script:Results }
    $report.imageHostCoverage = [bool]$script:Run.imageHostCoverage
    $report.preferDeletableHosts = [bool]$script:Run.preferDeletableHosts
    Write-PrivateJson (Join-Path $script:RunDir 'report.json') $report
    $markdown = @("# Live testing $runID", '', "Status: $overall. Mode: $($script:Run.executionMode).", '', "Intended trackers: $($script:Run.selectedTrackers -join ', ').", "Available trackers: $($script:Run.availableTrackers -join ', ').", "Unavailable trackers: $($script:Run.unavailableTrackers -join ', ').", '', 'This receipt contains only stable case IDs, outcomes, and counters. Private snapshots contain source and tracker evidence.', '', '| Case | Lane | Stage | Status | Reason |', '| --- | --- | --- | --- | --- |')
    foreach ($row in $script:Results) { $markdown += "| $($row.caseId) | $($row.laneId) | $($row.stage) | $($row.status) | $($row.reason) |" }
    $dupeRows = @($script:Results | Where-Object stage -EQ 'duplicates_decided')
    if (@($dupeRows.evidence.duplicateSearches).Count -gt 0) {
      $markdown += @('', 'Duplicate observations (local_client scope does not establish a site search):', '', '| Case | Lane | Tracker | SAT | Status | Decision | Scope | Complete | Pages | Candidates |', '| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |')
      foreach ($row in $dupeRows) {
        foreach ($search in $row.evidence.duplicateSearches) { $markdown += "| $($row.caseId) | $($row.laneId) | $($search.trackerId) | $($row.evidence.sat) | $($search.status) | $($search.decision) | $($search.scope) | $($search.complete) | $($search.pages) | $($search.candidateCount) |" }
      }
    }
    $markdown += @('', "Cleanup: $($script:Cleanup.state); deleted=$($script:Cleanup.deleted), retained=$($script:Cleanup.retained), pending=$($script:Cleanup.pending), unknown=$($script:Cleanup.unknown), failed=$($script:Cleanup.failed).", '', 'Coverage gaps: ' + ($script:Run.gaps -join ', '), '', 'Use feedback.private.json for exact typed actions; resume only the retained unchanged run. Use -CleanupRun to abandon feedback and reconcile every image effect.')
    [IO.File]::WriteAllText((Join-Path $script:RunDir 'report.md'), ($markdown -join "`n"))
    Write-Host "run=$runID status=$overall cleanup=$($script:Cleanup.state)"
    if (-not $CleanupRun -and $overall -ne 'pass') { $exitCode = 2 }
  }
  if ($runnerLock) { $runnerLock.Dispose() }
}
exit $exitCode
