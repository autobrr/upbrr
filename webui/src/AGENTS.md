# Frontend Source Guidelines

Scope: `webui/src`. Root and frontend guidance apply.

## React / TypeScript

- Keep TypeScript, ESLint, Stylelint, dead-code clean; never weaken rules.
- `useEffect` only for external sync. Derive during render or `useMemo`, not effects.
- User-driven logic: handlers. Fetch effects: cleanup/abort guards.
- Match existing component state patterns before new abstractions.
- Apply the root test-value rules: assert visible behavior, authority, error recovery, and accessibility; remove CSS-class, static-markup, and direct-delegation boilerplate. A mock call matters when its arguments or ordering establish a real boundary, not merely that delegation occurred.

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

- Use Tailwind utilities and shared UI components for local layout, spacing, and controls; avoid new page-specific stylesheets.
- `src/styles.css` owns theme token mapping and shared shell rules; `src/themes/palettes.css` owns bundled palette values. Keep tracker-generated markup rules scoped to `src/components/rendered-description.css`.
- `internal/description` converts and sanitizes tracker BBCode, mixed HTML, and MediaInfo before `RenderedDescription` inserts the HTML. Do not parse tracker markup or add tracker-specific presentation logic in React.
- Appearance changes need the catalog, palette, bootstrap, and theme tests kept in sync. Check applicable light/dark modes and supported accents at desktop and mobile widths.
- No repo-wide format/style sweeps unless explicitly requested.
- Text must fit desktop/mobile containers; no viewport-width font scaling dependence.
