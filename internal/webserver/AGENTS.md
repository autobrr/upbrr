# Webserver Guidelines

Scope: `internal/webserver` and callers changing runtime activation or browser transport. Root and backend guidance apply; shared payload changes also require `pkg/api/AGENTS.md`.

## Runtime and transport

- `SaveConfig` delegates runtime activation to `RuntimeActivator`. Read WebUI config/core/logger through `currentConfig()`, `requireRuntime()` or snapshots; never read `Backend.cfg` directly outside helpers. `Server.cfg` is startup-only.
- Keep `/api/app/*` routes, typed requests and browser clients synchronized. Preserve CLI/WebUI parity and backend-owned workflow authority; do not derive tracker semantics in transport DTOs.

## Checks

- Run `go test -race -v -timeout 20m ./internal/webserver/... ./pkg/api` plus the backend checks.
- Request/response or browser-client changes also need frontend `typecheck` and unit tests. Runtime-sensitive UI changes require embedded-web verification under `webui/e2e/AGENTS.md`, not Vite-only validation.
