# Public Documentation

Applies to the Docusaurus site published at `upbrr.com`.

## Source And Scope

- Verify claims against current code, tests, CLI help, API contracts, configuration loaders, and active workflows.
- Update public docs in the same PR as user-visible CLI, configuration, Web UI, workflow, tracker, installation, upgrade, or API changes.
- Keep internal planning material under `docs/` out of the public site.
- Use synthetic examples required by the root `AGENTS.md`; never publish credentials, private URLs, tracker secrets, or unannounced plans.
- Keep one reader task per page. Do not add unsupported placeholders.

## Checks

```powershell
pnpm --dir documentation install --frozen-lockfile
pnpm --dir documentation run check
```

`check` runs formatting, TypeScript, strict link validation, and the production build. Never commit `build/` or `.docusaurus/`.

## Pull Requests And Publishing

- Documentation changes must keep navigation, cross-links, generated indexes, and adjacent contract pages synchronized.
- When no public documentation change is needed, add `Documentation: not-required: <reason>` to the PR body.
- Netlify Deploy Previews are review surfaces. Production publishes only from the exact release tag through `.github/workflows/release.yml`.
- Never publish manually, expose Netlify credentials, change the production branch, or change DNS without direct user authorization.
