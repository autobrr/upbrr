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

- tracker API keys, cookies, or credentials required by the trackers you use
- image host credentials
- torrent client details or watch-folder paths
- existing Upload Assistant `config.py`, if migrating
- FFmpeg installed on your system for screenshot generation
    - On Windows, FFmpeg must be added to `PATH`
    - On Linux, install it from your distribution package manager
    - On macOS, install it with Homebrew:

      ```bash
      brew install ffmpeg
      ```

      The CLI and WebUI server can find ffmpeg when their process environment includes Homebrew's bin directory.
    - Automatic DVD menu screenshots need an FFmpeg build whose `dvdvideo` demuxer exposes `menu`, `menu_lu`, `menu_vts`, `pgc`, and `pg`. A version number alone does not prove this capability. upbrr checks the selected FFmpeg at runtime; see [FFmpeg's dvdvideo documentation](https://ffmpeg.org/ffmpeg-formats.html#dvdvideo).

- A TMDB API key is optional, but required for most trackers in upbrr.

By default, upbrr stores its database at:

```text
%USERPROFILE%\.upbrr\db.sqlite
```

Otherwise, if the `XDG_CONFIG_HOME` environment variable is set (normally `~/.config`), the path is:
```
$XDG_CONFIG_HOME/upbrr/db.sqlite
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

You can also import config files from the WebUI Settings page.

### Web UI Browse Access

Config import does not set web browse roots. Browse access is stored separately in `web-auth.json`, beside the upbrr database, because it controls which host folders the browser-based web UI can read.

On a fresh web UI setup, after creating the admin account, upbrr asks you to choose one or more browse roots such as `D:\Media, E:\Downloads`, or to explicitly allow unrestricted host browsing. The web UI cannot browse release folders or import menu images until this is set.

If you import an existing Upload Assistant config, the imported tracker, client, and app settings do not change browse access. If `web-auth.json` already exists, its current browse roots or unrestricted-browse setting stay in effect. If no `web-auth.json` exists yet, you will still be asked to set browse access the first time you set up the web UI.

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

Web UI:

```powershell
.\upbrr.exe serve
```

Web UI behind a reverse proxy:

The standard and simplest reverse-proxy setup is to give upbrr its own
subdomain, such as `https://upbrr.example.com`. In that setup the browser still
sees upbrr at the web root (`/`), so upbrr does not need any special base path
setting:

```powershell
.\upbrr.exe serve
```

Example nginx subdomain proxy:

```nginx
server {
  server_name upbrr.example.com;

  location / {
    proxy_pass http://localhost:7480/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
  }
}
```

A path-prefix proxy is different. Use this only when upbrr must live under an
existing domain path, such as `https://example.com/upbrr`. In that setup the
browser requests `/upbrr/...`, so upbrr must be told its external base URL:

```powershell
.\upbrr.exe serve --base-url https://reverseproxyaddr.com/upbrr/
```

To save that setting to `web-config.json`, add `--persist-web-config`:

```powershell
.\upbrr.exe serve --base-url https://example.com/upbrr/ --persist-web-config
```

For Docker or unattended deployments, use env vars instead of overriding the
container command:

```yaml
environment:
  - UPBRR_WEB_BASE_URL=/upbrr/
  - UPBRR_HEALTHCHECK_URL=http://127.0.0.1:7480/upbrr/api/auth/status
```

Serve settings precedence is CLI flags, then `UPBRR_WEB_*` env vars, then
`web-config.json`, then defaults. Supported env vars are `UPBRR_WEB_BASE_URL`,
`UPBRR_WEB_HOST`, `UPBRR_WEB_PORT`, `UPBRR_WEB_OPEN_BROWSER`, and
`UPBRR_WEB_TRUSTED_PROXIES`.

Example nginx path-prefix proxy:

```nginx
server {
  server_name example.com;

  location = /upbrr {
    return 301 /upbrr/;
  }

  location /upbrr/ {
    proxy_pass http://localhost:7480/upbrr/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
  }
}
```

Keep the `/upbrr/` path on both sides of `proxy_pass` unless you intentionally
configure every HTML, API, SSE, cookie, and asset path rewrite yourself. The
supported mode is path-retained proxying: browsers request `/upbrr/...` and the
proxy forwards `/upbrr/...` to upbrr. If the page loads but assets, login, or
live progress updates fail, check that requests are going to `/upbrr/api/...`
and `/upbrr/assets/...`, not root `/api/...` or `/assets/...`.

If your proxy terminates HTTPS and forwards plain HTTP to upbrr, configure
`trusted_proxies` in `web-config.json` so upbrr can trust `X-Forwarded-Proto`
when deciding whether to set secure cookies.

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

### Automatic DVD Menu Screenshots

Automatic DVD menu capture is opt-in. It accepts an extracted DVD directory containing `VIDEO_TS`, or the `VIDEO_TS` directory itself. ISO images, optical drives, and Blu-ray menus are not automatic-capture inputs in this version.

CLI example:

```powershell
.\upbrr.exe --get-dvd-menus "D:\releases\Example.Release.2026.DVD-GRP"
```

In the WebUI, fetch DVD metadata, then open **Disc Menus** and start automatic capture. Manual Disc Menu image import remains available for DVD, BDMV, and HDDVD sources. `screenshot_handling.max_menu_items` controls the maximum stored automatic menu images; the default is `6` and the supported range is `1` to `32`.

The FFmpeg capability check must find the `dvdvideo` demuxer and all required menu-coordinate options. If it does not, capture stops with an explicit capability error instead of sampling menu VOB files. Application Details shows the embedded Go DVD engine version, FFmpeg version, and menu-capability status without exposing local executable paths.

upbrr does not include CSS decryption or bundle `libdvdcss`. Encrypted/protected, unreadable, region-restricted, or corrupt inputs may fail. Use only media you are legally permitted to process; extract/decrypt it outside upbrr where allowed.

### Run with Docker Compose

A ready-to-use example lives in
[`example-docker-compose.yml`](example-docker-compose.yml). The image serves the web UI on
all interfaces by default and stores its config and database in the `/config` volume. Copy
the example to `docker-compose.yml` (or point `-f` at it) and start it:

```bash
cp example-docker-compose.yml docker-compose.yml
docker compose up -d
```

Then open `http://<host>:7480` to complete the first-run setup.

Notes:

- The container runs as **uid:gid 1000:1000**. The bind-mounted `./config` and `/data`
  directories must be owned by that uid — create and `chown` them before the first start
  (`mkdir -p /path/to/config && sudo chown -R 1000:1000 /path/to/config`, same for your
  data dir), or set
  `user:` in the compose file to a uid that already owns them. Docker auto-creates a
  missing bind-mount dir as `root`, which the non-root app can't write, so pre-create it.
  The `/data` mount (and any hardlink/reflink staging target) needs the same. (On Docker
  Desktop for macOS/Windows this is handled automatically.)
- First-run setup is reachable by anyone who can reach port `7480`. Complete it promptly,
  and don't expose the port to untrusted networks (scope it to `127.0.0.1:7480:7480` or
  use a reverse proxy) until you have.

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
