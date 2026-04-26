---
sidebar_position: 1
title: CLI Usage
---

# CLI usage

The CLI entrypoint is `cmd/upbrr`.

```bash
go run ./cmd/upbrr "D:\releases\Some.Release.2026.1080p.BluRay"
```

## Common modes

Run a safe site check:

```bash
go run ./cmd/upbrr --site-check --trackers BLU,OE "D:\releases\Some.Release"
```

Run a dry run:

```bash
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
```

Upload from previously prepared metadata:

```bash
go run ./cmd/upbrr --upload-only "D:\releases\Some.Release"
```

Process a queue directory:

```bash
go run ./cmd/upbrr --queue "D:\upload-queue" --limit-queue 5
```

## Unattended safety

Unattended and unattended-confirm flows are safety-critical. They should stay non-blocking and conservative:

- prefer dry-run or site-check when a choice cannot be made safely
- keep tracker selection explicit
- keep queue limits explicit
- avoid hidden interactive prompts
- preserve skip behavior for dupes, rule failures, screenshot/image-host uploads, torrent injection, and retries

## Overrides

The CLI supports release-name, external ID, screenshot, tracker, and execution overrides. Prefer narrow overrides on the command line and keep durable defaults in config.

## Flag reference

The table below is generated from the current CLI option definitions in `cmd/upbrr/cli_options.go`. Short aliases are intentionally omitted here unless they are commonly used or materially different from the long option.

### Run modes and execution

| Flag                   | Type   | Default | Purpose                                                          |
| ---------------------- | ------ | ------- | ---------------------------------------------------------------- |
| `--gui`                | bool   | `false` | Launch the GUI                                                   |
| `--dry-run`            | bool   | `false` | Run without uploading                                            |
| `--site-check`         | bool   | `false` | Search/check sites without uploading                             |
| `--upload-only`        | bool   | `false` | Upload using prepared metadata cache only                        |
| `--queue`              | string | `""`    | Process an entire folder queue                                   |
| `--limit-queue`        | int    | `0`     | Limit the number of queued items to process                      |
| `--unattended`         | bool   | `false` | Unattended mode                                                  |
| `--unattended_confirm` | bool   | `false` | Unattended mode with prompts                                     |
| `--uac`                | bool   | `false` | Unattended mode with prompts                                     |
| `--debug`              | bool   | `false` | Enable debug mode                                                |
| `--log-level`          | string | `""`    | Set run log level (`error`, `warn`, `info`, `debug`, `trace`)    |
| `--version`            | bool   | `false` | Show version and exit                                            |
| `--cleanup`            | bool   | `false` | Delete all stored database content for all releases and exit     |
| `--delete-tmp`         | bool   | `false` | Delete stored database content for each input path before upload |
| `--dtmp`               | bool   | `false` | Delete stored database content for each input path before upload |
| `--onlyID`             | bool   | `false` | Only grab tracker metadata IDs                                   |

### Config and tracker selection

| Flag                        | Type   | Default | Purpose                                                          |
| --------------------------- | ------ | ------- | ---------------------------------------------------------------- |
| `--config`                  | string | `""`    | Path to config file                                              |
| `--import-config`           | string | `""`    | Import config file (`.py`, `.yaml`, `.yml`, `.json`) and exit    |
| `--export-config`           | string | `""`    | Export SQLite config to YAML file and exit                       |
| `--export-config-plaintext` | bool   | `false` | Export config with plaintext secrets; requires `--export-config` |
| `--create-auth`             | bool   | `false` | Create `web-auth.json` beside the active database and exit       |
| `--trackers`                | string | `""`    | Upload to these trackers, comma-separated                        |
| `--trackers-remove`         | string | `""`    | Remove these trackers, comma-separated                           |
| `--rtk`                     | string | `""`    | Remove these trackers, comma-separated                           |
| `--site-upload`             | string | `""`    | Process a single tracker upload flow                             |

### Safety skips and retry controls

| Flag                      | Type   | Default | Purpose                                                      |
| ------------------------- | ------ | ------- | ------------------------------------------------------------ |
| `--skip-dupe-check`       | bool   | `false` | Skip dupe check                                              |
| `--sdc`                   | bool   | `false` | Skip dupe check                                              |
| `--skip-dupe-asking`      | bool   | `false` | Skip dupe asking                                             |
| `--sda`                   | bool   | `false` | Skip dupe asking                                             |
| `--double-dupe-check`     | bool   | `false` | Double dupe check                                            |
| `--ddc`                   | bool   | `false` | Double dupe check                                            |
| `--skip-imagehost-upload` | bool   | `false` | Skip automatic image host uploads                            |
| `--siu`                   | bool   | `false` | Skip automatic image host uploads                            |
| `--skip_auto_torrent`     | bool   | `false` | Skip automated torrent client searching                      |
| `--sat`                   | bool   | `false` | Skip automated torrent client searching                      |
| `--no-seed`               | bool   | `false` | Do not inject torrent into clients                           |
| `--force-recheck`         | bool   | `false` | Force recheck matched qBittorrent torrents before validation |
| `--frc`                   | bool   | `false` | Force recheck matched qBittorrent torrents before validation |
| `--nohash`                | bool   | `false` | Reuse existing torrents only without generating a new one    |
| `--rehash`                | bool   | `false` | Force generation of a fresh torrent                          |
| `--torrenthash`           | string | `""`    | Reuse an existing torrent info hash                          |
| `--infohash`              | string | `""`    | Override v1 info hash                                        |

### Metadata IDs and tracker IDs

