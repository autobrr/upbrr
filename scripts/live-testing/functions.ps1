#Requires -Version 7.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'bdinfo.ps1')
. (Join-Path $PSScriptRoot 'images.ps1')

function Get-ToolPath([string]$Name) {
  $command = Get-Command $Name -CommandType Application -ErrorAction Stop | Select-Object -First 1
  $command.Source
}

function Write-PrivateJson($Path, $Value) {
  $tempPath = "$Path.$([guid]::NewGuid().ToString('N')).tmp"
  [IO.File]::WriteAllText($tempPath, (ConvertTo-Json -InputObject $Value -Depth 100), [Text.UTF8Encoding]::new($false))
  Move-Item -LiteralPath $tempPath -Destination $Path -Force
}

function Read-PrivateJson($Path) {
  Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json -AsHashtable
}

function Get-TextHash([string]$Text) {
  [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($Text))).ToLowerInvariant()
}

function Assert-PrivatePath([string]$Path, [string]$Root) {
  $full = [IO.Path]::GetFullPath($Path)
  $prefix = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
  if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'private_path_required' }
  for ($part = $full; $part -and $part.Length -ge $prefix.Length; $part = Split-Path -Parent $part) {
    if ((Test-Path -LiteralPath $part) -and ((Get-Item -LiteralPath $part -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      throw 'private_path_reparse_point'
    }
  }
  $full
}

function Get-SourceFingerprint($Case) {
  if (-not [IO.Path]::IsPathFullyQualified($Case.input_path) -or -not [IO.Path]::IsPathFullyQualified($Case.probe_path)) { throw 'source_path_not_absolute' }
  $inputItem = Get-Item -LiteralPath $Case.input_path -Force
  $probeItem = Get-Item -LiteralPath $Case.probe_path -Force
  if ($inputItem.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'source_reparse_point' }
  if ($probeItem.PSIsContainer -or ($probeItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'probe_not_regular_file' }
  if (($Case.input_shape -eq 'file') -eq $inputItem.PSIsContainer) { throw 'source_shape_changed' }
  $epochTicks = [datetime]::UnixEpoch.Ticks
  $observed = [ordered]@{
    size_bytes = $probeItem.Length
    mtime_ns = [long](($probeItem.LastWriteTimeUtc.Ticks - $epochTicks) * 100)
    input_mtime_ticks = $inputItem.LastWriteTimeUtc.Ticks
    input_path_hash = Get-TextHash $inputItem.FullName
    probe_path_hash = Get-TextHash $probeItem.FullName
    membership_hash = ''
  }
  if ($inputItem.PSIsContainer) {
    $members = @(Get-ChildItem -LiteralPath $inputItem.FullName -Recurse -Force)
    if (@($members | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count -gt 0) { throw 'source_membership_reparse_point' }
    $membership = @($members | Where-Object { -not $_.PSIsContainer } | Sort-Object FullName | ForEach-Object {
      if ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'source_membership_reparse_point' }
      '{0}|{1}|{2}' -f [IO.Path]::GetRelativePath($inputItem.FullName, $_.FullName), $_.Length, $_.LastWriteTimeUtc.Ticks
    })
    $observed.membership_hash = Get-TextHash ($membership -join "`n")
  }
  $observed.fingerprint = Get-TextHash (ConvertTo-Json $observed -Compress)
  $observed
}

function Get-CaseIdentityOverrides($Case) {
  $identity = @{}
  if (-not $Case.Contains('metadata_ids')) { return $identity }
  if ($Case.metadata_ids -isnot [System.Collections.IDictionary]) { throw 'corpus_metadata_ids_invalid' }
  $fields = @{ imdb = 'IMDBID'; tmdb = 'TMDBID'; tvdb = 'TVDBID'; tvmaze = 'TVmazeID'; mal = 'MALID' }
  foreach ($provider in $Case.metadata_ids.Keys) {
    $value = $Case.metadata_ids[$provider]
    if ($provider -cnotin @('imdb', 'tmdb', 'tvdb', 'tvmaze', 'mal') -or
        ($value -isnot [int] -and $value -isnot [long]) -or $value -le 0 -or $value -gt [int]::MaxValue) {
      throw 'corpus_metadata_ids_invalid'
    }
    $identity[$fields[$provider]] = [int]$value
  }
  $identity
}

function Get-CaseIdentityCLIArguments($Case) {
  $null = Get-CaseIdentityOverrides $Case
  foreach ($provider in @('imdb', 'tmdb', 'tvdb', 'tvmaze', 'mal')) {
    if ($Case.metadata_ids -and $Case.metadata_ids.Contains($provider)) {
      "--$provider"
      [string]$Case.metadata_ids[$provider]
    }
  }
}

function Get-CaseSourceCLIArguments($Case) {
  $instructions = Get-CaseSourceLookupInstructions $Case
  if ($instructions.Contains('SourceLookup')) {
    '--source-lookup'
    [string]$instructions.SourceLookup
  }
  if (-not $instructions.Contains('TrackerIDs')) { return }
  $supported = @('PTP', 'BLU', 'AITHER', 'LST', 'OE', 'HDB', 'BTN', 'BHD', 'ULCX')
  foreach ($tracker in $instructions.TrackerIDs.Keys | Sort-Object) {
    if ($tracker -cnotin $supported) { throw 'cli_tracker_id_not_representable' }
    '--' + $tracker.ToLowerInvariant()
    [string]$instructions.TrackerIDs[$tracker]
  }
}

function Get-CaseSourceLookupInstructions($Case) {
  $instructions = @{}
  if ($Case.Contains('source_lookup')) {
    if ($Case.source_lookup -isnot [string] -or [string]::IsNullOrWhiteSpace($Case.source_lookup)) { throw 'corpus_source_lookup_invalid' }
    $instructions.SourceLookup = $Case.source_lookup.Trim()
  }
  if ($Case.Contains('tracker_ids')) {
    if ($Case.tracker_ids -isnot [System.Collections.IDictionary] -or $Case.tracker_ids.Count -eq 0) { throw 'corpus_tracker_ids_invalid' }
    $ids = @{}
    foreach ($tracker in $Case.tracker_ids.Keys) {
      $id = $Case.tracker_ids[$tracker]
      if ($tracker -cnotmatch '^[A-Za-z0-9]+$' -or $id -isnot [string] -or [string]::IsNullOrWhiteSpace($id)) { throw 'corpus_tracker_ids_invalid' }
      $ids[$tracker.ToUpperInvariant()] = $id.Trim()
    }
    $instructions.TrackerIDs = $ids
  }
  $instructions
}

function Read-Corpus([string]$Path, [string[]]$Selected) {
  $corpusData = Read-PrivateJson $Path
  if ($corpusData.schema_version -ne 1 -or @($corpusData.cases).Count -eq 0) { throw 'corpus_schema_invalid' }
  $known = @{}
  foreach ($entry in $corpusData.cases) {
    if ($entry.case_id -cnotmatch '^[A-Z0-9]+(?:-[A-Z0-9]+)*$' -or $known.ContainsKey($entry.case_id)) { throw 'corpus_case_id_invalid' }
    if ($entry.input_shape -notin @('file', 'disc-directory', 'episode-directory') -or -not $entry.fingerprint) { throw 'corpus_case_schema_invalid' }
    $null = Get-CaseIdentityOverrides $entry
    $null = Get-CaseSourceLookupInstructions $entry
    $null = @(Get-CaseBDMVPlaylists $entry)
    $known[$entry.case_id] = $entry
  }
  foreach ($id in $Selected) {
    if (-not $known.ContainsKey($id)) { throw 'corpus_case_missing' }
    $entry = $known[$id]
    try {
      $stat = Get-SourceFingerprint $entry
      $status = 'ready'; $reason = 'source_restat_verified'
      if ($entry.probe_status -ne 'ok') { $status = 'needs_input'; $reason = 'probe_not_verified' }
      # The saved inventory uses integer nanoseconds; compare without conversion through Double.
      if ([long]$entry.fingerprint.size_bytes -ne $stat.size_bytes -or [long]$entry.fingerprint.mtime_ns -ne $stat.mtime_ns) {
        $status = 'needs_input'; $reason = 'source_changed_since_inventory'
      }
      if ($entry.input_shape -ne 'file') {
        if ($entry.fingerprint.fingerprint -cne $stat.fingerprint) {
          $status = 'needs_input'; $reason = 'source_changed_since_inventory'
        }
        # DVD and episode directories use production preparation without MPLS instructions.
        # Nested discs also require source-bound, disc-scoped selections.
        if ($entry.bdmv_selection -or @(Get-CaseBDMVDiscs $entry).Count -gt 0) {
          $playlists = @(Get-CaseBDMVPlaylists $entry)
          if ($playlists.Count -eq 0) { $status = 'needs_input'; $reason = 'source_selection_unconfirmed' }
          elseif ($entry.bdmv_selection.source_fingerprint -cne $stat.fingerprint) { $status = 'needs_input'; $reason = 'source_changed_since_selection' }
          else { $null = @(Get-CaseBDMVScopedPlaylists $entry) }
        }
      }
      @{ case = $entry; stat = $stat; status = $status; reason = $reason }
    } catch {
      @{ case = $entry; stat = $null; status = 'blocked'; reason = 'source_unavailable_or_invalid' }
    }
  }
}

function Start-OwnedProcess([string]$File, [string[]]$Arguments, [string]$LogBase, [hashtable]$Environment = @{}, [switch]$CloseInput) {
  $info = [Diagnostics.ProcessStartInfo]::new()
  $info.FileName = $File
  $info.WorkingDirectory = $script:RepoRoot
  $info.UseShellExecute = $false
  $info.CreateNoWindow = $true
  $info.RedirectStandardOutput = $true
  $info.RedirectStandardError = $true
  $info.RedirectStandardInput = $CloseInput
  if ([IO.Path]::GetExtension($File) -in @('.cmd', '.bat')) {
    # PowerShell quotes each token before invoking a package-manager command shim.
    $tokens = @($File) + @($Arguments) | ForEach-Object { "'" + $_.Replace("'", "''") + "'" }
    $command = '& ' + ($tokens -join ' ') + '; exit $LASTEXITCODE'
    $info.FileName = Get-ToolPath 'pwsh'
    $Arguments = @('-NoProfile', '-EncodedCommand', [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command)))
  }
  foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
  foreach ($key in @($info.Environment.Keys)) {
    if ($key -like 'UPBRR_E2E_*' -or $key -in @('GOFLAGS', 'UPBRR_CONFIG', 'UPBRR_CONFIG_PATH')) { $info.Environment.Remove($key) | Out-Null }
  }
  foreach ($key in $Environment.Keys) { $info.Environment[$key] = $Environment[$key] }
  $child = [Diagnostics.Process]::new()
  $child.StartInfo = $info
  $outFile = [IO.File]::Create("$LogBase.stdout.private.log")
  $errFile = [IO.File]::Create("$LogBase.stderr.private.log")
  try {
    if (-not $child.Start()) { throw 'child_start_failed' }
    if ($CloseInput) { $child.StandardInput.Close() }
    @{
      process = $child; outFile = $outFile; errFile = $errFile
      outTask = $child.StandardOutput.BaseStream.CopyToAsync($outFile)
      errTask = $child.StandardError.BaseStream.CopyToAsync($errFile)
      logBase = $LogBase
    }
  } catch { $outFile.Dispose(); $errFile.Dispose(); throw }
}

function Stop-OwnedProcess($Handle) {
  if (-not $Handle) { return }
  $child = $Handle.process
  if (-not $child.HasExited) { $child.Kill($true) }
  $child.WaitForExit()
  $null = $Handle.outTask.GetAwaiter().GetResult()
  $null = $Handle.errTask.GetAwaiter().GetResult()
  $Handle.outFile.Dispose(); $Handle.errFile.Dispose()
}

function Invoke-OwnedProcess([string]$File, [string[]]$Arguments, [string]$LogBase, [int]$Deadline = 1200, [hashtable]$Environment = @{}) {
  $phase = [regex]::Replace([IO.Path]::GetFileName($LogBase).ToLowerInvariant(), '[^a-z0-9_]', '_')
  $handle = Start-OwnedProcess $File $Arguments $LogBase $Environment
  try {
    if (-not $handle.process.WaitForExit($Deadline * 1000)) { throw "child_deadline_exceeded_$phase" }
    $code = $handle.process.ExitCode
  } finally { Stop-OwnedProcess $handle }
  if ($code -ne 0) { throw "child_command_failed_$phase" }
}

function Get-CandidateState([string]$LogDirectory) {
  $git = Get-ToolPath 'git'
  Invoke-OwnedProcess $git @('rev-parse', 'HEAD') (Join-Path $LogDirectory 'git-head')
  Invoke-OwnedProcess $git @('diff', '--binary', 'HEAD') (Join-Path $LogDirectory 'git-diff')
  Invoke-OwnedProcess $git @('ls-files', '--others', '--exclude-standard', '-z') (Join-Path $LogDirectory 'git-untracked')
  $untracked = @([IO.File]::ReadAllText((Join-Path $LogDirectory 'git-untracked.stdout.private.log')).Split([char]0) | Where-Object { $_ } | Sort-Object | ForEach-Object {
      [ordered]@{ path = $_; sha256 = (Get-FileHash -LiteralPath (Join-Path $script:RepoRoot $_) -Algorithm SHA256).Hash }
  })
  [ordered]@{
    head = (Get-Content -LiteralPath (Join-Path $LogDirectory 'git-head.stdout.private.log') -Raw).Trim()
    diffSha256 = (Get-FileHash -LiteralPath (Join-Path $LogDirectory 'git-diff.stdout.private.log')).Hash
    untracked = $untracked
  }
}

function Get-RuleState {
  $ruleRoot = Join-Path $script:RepoRoot 'docs/trackerdata'
  if (Test-Path -LiteralPath $ruleRoot) {
    @(Get-ChildItem -LiteralPath $ruleRoot -File -Recurse | Where-Object { $_.Extension -in @('.md', '.htm', '.html', '.json') } | Sort-Object FullName | ForEach-Object {
      [ordered]@{ path = [IO.Path]::GetRelativePath($ruleRoot, $_.FullName); sha256 = (Get-FileHash -LiteralPath $_.FullName).Hash }
    })
  }
}

function Invoke-LiveAPI([string]$Method, $Body = @{}, [switch]$Poll, [int]$ExpectedStatus = 200) {
  if (-not $Poll) {
    if ($script:RequestCount -ge $script:Run.budgets.maxRequests) { throw 'request_budget_exhausted' }
    $script:RequestCount++
    $script:Run.requests = $script:RequestCount
    Write-PrivateJson (Join-Path $script:RunDir 'run.json') $script:Run
  }
  # Match browser pacing: at most four requests/second stays below the server's 300/minute limit.
  Start-Sleep -Milliseconds 250
  $response = Invoke-WebRequest -Uri "$script:BaseURL/api/app/$Method" -Method Post -ContentType 'application/json' -Body (ConvertTo-Json -InputObject $Body -Depth 100 -Compress) -WebSession $script:Session -Headers @{ Origin = $script:BaseURL; 'X-CSRF-Token' = $script:CSRF } -TimeoutSec 60 -SkipHttpErrorCheck
  if ($response.StatusCode -ne $ExpectedStatus) {
    if ($response.StatusCode -eq 429 -or $response.StatusCode -ge 500) { $script:RemoteStop = $true }
    # Keep only fixed local API errors; response bodies may contain private data.
    $errorCode = 'unclassified'
    $failureCode = 'unclassified'
    $recovery = 'unclassified'
    try {
      if ($response.Content -is [string] -and $response.Content.Length -le 4096) {
        $errorPayload = $response.Content | ConvertFrom-Json -AsHashtable -ErrorAction Stop
        $errorMessage = $errorPayload.error
        if ($errorMessage -is [string]) {
          $errorCode = switch -CaseSensitive ($errorMessage) {
            'rate limit exceeded' { 'rate_limit_exceeded' }
            'authentication required' { 'authentication_required' }
            'csrf validation failed' { 'csrf_validation_failed' }
            default { 'unclassified' }
          }
        }
        if ($errorPayload.failure -is [System.Collections.IDictionary]) {
          $structuredCode = $(if ($errorPayload.failure.Contains('code')) { $errorPayload.failure.code } else { $errorPayload.failure.Code })
          $structuredRecovery = $(if ($errorPayload.failure.Contains('recovery')) { $errorPayload.failure.recovery } else { $errorPayload.failure.Recovery })
          if ($structuredCode -is [string] -and $structuredCode -cmatch '^[a-z_]{1,80}$') { $failureCode = $structuredCode }
          if ($structuredRecovery -is [string] -and $structuredRecovery -cmatch '^[a-z_]{1,80}$') { $recovery = $structuredRecovery }
        }
      }
    } catch { }
    $diagnostic = @{ method = $(if ($Method -cmatch '^[A-Za-z]{1,80}$') { $Method } else { 'unknown' }); status = [int]$response.StatusCode; expectedStatus = $ExpectedStatus; errorCode = $errorCode; failureCode = $failureCode; recovery = $recovery }
    [IO.File]::AppendAllText((Join-Path $script:RunDir 'api-errors.private.jsonl'), (ConvertTo-Json $diagnostic -Compress) + "`n")
    throw 'api_request_failed'
  }
  if ($ExpectedStatus -in @(200, 202)) { $response.Content | ConvertFrom-Json -AsHashtable }
}

function Assert-Runtime($Info) {
  $runtime = $Info.testRuntime
  if ($Info.buildIdentifier -cne $script:Run.buildIdentifier -or -not $runtime -or $runtime.mode -cne 'live_test' -or $runtime.runId -cne $script:Run.runId -or $runtime.trackerSubmissionAllowed -ne $false -or $runtime.clientMutationAllowed -ne $false -or $runtime.imageUploadsRequireJournal -ne $true -or $runtime.imageUploadLimit -ne $script:Run.budgets.maxImages) {
    throw 'runtime_identity_or_policy_mismatch'
  }
}

function Set-RunTrackerScope($Catalog) {
  # Catalog names identify implementations; configured/auth readiness stays a workflow preflight concern.
  $registered = @($Catalog.entries | ForEach-Object { $_.name })
  if ($registered.Count -eq 0 -or @($registered | Where-Object { $_ -cnotmatch '^[A-Z0-9]+$' }).Count -gt 0) { throw 'tracker_catalog_invalid' }
  $available = @($script:Run.selectedTrackers | Where-Object { $_ -cin $registered })
  $unavailable = @($script:Run.selectedTrackers | Where-Object { $_ -cnotin $registered })
  if ($script:Run.ContainsKey('availableTrackers') -and (ConvertTo-Json -InputObject @($script:Run.availableTrackers) -Compress) -cne (ConvertTo-Json -InputObject $available -Compress)) { throw 'tracker_availability_changed_new_run_required' }
  $script:Run.availableTrackers = $available
  $script:Run.unavailableTrackers = $unavailable
  $script:Results = @($script:Results | Where-Object stage -NE 'tracker_availability')
  foreach ($id in $unavailable) {
    foreach ($caseID in $script:Run.caseIds) {
      Add-Result $caseID '' 'tracker_availability' 'blocked' ('tracker_not_registered_' + $id.ToLowerInvariant()) @{ trackerId = $id; trackerScope = $script:Run.trackerScope }
    }
  }
  if ($unavailable.Count -gt 0 -and $script:Run.trackerScope -eq 'explicit') { throw 'explicit_tracker_not_registered' }
  if ($available.Count -eq 0) { throw 'no_registered_selected_trackers' }
}

function Start-VerifiedServer($Profile) {
  $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 7480)
  try { $listener.Start() } catch { throw 'port_7480_in_use' } finally { $listener.Stop() }
  $arguments = @('serve', '--live-test', '--live-test-max-images', [string]$script:Run.budgets.maxImages, '--dev-no-auth', '--config', $Profile.configPath, '--host', '127.0.0.1', '--port', '7480')
  $handle = Start-OwnedProcess $script:Binary $arguments (Join-Path $script:RunDir ('server-' + [guid]::NewGuid().ToString('N')))
  try {
    $record = @{ pid = $handle.process.Id; path = $script:Binary; startTicks = $handle.process.StartTime.ToUniversalTime().Ticks; runId = $script:Run.runId; buildIdentifier = $script:Run.buildIdentifier }
    Write-PrivateJson (Join-Path $script:RunDir 'process.private.json') $record
    $script:Session = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
      if ($handle.process.HasExited) { throw 'server_exited_before_ready' }
      try {
        $auth = Invoke-RestMethod -Uri "$script:BaseURL/api/auth/status" -WebSession $script:Session -TimeoutSec 2
        if (-not $auth.authenticated -or -not $auth.csrfToken) { throw 'test_session_unavailable' }
        $script:CSRF = $auth.csrfToken
        $info = Invoke-LiveAPI 'GetApplicationInfo' -Poll
        Assert-Runtime $info
        $owner = @(Get-NetTCPConnection -State Listen -LocalPort 7480 -ErrorAction Stop | Select-Object -ExpandProperty OwningProcess -Unique)
        if ($owner.Count -ne 1 -or $owner[0] -ne $handle.process.Id) { throw 'listener_owner_mismatch' }
        return @{ handle = $handle; process = $record; info = $info }
      } catch {
        if ($_.Exception.Message -in @('runtime_identity_or_policy_mismatch', 'listener_owner_mismatch')) { throw }
        Start-Sleep -Milliseconds 1000
      }
    }
    throw 'server_readiness_deadline'
  } catch { Stop-OwnedProcess $handle; throw }
}

