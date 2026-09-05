#Requires -Version 7.0
# Non-network regression checks. Synthetic files stay in the private validation directory.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'functions.ps1')
$script:RepoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$root = Join-Path $env:LOCALAPPDATA 'upbrr-live-testing'
$validationDir = Assert-PrivatePath (Join-Path $root ('validation/' + [guid]::NewGuid().ToString('N'))) $root
New-Item -ItemType Directory -Path $validationDir -Force | Out-Null
function Assert-Check([bool]$Condition, [string]$Code) { if (-not $Condition) { throw $Code } }
try {
  $source = Join-Path $validationDir 'Example.Movie.2025.1080p.WEB-DL-GRP.mkv'
  [IO.File]::WriteAllText($source, 'synthetic validation bytes; never treated as playable media')
  $case = @{ case_id = 'MOV-1080-WEB'; input_path = $source; probe_path = $source; input_shape = 'file'; probe_status = 'ok'; fingerprint = @{ size_bytes = 0; mtime_ns = 0 } }
  $stat = Get-SourceFingerprint $case
  $case.fingerprint.size_bytes = $stat.size_bytes; $case.fingerprint.mtime_ns = $stat.mtime_ns
  $corpusPath = Join-Path $validationDir 'corpus.private.json'
  Write-PrivateJson $corpusPath @{ schema_version = 1; cases = @($case) }
  $loaded = @(Read-Corpus $corpusPath @('MOV-1080-WEB'))
  Assert-Check ($loaded.Count -eq 1 -and $loaded[0].status -eq 'ready') 'valid_corpus_rejected'
  Invoke-OwnedProcess (Get-ToolPath 'pwsh') @('-NoProfile', '-File', (Join-Path $PSScriptRoot 'run.ps1'), '-ValidateOnly', '-CaseId', 'MOV-1080-WEB', '-Corpus', $corpusPath) (Join-Path $validationDir 'validate-only') 30
  Assert-Check ((Get-SourceFingerprint $case).fingerprint -ceq $stat.fingerprint) 'validation_modified_source'
  [IO.File]::AppendAllText($source, ' changed')
  Assert-Check (@(Read-Corpus $corpusPath @('MOV-1080-WEB'))[0].reason -eq 'source_changed_since_inventory') 'changed_source_not_detected'
  $duplicateRejected = $false
  Write-PrivateJson $corpusPath @{ schema_version = 1; cases = @($case, $case) }
  try { Read-Corpus $corpusPath @('MOV-1080-WEB') | Out-Null } catch { $duplicateRejected = $_.Exception.Message -eq 'corpus_case_id_invalid' }
  Assert-Check $duplicateRejected 'duplicate_case_not_rejected'
  $escapeRejected = $false
  try { Assert-PrivatePath (Join-Path $script:RepoRoot 'private.json') $root | Out-Null } catch { $escapeRejected = $true }
  Assert-Check $escapeRejected 'private_path_escape_not_rejected'
  $dir = Join-Path $validationDir 'Example.Season.01-GRP'
  New-Item -ItemType Directory -Path $dir | Out-Null
  $episode = Join-Path $dir 'Example.S01E01-GRP.mkv'
  [IO.File]::WriteAllText($episode, 'synthetic episode')
  $pack = @{ case_id = 'TV-PACK'; input_path = $dir; probe_path = $episode; input_shape = 'episode-directory'; probe_status = 'ok'; fingerprint = @{} }
  $packStat = Get-SourceFingerprint $pack
  $pack.fingerprint = @{ size_bytes = $packStat.size_bytes; mtime_ns = $packStat.mtime_ns }
  Write-PrivateJson $corpusPath @{ schema_version = 1; cases = @($pack) }
  Assert-Check (@(Read-Corpus $corpusPath @('TV-PACK'))[0].reason -eq 'source_selection_unconfirmed') 'pack_completeness_invented'
  [IO.File]::WriteAllText((Join-Path $dir 'Example.S01E02-GRP.mkv'), 'synthetic second episode')
  Assert-Check ((Get-SourceFingerprint $pack).fingerprint -cne $packStat.fingerprint) 'membership_change_not_detected'
  $probeScript = Join-Path $validationDir 'child.ps1'
  [IO.File]::WriteAllText($probeScript, '[pscustomobject]@{e2e=$env:UPBRR_E2E_RUNNER_VALIDATION;literal=$args[0]}|ConvertTo-Json -Compress')
  $prior = $env:UPBRR_E2E_RUNNER_VALIDATION
  try {
    $env:UPBRR_E2E_RUNNER_VALIDATION = 'must-not-reach-child'
    $literal = 'a space '' quote ` tick $(not-a-command)'
    Invoke-OwnedProcess (Get-ToolPath 'pwsh') @('-NoProfile', '-File', $probeScript, $literal) (Join-Path $validationDir 'child') 30
    $child = Read-PrivateJson (Join-Path $validationDir 'child.stdout.private.log')
    Assert-Check (-not $child.e2e -and $child.literal -ceq $literal) 'child_environment_or_quoting_failed'
    Assert-Check ($env:UPBRR_E2E_RUNNER_VALIDATION -ceq 'must-not-reach-child') 'parent_environment_changed'
  } finally { $env:UPBRR_E2E_RUNNER_VALIDATION = $prior }
  $script:Run = @{ runId = 'synthetic-run'; buildIdentifier = 'synthetic-build'; budgets = @{ maxImages = 0 } }
  $runtime = @{ buildIdentifier = 'synthetic-build'; testRuntime = @{ mode = 'live_test'; runId = 'synthetic-run'; trackerSubmissionAllowed = $false; clientMutationAllowed = $false; imageUploadsRequireJournal = $true; imageUploadLimit = 0 } }
  Assert-Runtime $runtime
  $runtime.testRuntime.imageUploadLimit = 1
  $policyRejected = $false
  try { Assert-Runtime $runtime } catch { $policyRejected = $true }
  Assert-Check $policyRejected 'unexpected_image_permission_not_rejected'
  $priorRun = $script:Run
  try {
    $script:Results = @()
    $intended = @('BLU', 'RETIRED', 'LST')
    $script:Run = @{ selectedTrackers = $intended; configDefaultTrackers = @($intended); trackerScope = 'config_defaults'; caseIds = @('MOV-1080-WEB', 'TV-480') }
    $catalog = @{ entries = @(@{ name = 'LST'; configured = $true }, @{ name = 'ANT'; configured = $true }, @{ name = 'BLU'; configured = $false }) }
    Set-RunTrackerScope $catalog
    Assert-Check (($script:Run.selectedTrackers -join ',') -ceq 'BLU,RETIRED,LST' -and ($script:Run.configDefaultTrackers -join ',') -ceq 'BLU,RETIRED,LST') 'intended_default_scope_changed'
    Assert-Check (($script:Run.availableTrackers -join ',') -ceq 'BLU,LST' -and ($script:Run.unavailableTrackers -join ',') -ceq 'RETIRED') 'available_scope_reordered_filtered_by_auth_or_substituted'
    Assert-Check ($script:Results.Count -eq 2 -and @($script:Results | Where-Object { $_.stage -ne 'tracker_availability' -or $_.status -ne 'blocked' -or $_.evidence.trackerId -cne 'RETIRED' }).Count -eq 0) 'unavailable_default_not_reported_per_case'
    Assert-Check (($script:Results.caseId -join ',') -ceq 'MOV-1080-WEB,TV-480') 'unavailable_case_order_changed'
    Set-RunTrackerScope $catalog
    Assert-Check ($script:Results.Count -eq 2) 'resume_duplicated_unavailable_results'
    $changedRejected = $false
    try { Set-RunTrackerScope @{ entries = @(@{ name = 'BLU' }, @{ name = 'ANT' }) } } catch { $changedRejected = $_.Exception.Message -eq 'tracker_availability_changed_new_run_required' }
    Assert-Check ($changedRejected -and ($script:Run.availableTrackers -join ',') -ceq 'BLU,LST') 'resume_silently_changed_tracker_availability'
    $script:Run = @{ selectedTrackers = @('RETIRED'); configDefaultTrackers = $intended; trackerScope = 'explicit'; caseIds = @('MOV-1080-WEB') }
    $explicitRejected = $false
    try { Set-RunTrackerScope $catalog } catch { $explicitRejected = $_.Exception.Message -eq 'explicit_tracker_not_registered' }
    Assert-Check ($explicitRejected -and $script:Run.availableTrackers.Count -eq 0 -and $script:Run.selectedTrackers[0] -ceq 'RETIRED' -and $script:Results.Count -eq 1) 'explicit_unknown_tracker_not_blocked'
    $script:Run = @{ selectedTrackers = @('RETIRED'); trackerScope = 'config_defaults'; caseIds = @('MOV-1080-WEB') }
    $emptyRejected = $false
    try { Set-RunTrackerScope $catalog } catch { $emptyRejected = $_.Exception.Message -eq 'no_registered_selected_trackers' }
    Assert-Check ($emptyRejected -and $script:Run.availableTrackers.Count -eq 0) 'empty_registered_scope_used_fallback'
  } finally { $script:Run = $priorRun }
  $script:Results = @(); $script:Feedback = @(); $script:RemoteStop = $false
  $lane = @{ laneId = 'lane-0001'; caseId = 'MOV-1080-WEB'; trackerIds = @('LST', 'BLU'); sat = $true }
  $current = @{ workflow = @{ id = 'workflow-1'; revision = 7 }; selection = @{ trackerIds = @('LST', 'BLU'); fingerprint = 'selection-1' }; preflight = @{ status = 'ready'; results = @(@{ trackerId = 'LST'; state = 'ready'; authReady = $true }) } }
  Assert-Check ((Record-Stage $lane $current 'trackers_assessed') -eq 'pass') 'ready_stage_not_recorded'
  $binding = Get-FeedbackEvidence $current
  $current.workflow.revision++
  Assert-Check ((Get-FeedbackEvidence $current) -cne $binding) 'feedback_revision_not_bound'
  $current.selection.trackerIds = @('LST')
  $selectionRejected = $false
  try { Record-Stage $lane $current 'trackers_assessed' | Out-Null } catch { $selectionRejected = $_.Exception.Message -eq 'tracker_selection_changed' }
  Assert-Check $selectionRejected 'narrowed_default_list_not_rejected'
  $current.selection.trackerIds = @('LST', 'BLU')
  $current.dryRun = @{ status = 'ready'; noSeed = $false }
  Assert-Check ((Record-Stage $lane $current 'dry_run') -eq 'fail') 'conflicting_no_seed_not_detected'
  $gitPath = Get-ToolPath 'git'
  Assert-Check ($gitPath -is [string] -and $gitPath -ceq (Get-Command git -CommandType Application | Select-Object -First 1).Source) 'tool_path_not_single_application'
  Invoke-OwnedProcess $gitPath @('--version') (Join-Path $validationDir 'git-single-path') 30
  $script:RunDir = $validationDir
  New-Item -ItemType Directory -Path (Join-Path $validationDir 'snapshots') | Out-Null
  $script:Run.budgets.timeoutSeconds = 30
  $script:Lanes = @($lane)
  $originalAPI = (Get-Item Function:Invoke-LiveAPI).ScriptBlock
  $script:FakeRequests = @(); $script:FakeStep = 0; $script:FakePolls = 0
  function Invoke-LiveAPI([string]$Method, $Body = @{}, [switch]$Poll, [int]$ExpectedStatus = 200) {
    if ($Method -eq 'GetReleaseWorkflow') {
      $script:FakePolls++
      return @{ workflow = @{ id = 'workflow-1'; revision = 3 }; operation = @{ status = 'completed' }; release = @{ id = 'release-1' } }
    }
    $script:FakeRequests += @(ConvertTo-Json $Body -Depth 40 | ConvertFrom-Json -AsHashtable)
    $script:FakeStep++
    switch ($script:FakeStep) {
      1 { return @{ workflow = @{ id = 'workflow-1'; revision = 1 } } }
      2 { return @{ workflow = @{ id = 'workflow-1'; revision = 2 }; operation = @{ status = 'queued' } } }
      default {
        return @{ workflow = @{ id = 'workflow-1'; revision = 4; requiredActions = @(@{ id = 'action-lst'; kind = 'provide_tracker_input'; trackerId = 'LST'; status = 'pending'; workflowRevision = 4 }) }; release = @{ id = 'release-1' }; preflight = @{ status = 'ready'; results = @(@{ trackerId = 'BLU'; state = 'ready'; authReady = $true }, @{ trackerId = 'LST'; state = 'blocked'; authReady = $false }) } }
      }
    }
  }
  try {
    $intent = @{ trackerIds = @('LST', 'BLU'); noSeed = $true; preparation = @{ SourcePath = $source; Search = @{ Skip = $true }; Force = $true } }
    $advanced = Continue-Lane $lane 'trackers_assessed' $null $intent
    Assert-Check ($script:FakeStep -eq 4 -and $script:FakePolls -eq 1 -and $advanced.release -and $advanced.preflight) 'continuation_stopped_after_creation'
    Assert-Check (-not $script:FakeRequests[0].authority -and $script:FakeRequests[1].authority.expectedRevision -eq 1 -and $script:FakeRequests[2].authority.expectedRevision -eq 3 -and $script:FakeRequests[3].authority.expectedRevision -eq 4) 'continuation_authority_not_current'
    Assert-Check (@($script:FakeRequests.idempotencyKey | Select-Object -Unique).Count -eq 1) 'continuation_idempotency_changed'
    Assert-Check (@($script:FakeRequests | Where-Object { -not $_.intent.preparation -or $_.intent.trackerIds.Count -ne 2 }).Count -eq 0) 'creation_intent_or_full_trackers_lost'
    $recorded = @(Read-PrivateJson (Join-Path $validationDir 'lanes.private.json'))[0]
    Assert-Check ($recorded.authority.expectedRevision -eq 4 -and $recorded.preparation.SourcePath -ceq $source) 'latest_authority_or_preparation_not_saved'
    # The blocked LST action does not make the adapter stop before BLU's backend transition.
    Assert-Check (@(Get-PendingActions $advanced).Count -eq 1 -and $advanced.preflight.results[0].state -eq 'ready') 'partial_tracker_evidence_lost'
  } finally { Set-Item Function:Invoke-LiveAPI -Value $originalAPI }

  $script:FakeRequests = @(); $script:FakeStep = 0
  function Invoke-LiveAPI([string]$Method, $Body = @{}, [switch]$Poll, [int]$ExpectedStatus = 200) {
    $script:FakeRequests += @(ConvertTo-Json $Body -Depth 40 | ConvertFrom-Json -AsHashtable)
    $script:FakeStep++
    @{ workflow = @{ id = 'workflow-1'; revision = 2 }; release = @{ id = 'release-1' }; factInstructions = @{ instructions = @{ SourceLookup = 'operator-selected-synthetic-source' } } }
  }
  try {
    $old = @{ workflow = @{ id = 'workflow-1'; revision = 1 } }
    $intent = @{ preparation = @{ SourcePath = $source; Instructions = @{} }; noSeed = $true }
    $answer = @{ actionId = 'action-1'; workflowRevision = 1; selectedValues = @('synthetic-1') }
    $null = Continue-Lane $lane 'prepared' $old $intent @($answer)
    Assert-Check ($script:FakeRequests.Count -eq 2 -and $script:FakeRequests[0].answers.Count -eq 1 -and -not $script:FakeRequests[1].answers) 'revision_bound_answer_replayed'
    Assert-Check ($script:FakeRequests[1].intent.preparation.Instructions.SourceLookup -ceq 'operator-selected-synthetic-source' -and -not $intent.preparation.Instructions.SourceLookup) 'accepted_facts_reset_or_caller_intent_mutated'
  } finally { Set-Item Function:Invoke-LiveAPI -Value $originalAPI }

  $script:FakeStep = 0
  function Invoke-LiveAPI([string]$Method, $Body = @{}, [switch]$Poll, [int]$ExpectedStatus = 200) {
    $script:FakeStep++
    @{ workflow = @{ id = 'workflow-1'; revision = $script:FakeStep + 1 } }
  }
  try {
    $limited = $false
    try { Continue-Lane $lane 'prepared' @{ workflow = @{ id = 'workflow-1'; revision = 1 } } @{ noSeed = $true } | Out-Null } catch { $limited = $_.Exception.Message -eq 'workflow_transition_limit_exceeded' }
    Assert-Check ($limited -and $script:FakeStep -eq 32) 'continuation_not_bounded'
  } finally { Set-Item Function:Invoke-LiveAPI -Value $originalAPI }

  $action = @{ id = 'action-1'; kind = 'select_metadata'; status = 'pending'; trackerId = ''; workflowRevision = 7; prompt = 'Choose the verified synthetic work'; options = @(@{ value = 'synthetic-1'; label = 'Synthetic Work' }) }
  $current = @{ workflow = @{ id = 'workflow-1'; revision = 7; requiredActions = @($action) }; selection = @{ fingerprint = 'selection-1' }; factInstructions = @{ fingerprint = 'facts-1' } }
  Save-Feedback $lane $current 'prepared'
  $feedback = $script:Feedback[0]
  $feedback.answers = @(@{ actionId = 'action-1'; workflowRevision = 7; selectedValues = @('synthetic-1') })
  $feedback.acceptedAt = '2026-09-05T00:00:00Z'; $feedback.rationale = 'Synthetic accepted choice'
  $current = ConvertTo-Json $current -Depth 30 | ConvertFrom-Json -AsHashtable
  $current.workflow.revision = 8; $current.workflow.requiredActions[0].workflowRevision = 8
  Assert-Check ((Resolve-FeedbackAuthority $lane $feedback $current) -eq 'rebound') 'equivalent_restart_authority_not_rebound'
  Assert-Check ($feedback.answers[0].workflowRevision -eq 8 -and $feedback.authority.expectedRevision -eq 8) 'answer_retains_old_authority'
  $current.workflow.revision = 9; $current.workflow.requiredActions[0].workflowRevision = 9
  $current.factInstructions.fingerprint = 'facts-changed'
  Assert-Check ((Resolve-FeedbackAuthority $lane $feedback $current) -eq 'refreshed') 'changed_feedback_evidence_not_refreshed'
  $refreshed = $script:Feedback[0]
  Assert-Check ($refreshed.authority.expectedRevision -eq 9 -and @($refreshed.answers).Count -eq 0 -and -not $refreshed.acceptedAt -and $refreshed.status -eq 'needs_input') 'stale_answers_not_cleared'
  # Another restart only changes authority: fresh user acceptance remains usable.
  $refreshed.answers = @(@{ actionId = 'action-1'; workflowRevision = 9; selectedValues = @('synthetic-1') })
  $refreshed.acceptedAt = '2026-09-05T01:00:00Z'; $refreshed.rationale = 'Reviewed changed synthetic evidence'
  $current.workflow.revision = 10; $current.workflow.requiredActions[0].workflowRevision = 10
  Assert-Check ((Resolve-FeedbackAuthority $lane $refreshed $current) -eq 'rebound' -and $refreshed.answers[0].workflowRevision -eq 10) 'restart_refresh_loop_prevents_resume'
  $originalProcess = ${function:Invoke-OwnedProcess}
  $requestsBeforeBrowser = $script:RequestCount
  function Invoke-OwnedProcess {
    Write-PrivateJson (Join-Path $script:RunDir 'browser-requests.private.json') @{ requests = 3 }
    Write-PrivateJson (Join-Path $script:RunDir 'browser-results.json') @{ results = @(@{ caseId = 'MOV-1080-WEB'; laneId = 'lane-0001'; stage = 'synthetic_browser_evidence'; status = 'pass'; reason = 'completed_before_failure' }) }
    throw 'synthetic_browser_failed'
  }
  try {
    $browserFailed = $false
    try { Invoke-BrowserCheck | Out-Null } catch { $browserFailed = $_.Exception.Message -eq 'synthetic_browser_failed' }
    Assert-Check ($browserFailed -and $script:RequestCount -eq $requestsBeforeBrowser + 3) 'failed_browser_budget_lost'
    Assert-Check (@($script:Results | Where-Object stage -EQ 'synthetic_browser_evidence').Count -eq 1) 'failed_browser_evidence_lost'
  } finally { Set-Item Function:Invoke-OwnedProcess -Value $originalProcess }
  $originalFeedback = (Get-Item Function:Save-Feedback).ScriptBlock
  function Save-Feedback($Lane, $Current, [string]$Goal) {}
  try {
    $script:Results = @()
    $dupeLane = @{ caseId = 'SYNTHETIC'; laneId = 'lane-0001'; trackerIds = @('LST', 'BHD'); sat = $false }
    $dupeCurrent = @{ dupes = @{ status = 'completed'; results = @(
      @{ trackerId = 'LST'; status = 'completed'; decision = 'accepted'; uploadReleaseName = 'PRIVATE_SENTINEL'; search = @{ scope = 'local_client'; complete = $true; candidateCount = 1 }; matches = @(@{ title = 'PRIVATE_SENTINEL' }) },
      @{ trackerId = 'BHD'; status = 'completed'; decision = 'no_match'; search = @{ scope = 'work_category'; complete = $true; pages = 1 }; criteria = @{ privateURL = 'PRIVATE_SENTINEL' } }
    ) } }
    $null = Record-Stage $dupeLane $dupeCurrent 'duplicates_decided'
    $observations = $script:Results[0].evidence.duplicateSearches
    Assert-Check ($observations.Count -eq 2 -and $observations[0].scope -ceq 'local_client' -and $observations[0].pages -eq 0 -and $observations[1].scope -ceq 'work_category' -and $observations[1].pages -eq 1 -and $observations[1].candidateCount -eq 0) 'duplicate_search_scope_evidence_lost'
    Assert-Check ((ConvertTo-Json $script:Results -Depth 40) -cnotmatch 'PRIVATE_SENTINEL') 'duplicate_search_report_leaked_private_fields'
  } finally { Set-Item Function:Save-Feedback -Value $originalFeedback }
  $script:AcceptedPolls = 0
  $script:BaseURL = 'http://127.0.0.1:7480'; $script:CSRF = 'synthetic-csrf'
  function Invoke-WebRequest($Uri, $Method, $ContentType, $Body, $WebSession, $Headers, $TimeoutSec, [switch]$SkipHttpErrorCheck) {
    Assert-Check ($Headers.Origin -ceq $script:BaseURL -and $Headers.'X-CSRF-Token' -ceq 'synthetic-csrf') 'application_request_origin_or_csrf_missing'
    $script:AcceptedPolls++
    if ($Uri.EndsWith('/UploadReleaseWorkflowImages')) {
      return @{ StatusCode = 202; Content = '{"workflow":{"id":"synthetic-workflow"},"operation":{"status":"running"}}' }
    }
    Assert-Check ($Uri.EndsWith('/GetReleaseWorkflow')) 'accepted_operation_not_polled'
    return @{ StatusCode = 200; Content = '{"workflow":{"id":"synthetic-workflow"},"operation":{"status":"completed"}}' }
  }
  try {
    $accepted = Invoke-LiveAPI 'UploadReleaseWorkflowImages' @{} -ExpectedStatus 202 -Poll
    $completed = Wait-Workflow $accepted (Join-Path $validationDir 'accepted.private.json')
    Assert-Check ($script:AcceptedPolls -eq 2 -and $completed.operation.status -ceq 'completed') 'accepted_image_upload_not_awaited'
  } finally { Remove-Item Function:Invoke-WebRequest }
  Write-Host 'PASS: corpus/stat/process/policy checks; ordered partial tracker scope and per-case unavailable evidence; explicit/empty scope blocking; bounded continuation; feedback restart/rebind; failed browser budget/evidence retained; sanitized duplicate evidence; authenticated HTTP 202 upload polled to completion.'
} finally {
  $checked = Assert-PrivatePath $validationDir (Join-Path $root 'validation')
  Remove-Item -LiteralPath $checked -Recurse -Force
}
