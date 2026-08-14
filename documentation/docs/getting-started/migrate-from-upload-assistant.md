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

Copy legacy `.txt` or `.json` cookie files into a `cookies` directory beside the active `db.sqlite`, then restart upbrr. Successfully migrated legacy files are removed.

Verify tracker authentication from **Settings** before preparing a live upload.

## Restore Web UI browse access

Browse roots are not part of imported application config. They are stored in `web-auth.json` beside the database because they control which host paths the browser may access.

After import, complete first-run Web UI setup or update the browse policy. Existing browse roots remain unchanged when application config is imported.

## Validate the migration

Check metadata credentials, trackers, image hosts, torrent clients, screenshot settings, and post-upload behavior. Then run one workflow with `--debug --no-seed` or use the Web UI dry run before submitting anything.
