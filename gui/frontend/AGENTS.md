# Frontend Guidelines

Scoped rules for `gui/frontend`. Root repo rules still apply.

## Source Of Truth

- Scripts and dependencies: `package.json`, `pnpm-lock.yaml`.
- TypeScript config: `tsconfig*.json`, `vite.config.ts`, `vitest.config.ts`.
- Lint/format behavior: ESLint, Prettier, Stylelint config files and Lefthook.
- API/runtime contracts: `pkg/api`, `internal/guiapp`, `internal/webserver`, generated Wails bindings when present.

## Commands

```bash
pnpm --dir gui/frontend run typecheck
pnpm --dir gui/frontend run test:unit
pnpm --dir gui/frontend run lint
pnpm --dir gui/frontend run lint:style
pnpm --dir gui/frontend run format:check
pnpm --dir gui/frontend run build
```

## Check Selection

- TS/TSX changes: `pnpm --dir gui/frontend run lint`, `lint:dead`, `typecheck`, `test:unit`, and `format:check`.
- CSS changes: `pnpm --dir gui/frontend run lint:style`; also run `format:check`.
- Runtime/API bridge or Wails binding changes: frontend `test:unit` + `typecheck`, plus backend/API parity tests from `internal/AGENTS.md` and `pkg/api/AGENTS.md`.
- Bundle/import/env changes: `pnpm --dir gui/frontend run build`.
- Visual/embedded behavior changes: rebuild/sync embedded assets and inspect `http://localhost:7480`; avoid Vite `5173` for parity.

`make test-frontend` runs lint, dead-code, typecheck, unit, and format checks, but not Stylelint. Run Stylelint explicitly for CSS.

## React / TypeScript

- Keep TypeScript, ESLint, Stylelint, dead-code clean. Do not weaken rules.
- `useEffect` only for external sync. Avoid derived state in effects; render or `useMemo` instead.
- User-driven logic belongs in handlers. Fetch effects need cleanup/abort guards.
- Preserve CLI, Wails, and embedded web parity when changing request shapes, upload options, prepared metadata, or runtime bridge behavior.
- Match existing component state patterns before adding new abstraction.

## Styling

- Prefer Tailwind utilities for touched local layout/spacing.
- Keep CSS for shared/theme/cross-cutting selectors or JSX readability.
- Do not make repo-wide format/style sweeps unless explicitly requested.
- Text must fit containers across desktop/mobile; do not rely on viewport-width font scaling.

## Embedded Web Checks

- For embedded visual/runtime checks, rebuild frontend, sync embedded assets, rebuild CLI, then serve the embedded app:

```bash
pnpm --dir gui/frontend run build
pwsh -NoProfile -File .\scripts\sync-frontend-assets.ps1
go build -o .\dist\upbrr.exe .\cmd\upbrr
.\dist\upbrr.exe serve --dev-no-auth
```

- Use `http://localhost:7480`.
- Avoid Vite `5173` for embedded parity checks.
- Stop local servers after inspection.

## E2E

For Playwright E2E work, read `e2e/AGENTS.md` first. E2E tests must use the embedded web UI, local fake services, isolated temp config/DB, and no real credentials.