function Invoke-BrowserCheck([ValidateSet('local', 'hosted', 'restart', 'input', 'input-restart')][string]$Phase = 'local') {
  $name = $(if ($Phase -eq 'local') { 'browser' } else { "browser-$Phase" })
  $receiptPath = Join-Path $script:RunDir "$name-results.json"
  $requestsPath = Join-Path $script:RunDir "$name-requests.private.json"
  foreach ($path in @($receiptPath, $requestsPath)) { if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path } }
  try {
    Invoke-OwnedProcess (Get-ToolPath 'node') @((Join-Path $script:RepoRoot 'webui/node_modules/@playwright/test/cli.js'), 'test', '--config', (Join-Path $PSScriptRoot 'browser/playwright.config.cjs')) (Join-Path $script:RunDir $name) 600 @{ UPBRR_LIVE_RUN_DIR = $script:RunDir; UPBRR_LIVE_BROWSER_PHASE = $Phase }
    Read-PrivateJson $receiptPath
  } finally {
    # Failed browser checks still consume the budget and retain completed evidence.
    if (Test-Path -LiteralPath $requestsPath) { $script:RequestCount += (Read-PrivateJson $requestsPath).requests }
    if (Test-Path -LiteralPath $receiptPath) {
      $script:Results = @($script:Results | Where-Object { $_.evidence.browserPhase -cne $Phase })
      foreach ($row in (Read-PrivateJson $receiptPath).results) {
        if (-not $row.evidence) { $row.evidence = @{} }
        $row.evidence.browserPhase = $Phase
        Add-Result $row.caseId $row.laneId $row.stage $row.status $row.reason $row.evidence
      }
    }
  }
}

