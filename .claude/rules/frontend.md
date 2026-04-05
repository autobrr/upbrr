# Frontend Rules

- Working directory: `gui/frontend/`
- Package manager: pnpm 10 (`pnpm install --frozen-lockfile`)
- TypeScript strict, ESLint clean — no rule weakening or type error bypasses
- Validate: `pnpm run lint` and `pnpm run typecheck`
- For build/config changes: `pnpm run build`
- Vite dev server: `pnpm run dev` (port 5173)
- Stack: React 18 + Vite 5 + TypeScript 5 + Tailwind 4

## Architecture

- Dual runtime: `isWebUIRuntime()` in `src/utils/runtime.ts` detects Wails native vs. browser HTTP
- Wails bindings: auto-generated in `wailsjs/`, imported as `import { Method } from 'wailsjs/go/guiapp/App'`
- Browser mode: routes to `/api/app/MethodName` POST endpoints, uses EventSource for SSE
- State management: React hooks (useState, useCallback, useMemo)
- Custom hooks: `src/hooks/` (useSettingsState, useScreenshots, useUploadImages)
- Page-based layout: `src/` organized by workflow step (input, dupe_check, preparation, tracker_upload, etc.)
