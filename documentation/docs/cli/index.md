---
title: CLI reference
description: Commands, interaction modes, aliases, and upload preparation flags supported by upbrr.
---

# CLI reference

```text
upbrr [options] <input path>...
upbrr serve [options]
upbrr api-token <list|revoke> [options]
```

On Windows, examples use `upbrr.exe`. Put options before input paths.

Use executable help as the exact reference for your installed version:

```powershell
.\upbrr.exe --help
.\upbrr.exe serve --help
.\upbrr.exe api-token list --help
```

## Common operations

Prepare one release:

```powershell
.\upbrr.exe "D:\releases\Example.Release.2026.1080p-GRP"
```

Prepare without tracker submission or client injection:

```powershell
.\upbrr.exe --debug --no-seed "D:\releases\Example.Release.2026.1080p-GRP"
```

Run duplicate and site checks without uploading:

```powershell
.\upbrr.exe --site-check --trackers BLU,OE "D:\releases\Example.Release.2026.1080p-GRP"
```

Process at most five entries from a queue folder:

```powershell
.\upbrr.exe --queue "D:\upload-queue" --limit-queue 5
```

## Interaction and safety

| Option                     | Behavior                                                                                                                                |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `--debug`                  | Runs end-to-end preparation and payload preview without tracker submission. Client injection remains enabled unless `--no-seed` is set. |
| `--log-level debug`        | Changes application logging verbosity for this run. It does not enable debug/non-submitting behavior.                                   |
| `--console-log-level info` | Changes terminal log verbosity for this run without changing file or retained application logs.                                         |
| `--unattended`             | Never prompts. Unsafe global ambiguity returns an error; tracker-specific manual prerequisites can block only that tracker.             |
| `--unattended_confirm`     | Uses unattended defaults but permits required confirmation or manual-input prompts.                                                     |
| `--no-seed`                | Disables torrent-client injection.                                                                                                      |

:::danger Destructive maintenance

`--cleanup` deletes all stored release content from the active database. `--delete-tmp` deletes stored database content for each supplied input before processing it. Back up state and verify the active config/database before using either option.

:::

## Config and application

| Option                      | Aliases                    | Purpose                                                     |
| --------------------------- | -------------------------- | ----------------------------------------------------------- |
| `--config <path>`           | `-config`                  | Use a config file path.                                     |
| `--export-config <path>`    | `-export-config`           | Export SQLite config to YAML and exit.                      |
| `--export-config-plaintext` | `-export-config-plaintext` | Include plaintext secrets; requires `--export-config`.      |
| `--import-config <path>`    | `-import-config`           | Import `.py`, `.yaml`, `.yml`, or `.json` config and exit.  |
| `--create-auth`             | `-create-auth`             | Create `web-auth.json` beside the active database and exit. |
| `--version`                 | `-version`                 | Print version and exit.                                     |
| `--cleanup`                 | `-cleanup`                 | Delete all stored release content and exit.                 |

## Execution

| Option                        | Aliases                       | Purpose                                                                               |
| ----------------------------- | ----------------------------- | ------------------------------------------------------------------------------------- |
| `--queue <path>`              | `-queue`                      | Process an entire folder queue.                                                       |
| `--limit-queue <count>`       | `-limit-queue`, `-lq`         | Limit queued items processed.                                                         |
| `--site-check`                | `-site-check`, `-sc`          | Search/check sites without uploading.                                                 |
| `--site-upload <tracker>`     | `-site-upload`, `-su`         | Process one tracker upload flow.                                                      |
| `--debug`                     | `-debug`                      | Enable non-submitting debug mode.                                                     |
| `--log-level <level>`         | `-log-level`                  | Set application logging to `error`, `warn`, `info`, `debug`, or `trace` for this run. |
| `--console-log-level <level>` | `-cll`                        | Set console logging to the same levels without changing application logs.             |
| `--upload-only`               | `-upload-only`                | Upload using prepared metadata cache only.                                            |
| `--delete-tmp`                | `-delete-tmp`, `-dtmp`        | Delete stored content for each input before processing.                               |
| `--unattended`                | `-unattended`, `-ua`          | Run without prompts.                                                                  |
| `--unattended_confirm`        | `-unattended_confirm`, `-uac` | Run unattended defaults with prompts allowed.                                         |

## Tracker selection and IDs