function Wait-Workflow($Current, [string]$SnapshotPath, [datetime]$Deadline = [datetime]::MinValue) {
  $workflowID = $Current.workflow.id
  if (-not $workflowID) { throw 'workflow_identity_missing' }
  if ($Deadline -eq [datetime]::MinValue) { $Deadline = [datetime]::UtcNow.AddSeconds($script:Run.budgets.timeoutSeconds) }
  while ($Current.operation.status -in @('queued', 'running', 'pending')) {
    Write-PrivateJson $SnapshotPath $Current
    if ([datetime]::UtcNow -ge $deadline) { throw 'workflow_deadline_exceeded' }
    Start-Sleep -Milliseconds 1000
    $Current = Invoke-LiveAPI 'GetReleaseWorkflow' @{ workflowId = $workflowID } -Poll
    if ($Current.workflow.id -cne $workflowID) { throw 'workflow_identity_changed' }
  }
  Write-PrivateJson $SnapshotPath $Current
  $Current
}

function Get-PendingActions($Current) {
  @(@($Current.workflow.requiredActions) + @($Current.continuation.requiredActions) + @($Current.media.requiredActions) | Where-Object { $_ -and $_.status -in @('', 'pending', $null) } | Sort-Object id -Unique)
}

function Get-FeedbackEvidence($Current, [switch]$Semantic) {
  $bound = [ordered]@{
    workflowId = $Current.workflow.id; revision = $Current.workflow.revision
    release = $Current.workflow.release; selection = $Current.selection.fingerprint
    facts = $Current.factInstructions.fingerprint; projections = $Current.projections.inputFingerprint
    preflight = $Current.preflight.inputFingerprint; dupes = $Current.dupes.inputFingerprint
    capture = $Current.media.captureFingerprint; requirements = $Current.media.requirementsFingerprint
  }
  if ($Semantic) { $bound.Remove('revision') }
  Get-TextHash (ConvertTo-Json $bound -Depth 20 -Compress)
}

