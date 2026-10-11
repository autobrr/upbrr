---
title: Upgrading
description: Back up upbrr state, replace the binary or image, and verify automatic database migration.
---

# Upgrading

upbrr applies forward-only SQLite migrations during startup. Back up application state before running a newer version.

The upgrade from `v0.3.5.1` to `v0.4.0` adds stored metadata-lookup evidence, release-cut metadata, image-account reuse boundaries, and imported tracker-media details. Older prepared snapshots may need preparation again. Keep the pre-upgrade backup if you might return to `v0.3.5.1`; changing only the binary or image tag does not undo these changes.

## 1. Stop upbrr

Stop every binary, service, and container using the database before upgrading or copying it. Disable scheduled tasks and automatic restarts that could start an older process during the upgrade. Take the backup only after all writers have stopped.

Running old and new versions against the same database is unsupported. Older binaries do not enforce the newer active-input and submission protections.

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

Startup automatically removes identifiable leftovers from previously deleted History releases, including their workflow records and generated artifacts. Retained History, active work, and records with ambiguous ownership are preserved.

After prior-process input and work leases expire, startup retains completed checkpoints and marks unfinished operations interrupted. It marks unresolved prior-process external attempts failed locally and removes their retry blocks. This does not prove that the tracker upload or client operation failed remotely. Check the tracker or torrent client before retrying an interrupted stage.

If **Legacy workflow recovery** still lists an unresolved external outcome, choose **Recover workflow** and inspect the remote result before resolving the action. Choose **Confirmed not completed; allow a fresh exact attempt** only when you have verified that the operation did not complete. Recovery itself does not resubmit anything.

The CLI presents the same confirmation for a remaining recovery action when run interactively or with `--unattended_confirm`. Strict `--unattended` exits without prompting. Deleting the associated release from History removes its local workflow and effect records; it does not establish whether the external operation completed.

:::caution Downgrades

Never start an older binary against the upgraded database. To roll back, stop every upbrr process and restore the matching pre-upgrade state backup before starting the older version.

:::
