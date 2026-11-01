---
title: Torrent preparation settings
description: Prefer reusable torrents with smaller pieces and delay rehash-dependent submissions.
---

# Torrent preparation settings

Use **Settings → Torrent preparation** for torrent reuse and rehash scheduling.

| Field                                                 | Default | Effect                                                                                                      |
| ----------------------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------- |
| **Prefer reusable torrents with pieces up to 16 MiB** | Off     | During torrent-client search, prefers a validated reusable torrent whose piece size is at most 16 MiB.      |
| **Rehash-dependent upload delay (seconds)**           | `0`     | Waits this many seconds after reusable-torrent submissions finish before starting rehash-dependent uploads. |

**Prefer reusable torrents with pieces up to 16 MiB** affects selection among reusable client torrents; it does not impose a piece size on every newly generated torrent. A negative rehash cooldown behaves as zero.

:::note Compatibility field

**Mkbrr threads** remains in the schema but has no current runtime reader.

:::

## Verify the change

Save, prepare a source with known torrents in a configured search client, and inspect the selected reusable torrent. Rehash cooldown is observable only in a real upload plan containing both reusable and rehash-dependent tracker submissions.