| Flag       | Type   | Default | Purpose                  |
| ---------- | ------ | ------- | ------------------------ |
| `--tmdb`   | string | `""`    | Override TMDB id         |
| `--imdb`   | string | `""`    | Override IMDb id         |
| `--tvdb`   | int    | `0`     | Override TVDB id         |
| `--tvmaze` | int    | `0`     | Override TVmaze id       |
| `--mal`    | int    | `0`     | Override MAL id          |
| `--ptp`    | string | `""`    | PTP torrent id or URL    |
| `--hdb`    | string | `""`    | HDB torrent id or URL    |
| `--btn`    | string | `""`    | BTN torrent id or URL    |
| `--bhd`    | string | `""`    | BHD torrent id or URL    |
| `--blu`    | string | `""`    | BLU torrent id or URL    |
| `--aither` | string | `""`    | Aither torrent id or URL |
| `--lst`    | string | `""`    | LST torrent id or URL    |
| `--ulcx`   | string | `""`    | ULCX torrent id or URL   |

### Release metadata overrides

| Flag                     | Type   | Default | Purpose                    |
| ------------------------ | ------ | ------- | -------------------------- |
| `--category`             | string | `""`    | Override category          |
| `--type`                 | string | `""`    | Override release type      |
| `--source`               | string | `""`    | Override source            |
| `--resolution`           | string | `""`    | Override resolution        |
| `--res`                  | string | `""`    | Override resolution        |
| `--region`               | string | `""`    | Override disc region       |
| `--reg`                  | string | `""`    | Override disc region       |
| `--distributor`          | string | `""`    | Override distributor       |
| `--dist`                 | string | `""`    | Override distributor       |
| `--service`              | string | `""`    | Override streaming service |
| `--serv`                 | string | `""`    | Override streaming service |
| `--edition`              | string | `""`    | Override edition text      |
| `--repack`               | string | `""`    | Override edition text      |
| `--season`               | string | `""`    | Override season value      |
| `--episode`              | string | `""`    | Override episode value     |
| `--episode-title`        | string | `""`    | Override episode title     |
| `--manual-episode-title` | string | `""`    | Override episode title     |
| `--met`                  | string | `""`    | Override episode title     |
| `--manual-year`          | int    | `0`     | Override release year      |
| `--year`                 | int    | `0`     | Override release year      |
| `--daily`                | string | `""`    | Set daily episode air date |
| `--original-language`    | string | `""`    | Override original language |
| `--tag`                  | string | `""`    | Override group tag         |

### Naming and tracker flags

| Flag                | Type   | Default | Purpose                                  |
| ------------------- | ------ | ------- | ---------------------------------------- |
| `--anon`            | bool   | `false` | Upload anonymously                       |
| `--draft`           | bool   | `false` | Send uploads to drafts where supported   |
| `--modq`            | bool   | `false` | Opt into mod queue where supported       |
| `--personalrelease` | bool   | `false` | Mark release as personal                 |
| `--stream`          | bool   | `false` | Mark release as stream optimized         |
| `--commentary`      | bool   | `false` | Mark release as containing commentary    |
| `--webdv`           | bool   | `false` | Mark release as WEB-DV                   |
| `--dual-audio`      | bool   | `false` | Add dual-audio tag to audio name         |
| `--no-dual`         | bool   | `false` | Remove dual-audio tag from audio name    |
| `--no-dub`          | bool   | `false` | Remove dubbed tag from audio name        |
| `--no-aka`          | bool   | `false` | Remove AKA from name                     |
| `--no-edition`      | bool   | `false` | Remove edition from name                 |
| `--no-season`       | bool   | `false` | Remove season and episode from name      |
| `--no-tag`          | bool   | `false` | Remove group tag from name               |
| `--no-year`         | bool   | `false` | Remove year from name                    |
| `--not-anime`       | bool   | `false` | Force release to be treated as not anime |
| `--foreign`         | bool   | `false` | Mark TIK release as foreign              |
| `--asian`           | bool   | `false` | Mark TIK release as asian                |
| `--opera`           | bool   | `false` | Mark TIK release as opera or musical     |
| `--disctype`        | string | `""`    | Override TIK disc type                   |
| `--channel`         | string | `""`    | Override SPD channel                     |

### Screenshots, descriptions, and torrents

| Flag                 | Type   | Default | Purpose                                              |
| -------------------- | ------ | ------- | ---------------------------------------------------- |
| `--screens`          | int    | `-1`    | Number of screenshots to take                        |
| `--manual_frames`    | string | `""`    | Comma-separated frame numbers to use for screenshots |
| `--comparison`       | string | `""`    | Comparison folder path or comma-separated paths      |
| `--comparison_index` | int    | `0`     | Primary comparison index                             |
| `--comps`            | string | `""`    | Comparison folder path or comma-separated paths      |
| `--comps_index`      | int    | `0`     | Primary comparison index                             |
| `--descfile`         | string | `""`    | Custom description file path                         |
| `--desclink`         | string | `""`    | Custom description link                              |
| `--imghost`          | string | `""`    | Override image host                                  |
| `--client`           | string | `""`    | Override torrent client                              |
| `--qbit-cat`         | string | `""`    | Override qBittorrent category                        |
| `--qbc`              | string | `""`    | Override qBittorrent category                        |
| `--qbit-tag`         | string | `""`    | Override qBittorrent tag                             |
| `--qbt`              | string | `""`    | Override qBittorrent tag                             |
| `--max-piece-size`   | int    | `0`     | Set maximum torrent piece size in MiB                |
| `--mps`              | int    | `0`     | Set maximum torrent piece size in MiB                |
