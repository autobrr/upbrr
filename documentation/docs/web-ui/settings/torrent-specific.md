---
title: Torrent preparation settings
description: Automatic torrent reuse and rehash-dependent upload scheduling.
---

# Torrent preparation settings

Use **Settings → Torrent preparation** to delay rehash-dependent submissions. Torrent artifact selection is automatic.

| Field                                       | Default | Effect                                                                                                      |
| ------------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------- |
| **Rehash-dependent upload delay (seconds)** | `0`     | Waits this many seconds after reusable-torrent submissions finish before starting rehash-dependent uploads. |

A negative rehash cooldown behaves as zero.

Preparation retains reusable base torrents with different piece layouts when needed. Each tracker uses a base that satisfies its limits. upbrr tries to retain a variant with pieces no larger than 16 MiB and a general-purpose variant near 1,000–2,000 pieces; one artifact can serve both purposes. Tracker limits and an explicit maximum piece size take precedence over the general piece-count preference.

The former `prefer_max_16_torrent` setting and `UA_DEFAULT_PREFER_MAX_16_TORRENT` environment variable are removed. Older configuration files can still be imported; that preference is ignored and omitted when settings are exported. The CLI `--max-piece-size` override remains available.

With `--nohash`, upbrr only retains and reuses suitable existing pieces. It does not hash media to create a missing variant, and skips trackers for which no compatible artifact is available.

Compatible existing torrents are checked for metainfo, layout and applicable limits without automatically reading every media byte. Those checks do not prove that same-sized media bytes match the piece hashes. Explicit recheck verifies pieces; rehash creates a fresh torrent. Tracker metadata personalization and eligible wrapper-only changes can reuse existing pieces.

Reuse currently supports v1 torrents. V2 and hybrid v1/v2 torrents require a new torrent.

:::note Compatibility field

**Mkbrr threads** remains in the schema but has no current runtime reader.

:::

## Verify the change

Save, prepare a source with known torrents in a configured search client, and inspect the selected reusable torrent. Rehash cooldown is observable only in a real upload plan containing both reusable and rehash-dependent tracker submissions.
