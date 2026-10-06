---
title: Migrate from Upload Assistant
description: Import an Upload Assistant config.py directly or convert it to reviewable YAML first.
---

# Migrate from Upload Assistant

upbrr accepts Upload Assistant `config.py` files through its config importer. Imported settings are a starting point; unsupported or renamed options can require manual correction.

## Direct import

Use direct import to convert and save the configuration into upbrr's database:

```powershell
.\upbrr.exe --import-config "C:\path\to\Upload-Assistant\data\config.py"
```

The importer accepts:

- Upload Assistant `.py` files;
- upbrr `.yaml` and `.yml` files;
- upbrr `.json` files.

Read every warning. Unknown legacy keys, unsupported tracker fields, and unsupported image-host settings can be omitted or adjusted.

Python imports accept both `config = {...}` and type-annotated assignments such as `config: dict = {...}`. The importer reads supported literal values; it does not execute the Python file.

Unused incomplete torrent-client examples are skipped with a warning. A client referenced by default, search, injection, or tracker settings is retained for validation instead of silently discarded. Complete or remove those references before retrying a rejected import. THR and its retired Pronfo settings are no longer imported.

The Web UI also provides config import in **Settings**.

## Convert to YAML first

Use the repository converter when you want to inspect the result before import:

```powershell
py .\scripts\convert_ua_config.py "C:\path\to\Upload-Assistant\data\config.py" -o ".\config.converted.yaml"
```

Review the generated file, then import it:

```powershell
.\upbrr.exe --import-config ".\config.converted.yaml"
```

## Migrate tracker cookies

Follow the [cookie export and import walkthrough](../web-ui/settings/tracker-auth.md#export-and-import-cookies) to export a current browser session and choose Web UI or cookie-folder import. Web UI import uploads the file contents directly, without a server-side copy or restart.

For folder migration, place Netscape `<tracker-id>.txt` files or supported cookie-map `<tracker-id>.json` files directly in `cookies` beside the active `db.sqlite`, then restart upbrr. The walkthrough covers tracker IDs, active state paths, Docker mounts, and accepted JSON formats. Successfully migrated files are removed after encrypted database storage.

[Verify tracker authentication](../web-ui/settings/tracker-auth.md#verify-authentication) before preparing a live upload; successful import alone does not prove the session is valid.

## Restore Web UI browse access

Browse roots are not part of imported application config. They are stored in `web-auth.json` beside the database because they control which host paths the browser may access.

If no browse policy exists, the first authenticated Web UI setup can establish it. To replace a policy after setup, stop the Web UI server and use the local binary:

```powershell
.\upbrr.exe auth browse-roots "D:\Media" "E:\Downloads"
```

Existing browse roots remain unchanged when application config is imported.

## Validate the migration

Check metadata credentials, trackers, image hosts, torrent clients, screenshot settings, and post-upload behavior. Then run one workflow with `--debug --no-seed`, or select **Skip client injection** before the Web UI **Dry Run**, before submitting anything.
