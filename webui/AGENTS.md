# Frontend Guidelines

`webui`-scoped; root repo rules apply.

## Source Of Truth

- Scripts/dependencies: `package.json`, `pnpm-lock.yaml`.
- TypeScript config: `tsconfig*.json`, `vite.config.ts`, `vitest.config.ts`.
- Lint/format: ESLint, Prettier, Stylelint configs, Lefthook.
- API/runtime contracts: `pkg/api`, `internal/webserver`, generated workflow transport types/clients under `src/api`, release ownership under `src/releaseSession`.

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

- TS/TSX: `pnpm --dir webui run lint`, `lint:dead`, `typecheck`, `test:unit`, `format:check`.
- CSS: `pnpm --dir webui run lint:style`; also `format:check`.
- Browser client/API: frontend `test:unit` + `typecheck`; backend/API tests from `internal/AGENTS.md` and `pkg/api/AGENTS.md`.
- Bundle/import/env: `pnpm --dir webui run build`.
- Visual/embedded: rebuild/sync embedded assets; inspect `http://localhost:7480`; avoid Vite `5173` for parity.

`make test-frontend` runs lint, dead-code, typecheck, unit, format; not Stylelint. Run Stylelint explicitly for CSS.

## React / TypeScript

- Keep TypeScript, ESLint, Stylelint, dead-code clean; never weaken rules.
- `useEffect` only for external sync. Derive during render or `useMemo`, not effects.
- User-driven logic: handlers. Fetch effects: cleanup/abort guards.
- Shared request shapes, upload options, prepared metadata changes: preserve CLI/WebUI behavior.
- Match existing component state patterns before new abstractions.

## Release Session / Workflow Ownership

- Release workflow pages consume `useReleaseSession` facets; no production API client imports for direct release coordination.
- `src/releaseSession` owns active release state, workflow operation intents, durable polling, progress projection. Views render facet state; no independent subscriptions.
- Render backend-provided tracker auth capabilities/status/actions, questionnaire schemas/defaults, reviewed upload/search names. Submit answers/actions through shared contracts.
- Backend stage controls authoritatively select WebUI trackers. Preserve exact subset through preparation/upload; no CLI/composite post-dupe approval gate or frontend widening.
- Frontend must not derive tracker-specific auth readiness, taxonomy, descriptions, media facts, questionnaire fields, or release-name transformations.
- Production workflow transport mandatory. No release Job clients, optional workflow ports, or fallback orchestration.
- Facets expose declarative state/intent methods; no React setters, dispatch functions, or refs.
- Recovery: structured failure codes/metadata; never infer from error-message text.
- Consume backend-provided disc resource paths; never derive BDMV paths from preparation source strings.

## Frontend Output / Logging

- Follow root log-level guidance for browser-visible diagnostics/WebUI event logging.
- Never expose credentials, tokens, API keys, passkeys, cookies, 2FA codes, challenge IDs, or secret payloads in console output, UI errors, toasts, test failure text, or debug panels.
- Avoid permanent `console.*` diagnostics. Retained diagnostics: dev-scoped, concise, redacted.
- User errors: stable outcomes/next steps; troubleshooting detail: developer diagnostics.

## Styling

- Touched local layout/spacing: prefer Tailwind utilities.
- Keep CSS for shared/theme/cross-cutting selectors or JSX readability.
- No repo-wide format/style sweeps unless explicitly requested.
- Text must fit desktop/mobile containers; no viewport-width font scaling dependence.

## Embedded Web Checks

- Embedded visual/runtime checks: rebuild frontend, sync embedded assets, rebuild CLI, serve embedded app:

```bash
pnpm --dir webui run build
pwsh -NoProfile -File .\scripts\sync-webui-assets.ps1
go build -o .\dist\upbrr.exe .\cmd\upbrr
.\dist\upbrr.exe serve --dev-no-auth
```

- Use `http://localhost:7480`.
- Avoid Vite `5173` for embedded parity.
- Stop local servers after inspection.

## E2E

Playwright E2E: read `e2e/AGENTS.md` first. Tests require embedded web UI, local fake services, isolated temp config/DB, no real credentials.
