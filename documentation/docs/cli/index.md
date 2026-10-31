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

Create or resume a named queue, processing at most five pending entries:

```powershell
.\upbrr.exe --queue uploads --limit-queue 5 "D:\upload-queue"
```

Supply a non-empty queue name and exactly one queue-root path. The first invocation saves the sorted first-level media files and release folders. Reusing that name with the same root loads its saved membership and progress. `--limit-queue` applies after filtering progress; `0` means unlimited, so repeated limited runs advance through the queue. An item failure does not stop later entries, but the command returns a nonzero summary result if any item failed. Cancellation stops the queue.

The CLI prints the saved file's location on creation and resume. Files live in `queues/<name-hash>.json` beside the active `db.sqlite` (including a custom database location). The hash is the SHA-256 of the exact, trimmed queue name; names have independent progress even when their filesystem-safe spellings would collide. The private JSON contains the name, root, member paths, attempt counts, workflow IDs and per-item progress. It contains no tracker credentials or submission payloads. Protect these local paths when sharing the file. An OS lock excludes concurrent writers and releases on process exit; keep its adjacent `.lock` file in place.

Each item is saved before processing and after its outcome. The checkpoint's temporary file is flushed before replacement. Unix also flushes the queue directory and its ancestors before queue work, then the containing directory after each replacement. Windows flushes the writable destination after replacement; Microsoft documents directory-structure persistence for this operation on NTFS, without an equivalent guarantee for other Windows filesystems. These barriers depend on the filesystem and storage honoring flush requests; they do not establish unconditional power-loss or exactly-once tracker submission guarantees.

A checkpoint write or sync failure stops the queue and reports the item as failed. If replacement succeeded before a sync error, the saved file may already contain the new progress. The command reports durability as unconfirmed, retains the file and does not automatically repeat the write or work. Reopen the named queue to load and flush its retained state before deciding whether to retry an interrupted item. Do not assume that an error means no checkpoint was written.

States are `pending`, `running`, `completed`, `failed`, `interrupted` and `skipped`. The TUI reports skipped items without replacing more specific recovery messages. Failed or partial debug/site-check reports remain retryable failures. A leftover `running` checkpoint becomes `interrupted` when you explicitly resume the queue. Normal resume selects pending items only. To include failed, interrupted and skipped items, inspect their outcomes and run:

```powershell
.\upbrr.exe --queue uploads --retry-queue --limit-queue 5 "D:\upload-queue"
```

Retry selection does not bypass input ownership, confirmed-upload exclusions or uncertain-submission reconciliation. A tracker submission with an unknown outcome still requires the existing recovery process; no remote submission is retried automatically. Confirmed tracker uploads are recorded separately from item progress, including partial failures. `completed` means the requested processing finished: Input-only and debug/site-check runs can complete without uploading. Skips and declined actions do not prove an upload. Use a different queue name when starting a different task, such as switching a completed Input-only queue to uploads.

Membership stays unchanged until explicitly refreshed:

```powershell
.\upbrr.exe --queue uploads --refresh-queue "D:\upload-queue"
```

Refresh adds newly discovered members and preserves existing progress, including missing sources and failed items. It does not reset completed items, remove old members, or change the saved root. Missing members fail when processed while other entries continue. A queue with no remaining work reports completion; retained failed, interrupted or skipped items remain inspectable and require `--retry-queue`. Queue processing never starts automatically when the application or Web UI starts. Saved queues cannot be combined with `--cleanup` or `--delete-tmp`; perform stored-release maintenance separately, preserving any unresolved remote effects.

### Multi-disc folders

Pass the collection parent, not each disc separately:

```powershell
.\upbrr.exe "D:\releases\Example BDMV Collection"
```

For BDMV, interactive preparation groups playlist choices by disc and requires at least one selection from every disc. `--unattended` stops if no complete stored or configured selection can be resolved; `--unattended_confirm` can show the required prompt.

`--manual_frames 240,480` requests both frames from every prepared disc. The selected parent remains one collection-root torrent containing all disc folders.