function Get-ActionSemantics($Actions) {
  $canonical = @($Actions | Sort-Object id | ForEach-Object {
    [ordered]@{ id = $_.id; kind = $_.kind; status = $_.status; trackerId = $_.trackerId; effectKind = $_.effectKind; effectScopeId = $_.effectScopeId; prompt = $_.prompt; options = $_.options; allowsFreeText = $_.allowsFreeText; createdAt = $_.createdAt; expiresAt = $_.expiresAt }
  })
  Get-TextHash (ConvertTo-Json -InputObject $canonical -Depth 40 -Compress)
}

function Save-Feedback($Lane, $Current, [string]$Goal) {
  $actions = @(Get-PendingActions $Current)
  if ($actions.Count -eq 0) { return }
  $receipt = @{
    laneId = $Lane.laneId; caseId = $Lane.caseId; trackerIds = $Lane.trackerIds; sat = $Lane.sat
    goal = $Goal; status = 'needs_input'; sourceFingerprint = $Lane.sourceFingerprint
    authority = @{ workflowId = $Current.workflow.id; expectedRevision = $Current.workflow.revision }
    evidenceSha256 = Get-FeedbackEvidence $Current
    semanticSha256 = Get-FeedbackEvidence $Current -Semantic
    requiredActions = $actions; answers = @(); acceptedAt = $null; rationale = $null
  }
  $script:Feedback = @($script:Feedback | Where-Object { $_.laneId -cne $Lane.laneId }) + @($receipt)
  Write-PrivateJson (Join-Path $script:RunDir 'feedback.private.json') $script:Feedback
}

