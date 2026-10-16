---
title: Tracker Auth
description: Manage encrypted tracker cookies, remote validation, automatic relogin, and 2FA state.
---

# Tracker Auth

Use **Settings → Tracker Auth** after saving a tracker entry. The page shows only configured trackers whose backend capability requires managed cookies, login, refresh, or 2FA handling.

Tracker Auth actions persist immediately and do not use the page-level **Save** button. Static API keys, passkeys, usernames, passwords, and OTP URIs remain under [Trackers](./trackers.md).

For browser cookies, follow [Export and import cookies](#export-and-import-cookies), then [verify authentication](#verify-authentication). If a step fails, use [Cookie troubleshooting](#cookie-troubleshooting).

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

## Export and import cookies

Use this walkthrough for a tracker that supports **cookie import**. Save its required fields under **Settings → Trackers** first. API-key/passkey-only trackers use their advertised credentials instead.

:::danger Cookie exports grant account access

Keep exported files private and restrict filesystem access. Never attach cookie files or values to issues, logs, screenshots, or chat. Remove unneeded plaintext exports after verifying the import.

:::

### Export from Firefox

1. Install the [cookies.txt extension by Lennon Hill](https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/) from Firefox Add-ons. Review its permissions: it can access website cookies.
2. In the Firefox **profile and container where you use the tracker**, open the tracker and sign in. Complete any required 2FA or browser challenge and confirm that a logged-in tracker page loads.
3. Keep that tracker tab active. Open the extension's toolbar popup and choose **Download** beside **Current Container and Site**.
4. Save the exported Netscape HTTP Cookie File with its `.txt` extension. The default filename is `cookies.txt`, or `cookies.<container-name>.txt` for a named container.
5. Import that saved file directly using either route below. Do not convert it to JSON or copy an HTTP `Cookie` request header.

Firefox containers and profiles hold separate sessions. Exporting a different one can capture a logged-out session or another account. Exporting all sites also includes unrelated private cookies, can introduce duplicate cookie names, and can exceed the Web UI size limit.

### Import through the Web UI

1. Under **Settings → Trackers**, configure the tracker and select **Save**. Wait for any pending save to activate.
2. Open **Settings → Tracker Auth**, find that tracker's card, and select **Import Cookies**.
3. Choose the exported `.txt` file from your browser's file picker. It must be no larger than **1 MiB (1,048,576 bytes)**.
4. Wait for the import result, then [verify authentication](#verify-authentication).

The browser uploads the file **contents** to upbrr, so you do not need to copy it onto the server or into a Docker volume. The selected tracker determines where the cookies are stored; the export does not need a tracker-ID filename for this route. Import persists accepted cookies encrypted immediately, without the page-level **Save** button or a restart.

### Import through the cookie folder

Use this route when you can access upbrr's application-state directory, including when migrating an existing export.

1. Find the directory containing the **active database** (normally `db.sqlite`). In the Web UI, open [Logging](../logging.md) and read **Log path**, even if **File enabled** is off. The path ends in `logs/upbrr.log` beneath the active state directory: `/config/upbrr/logs/upbrr.log` means the state directory is `/config/upbrr`. See [State location](../../configuration/index.md#state-location) for default and legacy paths. Check the account/environment that starts upbrr, including its service or container; your shell's home directory or an old config path may differ.
2. Create or open its `cookies` subdirectory. Put the export directly inside it, not inside another subdirectory.
3. Rename the Netscape file to **`<tracker-id>.txt`**, replacing `<tracker-id>` with the configured tracker's ID shown in **Settings → Trackers**, not its full site name or your Firefox container name.
4. Allow the upbrr process to read the file and write its state directory, including removing migrated source files. Keep access restricted to the account running upbrr.
5. Restart upbrr, then [verify authentication](#verify-authentication).

For a standard Docker installation, the active database is normally `/config/upbrr/db.sqlite`, so the destination is `/config/upbrr/cookies/<tracker-id>.txt` **inside the container**. With the example bind mount `/path/to/config:/config`, copy the file to `/path/to/config/upbrr/cookies/<tracker-id>.txt` **on the host**. An older installation using `/config/.upbrr/db.sqlite` instead needs `/path/to/config/.upbrr/cookies/<tracker-id>.txt`. Check your actual Compose volume mapping and existing database before copying; `/data` is normally the media mount, not application state. See [Docker installation](../../getting-started/installation.md#docker-compose).

On startup, eligible top-level cookie files are migrated into encrypted database storage. Cleanup is batch-wide: after at least one cookie is stored and no file has a parse or storage failure, upbrr removes the top-level `.txt` and `.json` files. Their disappearance is expected, not a lost session. Any parse or storage failure keeps all source files, including files whose cookies were stored. Cleanup permission failures can also leave files behind. Inspect the status/count and sanitized migration errors rather than using file presence alone as proof of success.

### Accepted file formats

The Firefox example uses **Netscape HTTP Cookie File** text, normally beginning with `# Netscape HTTP Cookie File`. Each cookie occupies a tab-separated row: domain, include-subdomains flag, path, secure flag, expiry, name, and value. Keep the real export unchanged. This row is synthetic and cannot authenticate:

```text
# Netscape HTTP Cookie File
.tracker.example	TRUE	/	TRUE	0	example_session	synthetic-value
```

A copied header such as `Cookie: example_session=synthetic-value` is not a Netscape export. Renaming a header, HTML page, or arbitrary JSON to `.txt` does not convert it.

Accepted JSON alternatives are:

- a cookie-name object such as `{"example_session":"synthetic-value"}`;
- a cookie-name object with nested values such as `{"example_session":{"value":"synthetic-value"}}`;
- for **Web UI import**, an array of objects with `name` and `value`, such as `[{"name":"example_session","value":"synthetic-value"}]`.

Folder migration supports the two object forms as `<tracker-id>.json`; use the Web UI for JSON arrays. The Web UI rejects duplicate cookie names and files with no usable entries. For the Firefox workflow, keep the saved Netscape `.txt` file instead of changing formats.

## Verify authentication

1. Open **Settings → Tracker Auth** and inspect the tracker's status and **Cookies:** count. **Has cookies** and a nonzero count confirm stored cookie data, not acceptance by the tracker.
2. Select **Check Auth** where offered. Read the returned status and message, complete supported 2FA if requested, and check again. Not every cookie tracker provides remote validation, so this action may be absent.
3. Before a live upload, select **Skip client injection** and run **Dry Run** to check tracker preparation without tracker submission or client injection.

A successful import does not prove the cookies belong to the correct site/account or that the exported session remains valid. Even a successful remote check is only current at the time of that check.

## Cookie troubleshooting

### No cookies, login required, or expired session

Confirm the tracker is still logged in in the same Firefox profile/container and that the export came from that tracker tab after 2FA or browser challenges were completed. Sessions can expire or be invalidated after export. Sign in again, download fresh **Current Container and Site** cookies, re-import, and select **Check Auth** where supported.

### Import is rejected or contains no entries

Inspect the file locally without sharing its values. Check the actual [format](#accepted-file-formats), not only the extension: Netscape needs cookie rows, not just its comment header; JSON must use an accepted shape. Re-export only the relevant container/site to avoid unrelated cookies, duplicate names, and files over the Web UI's 1 MiB limit. Do not fix an oversized file by truncating it.

### Folder import is not detected

Check the active database directory, exact tracker-ID filename, lowercase `.txt` extension, and top-level `cookies` placement. Enable filename extensions in your file manager to catch names such as `<tracker-id>.txt.txt`. Verify process read/write permissions, the Docker host-to-container volume mapping, and that the intended instance restarted. If files remain, review migration/cleanup messages; if they disappear, check the stored count. See [Storage recovery](#storage-recovery) if encryption could not initialize.

### Import Cookies is missing

Save the tracker entry and wait for activation, then return to **Tracker Auth**. Clear any tracker filter. Only configured trackers with cookie-file support offer **Import Cookies**. If the tracker uses an API key, passkey, or another auth method instead, use its fields under [Trackers settings](./trackers.md).

### Cookies are stored but remote checks fail

Distinguish local parsing/storage errors from a tracker rejecting the session, presenting another browser challenge, or returning a network/service error. Refresh the browser session and re-export first for session rejection. A challenge completed in Firefox does not guarantee upbrr's requests will be accepted.

Cookies do not replace other required credentials. Follow the tracker card's requirements and returned remediation message rather than removing required fields.

For a report, include the upbrr version/build, installation type, tracker ID, import route, status/count, and shortest sanitized error or log excerpt around the failed action. Check both the auth message and **Logging** yourself before sharing; remove cookie values, credentials, private URLs, and account details. Never include the export, database, key material, or raw tracker responses. See [Safe issue reports](../../troubleshooting/index.md#safe-issue-reports).

## Storage recovery

Cookie storage depends on key material beside the active database. Treat `web-auth.json` and `db.sqlite` as sensitive credentials: give both files restrictive permissions, keep them out of shared storage, and never attach them to support requests. Together they can expose encrypted cookies and other stored secrets. Back up and move them together; replacing or losing `web-auth.json` can make encrypted secrets and cookies unusable.

If **Storage unavailable** appears, check that first-run Web UI account setup is complete, that the running instance uses the intended state directory, and that the matching `web-auth.json` is present and readable. Check database/directory write permissions and Docker mounts, then inspect the sanitized storage error. Do not delete `db.sqlite` or replace/delete `web-auth.json` to force an import. If state was moved or restored, recover the matching database and auth material together from a trusted backup; preserve the current state before recovery. See [State location](../../configuration/index.md#state-location) and [upgrade backups](../../getting-started/upgrading.md).
