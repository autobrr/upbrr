---
title: API Tokens
description: Create least-privilege bearer tokens, preserve the one-time secret, and revoke access.
---

# API Tokens

Use **Settings → API Tokens** for external clients of the versioned `/api/v1` release-workflow API. Token actions persist immediately and do not use the page-level **Save** button.

## Generate a token

1. Enter a descriptive **Name**.
2. Enter a stable **Owner** for the automation identity.
3. Select the minimum required scopes.
4. Select **Generate token**.
5. Copy the plaintext token immediately into a secret manager.

The plaintext token is shown once. Only its hash persists in `web-auth.json`; it cannot be recovered later. Tokens with the same owner share the same owner-scoped durable workflows.

| Scope              | Grants                                                                |
| ------------------ | --------------------------------------------------------------------- |
| `workflow:read`    | Workflow, operation, capability, and result reads.                    |
| `workflow:write`   | Preparation and non-execution workflow commands.                      |
| `workflow:execute` | Commands that consume reviewed upload plans and can cause submission. |

Keep `workflow:execute` out of monitoring-only integrations. Send the token as:

```http
Authorization: Bearer <token>
```

See the [API reference](../../api/index.md) for routes and OpenAPI documentation.

## Revoke a token

Select **Revoke**, then confirm. Revocation rejects the next request and cannot be undone. Create a replacement token before revoking when an integration needs uninterrupted access.

API token metadata is not part of configuration export. Never place a plaintext token in source control, logs, screenshots, or support reports.
