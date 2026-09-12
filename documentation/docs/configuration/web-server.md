---
title: Web server and reverse proxy
description: Configure the embedded Web UI listener, environment variables, browse policy, and reverse proxy base path.
---

# Web server and reverse proxy

Start the embedded Web UI with:

```powershell
.\upbrr.exe serve
```

Defaults:

| Setting            | Default       |
| ------------------ | ------------- |
| Host               | `localhost`   |
| Port               | `7480`        |
| Open browser       | enabled       |
| External base path | `/`           |
| Session lifetime   | 1,440 minutes |

## Precedence

Serve settings resolve in this order:

1. CLI flags;
2. `UPBRR_WEB_*` environment variables;
3. `web-config.json` beside the active database;
4. defaults.

Supported environment variables:

| Variable                    | Purpose                                             |
| --------------------------- | --------------------------------------------------- |
| `UPBRR_WEB_HOST`            | Listen host                                         |
| `UPBRR_WEB_PORT`            | Listen port                                         |
| `UPBRR_WEB_BASE_URL`        | External URL or path prefix                         |
| `UPBRR_WEB_OPEN_BROWSER`    | Whether to open a browser at startup                |
| `UPBRR_WEB_TRUSTED_PROXIES` | Comma-separated trusted proxy addresses or networks |

Use `--persist-web-config` to save supplied `serve` settings. Add `--persist-listen` when the listen host or port should also be saved.

```powershell
.\upbrr.exe serve --host 127.0.0.1 --port 7480 --persist-listen --persist-web-config
```

## First-run authentication and browse policy

The first browser connection creates an administrator account and establishes the initial browse policy. After that setup, browse-policy and password changes are intentionally unavailable through the Web UI.

Browse roots restrict browser-driven folder selection and file import. They do not restrict a direct CLI input path. Store them in `web-auth.json`, separate from imported application configuration.

To replace the initial policy later, stop the server and set one or more existing directories with the local binary:

```powershell
.\upbrr.exe auth browse-roots "D:\Media" "E:\Downloads"
```

Restart `upbrr serve` after the command completes. Use `auth password` with the server stopped when the administrator password must change. See the [`auth` CLI reference](../cli/index.md#auth) for unrestricted browsing, custom config paths, and session behavior.

`--dev-no-auth` disables browser authentication only on loopback hosts and is intended for local development. Do not use it for a network-accessible deployment.

## Single sign-on (OIDC)

The Web UI can authenticate against an OpenID Connect provider (Authentik, Keycloak, Authelia, Pocket ID, Zitadel, and similar) instead of, or alongside, the built-in username and password.

Register upbrr as a confidential OAuth2/OIDC client with your provider, set the redirect URI to `https://<your-upbrr-host>/api/auth/oidc/callback`, then:

```yaml
environment:
  - UPBRR_WEB_OIDC_ENABLED=true
  - UPBRR_WEB_OIDC_ISSUER=https://auth.example.test/application/o/upbrr/
  - UPBRR_WEB_OIDC_CLIENT_ID=<client id>
  - UPBRR_WEB_OIDC_CLIENT_SECRET=<client secret>
  - UPBRR_WEB_OIDC_REDIRECT_URL=https://upbrr.example.test/api/auth/oidc/callback
  # Optional. Defaults to "openid profile email".
  - UPBRR_WEB_OIDC_SCOPES=openid profile email
  # Optional. Removes the password form and rejects password logins.
  - UPBRR_WEB_OIDC_DISABLE_BUILT_IN_LOGIN=true
```

The same settings can live under an `oidc` object in `web-config.json`. Note that `--persist-web-config` writes the client secret to that file; supply the secret by environment on every start to keep it out of files.

`UPBRR_WEB_OIDC_REDIRECT_URL` must be the callback URL as the *browser* reaches it, so a deployment served under a path prefix has to include that prefix, and the same URL must be registered with the provider. Serving upbrr under `/upbrr/` (see [Path-prefix proxy](#path-prefix-proxy)) means:

```yaml
  - UPBRR_WEB_OIDC_REDIRECT_URL=https://example.test/upbrr/api/auth/oidc/callback
```

Notes:

- OIDC is **additive by default**. With `UPBRR_WEB_OIDC_DISABLE_BUILT_IN_LOGIN` unset, the login page shows a "Sign in with SSO" button next to the usual password form, so you can verify SSO works before you rely on it.
- upbrr is a single-user application. A successful OIDC login signs in to the existing local account rather than creating a second identity: that account's username and stored key seed derive the key protecting your tracker credentials. **Any identity that completes the flow against the configured client signs in to that one account, whether or not its username matches** - upbrr authenticates, and the provider authorizes. Scope the application to the users you trust, because upbrr holds your tracker credentials and announce URLs; it has no second permission tier to fall back on.
- On a **fresh** install with the built-in login disabled, the first successful OIDC login provisions the local account. The first-run setup form is closed in that mode, so it cannot be claimed by an unauthenticated caller.
- PKCE (S256) is used automatically when the provider advertises support for it.
- Provider discovery is lazy: if the identity provider is unreachable at start, upbrr still starts and retries discovery on the next sign-in attempt.

## Subdomain proxy

A dedicated subdomain keeps upbrr at the web root, so no base-path setting is needed:

```nginx
server {
  server_name upbrr.example.test;

  location / {
    proxy_pass http://localhost:7480/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
  }
}
```

Start normally:

```powershell
.\upbrr.exe serve
```

## Path-prefix proxy

When the browser sees `https://example.test/upbrr/`, tell upbrr the same external base path:

```powershell
.\upbrr.exe serve --base-url https://example.test/upbrr/
```

For containers:

```yaml
environment:
  - UPBRR_WEB_BASE_URL=/upbrr/
  - UPBRR_HEALTHCHECK_URL=http://127.0.0.1:7480/upbrr/api/auth/status
```

Retain the prefix when proxying:

```nginx
server {
  server_name example.test;

  location = /upbrr {
    return 301 /upbrr/;
  }

  location /upbrr/ {
    proxy_pass http://localhost:7480/upbrr/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
  }
}
```

The supported layout keeps `/upbrr/` on both sides. Rewriting only some HTML, API, event-stream, cookie, or asset paths causes partial failures.

## HTTPS termination

When a trusted reverse proxy terminates HTTPS and forwards HTTP to upbrr, add its address or network to `trusted_proxies` in `web-config.json` or `UPBRR_WEB_TRUSTED_PROXIES`. upbrr trusts forwarded scheme information only from configured proxies when deciding whether cookies require the secure flag.

Do not trust broad networks unless every host in them is under your control.
