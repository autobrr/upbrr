# upbrr documentation

Public documentation for [upbrr.com](https://upbrr.com). Use Node.js 24 or newer and the pnpm version pinned in `documentation/package.json`. Run these commands from the repository root:

```powershell
pnpm --dir documentation install --frozen-lockfile
pnpm --dir documentation run start
```

Before submitting documentation changes:

```powershell
pnpm --dir documentation run check
```

`check` runs formatting, TypeScript, and a production build with strict internal-link checks. Generated `documentation/build/` and `documentation/.docusaurus/` output must not be committed.

Pull requests use Netlify Deploy Previews. The release workflow builds the exact release tag and publishes it to production using the `documentation-production` GitHub environment. That environment requires `NETLIFY_AUTH_TOKEN` and `NETLIFY_SITE_ID` secrets.
