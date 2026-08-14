---
title: Upgrading
description: Back up upbrr state, replace the binary or image, and verify automatic database migration.
---

# Upgrading

upbrr applies forward-only SQLite migrations during startup. Back up application state before running a newer version.

## 1. Stop upbrr

Stop the binary, service, or container so the database is not changing while copied.

## 2. Back up the state directory

Back up the directory containing `db.sqlite`. Keep adjacent files with it, especially:

- `web-auth.json`, which protects browser authentication and encrypted application secrets;
- `web-config.json`, when persisted serve settings are used;
- the `cookies` directory, when legacy cookie import files have not yet been migrated.

Default locations are described in [Configuration](../configuration/index.md#state-location).

## 3. Install the new version

### Binary

Download the matching archive from [GitHub Releases](https://github.com/autobrr/upbrr/releases) and replace the old executable. Keep the state directory unchanged.

### Docker

Update the pinned image tag, then pull and recreate the container:

```bash
docker compose pull
docker compose up -d
```

Keep the same `/config` volume.

## 4. Verify startup

1. start upbrr;
2. check startup output for migration or configuration errors;
3. sign in to the Web UI;
4. confirm Settings, tracker authentication status, history, and browse roots;
5. select **Skip client injection**, then run **Dry Run** before the next live upload. Dry Run suppresses tracker submission; the skip option prevents torrent injection. For CLI verification, use `--debug --no-seed` (`-ns`).

:::caution Downgrades

Do not assume an older binary can use a database migrated by a newer binary. To roll back safely, stop upbrr and restore the matching pre-upgrade state backup before starting the older version.

:::
