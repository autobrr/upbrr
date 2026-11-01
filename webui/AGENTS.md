# Frontend Guidelines

`webui`-scoped; root repo rules apply.

Provider-ID edits follow the root manual-provider authority rule through the shared backend: multiple manual selections remain independent despite conflicting provider links. Preserve explicit clear and Auto/reset; do not add a browser agreement gate or display-only workaround.

## Source Of Truth

- Scripts/dependencies: `package.json`, `pnpm-lock.yaml`.
- TypeScript config: `tsconfig*.json`, `vite.config.ts`, `vitest.config.ts`.
- Lint/format: ESLint, Prettier, Stylelint configs, Lefthook.
- API/runtime contracts: `pkg/api`, `internal/webserver`, generated workflow transport types/clients under `src/api`, release ownership under `src/releaseSession`.

## Check Selection

- TS/TSX: `pnpm --dir webui run lint`, `lint:dead`, `typecheck`, `test:unit`, `format:check`.
- CSS: `pnpm --dir webui run lint:style`; also `format:check`.
- Browser client/API: frontend `test:unit` + `typecheck`; backend/API tests from `internal/AGENTS.md` and `pkg/api/AGENTS.md`.
- Bundle/import/env: `pnpm --dir webui run build`.
- Visual/embedded: rebuild/sync embedded assets; inspect `http://localhost:7480`; avoid Vite `5173` for parity.
- E2E selection: inspect the PR-target merge-base diff and staged/unstaged/untracked work first (`CONTRIBUTING.md`). Docs-only changes need no browser run. Use the affected project or `--grep` for narrow browser changes; use all five projects for shared workflow/API/build changes. Explicit full-suite requests still run in full.

`make test-frontend` runs lint, dead-code, typecheck, unit, format; not Stylelint. Run Stylelint explicitly for CSS.

## Source and embedded runtime

React, styling, browser diagnostics and release-workflow ownership: [src/AGENTS.md](src/AGENTS.md). Read it when changing those contracts outside `src/` too.

Use `make build` to build the frontend, sync embedded assets and build the native CLI. Serve the resulting `dist/upbrr` (`dist/upbrr.exe` on Windows) with `serve --dev-no-auth`, inspect `http://localhost:7480`, and stop the owned server afterward. See [CONTRIBUTING.md](../CONTRIBUTING.md#frontend) for setup; Vite `5173` does not establish embedded parity.

Playwright tests require embedded UI, local fake services and isolated config/DB without real credentials: read [e2e/AGENTS.md](e2e/AGENTS.md).