function Resolve-FeedbackAuthority($Lane, $Feedback, $Current) {
  $actions = @(Get-PendingActions $Current)
  if ($Feedback.authority.expectedRevision -eq $Current.workflow.revision -and $Feedback.evidenceSha256 -ceq (Get-FeedbackEvidence $Current) -and (Get-ActionSemantics $Feedback.requiredActions) -ceq (Get-ActionSemantics $actions)) { return 'current' }
  # A restart may only restamp workflow/action revisions. Rebind an accepted choice
  # only when every retained evidence fingerprint and action semantic is identical.
  $equivalent = $Feedback.semanticSha256 -and $Feedback.semanticSha256 -ceq (Get-FeedbackEvidence $Current -Semantic) -and (Get-ActionSemantics $Feedback.requiredActions) -ceq (Get-ActionSemantics $actions)
  if ($equivalent) {
    $oldAuthority = $Feedback.authority
    foreach ($answer in $Feedback.answers) {
      if ($answer.workflowRevision -ne $oldAuthority.expectedRevision) { throw 'feedback_answer_revision_stale' }
      $answer.workflowRevision = $Current.workflow.revision
    }
    $Feedback.authority = @{ workflowId = $Current.workflow.id; expectedRevision = $Current.workflow.revision }
    $Feedback.requiredActions = $actions
    $Feedback.evidenceSha256 = Get-FeedbackEvidence $Current
    $receipt = @{ laneId = $Lane.laneId; event = 'equivalent_restart_authority_rebound'; at = [datetime]::UtcNow.ToString('o'); oldAuthority = $oldAuthority; authority = $Feedback.authority; semanticSha256 = $Feedback.semanticSha256; answers = $Feedback.answers; acceptedAt = $Feedback.acceptedAt; rationale = $Feedback.rationale }
    [IO.File]::AppendAllText((Join-Path $script:RunDir 'feedback-history.private.jsonl'), (ConvertTo-Json $receipt -Depth 40 -Compress) + "`n")
    Write-PrivateJson (Join-Path $script:RunDir 'feedback.private.json') $script:Feedback
    return 'rebound'
  }
  $previous = @{ laneId = $Lane.laneId; event = 'changed_evidence_requires_acceptance'; at = [datetime]::UtcNow.ToString('o'); feedback = $Feedback }
  [IO.File]::AppendAllText((Join-Path $script:RunDir 'feedback-history.private.jsonl'), (ConvertTo-Json $previous -Depth 60 -Compress) + "`n")
  if ($actions.Count -gt 0) { Save-Feedback $Lane $Current $Feedback.goal }
  else {
    $Feedback.authority = @{ workflowId = $Current.workflow.id; expectedRevision = $Current.workflow.revision }
    $Feedback.evidenceSha256 = Get-FeedbackEvidence $Current
    $Feedback.semanticSha256 = Get-FeedbackEvidence $Current -Semantic
    $Feedback.requiredActions = @(); $Feedback.answers = @(); $Feedback.acceptedAt = $null; $Feedback.rationale = $null
    $Feedback.status = 'needs_input'; $Feedback.topic = 'workflow_revalidation_required'
    Write-PrivateJson (Join-Path $script:RunDir 'feedback.private.json') $script:Feedback
  }
  'refreshed'
}

function Add-Result([string]$CaseID, [string]$LaneID, [string]$Stage, [string]$Status, [string]$Reason, $Evidence = @{}) {
  # Reports describe the latest stage observation. Safety violations remain historical.
  if ($Stage -notin @('submission_negative_control', 'forbidden_effects', 'restart_forbidden_effects')) {
    $script:Results = @($script:Results | Where-Object {
      $_.caseId -cne $CaseID -or $_.laneId -cne $LaneID -or $_.stage -cne $Stage -or
      $_.evidence.host -cne $Evidence.host -or $_.evidence.trackerId -cne $Evidence.trackerId -or
      $_.evidence.browserPhase -cne $Evidence.browserPhase
    })
  }
  $script:Results += @{
    caseId = $CaseID; laneId = $LaneID; stage = $Stage; status = $Status; reason = $Reason; evidence = $Evidence
  }
}

