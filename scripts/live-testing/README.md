# Opt-in live testing

Run from a Windows checkout with PowerShell 7, Go, Node, pnpm, FFmpeg/ffprobe,
the installed WebUI dependencies, and Playwright Chromium. The runner builds the
current embedded production application, runs the focused local `LiveTest` safety
tests, snapshots the normal configuration into a private profile, and verifies
the owned process on `127.0.0.1:7480` before using it. Port 7480 must be unused.

```powershell
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Smoke -ValidateOnly
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Smoke
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Screenshots
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Dupe -Tracker LST -Sat
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Screenshots -DebugCoverage
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Smoke -CaseId MOV-1080-WEB -UploadImages -MaxImages 1
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -ResumeRun '<run-id>'
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -CleanupRun '<run-id>'
pwsh -NoProfile -File .\scripts\live-testing\compare.ps1 -BaselineRunDir '<absolute-private-baseline>' -CandidateRunDir '<absolute-private-candidate>'
pwsh -NoProfile -File .\scripts\live-testing\check-cli.ps1 -BaselineRunDir '<absolute-private-baseline>' -LaneId 'lane-0001'
```

`-Tracker` is for an explicitly tracker-specific investigation and accepts exactly
one ID. Otherwise every selected case receives the current configured default
tracker list, in its existing order. The backend determines eligibility and
preserves blocked lanes. No replacement tracker or image host is selected when a
configured service fails. `-Config` selects the source configuration for cloning.

`Smoke` uses five representative cases. `Screenshots` selects all 25 saved cases.
`Dupe` uses one movie and the paired pack/episode inputs; without `-Sat` it creates
independent ordinary and skip-client-lookup preparations. `Full` selects all 25
cases, with the same explicit paired lookup comparison and representative dupe
observations. Media preparation may itself require tracker duplicate searches;
site duplicate checks always remain enabled. Sources blocked by exact duplicates,
client matches, authentication, rules, or missing facts remain blocked.

`-CaseId` selects named cases from your corpus, including your own case IDs;
omitting it retains the suite's default case list. `-Sat` sets
`PrepareInput.Search.Skip=true` on a fresh workflow and forces fresh preparation,
without carrying client-derived matches from an earlier variant. It cannot clear
an authoritative `in_client` block. Ordinary requests use `executionMode=normal`.
`-DebugCoverage` explicitly records `executionMode=debug`, including its normal
policy bypass semantics. Both modes prohibit tracker submission and client writes.

The default image budget is zero. `-UploadImages` enables one bounded canary after
local images have decoded successfully. `-MaxImages` (1–10, default 3) is the
durable per-run dispatch budget, including failed/unknown attempts; it cannot be
increased on resume. The configured host choices remain authoritative. Only hosts
with runtime cleanup support can upload; currently that is Lostimg. A one-image
canary exercises hosting and cleanup only and cannot satisfy LST's three-image
description rule. Description/dry-run stages are attempted after successful
hosting and remain blocked if their normal prerequisites are unmet.

`-ScreenshotCount` is 1–10 (default 3). `-MaxRequests` bounds runner API commands
(default 200, maximum 2000), with operation polls bounded separately by
`-TimeoutSeconds` (default 900, maximum 3600). It is not a count of internal tracker
HTTP requests. Cases run sequentially. A network/rate-limit signal stops further
runner remote work; there are no automatic host retries. Build/tool output and
all browser artifacts remain private.

Runner API calls and browser requests to the local API are paced at no more than
four requests per second, below the production server's 300-per-minute limit.
The browser shares pacing between page traffic and direct API/media requests.
Unexpected API statuses still stop the request without retrying; the private
`api-errors.private.jsonl` records the method, actual/expected status, and an
allowlisted local error code, excluding response bodies and credentials.

## Private corpus and feedback

The default corpus is
`%LOCALAPPDATA%\upbrr-live-testing\media-corpus.private.json`. Its schema is shown
in `corpus.example.json`; copy examples outside the repository before using them.
Each case requires a stable case ID, absolute input/probe paths, input shape,
successful probe status, and original probe-file size/mtime in integer
nanoseconds. Validation re-stats originals and records directory membership.
It never modifies source media. A changed source requires a reviewed new corpus
observation, not automatic fingerprint replacement.

