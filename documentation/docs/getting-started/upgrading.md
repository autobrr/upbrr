---
title: Upgrading
description: Back up upbrr state, replace the binary or image, and verify automatic database migration.
---

# Upgrading

upbrr applies forward-only SQLite migrations during startup. Back up application state before running a newer version.

The upgrade from `v0.3.5.1` to `v0.4.0` adds stored metadata-lookup evidence, release-cut metadata, image-account reuse boundaries, and imported tracker-media details. Older prepared snapshots may need preparation again. Keep the pre-upgrade backup if you might return to `v0.3.5.1`; changing only the binary or image tag does not undo these changes.

The upgrade from `v0.4.0` to `v0.4.1` adds stored acceptance evidence for imported screenshot links, screenshot content and tonemapping provenance, and retained HDR analysis associations. Existing images and links without the new evidence remain unknown. Imported-link acceptance survives a restart of the same prepared generation, but preparation for a new or corrected generation requires fresh matching evidence. Older prepared snapshots require preparation again. These additive database migrations are forward-only; keep a stopped-service backup before upgrading and restore it before returning to `v0.4.0`.

The upcoming `v0.4.2` moves workflow source ownership and command receipts into separate database rows and stores exact immutable snapshot references. The first startup backfills existing workflows; allow extra time and disk space for that transaction. An interrupted migration rolls back and retries on the next startup. Retained History and command results are preserved. Malformed or uncertain source ownership defers cleanup so recoverable evidence remains available. This storage format is forward-only: restore the complete stopped-service backup before returning to an earlier version, rather than replacing only the binary or image tag.

If a retained workflow reports incompatible prepared data after an update, prepare the input again before continuing. Repeating continuation does not rebuild that data. Saved corrections remain available; preparation and the normal review steps must complete again. Check and reconcile any uncertain external outcome before retrying a tracker or client operation.

## 1. Stop upbrr

For the upcoming `v0.4.2` artifact layout, also follow the [manual legacy report-cache cleanup notice](../changelog/v0.4.2.md#remove-the-legacy-report-cache). No automatic migration or legacy cache discovery is performed; releases must be prepared again.

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

Saving effective settings reuses the running process's startup maintenance. It does not repeat the startup-wide History and provisional-file cleanup; expiry cleanup continues while the server runs.

After prior-process input and work leases expire, startup retains completed checkpoints and marks unfinished operations interrupted. It marks unresolved prior-process external attempts failed locally and removes their retry blocks. This does not prove that the tracker upload or client operation failed remotely. Check the tracker or torrent client before retrying an interrupted stage.

If **Legacy workflow recovery** still lists an unresolved external outcome, choose **Recover workflow** and inspect the remote result before resolving the action. Choose **Confirmed not completed; allow a fresh exact attempt** only when you have verified that the operation did not complete. Recovery itself does not resubmit anything.

The CLI presents the same confirmation for a remaining recovery action when run interactively or with `--unattended_confirm`. Strict `--unattended` exits without prompting. Deleting the associated release from History removes its local workflow and effect records; it does not establish whether the external operation completed.

:::caution Downgrades

Never start an older binary against the upgraded database. To roll back, stop every upbrr process and restore the matching pre-upgrade state backup before starting the older version.

:::

Development builds for v0.4.2 also add per-image screenshot renderer outcomes. Existing captures keep their known tone-mapping status but have no invented renderer history. The capture compatibility version changes, so older automatic captures are not reused as if they were made by the new renderer policy. Stop the service and back up the whole state directory before testing; restore that backup when returning to an older version.
