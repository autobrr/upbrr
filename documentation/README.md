# upbrr documentation

Public documentation for [upbrr.com](https://upbrr.com).

```powershell
pnpm install --frozen-lockfile
pnpm run start
```

Before submitting documentation changes:

```powershell
pnpm run check
```

The production output is written to `build/` and must not be committed.

Pull requests use Netlify Deploy Previews. The release workflow builds the exact release tag and publishes it to production using the `documentation-production` GitHub environment. That environment requires `NETLIFY_AUTH_TOKEN` and `NETLIFY_SITE_ID` secrets.