An optional `metadata_ids` object pins operator-verified provider identities for
one case, for example `"metadata_ids": { "imdb": 1234567, "tmdb": 12345 }`.
Supported keys are `imdb`, `tmdb`, `tvdb`, and `tvmaze`; values must be positive
integers (omit IMDb's `tt` prefix and leading zeros). TV cases use series IDs,
including when the input is one episode. Keep actual IDs in the private corpus.
The runner sends these through the existing explicit identity overrides, checks
the prepared identity, and passes the same IDs to the CLI comparison helper.
Omitted providers retain normal resolution. Changing IDs requires a new run;
saved-run continuation and comparisons retain the corpus fingerprint. Overrides
do not waive tracker rules or existing-client duplicate blocks.

Disc main-playlist/title and pack completeness are unresolved until verified by
the operator. Without a Blu-ray selection as described below, directory cases
produce `needs_input` before preparation. DVD title and pack completeness inputs
remain unsupported; provider IDs establish identity, not source selection.
These preflight feedback entries have no backend action authority.

### Blu-ray playlists and reusable BDInfo reports

For your own Blu-ray or UHD disc, use `input_shape: "disc-directory"` and an
absolute `input_path` pointing to its disc folder, mounted drive/share root, or
`BDMV` directory. Keep a
successfully probed stream in `probe_path` with the usual inventory fingerprint.
After inspecting the disc and choosing the required playlists, record that
explicit selection against its current source fingerprint:

```powershell
. .\scripts\live-testing\functions.ps1
$corpusPath = Join-Path $env:LOCALAPPDATA 'upbrr-live-testing/media-corpus.private.json'
$corpus = Read-PrivateJson $corpusPath
$case = @($corpus.cases | Where-Object case_id -CEQ 'MY-BD-DISC')[0]
$case.bdmv_selection = @{
  playlists = @('00001.mpls') # Your verified playlist names, in intended order.
  source_fingerprint = (Get-SourceFingerprint $case).fingerprint
}
Write-PrivateJson $corpusPath $corpus
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Screenshots -CaseId MY-BD-DISC -ValidateOnly
pwsh -NoProfile -File .\scripts\live-testing\run.ps1 -Suite Screenshots -CaseId MY-BD-DISC
```

Only explicit five-digit MPLS filenames are accepted; no playlist is chosen by
size or guessed from the folder name. Changing directory membership, file sizes
or timestamps requires reviewing and recording the selection again. A fresh
run passes the selection through the production preparation instructions and
checks the prepared playlist list. Duplicate, tracker and image-host gates still
apply. Identical sanitized temporary basenames for different sources require
separate runs, including two inputs both ending in `BDMV`.

The first preparation scans the selected playlists with production BDInfo and
saves its quick, extended and full reports under
`%LOCALAPPDATA%\upbrr-live-testing\bdinfo`. Later runs validate report hashes and
restore those files into their own private profile before preparation. They
recollect release facts while the production metadata service reuses the reports.
The cache survives run cleanup and is keyed by source membership/stat evidence,
playlist order, dependency lockfiles and the BDInfo scanner/report parser and
artifact-layout source.
New build timestamps and unrelated application edits do not invalidate it.
Changed scanner inputs produce a new cache entry; corrupt or incomplete saved
reports block reuse instead of being trusted. Cache manifests retain the binary
hash that originally produced the reports, and lane receipts record restored
versus newly saved reports and their hashes. Keep this entire cache private.

To measure a cold scan again, move the relevant private cache entry aside while
no run is active. Do not modify its manifest or replace report hashes to approve
different contents. No tracker submission or client write is needed to save
BDInfo. These reusable reports establish disc analysis, not screenshot decoding
or human visual review.

Backend questions are saved in each run's `feedback.private.json` with the exact
typed `requiredActions`, workflow revision, source fingerprint, and evidence
fingerprint. To resume one, put the appropriate `RequiredActionAnswer` objects in
its `answers` array and fill `acceptedAt` (UTC timestamp) and `rationale`. Preserve
the saved action IDs, revisions, and evidence. Do not put credentials or OTPs into
feedback. The runner never creates duplicate waivers or automatic approvals.

```json
{
  "answers": [
    {
      "actionId": "copy-the-current-action-id",
      "workflowRevision": 7,
      "selectedValues": ["copy-a-current-option-value"]
    }
  ],
  "acceptedAt": "2026-09-05T00:00:00Z",
  "rationale": "Operator verified the selected source against the current evidence."
}
```

Resume uses the original binary and profile, re-stats sources, checks repository,
config and rule fingerprints, retrieves the current workflow, and verifies every
action before supplying an answer. A restart that only restamps revisions can
rebind an accepted answer when all retained evidence and action semantics match;
the private feedback history records old and new authority. Changed workflow
evidence refreshes the questions and clears old answers for new acceptance.
Changed source/config/rule evidence requires a new run. Pending
typed feedback stops the server but keeps the run resumable; browser mutations
and hosting do not alter those pending lanes. Run `-CleanupRun` to abandon that
feedback and reconcile image effects. Cleanup creates a terminal marker: that
profile can never prepare or upload again. New work requires a new run. Unknown
or failed deletion results remain pending. The cleanup command cannot resolve an
upload whose provider URL was never returned, or accept a manual-deletion claim.
Those cases need operator reconciliation of the exact image and terminal run
state, with a private confirmation receipt alongside the unchanged journal.
Never retry an uncertain upload or invent a provider acknowledgement.

## Reports and checks

Private run directories live under
`%LOCALAPPDATA%\upbrr-live-testing\runs\<run-id>`. `run.json` records build,
configuration, corpus, rules, tools, budgets and effect counters. `report.json`
and `report.md` contain stable case IDs and sanitized outcomes; source identities,
provider data, snapshots, cookies and logs remain in private artifacts.
`contact-sheet.private.html` and browser screenshots support visual inspection.
Treat all files in the private root as local evidence, including session cookies.

The browser check verifies the exact owned production process, live banner,
available dry-run/locked client controls, decoded images and aspect ratio,
distinct source timestamps, image-byte hashes, and selection/reorder/reload
persistence. Identical image bytes require visual review; a static scene can
legitimately repeat. One eligible workflow also deletes a frame, recaptures its
logical slot at a changed timestamp, makes one cancellation attempt, and restores
the intended selection/order using fresh artifact IDs. A capture completing
before cancellation is inconclusive. Typed feedback preserves the exact pending
authority. After an API host canary, a separate browser pass verifies
retained published links, decodes their remote images, and verifies persistence
after reload. Finally, one owned server restart verifies the original binary,
profile, policy and workflow identity, compares retained media, and decodes any
retained local images. Recovery that requires re-preparation produces fresh typed
feedback. Original and restarted process effect counters are separate. Unavailable
pages remain inconclusive. Human color/scene review, provider fault injection,
and the listed missing media tiers remain explicit gaps. CLI/API parity and
baseline comparisons use the separate opt-in helpers shown above.

Reports distinguish `pass`, `fail`, `blocked`, `needs_input`, `inconclusive`, and
`not_applicable`. A successful individual stage does not prove the whole suite.
Normal forbidden-effect counters must be zero; the single explicit HTTP 403
negative control is counted separately. Exit 2 means incomplete or failed
coverage/cleanup. `-CleanupRun` exits 0 when cleanup completes even if retained
coverage remains incomplete. `-ValidateOnly` performs schema/stat checks without
building, launching a server, or making network requests.

```powershell
pwsh -NoProfile -File .\scripts\live-testing\validate.ps1
node --check .\scripts\live-testing\browser\live.spec.cjs
node .\webui\node_modules\@playwright\test\cli.js test --config .\scripts\live-testing\browser\playwright.config.cjs --list
```

These tests are intentionally outside `webui/e2e` discovery and all ordinary
hooks/CI. `compare.ps1` compares compatible isolated runs and writes sanitized
observed differences under the private comparisons directory; changing live
tracker contents does not automatically establish a regression. After the baseline
server has stopped and cleanup has completed, `check-cli.ps1` runs the same binary
and lane inputs in a separate zero-image profile and compares retained CLI/API
observations. Full
Go/frontend/E2E validation and a representative live run remain
separate release-readiness checks.
