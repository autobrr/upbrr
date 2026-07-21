# Frontend Guidelines

Scoped rules for `webui`. Root repo rules still apply.

## Source Of Truth

- Scripts and dependencies: `package.json`, `pnpm-lock.yaml`.
- TypeScript config: `tsconfig*.json`, `vite.config.ts`, `vitest.config.ts`.
- Lint/format behavior: ESLint, Prettier, Stylelint config files and Lefthook.
- API/runtime contracts: `pkg/api`, `internal/webserver`, typed clients under `src/api`, release ownership under `src/releaseSession`, and shared Job coordination under `src/jobRegistry`.

## Commands

```bash
pnpm --dir webui run typecheck
pnpm --dir webui run test:unit
pnpm --dir webui run lint
pnpm --dir webui run lint:style
pnpm --dir webui run format:check
pnpm --dir webui run build
```

## Check Selection

- TS/TSX changes: `pnpm --dir webui run lint`, `lint:dead`, `typecheck`, `test:unit`, and `format:check`.
- CSS changes: `pnpm --dir webui run lint:style`; also run `format:check`.
- Browser client/API changes: frontend `test:unit` + `typecheck`, plus backend/API tests from `internal/AGENTS.md` and `pkg/api/AGENTS.md`.
- Bundle/import/env changes: `pnpm --dir webui run build`.
- Visual/embedded behavior changes: rebuild/sync embedded assets and inspect `http://localhost:7480`; avoid Vite `5173` for parity.

`make test-frontend` runs lint, dead-code, typecheck, unit, and format checks, but not Stylelint. Run Stylelint explicitly for CSS.

## React / TypeScript

- Keep TypeScript, ESLint, Stylelint, dead-code clean. Do not weaken rules.
- `useEffect` only for external sync. Avoid derived state in effects; render or `useMemo` instead.
- User-driven logic belongs in handlers. Fetch effects need cleanup/abort guards.
- Preserve CLI and WebUI behavior when changing shared request shapes, upload options, or prepared metadata.
- Match existing component state patterns before adding new abstraction.

## Release Session / Job Ownership

- Release workflow pages consume `useReleaseSession` facets; they do not import production API clients to coordinate release operations directly.
- `src/releaseSession` owns active release state, operation intents, and preparation/image-upload progress subscriptions. Views render facet state instead of subscribing independently.
- `src/jobRegistry` owns `jobs:update` transport and duplicate-check/tracker-upload Job coordination. Active release Job access remains behind `useReleaseSession`.
- Facets expose declarative state and intent methods, not React setters, dispatch functions, or refs.
- Use structured failure codes/metadata for recovery. Do not infer recovery from error-message text.
- Consume backend-provided disc resource paths; do not derive BDMV paths from preparation source strings in the frontend.

## Frontend Output / Logging

- Follow root log-level guidance for browser-visible diagnostics and WebUI event logging.
- Do not expose credentials, tokens, API keys, passkeys, cookies, 2FA codes, challenge IDs, or secret payloads in console output, UI errors, toasts, test failure text, or debug panels.
- Avoid permanent `console.*` diagnostics. If a diagnostic is intentionally kept, make it dev-scoped, concise, and redacted.
- User-facing errors should be stable outcomes or next steps; detailed troubleshooting context belongs in developer diagnostics.

## Styling

- Prefer Tailwind utilities for touched local layout/spacing.
- Keep CSS for shared/theme/cross-cutting selectors or JSX readability.
- Do not make repo-wide format/style sweeps unless explicitly requested.
- Text must fit containers across desktop/mobile; do not rely on viewport-width font scaling.

## Embedded Web Checks

- For embedded visual/runtime checks, rebuild frontend, sync embedded assets, rebuild CLI, then serve the embedded app:

```bash
pnpm --dir webui run build
pwsh -NoProfile -File .\scripts\sync-webui-assets.ps1
go build -o .\dist\upbrr.exe .\cmd\upbrr
.\dist\upbrr.exe serve --dev-no-auth
```

- Use `http://localhost:7480`.
- Avoid Vite `5173` for embedded parity checks.
- Stop local servers after inspection.

## E2E

For Playwright E2E work, read `e2e/AGENTS.md` first. E2E tests must use the embedded web UI, local fake services, isolated temp config/DB, and no real credentials.
