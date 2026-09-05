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
