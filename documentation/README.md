# upbrr documentation

Public documentation for [upbrr.com](https://upbrr.com).

```powershell
pnpm install --frozen-lockfile
pnpm run start
```

Before submitting documentation changes:

```powershell
pnpm run format:check
pnpm run typecheck
pnpm run build
```

The production output is written to `build/` and must not be committed.
