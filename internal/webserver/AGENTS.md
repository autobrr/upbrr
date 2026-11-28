# Webserver Guidelines

Scope: `internal/webserver` and callers changing runtime activation, browser transport, or public API transport. Root and backend guidance apply. Follow [shared API guidance](../../pkg/api/AGENTS.md) when changing shared payloads.

## Runtime and transport

- `SaveConfig` delegates runtime activation to `RuntimeActivator`. Read WebUI config/core/logger through `currentConfig()`, `requireRuntime()` or snapshots; never read `Backend.cfg` directly outside helpers. `Server.cfg` is startup-only.
- Keep `/api/app/*` routes, typed requests and browser clients synchronized. Preserve CLI/WebUI parity and backend-owned workflow authority; do not derive tracker semantics in transport DTOs.
- Keep public `/api/v1` request mappings and responses in sync with the generated OpenAPI contract. Preserve API token scopes and workflow-owner authorization for reads, commands, and continuations.

## Checks

- Run `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api` plus the backend checks.
- Browser request/response or client changes also need frontend `typecheck` and unit tests. Verify runtime-sensitive UI changes against the embedded build using [E2E guidance](../../webui/e2e/AGENTS.md).