function Get-LiveFailureCodes($Current) {
  $failures = @($Current.workflow.failures) + @($Current.operation.failures) + @($Current.preflight.results.failures) +
    @($Current.dupes.results.failures) + @($Current.media.failures) + @($Current.media.hostAttempts.failures) +
    @($Current.descriptions.failures) + @($Current.descriptions.trackerResults.failures) + @($Current.dryRun.reports.failures)
  # OperationFailure uses Go's exported field names, including inside lower-case workflow envelopes.
  $codes = @($failures | ForEach-Object { $_.failure.Code } | Where-Object { $_ -cmatch '^[a-z][a-z0-9_]{0,80}$' } | Sort-Object -Unique)
  if (@($codes | Where-Object { $_ -match 'rate_limit|network|timeout|transport' }).Count -gt 0 -or
      @($failures | Where-Object { $_.failure.Message -match '(?i)rate.limit|too many requests|network|timed?\s*out|connection refused' }).Count -gt 0) {
    $script:RemoteStop = $true
  }
  $codes
}

function Record-Stage($Lane, $Current, [string]$Goal) {
  $field = @{ prepared = 'release'; input_ready = 'inputReadiness'; trackers_assessed = 'preflight'; duplicates_decided = 'dupes'; media_ready = 'media'; descriptions_ready = 'descriptions'; dry_run = 'dryRun' }[$Goal]
  $value = $Current[$field]
  $actions = @(Get-PendingActions $Current)
  $blockingInputFields = @()
  if ($Goal -eq 'input_ready') {
    $blockingInputFields = @($value.fields | Where-Object { $_.status -in @('missing', 'invalid') -and $_.disposition -ne 'advisory' })
  }
  $status = 'inconclusive'; $reason = 'stage_result_missing'
  if ($actions.Count -gt 0) { $status = 'needs_input'; $reason = 'typed_action_required' }
  elseif ($Goal -eq 'input_ready' -and $blockingInputFields.Count -gt 0) { $status = 'blocked'; $reason = 'input_readiness_incomplete' }
  elseif ($value -and (($Goal -eq 'prepared') -or $value.status -in @('succeeded', 'completed', 'ready'))) { $status = 'pass'; $reason = 'retained_stage_succeeded' }
  elseif ($Current.operation.status -eq 'failed') { $status = 'fail'; $reason = 'workflow_operation_failed' }
  elseif ($value.status -in @('failed', 'blocked', 'partial', 'needs_input') -or $Current.continuation.disposition -in @('failed', 'needs_action', 'partial', 'canceled')) { $status = 'blocked'; $reason = 'workflow_stage_blocked' }
  elseif ($Goal -in @('media_ready', 'descriptions_ready') -and $value.status -eq 'skipped') { $status = 'pass'; $reason = 'retained_stage_not_required' }
  elseif ($Goal -eq 'dry_run' -and $value.status -eq 'skipped' -and $Current.operation.status -eq 'completed') { $status = 'not_applicable'; $reason = 'tracker_dry_run_skipped' }
  if ($Goal -eq 'media_ready' -and $status -eq 'pass' -and @($value.artifacts | Where-Object { $_.kind -eq 'screenshot' }).Count -gt 0) { $status = 'inconclusive'; $reason = 'local_capture_requires_decode' }
  $failureCodes = @(Get-LiveFailureCodes $Current)
  if ($Current.selection -and (ConvertTo-Json -InputObject @($Current.selection.trackerIds) -Compress) -cne (ConvertTo-Json -InputObject @($Lane.trackerIds) -Compress)) { throw 'tracker_selection_changed' }
  if ($Goal -eq 'dry_run' -and $value -and $value.noSeed -ne $true) { $status = 'fail'; $reason = 'dry_run_no_seed_not_locked' }
  if ($Goal -in @('prepared', 'input_ready') -and $status -eq 'pass') {
    $prepared = $(if ($Goal -eq 'input_ready') { $Current.release } else { $value })
    foreach ($field in $Lane.expectedIdentity.Keys) {
      if ($prepared.release.Identity.$field -ne $Lane.expectedIdentity[$field]) {
        $status = 'fail'; $reason = 'metadata_identity_mismatch'
      }
    }
    if ($Lane.expectedPlaylists -and
        (@($prepared.release.Source.SelectedPlaylists | ForEach-Object {
          $file = ([string]$_.file).ToUpperInvariant()
          if ($Lane.expectedPlaylists[0].Contains(':')) { ([string]$_.discId) + ':' + $file } else { $file }
        }) -join ',') -cne ($Lane.expectedPlaylists -join ',')) {
      $status = 'fail'; $reason = 'bdmv_playlist_selection_mismatch'
    }
  }
  $trackers = @($Current.preflight.results | Where-Object { $_.trackerId -cin $Lane.trackerIds } | ForEach-Object {
    @{ trackerId = $_.trackerId; authReady = [bool]$_.authReady; eligible = $_.state -eq 'ready'; state = $(if ($_.state -cmatch '^[a-z_]+$') { $_.state } else { 'unknown' }) }
  })
  $duplicateSearches = @($Current.dupes.results | Where-Object { $_.trackerId -cin $Lane.trackerIds } | ForEach-Object {
    @{
      trackerId = $_.trackerId
      status = $(if ($_.status -cmatch '^[a-z_]+$') { $_.status } else { 'unknown' })
      decision = $(if ($_.decision -cmatch '^[a-z_]+$') { $_.decision } else { 'unknown' })
      scope = $(if ($_.search.scope -cmatch '^[a-z_]+$') { $_.search.scope } else { 'unknown' })
      complete = [bool]$_.search.complete; pages = [int]$_.search.pages; candidateCount = [int]$_.search.candidateCount
    }
  })
  Add-Result $Lane.caseId $Lane.laneId $Goal $status $reason @{ failureCodes = $failureCodes; trackerCount = @($Lane.trackerIds).Count; trackers = $trackers; duplicateSearches = $duplicateSearches; sat = $Lane.sat; requiredActionCount = $actions.Count; artifacts = @($Current.media.artifacts | Where-Object kind -EQ 'screenshot').Count }
  Save-Feedback $Lane $Current $Goal
  $status
}

