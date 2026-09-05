---
title: Trackers settings
description: Add supported trackers, choose defaults, and configure tracker-owned credentials and upload options.
---

# Trackers settings

Use **Settings → Trackers** to enable supported tracker entries and configure only the fields advertised by the current tracker catalog.

## Add a tracker

1. Choose a tracker under **Entries** and select **Add entry**.
2. Open its card and enter the required activation credential, such as an API key, passkey, announce URL, or account credentials.
3. Configure only the upload options you understand.
4. Select **Save**.
5. If the tracker uses managed cookies or login, continue under [Tracker Auth](./tracker-auth.md).

Adding an empty card does not make an unusable tracker ready. upbrr determines configured state from tracker-owned activation fields supplied by the backend catalog.

## Defaults and priority

| Control                           | Effect                                                                                                |
| --------------------------------- | ----------------------------------------------------------------------------------------------------- |
| **Default trackers**              | Preselects configured trackers when starting a release.                                               |
| **Preferred tracker data source** | Moves that tracker to the front of tracker-data lookup and qBittorrent tracker priority when present. |

Defaults do not bypass workflow eligibility, auth, duplicate checks, validation, or manual review.

## Common tracker fields

Each tracker shows a different subset.

| Field family                            | Purpose                                                                                      |
| --------------------------------------- | -------------------------------------------------------------------------------------------- |
| API key, passkey, announce URL          | Authenticates tracker API, upload, or announce operations as required by that tracker.       |
| Username, password, OTP URI             | Supports tracker-owned login or automatic relogin. Managed cookie state is shown separately. |
| **Anonymous**, **Mod queue**, **Draft** | Sets tracker-specific upload flags where supported.                                          |
| **Image host**                          | Chooses an eligible host for that tracker instead of relying only on global priority.        |
| **Torrent client**                      | Overrides global client handling for torrents registered by that tracker.                    |
| **Link dir name**                       | Names the tracker staging directory when torrent-client linking is enabled.                  |
| **Favicon URL**                         | Overrides the tracker icon source used by the Web UI.                                        |
| **Skip if rehash**                      | Omits the tracker when preparation must generate new torrent data.                           |
| **Inject delay**                        | Overrides the global post-upload client-injection delay for that tracker.                    |

Other fields are tracker-owned. Their labels and defaults come from the running backend, not a universal schema. See [Trackers](../../trackers/index.md) for support boundaries.

## Remove a tracker

**Remove** resets the entry to catalog defaults, hides its card, and removes its default and preferred-source selections. Select **Save** to persist those changes. Unsupported preserved entries appear separately because no current implementation can use them; delete them only when you no longer need their retained config.

Tracker credentials are secrets. Leave `[REDACTED]` unchanged to preserve a value. After any tracker change, check [Tracker Auth](./tracker-auth.md) and run **Dry Run** before submission.
