# upbrr

> **Alpha release:** upbrr is early software and the documentation is incomplete. It is intended for users who already understand Upload Assistant workflows, tracker rules, release naming, category/type selection, screenshots, descriptions, torrent clients, and post-upload checking.
>
> **Quality check every upload before submitting.** Pay close attention to generated names, tracker category, tracker type, edition/source/resolution handling, description output, image links, and torrent-client injection settings. Do not rely on alpha automation as the final authority.

<img width="1899" height="1580" alt="Screenshot 2026-06-12 093436" src="https://github.com/user-attachments/assets/d0bd8123-52d0-4d17-9aed-1cba270f258b" />

upbrr is an upload preparation app for private-tracker workflows.

- pick a release folder or file
- fetch and adjust metadata
- review tracker choices and dupe checks
- generate, select, and upload screenshots
- build tracker descriptions
- preview payloads before upload
- submit to trackers
- inject or save torrents for your client

The goal is a guided upload workspace, not a replacement for user judgement or tracker rules.

## Before You Start

You should already know how your trackers expect uploads to be named and categorized. Before doing real uploads, run test paths, dry runs, or site checks where possible and compare upbrr output with what you would have submitted manually.

Have these ready:

- TMDB API key
- tracker API keys, cookies, or credentials required by the trackers you use
- image host credentials
- torrent client details or watch-folder paths
- existing Upload Assistant `config.py`, if migrating

By default, upbrr stores its database at:

```text
%USERPROFILE%\.upbrr\db.sqlite
```

Cookie files live beside that database:

```text
%USERPROFILE%\.upbrr\cookies
```

If you use a custom database path, put cookies in a `cookies` folder beside that database.

## Migration From Upload Assistant

upbrr can import a legacy Upload Assistant `config.py` directly, or convert it to a YAML file first so you can inspect it.

### Option 1: Direct Import

Use this when you want the app to convert and save the config straight into the upbrr database:

```powershell
.\upbrr.exe --import-config "C:\path\to\Upload-Assistant\data\config.py"
```

The importer accepts:

- `.py` legacy Upload Assistant config files
- `.yaml` / `.yml` upbrr config files
- `.json` upbrr config files

Read all warnings printed during import. Unknown legacy keys, unsupported tracker fields, or unsupported image-host settings may be skipped or adjusted.

You can also import config files from the Settings page in the GUI or web UI.

### Option 2: Convert To YAML First

Use the included converter when you want a human-readable file to review before importing:

```powershell
py .\scripts\convert_ua_config.py "C:\path\to\Upload-Assistant\data\config.py" -o ".\config.converted.yaml"
```

Then inspect `config.converted.yaml`, fix any values that need manual attention, and import it:

```powershell
.\upbrr.exe --import-config ".\config.converted.yaml"
```

The converter maps known Upload Assistant defaults, tracker settings, and torrent-client settings onto the current upbrr config shape. Legacy-only settings that do not exist in upbrr are not carried over. Treat the converted file as a starting point, not proof that your setup is complete.

### Migrating Cookies

If your old Upload Assistant setup has tracker cookies under `data\cookies`, copy those files to upbrr's cookie folder:

```text
%USERPROFILE%\.upbrr\cookies
```

Restart upbrr after copying cookies, then verify tracker login/session status before uploading.

## Running upbrr

Desktop GUI:

```powershell
.\upbrr-gui.exe
```

Web UI:

```powershell
.\upbrr.exe serve
```

CLI upload preparation:

```powershell
.\upbrr.exe "D:\releases\Some.Release.2026.1080p.BluRay"
```

Useful CLI checks:

```powershell
.\upbrr.exe --site-check --trackers BLU,OE "D:\releases\Some.Release"
.\upbrr.exe --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
.\upbrr.exe --queue "D:\upload-queue" --limit-queue 5
```

NOTE: with cli `--debug` works as expected. Additionally, the printed feedback (even with debug) can be adjusted with `--log-level`. See `upbrr.exe --help`

## Typical Upload Flow

1. Open upbrr.
2. Go to Settings and confirm required API keys, trackers, image hosts, torrent clients, screenshots, and post-upload options.
3. Select a release path.
4. Fetch metadata.
5. Review title, year, IDs, category, type, source, resolution, edition, season/episode, and generated release name.
6. Run dupe checks and read tracker-specific warnings.
7. Generate or import screenshots, then verify the selected images and image-host output.
8. Build descriptions and inspect rendered tracker descriptions.
9. Run a dry run or payload preview where available.
10. Upload only after every tracker tab looks correct.

## Required Manual Checks

Before submitting, verify:

- release name matches tracker rules
- category and type are correct for each tracker
- movie vs TV handling is correct
- disc, remux, encode, WEB, HDTV, pack, season, and episode handling is correct
- source, resolution, edition, service, tag, and group are correct
- screenshots are valid, ordered, and hosted on allowed hosts
- description BBCode renders correctly
- torrent file contents and piece settings are expected
- torrent client category, tags, save path, and injection target are correct
- dupe results and rule warnings have been read

Alpha builds can have rough edges. When in doubt, stop before upload and check manually.

## License

GPL-2.0-or-later. See [LICENSE](./LICENSE).
