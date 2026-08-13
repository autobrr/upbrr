---
title: Tracker Auth
description: Manage encrypted tracker cookies, remote validation, automatic relogin, and 2FA state.
---

# Tracker Auth

Use **Settings → Tracker Auth** after saving a tracker entry. The page shows only configured trackers whose backend capability requires managed cookies, login, refresh, or 2FA handling.

Tracker Auth actions persist immediately and do not use the page-level **Save** button. Static API keys, passkeys, usernames, passwords, and OTP URIs remain under [Trackers](./trackers.md).

## Status

| Status                  | Meaning                                                                   |
| ----------------------- | ------------------------------------------------------------------------- |
| **Configured**          | Required managed auth is ready.                                           |
| **Has cookies**         | Encrypted stored cookies are available.                                   |
| **Login required**      | Current state needs login or renewed cookies.                             |
| **Storage unavailable** | Encrypted cookie storage cannot be used.                                  |
| **Error**               | Status or remote validation failed; read the displayed sanitized message. |
| **Not configured**      | No usable managed auth state is stored.                                   |

Capability chips show whether the tracker supports cookie import, login, automatic relogin, TOTP, manual 2FA, API keys, or passkeys. They describe backend support; the page renders only actions valid for that tracker.

## Actions

| Action             | Result                                                                                                           |
| ------------------ | ---------------------------------------------------------------------------------------------------------------- |
| **Import Cookies** | Selects a Netscape `.txt` or JSON cookie file and stores accepted cookies encrypted. Files are limited to 1 MiB. |
| **Check Auth**     | Performs remote validation when the tracker supports it.                                                         |
| **Submit 2FA**     | Completes an active manual 2FA challenge when the code field appears.                                            |
| **Delete Auth**    | Deletes stored cookies and tracker-managed auth state; tracker configuration remains.                            |

Automatic relogin uses saved tracker credentials only when the backend capability supports it. Never share cookie files, 2FA codes, challenge details, or auth errors that may contain private tracker information.

## Storage recovery

Cookie storage depends on key material beside the active database. Treat `web-auth.json` and `db.sqlite` as sensitive credentials: give both files restrictive permissions, keep them out of shared storage, and never attach them to support requests. Together they can expose encrypted cookies and other stored secrets. Back up and move them together; replacing or losing `web-auth.json` can make encrypted secrets and cookies unusable.

After importing or changing credentials, select **Check Auth** where available. To verify tracker preparation without submission or client injection, select **Skip client injection** before running **Dry Run**.
