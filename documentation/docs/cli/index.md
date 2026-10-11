---
title: CLI reference
description: Commands, interaction modes, aliases, and upload preparation flags supported by upbrr.
---

# CLI reference

```text
upbrr [options] <input path>...
upbrr serve [options]
upbrr api-token <list|revoke> [options]
upbrr auth <password|browse-roots> [options]
upbrr live-test <init|cleanup> [options]
```

On Windows, examples use `upbrr.exe`. Put options before input paths for consistent behavior across commands. Use `--` to end option parsing when a path begins with a hyphen or could be mistaken for an option value.

Upload, `serve`, `auth`, and `api-token` options accept either one or two leading hyphens, including the aliases below. The exception is `--console-log-level`, which requires two hyphens for its full name; `-cll` and `--cll` also work. Boolean options can use `=true` or `=false`; use the equals sign instead of a separate value. The `-hc` language shorthand has additional forms described under [Subtitle review](#subtitle-review).

Omitted correction flags preserve saved values, and omitted configuration overrides use the active configuration. Workflow switches such as `--debug`, `--no-seed`, and `--unattended` are off unless enabled. Numeric and audio-selection defaults are noted below.

Use `--help` (also `-h`) as the exact option-name reference for your installed version:

```powershell
.\upbrr.exe --help
.\upbrr.exe serve --help
.\upbrr.exe api-token list --help
.\upbrr.exe auth password --help
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
.\upbrr.exe --site-check "D:\releases\Example.Release.2026.1080p-GRP"
```

Process at most five entries from a queue folder:

```powershell
.\upbrr.exe --queue uploads --limit-queue 5 "D:\upload-queue"
```

`--queue` supplies a non-empty queue name. Supply exactly one queue-root path separately. The CLI gathers first-level media files and release folders in sorted order, then applies `--limit-queue`; `0` means no limit. An item failure does not stop later queue entries, but the command returns a nonzero summary result if any item failed. Cancellation stops the queue.

### Multi-disc folders

Pass the collection parent, not each disc separately:

```powershell
.\upbrr.exe "D:\releases\Example BDMV Collection"
```

For BDMV, interactive preparation groups playlist choices by disc and requires at least one selection from every disc. `--unattended` stops if no complete stored or configured selection can be resolved; `--unattended_confirm` can show the required prompt.

`--manual_frames 240,480` requests both frames from every prepared disc. The selected parent remains one collection-root torrent containing all disc folders.

## Interaction and safety

The CLI shares one active-input slot with every process using its database. Queue entries run serially. If another session owns the input, the command returns a busy error; unattended mode never prompts or takes over a live input. After its terminal result, the CLI closes its owned input. Cancellation uses bounded cleanup; a reported cleanup failure requires recovery before further work.

After an interrupted legacy workflow, check the tracker or torrent client before answering the recovery confirmation. Confirm that the operation did not complete only when you have verified its outcome. Interactive mode and `--unattended_confirm` can ask this question; `--unattended` exits without prompting or starting a new submission. Recovery does not submit anything by itself.

After you answer tracker questions, the CLI rebuilds the affected tracker stages and refreshes duplicate evidence before asking for duplicate review. Follow-up questions can depend on earlier answers. Accepted answers, reviewed names, and rule acknowledgements remain valid only while their underlying inputs still match. Previous duplicate review decisions are requested again after evidence changes; explicit duplicate-policy options still apply. Answering tracker questions does not approve duplicates or authorize upload.

Trackers with a confirmed upload of the same verified submitted content are excluded before duplicate checks. If every selected tracker is already uploaded, the command succeeds without another approval prompt or submission. See [the upload workflow](../workflow/index.md#4-review-duplicate-evidence).

| Option                     | Behavior                                                                                                                                |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `--debug`                  | Runs end-to-end preparation and payload preview without tracker submission. Client injection remains enabled unless `--no-seed` is set. |
| `--log-level debug`        | Changes application logging verbosity for this run. It does not enable debug/non-submitting behavior.                                   |
| `--console-log-level info` | Changes terminal log verbosity for this run without changing file or retained application logs.                                         |
| `--unattended`             | Never prompts. Unsafe global ambiguity returns an error; tracker-specific manual prerequisites can block only that tracker.             |
| `--unattended_confirm`     | Uses unattended defaults but permits required confirmation or manual-input prompts.                                                     |
| `--no-seed`                | Disables torrent-client injection.                                                                                                      |

Interactive tracker questions with exactly `yes`/`no` choices also accept `y`/`n`, regardless of letter case. Other choices still require their listed values. Strict `--unattended` remains prompt-free.

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

`--create-auth`, `--export-config`, and `--import-config` are mutually exclusive. Config exports omit plaintext secrets unless `--export-config-plaintext` is supplied; protect plaintext exports as credentials. See [configuration import and export](../configuration/index.md#import-configuration) for config selection and encryption requirements.

## Execution

| Option                        | Aliases                       | Purpose                                                                                  |
| ----------------------------- | ----------------------------- | ---------------------------------------------------------------------------------------- |
| `--queue <name>`              | `-queue`                      | Name a queue; supply its root as the single input path.                                  |
| `--limit-queue <count>`       | `-limit-queue`, `-lq`         | Limit queued items processed; defaults to `0` (unlimited). Negative values are rejected. |
| `--site-check`                | `-site-check`, `-sc`          | Search/check sites without uploading.                                                    |
| `--site-upload <tracker>`     | `-site-upload`, `-su`         | Process one tracker upload flow.                                                         |
| `--debug`                     | `-debug`                      | Enable non-submitting debug mode.                                                        |
| `--log-level <level>`         | `-log-level`                  | Set application logging to `error`, `warn`, `info`, `debug`, or `trace` for this run.    |
| `--console-log-level <level>` | `-cll`                        | Set console logging to the same levels without changing application logs.                |
| `--upload-only`               | `-upload-only`                | Upload using prepared metadata cache only.                                               |
| `--input-only`                | `-input-only`                 | Stop after local Input preparation and readiness evaluation.                             |
| `--delete-tmp`                | `-delete-tmp`, `-dtmp`        | Delete stored content for each input before processing.                                  |
| `--unattended`                | `-unattended`, `-ua`          | Run without prompts.                                                                     |
| `--unattended_confirm`        | `-unattended_confirm`, `-uac` | Run unattended defaults with prompts allowed.                                            |

`--live-test` and `--live-test-max-images` are development options described under [`live-test`](#live-test). There is no `--dry-run` option; use `--debug --no-seed` for preparation without tracker submission or client injection.

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

Omitting `--trackers` uses the configured default trackers. `--site-upload` replaces the requested tracker list with its one tracker; `--trackers-remove` still excludes named trackers. Tracker ID flags supply lookup identities and do not select upload destinations.

## Release overrides

Explicit corrections take precedence over saved history and provider metadata. Omitting a correction flag preserves the saved value. Resetting a field removes its manual value and restores automatic detection. A boolean value such as `--commentary=false` is an explicit correction, not a reset.

Metadata review always prints the effective `Commentary: true` or `Commentary: false` before confirmation. Answer No and enter `--commentary=false` to disable automatic commentary detection, then review the refreshed value. These displays add no prompts to `--unattended`. See [editions and commentary](../trackers/index.md#editions-and-commentary) for manual corrections.

### Review Input without advancing the workflow

```powershell
.\upbrr.exe --input-only --skip_auto_torrent --trackers BLU,PTP "E:\Media\Example.Release.2026.1080p-GRP.mkv"
```

This loads release facts and evaluates selected tracker input requirements. It stops before tracker assessment, duplicate searches, screenshots, descriptions, torrent preparation, or uploads. Metadata provider requests and media inspection can still run. `--skip_auto_torrent` also disables torrent-client discovery.

Exit code `0` means Input is ready. Exit code `2` means required input prevents the requested readiness. Strict `--unattended` never prompts.

### Correct descriptive fields and languages

| Option                                            | Purpose                                                                                                |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `--title <text>`                                  | Override the resolved title.                                                                           |
| `--alternate-title <text>`                        | Override the alternate title.                                                                          |
| `--original-title <text>`                         | Override the original title.                                                                           |
| `--genres "Drama, Comedy"`                        | Supply one or more genres.                                                                             |
| `--audio-languages "English, Spanish"`            | Replace the release-level audio language list.                                                         |
| `--subtitle-languages "French"`                   | Replace the regular subtitle language list.                                                            |
| `--hardcoded-subs` / `-hc`                        | Enable hardcoded subtitles; `-hc Spanish` supplies a language; `--hardcoded-subs=false` disables them. |
| `--hardcoded-subtitle-languages "English"`        | Set hardcoded languages and enable them; an empty list disables them.                                  |
| `--track-languages "<track-id>=English, Spanish"` | Correct one previously inspected track. Repeat for distinct track IDs.                                 |
| `--source-lookup "<tracker-url>"`                 | Look up source metadata using a tracker URL.                                                           |
| `--reset-input <field>`                           | Remove one saved correction. Repeat for distinct fields.                                               |
| `--confirm-input <field>`                         | Confirm one stale content correction against the current required action.                              |
| `--tracker-input "<tracker-id>:<field>=<answer>"` | Answer a field shown by the current tracker questionnaire.                                             |

Language entries accept one or more comma-separated values. Blank segments are ignored, duplicate languages are removed, and multiword names remain intact. Use `--audio-languages=` for an explicit empty list. Original production language remains separate from track languages.

Run `--input-only` first to obtain track IDs. Track corrections require the retained scan manifest and one source. If the source, playlist, or track order changes, inspect the source again and select a current track. Release-level lists do not assign languages to individual streams.

Reset examples:

```powershell
.\upbrr.exe --input-only --reset-input metadata.audio_languages --reset-input release_name.tag "E:\Media\Example.Release.2026.1080p-GRP.mkv"
.\upbrr.exe --input-only --reset-input metadata.hardcoded_subs "E:\Media\Example.Release.2026.1080p-GRP.mkv"
```

Apply fact corrections before tracker answers in separate commands. Combining them is rejected. Changes to source identity can require confirmation of saved content fields. Track corrections always require current scan evidence.

Generated descriptions include manually supplied audio, subtitle, and hardcoded language lines. User-supplied descriptions pass through each tracker's normal formatting. Selected screenshots still appear in the tracker's usual location, including separate screenshot fields where applicable.

Saved corrections use a newer database format. Older binaries do not support writing this correction state.

### Subtitle review

Supply known hardcoded languages directly: `-hc English Forced`, `-hc "English Full"`, or `-hc Spanish`. Coverage is retained as `English (Forced)` or `English (Full)`. Explicit hardcoded languages enable hardcoded handling and supply known evidence to the workflow. `--hardcoded-subtitle-languages "English (Forced), Spanish"` also accepts a language list.

Custom names are allowed: use the attached form `-hc="Custom Dialect"` (quote multiword names). Unknown space-separated values remain source paths, so bare `-hc` never consumes a custom-named source folder. If a source name could be confused with a language, end the options explicitly, for example `-hc -- "Spanish"`. Put other options before `--`. For unlisted custom names, a trailing bare `Full` or `Forced` stays part of the name; use `(Full)`/`(Forced)` or `- Full`/`- Forced` to specify coverage.

Bare `-hc` or `-hc=""` explicitly marks unknown hardcoded languages, clearing saved language choices and leaving that evidence unresolved. Legacy boolean forms such as `-hc=true`, `-hc=false`, and `--hardcoded-subs=false` remain supported; explicit false disables hardcoded handling.

The review is tracker-specific. Strict `--unattended` never prompts and skips a tracker when its required review is unanswered; other eligible trackers remain available. `--unattended_confirm` permits the required questions. Existing global tracker-approval requirements still apply.

### Naming fields

Season tokens require exactly two or four digits, including year-numbered seasons such as `--season 2026` or `--season S2026`. Source names can use `S01E03` or `S2026E03` for an episode and `S01` or `S2026` for a season pack. One- and three-digit seasons and x-separated forms such as `1x05` are not recognized; use `S01E05` instead.

| Option                        | Aliases                                           | Purpose                                                                                     |
| ----------------------------- | ------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `--category <value>`          | `-category`, `-c`                                 | Override category.                                                                          |
| `--type <value>`              | `-type`, `-t`                                     | Override release type.                                                                      |
| `--source <value>`            | `-source`                                         | Override source.                                                                            |
| `--resolution <value>`        | `-resolution`, `-res`                             | Override resolution.                                                                        |
| `--tag <value>`               | `-tag`, `-g`                                      | Override group tag.                                                                         |
| `--service <value>`           | `-service`, `-serv`                               | Override streaming service.                                                                 |
| `--distributor <value>`       | `-distributor`, `-dist`                           | Override distributor.                                                                       |
| `--original-language <value>` | `-original-language`, `-ol`                       | Override original language.                                                                 |
| `--edition <value>`           | `-edition`, `-repack`                             | Override edition text, replacing automatic cut, presentation, and multi-edition set labels. |
| `--season <value>`            | `-season`                                         | Override one season token, such as `05` or `S05`.                                           |
| `--episode <value>`           | `-episode`                                        | Override one episode token, such as `5` or `E05`.                                           |
| `--episode-title <value>`     | `-episode-title`, `-manual-episode-title`, `-met` | Override episode title.                                                                     |
| `--manual-year <year>`        | `-manual-year`, `-year`                           | Override release year; `0` explicitly clears it.                                            |
| `--daily <YYYY-MM-DD>`        | `-daily`                                          | Set daily episode air date.                                                                 |
| `--use-season-episode`        | `-use-season-episode`                             | Prefer explicit or TMDB-matched season/episode naming over a daily date.                    |
| `--region <value>`            | `-region`, `-reg`                                 | Override disc region.                                                                       |
| `--no-season`                 | `-no-season`                                      | Remove season and episode from name.                                                        |
| `--no-year`                   | `-no-year`                                        | Remove year from name.                                                                      |
| `--no-aka`                    | `-no-aka`                                         | Remove AKA from name.                                                                       |
| `--no-tag`                    | `-no-tag`                                         | Remove group tag from name.                                                                 |
| `--no-episode-title`          | `-no-episode-title`, `-net`                       | Remove episode title from name.                                                             |
| `--no-distributor`            | `-no-distributor`, `-ndist`                       | Remove distributor.                                                                         |
| `--no-edition`                | `-no-edition`, `-ne`                              | Remove cut, edition, presentation, and multi-edition set labels from the name.              |
| `--no-dub`                    | `-no-dub`                                         | Remove dubbed tag from audio name.                                                          |
| `--no-dual`                   | `-no-dual`                                        | Remove dual-audio tag from audio name.                                                      |
| `--dual-audio`                | `-dual-audio`                                     | Add dual-audio tag to audio name.                                                           |

Filename cut, edition, and presentation labels take priority over provider runtime labels. If selected playlists identify two distinct editions, automatic naming uses only `2in1`, without listing the individual editions. Manual `--edition` and `--no-edition` choices take precedence over these automatic labels.

`-repack` remains an alias for `--edition`. It does not set the independent **Release version** correction. Set that correction in the Web UI; use `--reset-input release_name.repack` to restore automatic version detection from the CLI. The legacy `--no-edition` option also suppresses an automatic release version, but a saved explicit **Release version** selection takes precedence. See [Input corrections](../web-ui/index.md#correct-input-facts).

If `--use-season-episode` has neither an explicit season/episode nor a matching TMDB episode, an existing daily date is retained with a warning. `--use-season-episode=false` selects daily-date naming when a date is available.

## Metadata IDs

| Option          | Aliases   | Purpose             |
| --------------- | --------- | ------------------- |
| `--tmdb <id>`   | `-tmdb`   | Override TMDB ID.   |
| `--imdb <id>`   | `-imdb`   | Override IMDb ID.   |
| `--mal <id>`    | `-mal`    | Override MAL ID.    |
| `--tvdb <id>`   | `-tvdb`   | Override TVDB ID.   |
| `--tvmaze <id>` | `-tvmaze` | Override TVmaze ID. |

TMDB also accepts `movie/<id>`, `tv/<id>`, or a URL ending in the ID; a movie/TV hint overrides the category. IMDb accepts numeric IDs or the `tt` prefix. TVDB, TVmaze, and MAL take numeric IDs, not URLs.

During the **Metadata correct?** loop, correcting the same active input reuses provider lookups for unchanged inputs, including lookups that returned no result. A later run can retry those empty results. Completed fetch failures remain retained until the input is removed from **History**. Changing the source, provider ID, or lookup query requests its own result.

### Clear a metadata provider

Pass an empty value or `0` to stop using a provider for this release. This works with `--tmdb`, `--imdb`, `--tvdb`, `--tvmaze`, and `--mal`.

For example, clear TMDB and use a known IMDb ID:

```powershell
.\upbrr.exe --tmdb= --imdb tt1234567 "E:\Media\Example.Release.2026.1080p-GRP.mkv"
```

Use the equals sign in `--tmdb=` to pass an empty value reliably in PowerShell. `--tmdb=0` has the same effect. You can clear several providers in one command.

Clearing removes that provider's ID and metadata from the prepared release. It also prevents automatic rediscovery through title searches, tracker data, or other providers. Other providers can still supply metadata.

The clear is saved for that source path. Omitting the flag on a later run preserves the clear. Supply a positive ID to use that provider again, such as `--tmdb 123456`. Provider settings for other releases stay unchanged.

Trackers that require the cleared provider can remain blocked. Continue with trackers whose metadata requirements are satisfied, or supply a correct ID before retrying. See [metadata troubleshooting](../troubleshooting/index.md#a-metadata-provider-fails-or-selects-the-wrong-title).

## Tracker overrides

| Option                | Aliases                      | Purpose                                                                 |
| --------------------- | ---------------------------- | ----------------------------------------------------------------------- |
| `--skip-dupe-check`   | `-skip-dupe-check`, `-sdc`   | Skip remote tracker duplicate searches.                                 |
| `--skip-dupe-asking`  | `-skip-dupe-asking`, `-sda`  | Choose upload when duplicate evidence needs a decision.                 |
| `--double-dupe-check` | `-double-dupe-check`, `-ddc` | Request two duplicate checks instead of one.                            |
| `--commentary`        | `-commentary`, `-mc`         | Mark release as containing commentary.                                  |
| `--personalrelease`   | `-personalrelease`, `-pr`    | Explicitly set personal-release handling.                               |
| `--stream`            | `-stream`, `-st`             | Mark release as stream optimized.                                       |
| `--webdv`             | `-webdv`                     | Mark release as WEB-DV.                                                 |
| `--not-anime`         | `-not-anime`                 | Force release to be treated as not anime.                               |
| `--anime`             | `-anime`                     | Explicitly set anime handling; `false` clears the anime classification. |
| `--anon`              | `-anon`, `-a`                | Upload anonymously.                                                     |
| `--draft`             | `-draft`, `-dr`              | Send to drafts where supported.                                         |
| `--modq`              | `-modq`, `-mq`               | Opt into mod queue where supported.                                     |

`--personalrelease=true` and `--personalrelease=false` are both explicit choices and override tracker group defaults. Omit the option to leave **Personal Release** on **Auto**, where each tracker's configured personal-release groups can supply the default.

Duplicate options do not bypass authoritative client blocks or the workflow's explicit tracker-approval requirement. Strict `--unattended` still cannot answer that approval prompt. `--anime` and `--not-anime` cannot be combined, even with explicit false values.

TIK `--disctype` accepts `BD100`, `BD66`, `BD50`, `BD25`, `NTSC DVD9`, `NTSC DVD5`, `PAL DVD9`, `PAL DVD5`, `CUSTOM`, or `3D`. Quote values containing spaces.

## Screenshots, images, and descriptions

Without `--screens`, the CLI uses `screenshot_handling.screens` when selected trackers require screenshots. Tracker-specific image counts and limits still apply. An override below a tracker's screenshot requirements can block that upload.

| Option                       | Aliases                             | Purpose                                                                        |
| ---------------------------- | ----------------------------------- | ------------------------------------------------------------------------------ |
| `--screens <count>`          | `-screens`, `-s`                    | Override the configured screenshot count.                                      |
| `--manual_frames <list>`     | `-manual_frames`, `-mf`             | Use comma-separated positive frame numbers.                                    |
| `--comparison <paths>`       | `-comparison`, `-comps`             | Set one comparison folder or comma-separated folders.                          |
| `--comparison_index <index>` | `-comparison_index`, `-comps_index` | Select a one-based primary comparison path; omission keeps the supplied order. |
| `--menu-images <path>`       | `-menu-images`                      | Import manually captured disc-menu screenshots.                                |
| `--get-dvd-menus`            | `-get-dvd-menus`                    | Capture distinct menus from extracted DVD `VIDEO_TS`.                          |
| `--imghost <name>`           | `-imghost`, `-ih`                   | Override a supported, non-tracker-owned image host.                            |
| `--skip-imagehost-upload`    | `-skip-imagehost-upload`, `-siu`    | Skip automatic image-host uploads.                                             |
| `--descfile <path>`          | `-descfile`, `-df`                  | Use a custom description file.                                                 |
| `--desclink <url>`           | `-desclink`, `-pb`                  | Use a custom description link.                                                 |

### HDR analysis options

| Option                      | Purpose                                                                               |
| --------------------------- | ------------------------------------------------------------------------------------- |
| `--hdr-analysis`            | Generate HDR10+ plots during upload.                                                  |
| `--hdr-analysis-only`       | Analyze one MKV or Blu-ray content root without configuration or upload.              |
| `--hdr-targets <id,id>`     | Select prepared opaque target IDs in source order for upload.                         |
| `--hdr-playlist <number>`   | Select a numeric MPLS basename in standalone disc mode.                               |
| `--hdr-peak-source <value>` | Choose `histogram` (default), `histogram99`, `max-scl`, or `max-scl-luminance`.       |
| `--hdr-output-dir <path>`   | Required output parent for standalone mode; each run creates a fresh child directory. |

Every selected target must complete for requested upload analysis. See [HDR analysis](../workflow/hdr-analysis.md) for supported inputs, local examples, retained metadata, and recovery.

### Audio analysis

Use `--audio-analysis` to analyze prepared audio tracks during the upload workflow, or `--audio-analysis-only` to analyze one file without configuration or upload.

| Option                   | Purpose                                                                         |
| ------------------------ | ------------------------------------------------------------------------------- |
| `--audio-analysis`       | Generate local images during upload.                                            |
| `--audio-analysis-only`  | Analyze one media file without configuration or upload.                         |
| `--audio-output <path>`  | Required output directory for `--audio-analysis-only`.                          |
| `--audio-tracks <value>` | Select `primary` (default), `all`, or comma-separated one-based audio ordinals. |
| `--audio-images <value>` | Generate `both`, `waveform`, or `spectrogram` images. Defaults to `both`.       |

Generate waveform and spectrogram images for the prepared primary audio track:

```powershell
.\upbrr.exe --audio-analysis "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

Generate only spectrograms for the first and third audio tracks:

```powershell
.\upbrr.exe --audio-analysis --audio-tracks 1,3 --audio-images spectrogram "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

Analyze only audio and save it outside managed temporary storage:

```powershell
.\upbrr.exe --audio-analysis-only --audio-output "D:\reports\audio" "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

The numeric selectors are audio-only ordinals, not container-wide stream indexes. Repeated ordinals are ignored, and results retain source track order. `--audio-tracks` and `--audio-images` require either analysis mode.

`--audio-output` is only valid with `--audio-analysis-only`. Standalone analysis requires exactly one regular media file and accepts only its four audio-analysis options; do not combine it with `--config`, upload options, or `--audio-analysis`.

During upload, the CLI prints the path of every successfully retained PNG and statistics file. Standalone analysis prints paths to PNGs and statistics files in a new directory under `--audio-output`. A partial or failed analysis exits nonzero, even when some artifacts succeeded. See [Audio analysis](../workflow/audio-analysis.md) for output, retry, and retention behavior.

## Client and torrent

| Option                   | Aliases                            | Purpose                                                                                  |
| ------------------------ | ---------------------------------- | ---------------------------------------------------------------------------------------- |
| `--client <name>`        | `-client`                          | Override torrent client.                                                                 |
| `--qbit-tag <value>`     | `-qbit-tag`, `-qbt`                | Override qBittorrent tag.                                                                |
| `--qbit-cat <value>`     | `-qbit-cat`, `-qbc`                | Override qBittorrent category.                                                           |
| `--force-recheck`        | `-force-recheck`, `-frc`           | Force recheck of matched qBittorrent torrents before validation.                         |
| `--no-seed`              | `-no-seed`, `-ns`                  | Do not inject into torrent clients.                                                      |
| `--skip_auto_torrent`    | `-skip_auto_torrent`, `-sat`       | Skip automated torrent-client searching.                                                 |
| `--keep-folder`          | `-keep-folder`, `-kf`              | Keep a supplied folder instead of selecting its video file.                              |
| `--onlyID`               | `-onlyID`                          | Limit tracker metadata lookup to IDs where supported; does not stop the upload workflow. |
| `--infohash <hash>`      | `-infohash`, `-th`, `-torrenthash` | Supply a 40-character hexadecimal v1 info hash.                                          |
| `--max-piece-size <MiB>` | `-max-piece-size`, `-mps`          | Override the configured maximum: `1`, `2`, `4`, `8`, `16`, `32`, `64`, or `128` MiB.     |
| `--nohash`               | `-nohash`, `-nh`                   | Reuse existing torrents only; do not generate a new torrent.                             |
| `--rehash`               | `-rehash`, `-rh`                   | Force generation of a fresh torrent.                                                     |

`--nohash` and `--rehash` cannot be combined, even with explicit false values.

## `serve`

```text
upbrr serve [options]
```

`--config` seeds an empty database. For an existing database, the server uses its stored settings. Use Settings or configuration import to activate later changes.

| Option                     | Purpose                                                                    |
| -------------------------- | -------------------------------------------------------------------------- |
| `--config <path>`          | Use a config file path.                                                    |
| `--addr <host:port>`       | Set the complete listen address.                                           |
| `--host <host>`            | Set the listen host.                                                       |
| `--port <port>`            | Set the decimal listen port, from `1` through `65535`.                     |
| `--base-url <url-or-path>` | Set the external Web UI URL or path prefix.                                |
| `--persist-listen`         | Persist listen host and port to `web-config.json`.                         |
| `--persist-web-config`     | Persist the full resolved Web UI settings, including listen host and port. |
| `--dev-no-auth`            | Disable Web auth for local development on loopback only.                   |

`--addr` cannot be combined with `--host` or `--port`. Without overrides, the listener defaults to `localhost:7480`; environment variables and saved settings can change it. `--live-test` and `--live-test-max-images` also apply to `serve`. See [Web server and reverse proxy](../configuration/web-server.md) for precedence and proxy examples.

## `auth`

Password changes and browse-policy changes after initial setup are available only through the local binary. The first authenticated Web UI setup may establish the initial browse policy. Stop `upbrr serve` before running either command, then restart it when the command completes.

After validation, each command creates a unique `web-auth.json.backup-*` beside the active auth file and prints its exact path. The path is still printed if a later update step fails. Backups contain sensitive authentication and encryption material; protect them like `web-auth.json` and remove obsolete copies manually.

Change the Web UI password interactively:

```powershell
.\upbrr.exe auth password
```

The command prompts for the current password, the replacement, and confirmation without accepting password flags. Retained browser sessions are revoked immediately after the current password is verified. If a later update step fails, sign in again with the existing password before retrying.

Replace all browse roots by passing each existing directory as a separate argument:

```powershell
.\upbrr.exe auth browse-roots "D:\Media" "E:\Downloads"
```

To remove the roots and explicitly allow unrestricted host browsing:

```powershell
.\upbrr.exe auth browse-roots --allow-unrestricted
```

Both subcommands accept `--config` when the active database is selected through a non-default config file. For containers, run the command in the same environment as upbrr and use paths visible inside the container.

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

## `live-test`

These development commands create an isolated profile from existing configuration and authentication state, or clean up image uploads recorded by that profile. They do not replace normal upload commands.

```text
upbrr live-test init --run-dir <path> [--config <path>] [--prefer-deletable-hosts]
upbrr live-test cleanup --run-dir <path>
```

Use double-hyphen option names for these subcommands. `init` requires a new immediate child of the operating system's private cache directory under `upbrr-live-testing/runs`; `cleanup` requires an existing run. `--config` selects the source configuration. `--prefer-deletable-hosts` defaults to false and changes image-host preferences only inside the new profile. The profile includes sensitive configuration and authentication material; keep it private.

To use the profile with an upload command or `serve`, pass `--live-test --config <profile-config-path>`. Tracker submission and torrent-client writes are disabled. `--live-test-max-images <count>` requires `--live-test`, accepts `0` through `500`, and defaults to `0`, which keeps captured images local. A positive value permits that many journaled image-upload attempts. The budget cannot change after the run starts.

Upload-mode `--live-test` cannot be combined with `--create-auth`, `--export-config`, `--import-config`, `--cleanup`, or `--delete-tmp`. Use the dedicated `live-test init` and `live-test cleanup` commands for profile management.

`cleanup` attempts deletion only for journaled images owned by the run and reports uploads that must remain on their host. Unknown or failed outcomes require manual reconciliation. Once cleanup starts, the profile cannot be used for further preparation. For the opt-in runner and safety requirements, see the [live-testing guide](https://github.com/autobrr/upbrr/blob/main/scripts/live-testing/README.md).