| Option                     | Aliases                    | Purpose                             |
| -------------------------- | -------------------------- | ----------------------------------- |
| `--trackers <list>`        | `-trackers`, `-tk`         | Use comma-separated trackers.       |
| `--trackers-remove <list>` | `-trackers-remove`, `-rtk` | Remove comma-separated trackers.    |
| `--ptp <id-or-url>`        | `-ptp`                     | Supply a PTP torrent ID or URL.     |
| `--blu <id-or-url>`        | `-blu`                     | Supply a BLU torrent ID or URL.     |
| `--aither <id-or-url>`     | `-aither`                  | Supply an Aither torrent ID or URL. |
| `--lst <id-or-url>`        | `-lst`                     | Supply an LST torrent ID or URL.    |
| `--oe <id-or-url>`         | `-oe`                      | Supply an OE torrent ID or URL.     |
| `--hdb <id-or-url>`        | `-hdb`                     | Supply an HDB torrent ID or URL.    |
| `--btn <id-or-url>`        | `-btn`                     | Supply a BTN torrent ID or URL.     |
| `--bhd <id-or-url>`        | `-bhd`                     | Supply a BHD torrent ID or URL.     |
| `--ulcx <id-or-url>`       | `-ulcx`                    | Supply a ULCX torrent ID or URL.    |

## Release overrides

| Option                        | Aliases                                           | Purpose                                           |
| ----------------------------- | ------------------------------------------------- | ------------------------------------------------- |
| `--category <value>`          | `-category`, `-c`                                 | Override category.                                |
| `--type <value>`              | `-type`, `-t`                                     | Override release type.                            |
| `--source <value>`            | `-source`                                         | Override source.                                  |
| `--resolution <value>`        | `-resolution`, `-res`                             | Override resolution.                              |
| `--tag <value>`               | `-tag`, `-g`                                      | Override group tag.                               |
| `--service <value>`           | `-service`, `-serv`                               | Override streaming service.                       |
| `--distributor <value>`       | `-distributor`, `-dist`                           | Override distributor.                             |
| `--original-language <value>` | `-original-language`, `-ol`                       | Override original language.                       |
| `--edition <value>`           | `-edition`, `-repack`                             | Override edition text.                            |
| `--season <value>`            | `-season`                                         | Override one season token, such as `5` or `S05`.  |
| `--episode <value>`           | `-episode`                                        | Override one episode token, such as `5` or `E05`. |
| `--episode-title <value>`     | `-episode-title`, `-manual-episode-title`, `-met` | Override episode title.                           |
| `--manual-year <year>`        | `-manual-year`, `-year`                           | Override release year.                            |
| `--daily <YYYY-MM-DD>`        | `-daily`                                          | Set daily episode air date.                       |
| `--region <value>`            | `-region`, `-reg`                                 | Override disc region.                             |
| `--no-season`                 | `-no-season`                                      | Remove season and episode from name.              |
| `--no-year`                   | `-no-year`                                        | Remove year from name.                            |
| `--no-aka`                    | `-no-aka`                                         | Remove AKA from name.                             |
| `--no-tag`                    | `-no-tag`                                         | Remove group tag from name.                       |
| `--no-episode-title`          | `-no-episode-title`, `-net`                       | Remove episode title from name.                   |
| `--no-distributor`            | `-no-distributor`, `-ndist`                       | Remove distributor.                               |
| `--no-edition`                | `-no-edition`, `-ne`                              | Remove edition from name.                         |
| `--no-dub`                    | `-no-dub`                                         | Remove dubbed tag from audio name.                |
| `--no-dual`                   | `-no-dual`                                        | Remove dual-audio tag from audio name.            |
| `--dual-audio`                | `-dual-audio`                                     | Add dual-audio tag to audio name.                 |

## Metadata IDs

| Option          | Aliases   | Purpose             |
| --------------- | --------- | ------------------- |
| `--tmdb <id>`   | `-tmdb`   | Override TMDB ID.   |
| `--imdb <id>`   | `-imdb`   | Override IMDb ID.   |
| `--mal <id>`    | `-mal`    | Override MAL ID.    |
| `--tvdb <id>`   | `-tvdb`   | Override TVDB ID.   |
| `--tvmaze <id>` | `-tvmaze` | Override TVmaze ID. |

## Tracker overrides

| Option                | Aliases                      | Purpose                                   |
| --------------------- | ---------------------------- | ----------------------------------------- |
| `--skip-dupe-check`   | `-skip-dupe-check`, `-sdc`   | Skip duplicate checking.                  |
| `--skip-dupe-asking`  | `-skip-dupe-asking`, `-sda`  | Skip duplicate asking.                    |
| `--double-dupe-check` | `-double-dupe-check`, `-ddc` | Run a double duplicate check.             |
| `--foreign`           | `-foreign`                   | Mark a TIK release as foreign.            |
| `--opera`             | `-opera`                     | Mark a TIK release as opera or musical.   |
| `--asian`             | `-asian`                     | Mark a TIK release as Asian.              |
| `--disctype <value>`  | `-disctype`                  | Override TIK disc type.                   |
| `--commentary`        | `-commentary`, `-mc`         | Mark release as containing commentary.    |
| `--personalrelease`   | `-personalrelease`, `-pr`    | Mark release as personal.                 |
| `--stream`            | `-stream`, `-st`             | Mark release as stream optimized.         |
| `--webdv`             | `-webdv`                     | Mark release as WEB-DV.                   |
| `--not-anime`         | `-not-anime`                 | Force release to be treated as not anime. |
| `--anon`              | `-anon`, `-a`                | Upload anonymously.                       |
| `--draft`             | `-draft`, `-dr`              | Send to drafts where supported.           |
| `--modq`              | `-modq`, `-mq`               | Opt into mod queue where supported.       |
| `--channel <value>`   | `-channel`, `-ch`            | Override SPD channel.                     |

