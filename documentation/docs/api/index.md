---
title: API reference
description: Authenticate to the versioned release-workflow API, choose token scopes, and use the embedded OpenAPI documentation.
---

# API reference

upbrr exposes a versioned HTTP API for durable release workflows. The embedded OpenAPI document is the exact contract for the running binary.

After starting the Web UI server, open:

- Swagger UI: `http://localhost:7480/api/v1/docs`
- OpenAPI 3.1 JSON: `http://localhost:7480/api/v1/openapi.json`

When a base path is configured, prefix both routes, for example `http://localhost:7480/upbrr/api/v1/docs`.

## Authentication

API requests use a persistent bearer token:

```http
Authorization: Bearer <token>
```

After the active database and `web-auth.json` have been initialized, create tokens in [Settings → API Tokens](../web-ui/settings/api-tokens.md). Choose the required scopes, then copy the one-time plaintext value directly into a secret manager. CLI output never includes tokens.

List token metadata:

```powershell
.\upbrr.exe api-token list
```

Revoke a token:

```powershell
.\upbrr.exe api-token revoke <token-id>
```

## Scopes

| Scope              | Grants                                                                |
| ------------------ | --------------------------------------------------------------------- |
| `workflow:read`    | Immutable workflow, operation, capability, and result reads.          |
| `workflow:write`   | Preparation and non-execution workflow commands.                      |
| `workflow:execute` | Commands that consume reviewed upload plans and can cause submission. |

An omitted or empty scope selection grants all currently supported scopes. Prefer the smallest set. Keep `workflow:execute` separate from read-only monitoring where possible.

The token owner value isolates durable workflows. Reuse one stable owner for one automation identity; do not mix unrelated operators under the same owner without intending to share that workflow namespace.

## Route groups

The current API registers these top-level resources:

| Route                        | Purpose                                                                     |
| ---------------------------- | --------------------------------------------------------------------------- |
| `GET /api/v1/capabilities`   | Discover contract and workflow capabilities.                                |
| `POST /api/v1/uploads`       | Start an upload workflow.                                                   |
| `/api/v1/uploads/{id}/...`   | Supply upload workflow feedback where defined.                              |
| `POST /api/v1/continuations` | Continue a retained workflow from required actions.                         |
| `/api/v1/workflows/{id}/...` | Read workflows and operations, issue commands, and retrieve media previews. |

Methods, request bodies, response schemas, status codes, idempotency rules, and nested workflow routes can change as the alpha API evolves. Generate clients from the OpenAPI document shipped with the binary you run.

### Tracker questionnaires

The `trackers_projected` workflow goal prepares selected tracker names and questionnaire requirements before duplicate assessment. Read each tracker's returned questionnaire schema and current answers rather than hard-coding field names or assuming all trackers ask the same questions. Requirements can depend on the prepared release, saved answers, or later tracker lookup results.

Submit questionnaire answers through the projection instructions defined by the running OpenAPI contract. Changing only those answers can retain compatible duplicate evidence; changes to the release, selection, or relevant tracker context can invalidate it. Use the resulting workflow state and required actions to decide the next step. Questionnaire projection is not upload approval.