function Assert-InputOnlyBoundary($Lane, $Current) {
  if (-not $Current.release -or -not $Current.inputReadiness) { throw 'input_readiness_snapshot_missing' }
  if (-not $Current.workflow.release -or $Current.workflow.release.id -cne $Current.release.id -or $Current.workflow.release.revision -ne $Current.release.revision) { throw 'input_release_ref_mismatch' }
  if (-not $Current.factInstructions -or -not $Current.workflow.factInstructions -or $Current.workflow.factInstructions.id -cne $Current.factInstructions.id -or $Current.workflow.factInstructions.revision -ne $Current.factInstructions.revision) { throw 'input_fact_instructions_ref_mismatch' }
  if (-not $Current.workflow.inputReadiness -or $Current.workflow.inputReadiness.id -cne $Current.inputReadiness.id -or $Current.workflow.inputReadiness.revision -ne $Current.inputReadiness.revision) { throw 'input_readiness_ref_mismatch' }
  $selectedTrackerIDs = @($Current.inputReadiness.selectedTrackerIds | Sort-Object -CaseSensitive)
  $expectedTrackerIDs = @($Lane.trackerIds | Sort-Object -CaseSensitive)
  if ((ConvertTo-Json -InputObject $selectedTrackerIDs -Compress) -cne (ConvertTo-Json -InputObject $expectedTrackerIDs -Compress)) { throw 'input_tracker_requirements_changed' }
  $currentStageNames = @('projections', 'preflight', 'dupes', 'media', 'descriptions', 'dryRun', 'uploadResult')
  $workflowStageNames = @('trackerProjections', 'trackerPreflight', 'dupes', 'media', 'descriptions', 'dryRun', 'uploadResult')
  $currentStageRefs = @($currentStageNames | Where-Object { $Current[$_] })
  $workflowStageRefs = @($workflowStageNames | Where-Object { $Current.workflow[$_] })
  if ($currentStageRefs.Count -gt 0) { throw 'input_only_boundary_crossed' }
  if ($workflowStageRefs.Count -gt 0) { throw 'input_only_workflow_ref_crossed' }
  $operationJournal = $null
  if ($Current.operation) {
    $operationJournal = [ordered]@{
      operationId = [string]$Current.operation.id; status = [string]$Current.operation.status
      eventCount = @($Current.operation.events).Count
    }
  }
  Add-Result $Lane.caseId $Lane.laneId 'input_operation_receipt' 'pass' 'input_refs_bound_no_downstream_stage_refs' @{
    refs = [ordered]@{
      release = [ordered]@{ id = [string]$Current.release.id; revision = [uint64]$Current.release.revision }
      factInstructions = [ordered]@{ id = [string]$Current.factInstructions.id; revision = [uint64]$Current.factInstructions.revision }
      inputReadiness = [ordered]@{ id = [string]$Current.inputReadiness.id; revision = [uint64]$Current.inputReadiness.revision }
    }
    observed = [ordered]@{
      currentDownstreamStageRefs = $currentStageRefs; workflowDownstreamStageRefs = $workflowStageRefs
      workflowOperationJournal = $operationJournal
    }
    configured = [ordered]@{ clientSearchSkip = [bool]$Lane.preparation.Search.Skip }
    correctionRevision = [uint64]$Current.inputReadiness.correctionRevision
    workflowRevision = [uint64]$Current.workflow.revision; readinessFieldCount = @($Current.inputReadiness.fields).Count
    selectedTrackerCount = @($Lane.trackerIds).Count
  }
  foreach ($stage in @('trackers_assessed', 'duplicates_decided', 'media_ready', 'image_host', 'descriptions_ready', 'dry_run', 'uploaded', 'client_write')) {
    Add-Result $Lane.caseId $Lane.laneId $stage 'not_applicable' 'input_only_scope'
  }
}

function Continue-Lane($Lane, [string]$Goal, $Current, $Intent, $Answers = @()) {
  $intentCopy = ConvertTo-Json -InputObject $Intent -Depth 100 | ConvertFrom-Json -AsHashtable
  $request = @{ idempotencyKey = [guid]::NewGuid().ToString('N'); goal = $Goal; intent = $intentCopy }
  if ($intentCopy.preparation) { $Lane.preparation = $intentCopy.preparation }
  $remainingAnswers = @()
  foreach ($answer in $Answers) {
    $action = @(Get-PendingActions $Current | Where-Object id -CEQ $answer.actionId)[0]
    if ($action.kind -eq 'select_playlist') {
      if ($Answers.Count -ne 1 -or $answer.workflowRevision -ne $Current.workflow.revision -or
          -not $intentCopy.preparation -or @($answer.selectedValues).Count -eq 0 -or
          @($answer.selectedValues | Where-Object { $_ -cnotin @($action.options.value) }).Count -gt 0) { throw 'feedback_playlist_answer_invalid' }
      # Playlist answers replace preparation facts; generic action answers only dismiss the prompt.
      $instructions = ConvertTo-Json -InputObject $Current.factInstructions.instructions -Depth 100 | ConvertFrom-Json -AsHashtable
      $instructions.Playlist = @{ Set = $true; Selected = @($answer.selectedValues); UseAll = $false }
      $intentCopy.preparation.Instructions = $instructions
      $intentCopy.factInstructions = $instructions
      continue
    }
    if ($action.kind -ne 'review_duplicates') { $remainingAnswers += $answer; continue }
    if ($answer.workflowRevision -ne $Current.workflow.revision -or $action.trackerId -cnotin $Lane.trackerIds -or
        @($answer.selectedValues).Count -ne 1 -or $answer.selectedValues[0] -cnotin @('accepted', 'ignored') -or
        $answer.selectedValues[0] -cnotin @($action.options.value)) { throw 'feedback_duplicate_answer_invalid' }
    # Duplicate review uses the decision command; generic action resolution only dismisses its prompt.
    if (-not $intentCopy.duplicateDecisions) { $intentCopy.duplicateDecisions = @{} }
    $intentCopy.duplicateDecisions[$action.trackerId] = $answer.selectedValues[0]
  }
  if ($remainingAnswers.Count -gt 0) {
    if ($intentCopy.duplicateDecisions) { throw 'feedback_duplicate_requires_separate_transition' }
    $request.answers = $remainingAnswers
  }
  $snapshot = Join-Path $script:RunDir "snapshots/$($Lane.laneId).private.json"
  $deadline = [datetime]::UtcNow.AddSeconds($script:Run.budgets.timeoutSeconds)
  for ($transition = 0; $transition -lt 32; $transition++) {
    if ($script:RemoteStop) { throw 'remote_work_stopped' }
    if ([datetime]::UtcNow -ge $deadline) { throw 'workflow_deadline_exceeded' }
    if ($Current) { $request.authority = @{ workflowId = $Current.workflow.id; expectedRevision = $Current.workflow.revision } }
    $next = Invoke-LiveAPI 'ContinueReleaseWorkflow' $request
    if (-not $next.workflow.id -or ($Current -and $next.workflow.id -cne $Current.workflow.id)) { throw 'workflow_identity_changed' }
    $Lane.workflowId = $next.workflow.id; $Lane.goal = $Goal
    Write-PrivateJson (Join-Path $script:RunDir 'lanes.private.json') $script:Lanes
    $next = Wait-Workflow $next $snapshot $deadline
    $Lane.authority = @{ workflowId = $next.workflow.id; expectedRevision = $next.workflow.revision }
    Write-PrivateJson (Join-Path $script:RunDir 'lanes.private.json') $script:Lanes
    if ($Current -and $next.workflow.revision -eq $Current.workflow.revision) { return $next }
    if ($request.answers) {
      # One backend transition can consume one answer. Never replay old revision-bound answers.
      $request.Remove('answers')
      if ($next.factInstructions.instructions -and $intentCopy.preparation) {
        $intentCopy.preparation.Instructions = $next.factInstructions.instructions
        $Lane.preparation = $intentCopy.preparation
      }
      if ($next.factInstructions.instructions -and $intentCopy.factInstructions) { $intentCopy.factInstructions = $next.factInstructions.instructions }
    }
    $Current = $next
  }
  throw 'workflow_transition_limit_exceeded'
}

