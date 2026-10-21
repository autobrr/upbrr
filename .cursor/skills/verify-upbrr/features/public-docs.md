# Public documentation

The Docusaurus site under `documentation/` is published at upbrr.com from release tags. User-visible
CLI, config, Web UI, workflow, tracker, installation, upgrade, and API changes need matching page,
navigation, and cross-link updates in the same PR.

## Sub-features

- Guide pages under `documentation/docs/`.
- Sidebar and navigation configuration.
- Generated API reference and screenshot assets.

## How to get to it (user POV)

Read the built site locally after `pnpm --dir documentation run check`, or open the matching Markdown
page directly.

## Driving it with the harness

```bash
pnpm --dir documentation install --frozen-lockfile
pnpm --dir documentation run check   # Prettier, TypeScript, production build
```

## Gotchas

- `documentation/build/` is generated; never commit it.
- Public tracker documentation stays generic. Do not publish site-specific upload rules that belong to
  a tracker implementation.