See [Tracker questions](../workflow/index.md#tracker-questions) for the equivalent browser workflow.

### HDR-analysis routes

| Route                                                                                 | Scope            | Purpose                                                                  |
| ------------------------------------------------------------------------------------- | ---------------- | ------------------------------------------------------------------------ |
| `POST /api/v1/workflows/{workflowId}/hdr-analysis`                                    | `workflow:write` | Start native analysis for ordered targets in the exact prepared release. |
| `PUT /api/v1/workflows/{workflowId}/hdr-analysis/enabled`                             | `workflow:write` | Set description inclusion and current artifact selection.                |
| `POST /api/v1/workflows/{workflowId}/hdr-analysis/comparison`                         | `workflow:write` | Bind an explicit server-local reference file to the prepared release.    |
| `GET /api/v1/workflows/{workflowId}/hdr-analysis/{analysisId}/artifacts/{artifactId}` | `workflow:read`  | Stream a retained artifact from its exact analysis revision.             |

Writes use `If-Match` and `Idempotency-Key`. Downloads require a positive `revision` query parameter and the workflow owner's authorization. Artifact IDs and comparison handles are opaque and belong to the exact prepared generation.

The analysis request's `instructions` contains `release`, ordered `targetIds`, `peakSource`, and optional `formats`, `decoded`, `decodedRetry`, and `profileVersion`. Native formats are `auto`, `hdr10plus`, `dolby-vision`, or `both`. An omitted profile resolves to `hdr-analysis-v2`, or `hdr-analysis-v3` when bounded synchronization, all audio tracks, or a nonzero video-thread override requires it.

| `decoded` option                 | Meaning                                                                                                         |
| -------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `audio: true`                    | Complete primary-audio analysis alongside native plots without picture decoding.                                |
| `automatic: true, sampled: true` | Automatic sampled picture checks and audio. Omitting `sampled` with `automatic: true` requests full-frame work. |
| `boundarySync: true`             | Bounded scene checks; add `audio: true` and/or `activeArea: true` explicitly.                                   |
| `allAudioTracks: true`           | All prepared tracks, up to 64; requires audio or automatic mode.                                                |
| `videoDecoderThreads`            | `0` for defaults or `1`–`64` for a bounded maximum; requires picture work.                                      |
| `sceneSync`, `activeArea`        | Individual picture checks.                                                                                      |
| `comparison`                     | Reference `sourceId`, explicit `frameOffset`, common `crop`, and `grade` and/or `colour` selection.             |

Unlike the CLI and Web UI defaults, omitting `decoded` in the API requests native metadata only. Bounded mode cannot combine automatic, sampled, individual scene synchronization, or comparison checks. See the running OpenAPI schemas for the full validation contract.

Comparison selection accepts `release` and `sourcePath` and returns a generation-bound reference through the workflow. Use its handle as `comparison.sourceId`; it expires and does not replace the primary source. The frame mapping is `B = A + frameOffset`, with explicit zero for aligned sources. The common crop applies without resizing or automatic alignment.

`decodedRetry` identifies the current `analysisAttemptId` and explicit unsuccessful `modes`. It authorizes a new decoded pass, not native extraction. Read per-format and per-mode terminal outcomes and coverage; completed execution of bounded work does not mean whole-video picture coverage, and inconclusive evidence is not a media pass/fail verdict.

The inclusion request accepts `enabled` and optional `artifactIds`. Native plots and successful selected audio assets are defaults; picture-check images require explicit selection. Native-format-aware partial results can supply usable successful assets, while legacy v1 partial results cannot be enabled. Downloads remain available for successful partial artifacts.

Composite upload requests can include `hdrAnalysis` with `targetIds`, `peakSource`, `formats`, and `decoded`; all requested targets must complete. For initial comparison selection, supply `hdrComparisonSelection.path` with unresolved comparison options rather than inventing a source handle. See [HDR analysis](../workflow/hdr-analysis.md).

### Audio-analysis routes

Audio analysis is a workflow-bound optional operation:

| Route                                                                                   | Scope            | Purpose                                                                          |
| --------------------------------------------------------------------------------------- | ---------------- | -------------------------------------------------------------------------------- |
| `POST /api/v1/workflows/{workflowId}/audio-analysis`                                    | `workflow:write` | Start analysis for exact prepared release and track facts.                       |
| `PUT /api/v1/workflows/{workflowId}/audio-analysis/enabled`                             | `workflow:write` | Enable or disable the optional facet without starting analysis.                  |
| `GET /api/v1/workflows/{workflowId}/audio-analysis/{analysisId}/artifacts/{artifactId}` | `workflow:read`  | Stream one retained PNG or statistics text file from an exact analysis revision. |

Workflow writes use the general API concurrency and idempotency headers documented by the running OpenAPI contract, including `If-Match` and `Idempotency-Key`. The artifact request requires its positive `revision` query parameter. Treat artifact IDs and URLs as opaque, and authorize each request with the owner that created the workflow.

See [Audio analysis](../workflow/audio-analysis.md) for user-facing behavior. Use the embedded OpenAPI schemas for request and response fields.

One active input is shared by all processes using the same database. Reads do not acquire its lease or recover interrupted work. Startup waits for prior-process leases to expire, preserves completed work, and marks unfinished operations interrupted without replaying external work. It abandons unresolved prior-process effects without verifying their remote outcome; check the tracker or torrent client before explicitly retrying an interrupted stage. Unknown external outcomes during the running process still require reconciliation before another submission. If a continuation or workflow command returns a revision conflict, fetch the current workflow and review its required actions before sending a new command; do not replay a write with the old revision.

## Base-path example

```powershell
$headers = @{ Authorization = "Bearer $env:UPBRR_API_TOKEN" }
Invoke-RestMethod -Headers $headers -Uri "http://localhost:7480/api/v1/capabilities"
```

Keep the token in an environment variable or secret store. Do not paste it directly into scripts committed to source control.

## Upload log level

Set `execution.runLogLevel` on `POST /api/v1/uploads` to `error`, `warn`, `info`, `debug`, or `trace` to control that run's application logs. Omit it or send an empty value to inherit the application setting. Invalid levels are rejected before the workflow is accepted.

The level follows asynchronous work and feedback-driven continuation. Stage-based workflows use `descriptions.options.RunLogLevel` in their continuation intent; upload execution and retries inherit the retained description options when no new run override is supplied. Global settings and the server console threshold are unchanged. See [Logging](../web-ui/logging.md) for application outputs and viewer filters.

## Browser session API

Routes below `/api/auth`, `/api/app`, and `/api/events` serve the embedded Web UI and use browser-session authentication. They are not a substitute for the versioned bearer-token API. External integrations should use `/api/v1`.
