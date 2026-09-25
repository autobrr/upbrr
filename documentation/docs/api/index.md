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

### Audio-analysis routes

Audio analysis is a workflow-bound optional operation:

| Route                                                                                   | Scope            | Purpose                                                                          |
| --------------------------------------------------------------------------------------- | ---------------- | -------------------------------------------------------------------------------- |
| `POST /api/v1/workflows/{workflowId}/audio-analysis`                                    | `workflow:write` | Start analysis for exact prepared release and track facts.                       |
| `PUT /api/v1/workflows/{workflowId}/audio-analysis/enabled`                             | `workflow:write` | Enable or disable the optional facet without starting analysis.                  |
| `GET /api/v1/workflows/{workflowId}/audio-analysis/{analysisId}/artifacts/{artifactId}` | `workflow:read`  | Stream one retained PNG or statistics text file from an exact analysis revision. |

Workflow writes use the general API concurrency and idempotency headers documented by the running OpenAPI contract, including `If-Match` and `Idempotency-Key`. The artifact request requires its positive `revision` query parameter. Treat artifact IDs and URLs as opaque, and authorize each request with the owner that created the workflow.

See [Audio analysis](../workflow/audio-analysis.md) for user-facing behavior. Use the embedded OpenAPI schemas for request and response fields.

One active input is shared by all processes using the same database. Reads do not acquire its lease or recover interrupted work. After a restart, an explicit continuation or workflow command performs authorized recovery. If it returns a revision conflict, fetch the current workflow and review its required actions before sending a new command; do not replay a write with the old revision. Unknown external outcomes require reconciliation before another submission.

## Base-path example

```powershell
$headers = @{ Authorization = "Bearer $env:UPBRR_API_TOKEN" }
Invoke-RestMethod -Headers $headers -Uri "http://localhost:7480/api/v1/capabilities"
```

Keep the token in an environment variable or secret store. Do not paste it directly into scripts committed to source control.

## Browser session API

Routes below `/api/auth`, `/api/app`, and `/api/events` serve the embedded Web UI and use browser-session authentication. They are not a substitute for the versioned bearer-token API. External integrations should use `/api/v1`.