### Decrypted 3D Blu-ray ISO input

Select the original `.iso` file in the WebUI file browser, or pass it directly to the CLI:

```powershell
.\upbrr.exe "D:\releases\Example.Movie.2026.3D.iso"
```

Choose a playlist through the normal Blu-ray selector. A remembered choice is reused only for the same source fingerprint; a changed image requires a fresh selection. Preparation uses BDInfo and requires MVC evidence in every selected playlist. A filename or manual 3D correction cannot make an unsupported image eligible. MediaInfo is not required for this ISO workflow. If the filename has no resolution, naming uses the primary selected report's video resolution; explicit resolution corrections still apply.

Screenshots use FFmpeg's `bluray:` input and the explicitly selected playlist. Times refer to the complete playlist, including multiple clips. The images are ordinary single-view AVC base-view frames, not stereoscopic reconstructions; the base view is not necessarily the left eye. FFmpeg must provide the libbluray input protocol. The reported FFmpeg information includes `libbluray: available`, `unavailable`, or `unknown`; availability alone does not prove that a particular image can be decoded.

The torrent contains only the original ISO, with its physical size and source path preserved. No mounting or extraction is required. DVD ISOs, 2D Blu-ray ISOs, encrypted images, and generic archive browsing are not supported by this workflow. Existing extracted-disc preparation remains available.

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

With `--ui=auto`, a terminal-dashboard failure before work starts falls back to plain output. Explicit `--ui=tui` startup failures stop the command. User cancellation or interruption never starts a fallback run.

The terminal log view bounds each line to 4,096 bytes. Oversized raw diagnostic lines are replaced with `[oversized diagnostic omitted]` rather than showing a partial structured value that cannot be safely redacted. Already-sanitized application log messages retain a bounded prefix with `[truncated]`. File and retained application logs are not shortened by this display limit. Inspect logs and remove any remaining credentials, cookies, tokens, private URLs and unrelated personal information before sharing them.

:::danger Destructive maintenance

`--cleanup` deletes all stored release content from the active database. `--delete-tmp` deletes stored database content for each supplied input before processing it. Back up state and verify the active config/database before using either option.

:::

## Config and application

| Option                      | Aliases                    | Purpose                                                                         |
| --------------------------- | -------------------------- | ------------------------------------------------------------------------------- |
| `--config <path>`           | `-config`                  | Use a config file path.                                                         |
| `--export-config <path>`    | `-export-config`           | Export SQLite config, or built-in defaults when unconfigured, to YAML and exit. |
| `--export-config-plaintext` | `-export-config-plaintext` | Include plaintext secrets; requires `--export-config`.                          |
| `--import-config <path>`    | `-import-config`           | Import `.py`, `.yaml`, `.yml`, or `.json` config and exit.                      |
| `--create-auth`             | `-create-auth`             | Create `web-auth.json` beside the active database and exit.                     |
| `--version`                 | `-version`                 | Print version and exit.                                                         |
| `--cleanup`                 | `-cleanup`                 | Delete all stored release content and exit.                                     |