function Resume-Lane($Lane, $Current, [string]$CompletedGoal) {
  if ($script:Run.suite -eq 'Input') {
    if ($CompletedGoal -ne 'input_ready') { throw 'input_resume_goal_invalid' }
    Assert-InputOnlyBoundary $Lane $Current
    return $Current
  }
  $goals = @('prepared', 'trackers_assessed', 'duplicates_decided', 'media_ready')
  if ($script:Run.suite -eq 'Dupe') { $goals = @('prepared', 'trackers_assessed', 'duplicates_decided') }
  $start = [array]::IndexOf($goals, $CompletedGoal)
  if ($start -lt 0) { return $Current }
  $intent = @{ executionMode = $script:Run.executionMode; interaction = 'unattended'; trackerIds = $Lane.trackerIds; noSeed = $true; skipRemoteDuplicates = [bool]$script:Run.skipRemoteDuplicates }
  for ($index = $start + 1; $index -lt $goals.Count; $index++) {
    if ($goals[$index] -eq 'media_ready') {
      if (-not (Test-LiveContentLane $Lane)) { break }
      $intent.media = Get-LiveMediaInstructions $Lane $Current
    }
    $Current = Continue-Lane $Lane $goals[$index] $Current $intent
    $script:Results = @($script:Results | Where-Object { $_.laneId -cne $Lane.laneId -or $_.stage -cne $goals[$index] })
    if ((Record-Stage $Lane $Current $goals[$index]) -ne 'pass') { break }
  }
  $Current
}

function Stop-RecordedServer([string]$Directory) {
  $recordPath = Join-Path $Directory 'process.private.json'
  if (-not (Test-Path -LiteralPath $recordPath)) { return }
  $record = Read-PrivateJson $recordPath
  $child = Get-Process -Id $record.pid -ErrorAction SilentlyContinue
  if (-not $child) { return }
  if ($child.StartTime.ToUniversalTime().Ticks -ne $record.startTicks) { return } # The recorded process exited; this PID was reused.
  if ($child.Path -cne $record.path) { throw 'recorded_process_identity_mismatch' }
  $child.Kill($true); $child.WaitForExit()
}

function Get-LiveRestartLane {
  if ($script:Run.suite -eq 'Input') {
    $passed = @($script:Results | Where-Object { $_.stage -eq 'input_ready' -and $_.status -eq 'pass' } | Select-Object -ExpandProperty laneId -Unique)
    return $script:Lanes | Where-Object { $_.workflowId -and $_.laneId -cin $passed } | Select-Object -First 1
  }
  foreach ($stage in @('image_host', 'media_ready')) {
    $passed = @($script:Results | Where-Object { $_.stage -eq $stage -and $_.status -eq 'pass' } | Select-Object -ExpandProperty laneId -Unique)
    $eligible = @($script:Lanes | Where-Object { $_.workflowId -and $_.laneId -cin $passed } | Select-Object -First 1)
    if ($eligible.Count -gt 0) { return $eligible[0] }
  }
  $script:Lanes | Where-Object workflowId | Select-Object -First 1
}

function Invoke-RunCleanup {
  $script:Cleanup = @{ deleted = $null; retained = $null; pending = $null; unknown = $null; failed = $null; state = 'unresolved' }
  Stop-RecordedServer $script:RunDir
  $logBase = Join-Path $script:RunDir ('cleanup-' + [guid]::NewGuid().ToString('N'))
  $handle = Start-OwnedProcess $script:Binary @('live-test', 'cleanup', '--run-dir', $script:RunDir) $logBase
  try {
    if (-not $handle.process.WaitForExit(120000)) { throw 'cleanup_deadline_exceeded' }
    $exitCode = $handle.process.ExitCode
  } finally { Stop-OwnedProcess $handle }
  try { $receipt = Read-PrivateJson "$logBase.stdout.private.log" } catch { throw 'cleanup_receipt_missing' }
  if ($receipt.runId -cne $script:Run.runId) { throw 'cleanup_identity_mismatch' }
  Write-PrivateJson (Join-Path $script:RunDir 'cleanup.json') $receipt
  $script:Cleanup = @{ deleted = $receipt.deleted; retained = [int]$receipt.retained; pending = $receipt.pending; unknown = $receipt.unknown; failed = $receipt.failed; state = 'unresolved' }
  if ($exitCode -ne 0 -or $receipt.pending -gt 0 -or $receipt.unknown -gt 0 -or $receipt.failed -gt 0) { throw 'cleanup_unresolved' }
  $script:Cleanup.state = 'complete'
}
