# Public Documentation

Applies to the Docusaurus site published at `upbrr.com`.

## Source And Scope

- Verify claims against current code, tests, CLI help, API contracts, configuration loaders, and active workflows.
- Update public docs in the same PR as user-visible CLI, configuration, Web UI, workflow, tracker, installation, upgrade, or API changes.
- Keep user-facing documentation tracker-neutral: do not add tracker names, tracker-specific rules, mappings, requirements, examples, or operational details. Explain blocking reasons, warnings and waivers only in general terms, and direct readers to the tracker’s current rules for their meaning. This applies to existing guides and changelogs as well as new documentation.
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
- Production publishes only from the exact release tag through `.github/workflows/release.yml`.
- Never publish manually, expose Netlify credentials, change the production branch, or change DNS without direct user authorization.