`--create-auth`, `--export-config`, and `--import-config` are mutually exclusive. Config exports omit plaintext secrets unless `--export-config-plaintext` is supplied; protect plaintext exports as credentials. See [configuration import and export](../configuration/index.md#import-configuration) for config selection and encryption requirements.

## Execution

| Option                        | Aliases                       | Purpose                                                                                    |
| ----------------------------- | ----------------------------- | ------------------------------------------------------------------------------------------ |
| `--queue <name>`              | `-queue`                      | Create or resume a saved queue; supply its original root as the single input path.         |
| `--limit-queue <count>`       | `-limit-queue`, `-lq`         | Limit remaining eligible items; defaults to `0` (unlimited). Negative values are rejected. |
| `--refresh-queue`             | `-refresh-queue`              | Add new members without resetting progress; requires `--queue`.                            |
| `--retry-queue`               | `-retry-queue`                | Include failed, interrupted and skipped items; requires `--queue`.                         |
| `--site-check`                | `-site-check`, `-sc`          | Search/check sites without uploading.                                                      |
| `--site-upload <tracker>`     | `-site-upload`, `-su`         | Process one tracker upload flow.                                                           |
| `--debug`                     | `-debug`                      | Enable non-submitting debug mode.                                                          |
| `--log-level <level>`         | `-log-level`                  | Set application logging to `error`, `warn`, `info`, `debug`, or `trace` for this run.      |
| `--console-log-level <level>` | `-cll`                        | Set console logging to the same levels without changing application logs.                  |
| `--upload-only`               | `-upload-only`                | Upload using prepared metadata cache only.                                                 |
| `--input-only`                | `-input-only`                 | Stop after local Input preparation and readiness evaluation.                               |
| `--delete-tmp`                | `-delete-tmp`, `-dtmp`        | Delete stored content for each input before processing.                                    |
| `--unattended`                | `-unattended`, `-ua`          | Run without prompts.                                                                       |
| `--unattended_confirm`        | `-unattended_confirm`, `-uac` | Run unattended defaults with prompts allowed.                                              |

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

Title and original title are read-only provider values. Use `--alternate-title` to correct the alternate title.

| Option                                            | Purpose                                                                                                |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `--alternate-title <text>`                        | Override the alternate title.                                                                          |
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

The review is tracker-specific. Strict `--unattended` never prompts and skips a tracker when its required review is unanswered; other eligible trackers remain available. `--unattended_confirm` permits the required questions. The workflow still records the approved tracker subset before downstream work.

### Naming fields

Season tokens require exactly two or four digits, including year-numbered seasons such as `--season 2026` or `--season S2026`. Source names can use `S01E03` or `S2026E03` for an episode and `S01` or `S2026` for a season pack. One- and three-digit seasons and x-separated forms such as `1x05` are not recognized; use `S01E05` instead.

Specials can use `S00E01` in a source name or `--season S00 --episode 01`. An explicit season zero is distinct from an unknown season; episode zero is invalid. Use `--season=` to clear the season or `--reset-input release_name.season` to restore automatic detection. Tracker eligibility still determines whether a special or a pack can be uploaded. Changing the episode coordinates does not remove known multi-episode source membership.

Existing prepared releases are rebuilt after this update. Older binaries do not support saved season-zero corrections.

| Option                        | Aliases                                           | Purpose                                                                                               |
| ----------------------------- | ------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `--category <value>`          | `-category`, `-c`                                 | Override category.                                                                                    |
| `--type <value>`              | `-type`, `-t`                                     | Override release type.                                                                                |
| `--source <value>`            | `-source`                                         | Override source.                                                                                      |
| `--resolution <value>`        | `-resolution`, `-res`                             | Override resolution.                                                                                  |
| `--tag <value>`               | `-tag`, `-g`                                      | Override group tag.                                                                                   |
| `--service <value>`           | `-service`, `-serv`                               | Override streaming service.                                                                           |
| `--distributor <value>`       | `-distributor`, `-dist`                           | Override distributor.                                                                                 |
| `--original-language <value>` | `-original-language`, `-ol`                       | Override original language.                                                                           |
| `--edition <value>`           | `-edition`, `-repack`                             | Override cut/edition text and multi-edition set labels; independent presentation and features remain. |
| `--presentation <value>`      | `-presentation`                                   | Set `IMAX`, `Open Matte`, or `IMAX Open Matte`; `--presentation=` clears it.                          |
| `--release-features <list>`   | `-release-features`                               | Replace release features using comma-separated IDs; `--release-features=` clears them.                |
| `--season <value>`            | `-season`                                         | Override one season token, such as `05` or `S05`.                                                     |
| `--episode <value>`           | `-episode`                                        | Override one episode token, such as `5` or `E05`.                                                     |
| `--episode-title <value>`     | `-episode-title`, `-manual-episode-title`, `-met` | Override episode title.                                                                               |
| `--manual-year <year>`        | `-manual-year`, `-year`                           | Override release year; `0` explicitly clears it.                                                      |
| `--daily <YYYY-MM-DD>`        | `-daily`                                          | Set daily episode air date.                                                                           |
| `--use-season-episode`        | `-use-season-episode`                             | Prefer explicit or TMDB-matched season/episode naming over a daily date.                              |
| `--region <value>`            | `-region`, `-reg`                                 | Override disc region.                                                                                 |
| `--no-season`                 | `-no-season`                                      | Remove season and episode from name.                                                                  |
| `--no-year`                   | `-no-year`                                        | Remove year from name.                                                                                |
| `--no-aka`                    | `-no-aka`                                         | Remove AKA from name.                                                                                 |
| `--no-tag`                    | `-no-tag`                                         | Remove group tag from name.                                                                           |
| `--no-episode-title`          | `-no-episode-title`, `-net`                       | Remove episode title from name.                                                                       |
| `--no-distributor`            | `-no-distributor`, `-ndist`                       | Remove distributor.                                                                                   |
| `--no-edition`                | `-no-edition`, `-ne`                              | Remove cut, edition, presentation, and multi-edition set labels from the name.                        |
| `--no-dub`                    | `-no-dub`                                         | Remove dubbed tag from audio name.                                                                    |
| `--no-dual`                   | `-no-dual`                                        | Remove dual-audio tag from audio name.                                                                |
| `--dual-audio`                | `-dual-audio`                                     | Add dual-audio tag to audio name.                                                                     |

Filename cut, edition, and presentation labels take priority over provider runtime labels. If selected playlists identify two distinct editions, automatic naming uses only `2in1`, without listing the individual editions. Manual `--edition` and `--no-edition` choices take precedence over these automatic labels.

Presentation and release features are independent of Edition. For example, `--edition "Director's Cut" --presentation "Open Matte" --release-features hybrid,4k_restoration` retains all three meanings. Supported feature IDs are `hybrid`, `restoration`, `remaster`, `two_disc_set`, `4k_restoration`, `4k_remaster`, `extras`, `2d_3d_edition`, `3d_anaglyph`, `3d_full_sbs`, `3d_half_ou`, and `3d_half_sbs`. These are release annotations; selecting a 3D annotation does not change measured 3D media facts.

The independent `--3d` control sets effective 3D; `--3d=false` clears it. Auto and explicit `--3d` preserve detected or manually selected stereoscopic subtype annotations, including with `--no-edition`. Auto does not turn an annotation into a measured 3D fact. An explicit release-features selection, including an empty value, still takes precedence. Use `--reset-input release_name.three_d` to restore detected evidence. This does not edit Edition or release-version corrections. Clear also suppresses effective stereoscopic subtype annotations, while their saved feature selections remain available when 3D returns to Auto. Recognized subtype annotations contribute only one literal `3D` marker immediately before Source, after Resolution and any Country. Generated names never include Full/Half SBS or OU wording; the underlying subtype facts remain available for tracker payloads. Other release features retain their existing placement.

Omit a flag to retain a saved correction. Use `--reset-input release_name.presentation` or `--reset-input release_name.release_features` to return that control to automatic detection. An explicit empty value clears only that control. Legacy `--no-edition` suppresses automatic edition-related labels, but explicit presentation/features selections take precedence.

`-repack` remains an alias for `--edition`. It does not set the independent **Release version** correction. Set that correction in the Web UI; use `--reset-input release_name.repack` to restore automatic version detection from the CLI. The legacy `--no-edition` option also suppresses an automatic release version, but a saved explicit **Release version** selection takes precedence. See [Input corrections](../web-ui/index.md#correct-input-facts).

If `--use-season-episode` has neither an explicit season/episode nor a matching TMDB episode, an existing daily date is retained with a warning. `--use-season-episode=false` selects daily-date naming when a date is available.

### Required release facts

Category, Type, Source, and Resolution must be present in the saved prepared metadata before upload. The release preview labels the filesystem location **Path**, then shows Type, Resolution, and Source above Upload name. A recognizable filename or provider summary does not replace a missing saved fact.

If any are missing, **Missing fields** replaces the metadata confirmation and lists only the correction arguments you need: `--category <value>`, `--type <value>`, `--source <value>`, or `--resolution <value>`. Enter the listed corrections to prepare the input again. The normal **Metadata correct?** confirmation returns when all four saved facts are present; answering yes or entering `continue` cannot bypass missing facts.

Strict `--unattended` reports the missing facts and exits without prompting. Interactive mode and `--unattended_confirm` (`--uac`) allow corrections. `--input-only` still prepares incomplete input for inspection and reports its readiness without uploading.

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
| `--skip-dupe-asking`  | `-skip-dupe-asking`, `-sda`  | Skip trackers with duplicate evidence without asking.                   |
| `--double-dupe-check` | `-double-dupe-check`, `-ddc` | Request two duplicate checks instead of one.                            |
| `--commentary`        | `-commentary`, `-mc`         | Mark release as containing commentary.                                  |
| `--personalrelease`   | `-personalrelease`, `-pr`    | Explicitly set personal-release handling.                               |
| `--stream`            | `-stream`, `-st`             | Mark release as stream optimized.                                       |
| `--webdv`             | `-webdv`                     | Mark release as WEB-DV.                                                 |
| `--anime`             | `-anime`                     | Explicitly set anime handling; `false` clears the anime classification. |
| `--anon`              | `-anon`, `-a`                | Upload anonymously.                                                     |
| `--draft`             | `-draft`, `-dr`              | Send to drafts where supported.                                         |
| `--modq`              | `-modq`, `-mq`               | Opt into mod queue where supported.                                     |

`--personalrelease=true` and `--personalrelease=false` are both explicit choices and override tracker group defaults. Omit the option to leave **Personal Release** on **Auto**, where each tracker's configured personal-release groups can supply the default.

After duplicate checks, trackers with completed clear results default to **Yes** (`[Y/n]`). Strict `--unattended` applies this default without prompting and records only that exact eligible subset for preparation and upload. Duplicate results and separately overridden duplicate evidence default to **No**; other viable trackers can continue. Missing prerequisites, incomplete or failed checks, rule waivers and staff-token requirements never become automatic Yes decisions.

`--skip-dupe-asking` skips trackers with duplicate evidence; it does not authorize duplicate uploads. Separate explicit per-tracker duplicate overrides take precedence over this skip policy, but still require tracker authorization and any staff-token confirmation. Duplicate options never bypass authoritative client blocks. Use `--anime=false` to explicitly classify a release as non-anime. The obsolete `--not-anime` flag has been removed and is rejected as an unknown option.

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

| Option                         | Purpose                                                                                                                  |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `--hdr-analysis`               | Generate native HDR plots and primary-audio analysis before upload.                                                      |
| `--hdr-analysis-only`          | Analyze one supported HDR file or Blu-ray content root without configuration or upload.                                  |
| `--hdr-targets <id,id>`        | Select prepared opaque target IDs in source order for upload.                                                            |
| `--hdr-playlist <number>`      | Select a numeric MPLS basename in standalone disc mode.                                                                  |
| `--hdr-peak-source <value>`    | Choose `histogram` (default), `histogram99`, `max-scl`, or `max-scl-luminance`.                                          |
| `--hdr-output-dir <path>`      | Required output parent for standalone mode; each run creates a fresh child directory.                                    |
| `--hdr-formats <value>`        | Select `auto` (default), `hdr10plus`, `dolby-vision`, or `both`.                                                         |
| `--hdr-metadata-only`          | Generate native plots without automatic audio or picture measurement.                                                    |
| `--hdr-audio-only`             | Explicitly request native plots and primary audio without pictures, the default mode.                                    |
| `--hdr-pictures`               | Add sampled automatic picture checks to native metadata and audio.                                                       |
| `--hdr-full-frames`            | Select full-frame automatic picture checks instead of sampling; slower.                                                  |
| `--hdr-boundary-sync`          | Decode bounded windows at selected HDR10+ and Dolby Vision scene boundaries.                                             |
| `--hdr-boundary-audio=false`   | Disable audio in bounded mode; audio is otherwise enabled there by default.                                              |
| `--hdr-all-audio-tracks`       | Analyze all prepared audio tracks instead of the primary track; maximum 64. Requires an audio-enabled mode.              |
| `--hdr-decoder-threads <n>`    | Video threads: `0` uses the mode default; `1`–`64` requests a maximum subject to resource limits. Requires picture work. |
| `--hdr-scene-sync`             | Select the individual decoded scene check against Dolby Vision markers.                                                  |
| `--hdr-active-area`            | Check decoded picture borders against Dolby Vision active areas.                                                         |
| `--hdr-compare-source <path>`  | Explicit local reference source B for grade or colour checks.                                                            |
| `--hdr-compare-offset <n>`     | Confirm the frame mapping `B = A + n`; explicit zero is required for aligned sources.                                    |
| `--hdr-compare-crop <l,t,w,h>` | Common coded-pixel crop for both sources; positive even width and height must fit both.                                  |
| `--hdr-grade`                  | Compare brightness and PQ values against the selected reference.                                                         |
| `--hdr-colour`                 | Compare colour distributions against the selected reference.                                                             |

For requested upload analysis, every selected target needs usable native metadata or, when audio was requested, usable audio output. Partial results can qualify; canceled and interrupted attempts cannot. A comparison requires source B, an explicit offset, a crop, and at least one of `--hdr-grade` or `--hdr-colour`. Bounded mode accepts audio and active-area checks, but cannot combine metadata-only, audio-only, automatic pictures, full-frame pictures, the individual scene check, or comparison options. `--hdr-boundary-audio` requires bounded mode even when explicitly false.

Individual picture flags select manual work rather than the default audio operation. For scene checks, borders, and audio together, use `--hdr-boundary-sync --hdr-active-area`. See [HDR analysis](../workflow/hdr-analysis.md) for examples and the limits of sampled and bounded coverage.

#### Retained HDR workflows

| Option                       | Purpose                                                                                                          |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `--hdr-resume-workflow <id>` | Resume the exact retained active CLI workflow without replacing its request.                                     |
| `--hdr-retry-attempt <id>`   | Identify the retained analysis attempt for a new decoded measurement pass. Requires retry modes.                 |
| `--hdr-retry-modes <list>`   | Explicit unsuccessful decoded modes to measure again, from the current retained request. Requires an attempt ID. |
| `--hdr-image-ids <id,id>`    | Select exact current HDR artifact IDs for descriptions on continuation.                                          |

Retry and image-selection flags require `--hdr-resume-workflow`; do not combine resume with either new-analysis flag. Retry measurements before selecting their new artifact IDs, in separate invocations. Resume accepts configuration, logging, and unattended options but cannot change the retained analysis request. Use IDs from the retained workflow; they are opaque. A decoded retry does not authorize a native metadata rescan.

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
| `--keep-folder`          | `-keep-folder`, `-kf`              | Keep the supplied folder around one movie or episode video.                              |
| `--onlyID`               | `-onlyID`                          | Limit tracker metadata lookup to IDs where supported; does not stop the upload workflow. |
| `--infohash <hash>`      | `-infohash`, `-th`, `-torrenthash` | Supply a 40-character hexadecimal v1 info hash.                                          |
| `--max-piece-size <MiB>` | `-max-piece-size`, `-mps`          | Override the configured maximum: `1`, `2`, `4`, `8`, `16`, `32`, `64`, or `128` MiB.     |
| `--nohash`               | `-nohash`, `-nh`                   | Reuse existing torrents only; do not generate a new torrent.                             |
| `--rehash`               | `-rehash`, `-rh`                   | Force generation of a fresh torrent.                                                     |

`--keep-folder` applies only when a supplied folder contains one selected movie or episode video. The torrent retains that folder as its root and includes only the selected video; samples, NFOs, text files, and other sidecars are excluded. The default remains a single-file torrent. Season packs, multi-video folders, disc sources, and direct file inputs are unchanged. Reused torrents must match the requested layout.

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
