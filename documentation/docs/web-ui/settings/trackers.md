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

## Group policy lists

Every tracker card provides three independent comma-separated release-group lists. Configure each tracker separately; a group listed for one tracker has no effect on another.

| Field                       | Effect for a matching incoming group                                                                                                                                                                |
| --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Duplicate bypass groups** | Ignores confirmed duplicate candidates from other groups. Same-group candidates, exact duplicates, and candidates whose group cannot be determined still receive normal duplicate handling.         |
| **Personal release groups** | Selects the tracker API's personal-release option when that API supports one. An explicit Personal Release choice on the upload overrides this configured default, including an explicit **false**. |
| **Internal groups**         | Selects the tracker API's internal option when supported and applies the same-group duplicate behavior described above.                                                                             |

Enter tags without the conventional leading `-`, for example `NTb, GRP`. upbrr trims whitespace, removes one accidental leading hyphen, and removes repeated entries case-insensitively. Matching uses the complete group tag; wildcards and partial matches are not supported.

YAML accepts either a comma-separated value or a sequence:

```yaml
trackers:
  NBL:
    dupe_bypass_groups: NTb, GRP
    personal_release_groups:
      - GRP
  BTN:
    internal_groups: NTb
```

JSON uses arrays with the catalog field names `DupeBypassGroups`, `PersonalReleaseGroups`, and `InternalGroups`.

For BTN, configured internal groups can bypass two restrictions when ownership is unambiguous. Claimed-show handling uses fresh structured rows from the scoped BTN claim-list post. upbrr automatically refreshes an older title-only cache before evaluating ownership; if that refresh fails, the legacy data can still block an upload but cannot prove ownership for a bypass. Season-pack handling separately uses current BTN reservation API rows. Every applicable row from the relevant source must identify the same configured group.

With debug logging enabled, `reason=legacy_format` identifies an automatic cache migration attempt. A blocked claim warning includes `release_group`, `internal_group`, `fresh_structured`, `own_claim`, and `matched_claims` so you can see which bypass prerequisite was missing.

HDB uses BTN's authoritative claim-list data and the same claim window for non-Scene WEB TV uploads. upbrr reads an existing BTN session and its claim cache without starting a login or TOTP flow. If neither a usable structured cache nor BTN access is available, the HDB upload continues with a warning.

The older per-tracker **Internal** boolean is retained only so existing configuration can be read and exported. It no longer enables internal handling. Add the relevant tags to **Internal groups**; a list match works even when the legacy value is `false`.

## Remove a tracker

**Remove** resets the entry to catalog defaults, hides its card, and removes its default and preferred-source selections. Select **Save** to persist those changes. Unsupported preserved entries appear separately because no current implementation can use them; delete them only when you no longer need their retained config.

Tracker credentials are secrets. Leave `[REDACTED]` unchanged to preserve a value. After any tracker change, check [Tracker Auth](./tracker-auth.md) and run **Dry Run** before submission.