## Screenshots, images, and descriptions

| Option                       | Aliases                             | Purpose                                               |
| ---------------------------- | ----------------------------------- | ----------------------------------------------------- |
| `--screens <count>`          | `-screens`, `-s`                    | Set screenshot count.                                 |
| `--manual_frames <list>`     | `-manual_frames`, `-mf`             | Use comma-separated frame numbers.                    |
| `--comparison <paths>`       | `-comparison`, `-comps`             | Set one comparison folder or comma-separated folders. |
| `--comparison_index <index>` | `-comparison_index`, `-comps_index` | Select the primary comparison index.                  |
| `--menu-images <path>`       | `-menu-images`                      | Import manually captured disc-menu screenshots.       |
| `--get-dvd-menus`            | `-get-dvd-menus`                    | Capture distinct menus from extracted DVD `VIDEO_TS`. |
| `--imghost <name>`           | `-imghost`, `-ih`                   | Override image host.                                  |
| `--skip-imagehost-upload`    | `-skip-imagehost-upload`, `-siu`    | Skip automatic image-host uploads.                    |
| `--descfile <path>`          | `-descfile`, `-df`                  | Use a custom description file.                        |
| `--desclink <url>`           | `-desclink`, `-pb`                  | Use a custom description link.                        |

## Client and torrent

| Option                   | Aliases                            | Purpose                                                          |
| ------------------------ | ---------------------------------- | ---------------------------------------------------------------- |
| `--client <name>`        | `-client`                          | Override torrent client.                                         |
| `--qbit-tag <value>`     | `-qbit-tag`, `-qbt`                | Override qBittorrent tag.                                        |
| `--qbit-cat <value>`     | `-qbit-cat`, `-qbc`                | Override qBittorrent category.                                   |
| `--force-recheck`        | `-force-recheck`, `-frc`           | Force recheck of matched qBittorrent torrents before validation. |
| `--no-seed`              | `-no-seed`, `-ns`                  | Do not inject into torrent clients.                              |
| `--skip_auto_torrent`    | `-skip_auto_torrent`, `-sat`       | Skip automated torrent-client searching.                         |
| `--keep-folder`          | `-keep-folder`, `-kf`              | Keep a supplied folder instead of selecting its video file.      |
| `--onlyID`               | `-onlyID`                          | Only retrieve tracker metadata IDs.                              |
| `--infohash <hash>`      | `-infohash`, `-th`, `-torrenthash` | Override the v1 info hash.                                       |
| `--max-piece-size <MiB>` | `-max-piece-size`, `-mps`          | Set maximum torrent piece size in MiB.                           |
| `--nohash`               | `-nohash`, `-nh`                   | Reuse existing torrents only; do not generate a new torrent.     |
| `--rehash`               | `-rehash`, `-rh`                   | Force generation of a fresh torrent.                             |

## `serve`

```text
upbrr serve [options]
```

| Option                     | Purpose                                                  |
| -------------------------- | -------------------------------------------------------- |
| `--config <path>`          | Use a config file path.                                  |
| `--addr <host:port>`       | Set the complete listen address.                         |
| `--host <host>`            | Set the listen host.                                     |
| `--port <port>`            | Set the listen port.                                     |
| `--base-url <url-or-path>` | Set the external Web UI URL or path prefix.              |
| `--persist-listen`         | Persist listen host and port to `web-config.json`.       |
| `--persist-web-config`     | Persist supplied Web UI serve settings.                  |
| `--dev-no-auth`            | Disable Web auth for local development on loopback only. |

See [Web server and reverse proxy](../configuration/web-server.md) for precedence and proxy examples.

## `api-token`

Create persistent bearer tokens in [Settings → API Tokens](../web-ui/settings/api-tokens.md). The authenticated Web UI displays each plaintext token once so it can be copied directly into a secret manager; CLI output never includes tokens.

List safe token metadata:

```powershell
.\upbrr.exe api-token list
```

Revoke by token ID:

```powershell
.\upbrr.exe api-token revoke tok_example
```

List and revoke accept `--config`.

See the [API reference](../api/index.md) before granting `workflow:execute`.
