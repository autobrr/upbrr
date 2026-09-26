# Web UI development

The Vite application is embedded into the Go binary for production. Use Node.js 24 or newer and the pnpm version in `package.json`.

```powershell
pnpm --dir webui install --frozen-lockfile
pnpm --dir webui run dev
pnpm --dir webui run build
make test-frontend
pnpm --dir webui run lint:style
make e2e-web
```

`dev` and `build` generate the small appearance bootstrap script from the shared TypeScript theme rules. The generated `webui/public/appearance-bootstrap.js` is ignored. `build` writes `webui/dist/`, which is also ignored. The embedded browser tests build and sync those assets into the Go host; use them for route, auth, and base path changes.

The Playwright harness starts embedded upbrr with `UPBRR_WEB_OPEN_BROWSER=false`. This keeps test server launches from opening a separate desktop browser window; Playwright controls its own browser.

## Ownership

| Area                                                   | Owner                                                                                                                                                                             |
| ------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Sign-in, session expiry, dirty settings logout warning | `src/webRoot.tsx`                                                                                                                                                                 |
| Browser routes and base path                           | `src/router.tsx`, `src/routes/RouteViews.tsx`; the Go host mirrors paths in `internal/webserver/routes.go`                                                                        |
| Page frame and navigation                              | `src/layouts/AppLayout.tsx`, composed in `src/app.tsx`                                                                                                                            |
| Appearance and persistence                             | `src/themes/` with one pre-paint bootstrap                                                                                                                                        |
| Ordinary resource reads                                | Session-scoped TanStack Query client in `src/app.tsx`                                                                                                                             |
| Settings configuration and drafts                      | `src/hooks/useSettingsState.tsx`; `src/settings/` owns masking, save transforms, and field rendering; `src/routes/SettingsRoutes.tsx` owns the settings and logging route actions |
| Release workflow, polling, and recovery                | `src/releaseSession/`; pure projections in `projections.ts`; route components consume its facets                                                                                  |

The router is mounted inside the authenticated release provider so changing pages preserves release state. Settings drafts also live above the route outlet. Query holds ordinary reads; workflow snapshots and commands stay in `releaseSession`. Logging's live stream keeps its own bounded subscription.

Each browser route must also be listed in the Go host's UI fallback allowlist. Test direct links and reloads at `/` and a configured prefix such as `/upbrr/`; the host rewrites Vite's relative entry assets for nested routes. Never treat missing assets as UI routes.

Theme palettes are static free assets. Appearance is browser local, applies before React mounts, and does not add a server configuration field. Add a theme through the catalog, palette, bootstrap, and theme tests together.
